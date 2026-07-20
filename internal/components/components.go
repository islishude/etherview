// Package components defines the shared monolith/split-role lifecycle model.
package components

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type Role string

const (
	RoleAPI         Role = "api"
	RoleSync        Role = "sync"
	RoleEnrich      Role = "enrich"
	RoleTrace       Role = "trace"
	RoleVerify      Role = "verify"
	RoleMetadata    Role = "metadata"
	RoleMaintenance Role = "maintenance"
)

// Service is a long-lived component. Run must return after ctx is canceled.
type Service interface {
	Name() string
	Run(context.Context) error
}

type Factory func() (Service, error)

var (
	// ErrUnexpectedExit means a long-lived service returned without an error
	// before the runtime began shutting down.
	ErrUnexpectedExit = errors.New("component exited unexpectedly")
	// ErrShutdownTimeout means at least one service did not honor cancellation
	// within the configured process shutdown budget.
	ErrShutdownTimeout = errors.New("component shutdown timed out")
)

const (
	lifecycleStarting uint32 = iota
	lifecycleReady
	lifecycleStopping
	lifecycleStopped
)

// Lifecycle is the process-wide readiness state shared by the monolith and
// every split role. Readiness is published only after every selected service
// has entered Run, and is withdrawn before their contexts are canceled.
type Lifecycle struct {
	state atomic.Uint32
}

func NewLifecycle() *Lifecycle { return &Lifecycle{} }

// Ready reports whether every selected component has started and shutdown has
// not begun. Dependency-specific probes, such as PostgreSQL and core indexing
// readiness, remain additional conditions at their HTTP boundaries.
func (l *Lifecycle) Ready() bool {
	return l != nil && l.state.Load() == lifecycleReady
}

func (l *Lifecycle) set(state uint32) {
	if l != nil {
		l.state.Store(state)
	}
}

type RunOptions struct {
	Lifecycle       *Lifecycle
	ShutdownTimeout time.Duration
}

// ShutdownTimeoutError identifies services that failed to stop within the
// process shutdown budget. The process may return after this error; callers
// must not reuse resources owned by the listed services.
type ShutdownTimeoutError struct {
	After      time.Duration
	Components []string
}

func (e *ShutdownTimeoutError) Error() string {
	return fmt.Sprintf("%v after %s: %v", ErrShutdownTimeout, e.After, e.Components)
}

func (*ShutdownTimeoutError) Unwrap() error { return ErrShutdownTimeout }

// Registry maps roles to factories. A factory may be registered for multiple
// roles; its stable key prevents duplicate construction in roles=all mode.
type Registry struct {
	entries map[Role][]entry
}

type entry struct {
	key     string
	factory Factory
}

func NewRegistry() *Registry { return &Registry{entries: make(map[Role][]entry)} }

func (r *Registry) Register(role Role, key string, factory Factory) error {
	if role == "" || key == "" || factory == nil {
		return errors.New("role, key, and factory are required")
	}
	for _, existing := range r.entries[role] {
		if existing.key == key {
			return fmt.Errorf("component %q already registered for role %q", key, role)
		}
	}
	r.entries[role] = append(r.entries[role], entry{key: key, factory: factory})
	return nil
}

func (r *Registry) Build(roles []Role) ([]Service, error) {
	entries, err := r.selectedEntries(roles)
	if err != nil {
		return nil, err
	}
	services := make([]Service, 0, len(entries))
	for _, item := range entries {
		service, err := item.factory()
		if err != nil {
			return nil, fmt.Errorf("build component %q: %w", item.key, err)
		}
		if service == nil {
			return nil, fmt.Errorf("build component %q: factory returned nil", item.key)
		}
		services = append(services, service)
	}
	return services, nil
}

// Keys returns the exact deduplicated component identities that Build would
// instantiate. Runtime assembly uses this to assert that its production graph
// matches the feature-aware graph contract without executing factories twice.
func (r *Registry) Keys(roles []Role) ([]string, error) {
	entries, err := r.selectedEntries(roles)
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(entries))
	for index, item := range entries {
		keys[index] = item.key
	}
	return keys, nil
}

