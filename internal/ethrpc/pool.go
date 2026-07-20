package ethrpc

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
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
	Client       Caller
	Capabilities CapabilityReport
}

func (e *Endpoint) Supports(purpose Purpose) bool {
	return e != nil && e.Purposes[purpose]
}

type Pool struct {
	mu              sync.Mutex
	byPurpose       map[Purpose][]*endpointState
	next            map[Purpose]int
	failureCooldown time.Duration
	now             func() time.Time
}

type endpointState struct {
	endpoint         Endpoint
	unavailableUntil time.Time
	failures         uint32
}

type PoolOptions struct {
	FailureCooldown time.Duration
	Now             func() time.Time
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
	return &endpoint, nil
}

func (p *Pool) ReportSuccess(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if state := p.find(name); state != nil {
		state.failures = 0
		state.unavailableUntil = time.Time{}
	}
}

func (p *Pool) ReportFailure(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if state := p.find(name); state != nil {
		state.failures++
		multiplier := time.Duration(state.failures)
		if multiplier > 6 {
			multiplier = 6
		}
		state.unavailableUntil = p.now().Add(p.failureCooldown * multiplier)
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
	for purpose, enabled := range endpoint.Purposes {
		copy.Purposes[purpose] = enabled
	}
	copy.Capabilities = endpoint.Capabilities.Clone()
	return copy
}
