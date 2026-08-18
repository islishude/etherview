package ethrpc

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/rpc"
)

type Purpose string

const (
	PurposeHead    Purpose = "head"
	PurposeHistory Purpose = "history"
	PurposeState   Purpose = "state"
	PurposeTrace   Purpose = "trace"
	PurposeMempool Purpose = "mempool"
)

func ValidPurpose(purpose Purpose) bool {
	switch purpose {
	case PurposeHead, PurposeHistory, PurposeState, PurposeTrace, PurposeMempool:
		return true
	default:
		return false
	}
}

type Endpoint struct {
	Name         string
	Purposes     map[Purpose]bool
	Client       *rpc.Client
	Capabilities CapabilityReport
	purpose      Purpose
	observer     Observer
}

func (e *Endpoint) Supports(purpose Purpose) bool {
	return e != nil && e.Purposes[purpose]
}

func (e *Endpoint) CallContext(ctx context.Context, result any, method string, args ...any) error {
	if e == nil || e.Client == nil {
		return errors.New("RPC endpoint client is nil")
	}
	startedAt := time.Now()
	err := e.Client.CallContext(ctx, result, method, args...)
	e.record(method, 1, boolInt(err == nil), boolInt(err != nil), time.Since(startedAt))
	return err
}

func (e *Endpoint) BatchCallContext(ctx context.Context, elements []rpc.BatchElem) error {
	if e == nil || e.Client == nil {
		return errors.New("RPC endpoint client is nil")
	}
	startedAt := time.Now()
	err := e.Client.BatchCallContext(ctx, elements)
	if err != nil {
		e.record(batchMethod(elements), len(elements), 0, len(elements), time.Since(startedAt))
		return err
	}
	succeeded, failed := 0, 0
	for index := range elements {
		if elements[index].Error == nil {
			succeeded++
		} else {
			failed++
		}
	}
	e.record(batchMethod(elements), len(elements), succeeded, failed, time.Since(startedAt))
	return nil
}

