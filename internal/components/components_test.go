package components

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

type testService struct {
	name    string
	started *atomic.Int32
	err     error
}

type returningService struct {
	name string
	err  error
}

func (s returningService) Name() string { return s.name }
func (s returningService) Run(context.Context) error {
	return s.err
}

type lifecycleService struct {
	name      string
	lifecycle *Lifecycle
	stopped   chan bool
}

func (s lifecycleService) Name() string { return s.name }
func (s lifecycleService) Run(ctx context.Context) error {
	<-ctx.Done()
	s.stopped <- s.lifecycle.Ready()
	return ctx.Err()
}

type stubbornService struct {
	name    string
	release <-chan struct{}
}

func (s stubbornService) Name() string { return s.name }
func (s stubbornService) Run(context.Context) error {
	<-s.release
	return nil
}

func (s testService) Name() string { return s.name }

func (s testService) Run(ctx context.Context) error {
	s.started.Add(1)
	if s.err != nil {
		return s.err
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestRegistryDeduplicatesSharedService(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	var built atomic.Int32
	factory := func() (Service, error) {
		built.Add(1)
		return testService{name: "database", started: &atomic.Int32{}}, nil
	}
	if err := r.Register(RoleAPI, "database", factory); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(RoleSync, "database", factory); err != nil {
		t.Fatal(err)
	}
	services, err := r.Build([]Role{RoleAPI, RoleSync})
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 || built.Load() != 1 {
		t.Fatalf("services=%d built=%d", len(services), built.Load())
	}
}

func TestRegistryMonolithGraphEqualsUnionOfSplitRoleGraphs(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	roles := []Role{RoleAPI, RoleSync, RoleEnrich, RoleTrace, RoleVerify, RoleMetadata, RoleMaintenance}
	for _, role := range roles {
		if err := registry.Register(role, "00-operations-http", namedFactory("operations-http")); err != nil {
			t.Fatal(err)
		}
		if err := registry.Register(role, "50-role-"+string(role), namedFactory(string(role))); err != nil {
			t.Fatal(err)
		}
	}
	monolith, err := registry.Build(roles)
	if err != nil {
		t.Fatal(err)
	}
	want := make(map[string]struct{})
	for _, role := range roles {
		split, err := registry.Build([]Role{role})
		if err != nil {
			t.Fatal(err)
		}
		for _, service := range split {
			want[service.Name()] = struct{}{}
		}
	}
	gotNames := serviceTestNames(monolith)
	wantNames := make([]string, 0, len(want))
	for name := range want {
		wantNames = append(wantNames, name)
	}
	slices.Sort(wantNames)
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("monolith=%v split-union=%v", gotNames, wantNames)
	}
}

func namedFactory(name string) Factory {
	return func() (Service, error) {
		return testService{name: name, started: &atomic.Int32{}}, nil
	}
}

func serviceTestNames(services []Service) []string {
	names := make([]string, len(services))
	for index, service := range services {
		names[index] = service.Name()
	}
	slices.Sort(names)
	return names
}

func TestRunCancelsPeersOnFailure(t *testing.T) {
	t.Parallel()
	var started atomic.Int32
	boom := errors.New("boom")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := Run(ctx, []Service{
		testService{name: "waiter", started: &started},
		testService{name: "failure", started: &started, err: boom},
	})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want boom", err)
	}
	if started.Load() != 2 {
		t.Fatalf("started=%d", started.Load())
	}
}

func TestRunPublishesReadinessAndWithdrawsItBeforeCancellation(t *testing.T) {
	t.Parallel()
	lifecycle := NewLifecycle()
	stopped := make(chan bool, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunWithOptions(ctx, []Service{
			lifecycleService{name: "worker", lifecycle: lifecycle, stopped: stopped},
		}, RunOptions{Lifecycle: lifecycle, ShutdownTimeout: time.Second})
	}()
	waitReady(t, lifecycle)
	cancel()
	select {
	case wasReady := <-stopped:
		if wasReady {
			t.Fatal("lifecycle was still ready after service cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("service did not observe cancellation")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("graceful shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("supervisor did not finish graceful shutdown")
	}
	if lifecycle.Ready() {
		t.Fatal("lifecycle remained ready after shutdown")
	}
}

func TestRunTreatsUnexpectedCleanExitAsFailure(t *testing.T) {
	t.Parallel()
	var started atomic.Int32
	err := RunWithOptions(context.Background(), []Service{
		testService{name: "peer", started: &started},
		returningService{name: "early"},
	}, RunOptions{ShutdownTimeout: time.Second})
	if !errors.Is(err, ErrUnexpectedExit) {
		t.Fatalf("error=%v, want ErrUnexpectedExit", err)
	}
}

func TestRunBoundsGracefulShutdownAndNamesStuckComponents(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	started := time.Now()
	err := RunWithOptions(context.Background(), []Service{
		stubbornService{name: "stuck-worker", release: release},
		returningService{name: "failed-worker", err: errors.New("boom")},
	}, RunOptions{ShutdownTimeout: 25 * time.Millisecond})
	if !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("error=%v, want ErrShutdownTimeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("bounded shutdown took %s", elapsed)
	}
	var timeoutErr *ShutdownTimeoutError
	if !errors.As(err, &timeoutErr) || !slices.Equal(timeoutErr.Components, []string{"stuck-worker"}) {
		t.Fatalf("timeout error=%#v", timeoutErr)
	}
}

func waitReady(t *testing.T, lifecycle *Lifecycle) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("lifecycle did not become ready")
		case <-ticker.C:
			if lifecycle.Ready() {
				return
			}
		}
	}
}