func (r *Registry) selectedEntries(roles []Role) ([]entry, error) {
	if r == nil {
		return nil, errors.New("component registry is nil")
	}
	seen := make(map[string]bool)
	var entries []entry
	for _, role := range roles {
		registered, ok := r.entries[role]
		if !ok {
			return nil, fmt.Errorf("role %q has no registered components", role)
		}
		for _, item := range registered {
			if !seen[item.key] {
				seen[item.key] = true
				entries = append(entries, item)
			}
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	return entries, nil
}

// Run supervises all services with the default lifecycle options.
func Run(ctx context.Context, services []Service) error {
	return RunWithOptions(ctx, services, RunOptions{})
}

// RunWithOptions supervises all services. The first failure or unexpected
// clean exit withdraws readiness, cancels every peer, and waits up to the
// shared shutdown budget. A parent cancellation that all services honor is a
// successful graceful shutdown.
func RunWithOptions(ctx context.Context, services []Service, options RunOptions) error {
	if ctx == nil {
		return errors.New("component context is nil")
	}
	if len(services) == 0 {
		return errors.New("no services configured")
	}
	names := make([]string, len(services))
	for index, service := range services {
		if service == nil {
			return fmt.Errorf("component %d is nil", index)
		}
		name := service.Name()
		if name == "" {
			return fmt.Errorf("component %d has an empty name", index)
		}
		names[index] = name
	}

	lifecycle := options.Lifecycle
	if lifecycle == nil {
		lifecycle = NewLifecycle()
	}
	lifecycle.set(lifecycleStarting)
	if err := ctx.Err(); err != nil {
		lifecycle.set(lifecycleStopped)
		return nil
	}

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	shutdownTimeout := options.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = 20 * time.Second
	}

	type result struct {
		index int
		name  string
		err   error
	}
	results := make(chan result, len(services))
	started := make(chan struct{}, len(services))
	var wg sync.WaitGroup
	for index, service := range services {
		index, service := index, service
		name := names[index]
		wg.Add(1)
		go func() {
			defer wg.Done()
			started <- struct{}{}
			results <- result{index: index, name: name, err: service.Run(runCtx)}
		}()
	}

	remaining := make(map[int]string, len(names))
	for index, name := range names {
		remaining[index] = name
	}
	startedCount := 0
	ready := false
	stopping := false
	var runErr error
	var timeout *time.Timer
	var timeoutC <-chan time.Time
	parentDone := ctx.Done()
	beginShutdown := func() {
		if stopping {
			return
		}
		stopping = true
		lifecycle.set(lifecycleStopping)
		cancel()
		parentDone = nil
		timeout = time.NewTimer(shutdownTimeout)
		timeoutC = timeout.C
	}
	recordResult := func(item result) {
		delete(remaining, item.index)
		if !stopping {
			if item.err == nil {
				runErr = fmt.Errorf("component %s: %w", item.name, ErrUnexpectedExit)
			} else {
				runErr = fmt.Errorf("component %s: %w", item.name, item.err)
			}
			beginShutdown()
			return
		}
		if item.err != nil && !errors.Is(item.err, context.Canceled) {
			runErr = errors.Join(runErr, fmt.Errorf("shutdown component %s: %w", item.name, item.err))
		}
	}

	for len(remaining) > 0 {
		if startedCount == len(services) && !ready && !stopping {
			// Prefer a service that already returned over briefly advertising a
			// ready process with a dead component.
			select {
			case item := <-results:
				recordResult(item)
				continue
			default:
				ready = true
				lifecycle.set(lifecycleReady)
			}
		}
		select {
		case <-parentDone:
			beginShutdown()
		case <-started:
			startedCount++
		case item := <-results:
			recordResult(item)
		case <-timeoutC:
			unfinished := make([]string, 0, len(remaining))
			for _, name := range remaining {
				unfinished = append(unfinished, name)
			}
			sort.Strings(unfinished)
			lifecycle.set(lifecycleStopped)
			return errors.Join(runErr, &ShutdownTimeoutError{After: shutdownTimeout, Components: unfinished})
		}
	}
	if timeout != nil && !timeout.Stop() {
		select {
		case <-timeout.C:
		default:
		}
	}
	wg.Wait()
	lifecycle.set(lifecycleStopped)
	if runErr != nil {
		return runErr
	}
	return nil
}