func (e *Endpoint) record(method string, batchSize, succeeded, failed int, duration time.Duration) {
	if e == nil || e.observer == nil || !ValidPurpose(e.purpose) {
		return
	}
	e.observer.RecordRPC(Observation{
		Endpoint: e.Name, Purpose: e.purpose, Method: method, BatchSize: batchSize,
		SuccessCount: succeeded, ErrorCount: failed, Duration: duration,
	})
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func batchMethod(elements []rpc.BatchElem) string {
	if len(elements) == 0 {
		return "empty_batch"
	}
	method := elements[0].Method
	for index := 1; index < len(elements); index++ {
		if elements[index].Method != method {
			return "mixed_batch"
		}
	}
	return method
}

type Pool struct {
	mu              sync.Mutex
	byPurpose       map[Purpose][]*endpointState
	next            map[Purpose]int
	failureCooldown time.Duration
	now             func() time.Time
	observer        Observer
}

type endpointState struct {
	endpoint         Endpoint
	unavailableUntil time.Time
	failures         uint32
}

type PoolOptions struct {
	FailureCooldown time.Duration
	Now             func() time.Time
	Observer        Observer
}

func NewPool(endpoints []Endpoint, options PoolOptions) (*Pool, error) {
	if len(endpoints) == 0 {
		return nil, errors.New("RPC pool has no endpoints")
	}
	if options.FailureCooldown <= 0 {
		options.FailureCooldown = 5 * time.Second
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	pool := &Pool{
		byPurpose:       make(map[Purpose][]*endpointState),
		next:            make(map[Purpose]int),
		failureCooldown: options.FailureCooldown,
		now:             options.Now,
		observer:        options.Observer,
	}
	seen := make(map[string]struct{}, len(endpoints))
	usableEndpoints := 0
	for _, endpoint := range endpoints {
		if endpoint.Name == "" {
			return nil, errors.New("RPC endpoint name is empty")
		}
		if endpoint.Client == nil {
			return nil, fmt.Errorf("RPC endpoint %q has no client", endpoint.Name)
		}
		if _, exists := seen[endpoint.Name]; exists {
			return nil, fmt.Errorf("duplicate RPC endpoint name %q", endpoint.Name)
		}
		seen[endpoint.Name] = struct{}{}
		if len(endpoint.Purposes) == 0 {
			return nil, fmt.Errorf("RPC endpoint %q has no purposes", endpoint.Name)
		}
		usableEndpoint := cloneEndpoint(endpoint)
		configuredPurposes := 0
		for purpose, enabled := range endpoint.Purposes {
			if !ValidPurpose(purpose) {
				return nil, fmt.Errorf("RPC endpoint %q has invalid purpose %q", endpoint.Name, purpose)
			}
			if enabled {
				configuredPurposes++
			}
			if enabled && purpose == PurposeHistory &&
				endpoint.Capabilities.Status(CapabilityHistoricalData) == AvailabilityUnavailable {
				delete(usableEndpoint.Purposes, purpose)
			}
		}
		if configuredPurposes == 0 {
			return nil, fmt.Errorf("RPC endpoint %q has no enabled purposes", endpoint.Name)
		}
		state := &endpointState{endpoint: usableEndpoint}
		enabledPurposes := 0
		for purpose, enabled := range usableEndpoint.Purposes {
			if enabled {
				enabledPurposes++
				pool.byPurpose[purpose] = append(pool.byPurpose[purpose], state)
			}
		}
		if enabledPurposes == 0 {
			continue
		}
		usableEndpoints++
	}
	if usableEndpoints == 0 {
		return nil, errors.New("RPC pool has no usable endpoints")
	}
	return pool, nil
}

// Acquire returns one endpoint configured for purpose. Endpoints in a failure
// cooldown are skipped while a healthy candidate exists; if all candidates are
// cooling down, the one eligible soonest is returned so callers can make
// progress rather than deadlock.
func (p *Pool) Acquire(purpose Purpose) (*Endpoint, error) {
	if !ValidPurpose(purpose) {
		return nil, fmt.Errorf("invalid RPC purpose %q", purpose)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	candidates := p.byPurpose[purpose]
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no RPC endpoint configured for purpose %q", purpose)
	}
	start := p.next[purpose] % len(candidates)
	now := p.now()
	selected := -1
	for offset := range candidates {
		index := (start + offset) % len(candidates)
		if !candidates[index].unavailableUntil.After(now) {
			selected = index
			break
		}
	}
	if selected < 0 {
		selected = 0
		for index := 1; index < len(candidates); index++ {
			if candidates[index].unavailableUntil.Before(candidates[selected].unavailableUntil) {
				selected = index
			}
		}
	}
	p.next[purpose] = (selected + 1) % len(candidates)
	endpoint := cloneEndpoint(candidates[selected].endpoint)
	endpoint.purpose = purpose
	endpoint.observer = p.observer
	return &endpoint, nil
}

// AcquireNamed returns the exact configured endpoint for a sticky operation.
// It deliberately does not switch away during cooldown: a caller that has
// persisted an endpoint-bound snapshot must fail that operation rather than
// combine state from another node.
func (p *Pool) AcquireNamed(purpose Purpose, name string) (*Endpoint, error) {
	if !ValidPurpose(purpose) {
		return nil, fmt.Errorf("invalid RPC purpose %q", purpose)
	}
	if name == "" {
		return nil, errors.New("RPC endpoint name is empty")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, state := range p.byPurpose[purpose] {
		if state.endpoint.Name != name {
			continue
		}
		endpoint := cloneEndpoint(state.endpoint)
		endpoint.purpose = purpose
		endpoint.observer = p.observer
		return &endpoint, nil
	}
	return nil, fmt.Errorf("RPC endpoint %q is not configured for purpose %q", name, purpose)
}

func (p *Pool) ReportSuccess(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if state := p.find(name); state != nil {
		previous := state.failures
		state.failures = 0
		state.unavailableUntil = time.Time{}
		if previous > 0 {
			if observer, ok := p.observer.(EndpointStateObserver); ok {
				observer.RecordRPCEndpointState(EndpointState{Endpoint: name, State: "recovered"})
			}
		}
	}
}

func (p *Pool) ReportFailure(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if state := p.find(name); state != nil {
		state.failures++
		multiplier := min(time.Duration(state.failures), 6)
		cooldown := p.failureCooldown * multiplier
		state.unavailableUntil = p.now().Add(cooldown)
		if observer, ok := p.observer.(EndpointStateObserver); ok {
			observer.RecordRPCEndpointState(EndpointState{
				Endpoint: name, State: "degraded", ConsecutiveFailures: state.failures, Cooldown: cooldown,
			})
		}
	}
}

func (p *Pool) Names(purpose Purpose) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	states := p.byPurpose[purpose]
	names := make([]string, 0, len(states))
	for _, state := range states {
		names = append(names, state.endpoint.Name)
	}
	sort.Strings(names)
	return names
}

func (p *Pool) find(name string) *endpointState {
	for _, states := range p.byPurpose {
		for _, state := range states {
			if state.endpoint.Name == name {
				return state
			}
		}
	}
	return nil
}

func cloneEndpoint(endpoint Endpoint) Endpoint {
	copy := endpoint
	copy.Purposes = make(map[Purpose]bool, len(endpoint.Purposes))
	maps.Copy(copy.Purposes, endpoint.Purposes)
	copy.Capabilities = endpoint.Capabilities.Clone()
	return copy
}
