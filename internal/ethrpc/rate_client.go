package ethrpc

import (
	"context"
	"errors"
	"sync"
	"time"
)

// RateClient applies a local request cadence without changing RPC semantics.
// BatchCall consumes one request permit because it produces one HTTP request.
type RateClient struct {
	base     Caller
	interval time.Duration
	mu       sync.Mutex
	next     time.Time
	now      func() time.Time
}

func NewRateClient(base Caller, requestsPerSecond int) (*RateClient, error) {
	if base == nil {
		return nil, errors.New("rate-limited RPC client is nil")
	}
	if requestsPerSecond <= 0 {
		return nil, errors.New("RPC request rate must be positive")
	}
	return &RateClient{
		base:     base,
		interval: time.Second / time.Duration(requestsPerSecond),
		now:      time.Now,
	}, nil
}

func (c *RateClient) Call(ctx context.Context, method string, params []any, result any) error {
	if err := c.acquire(ctx); err != nil {
		return err
	}
	return c.base.Call(ctx, method, params, result)
}

func (c *RateClient) BatchCall(ctx context.Context, elements []BatchElem) error {
	if err := c.acquire(ctx); err != nil {
		return err
	}
	batch, ok := c.base.(BatchCaller)
	if !ok {
		return errors.New("underlying RPC client does not support batch calls")
	}
	return batch.BatchCall(ctx, elements)
}

func (c *RateClient) acquire(ctx context.Context) error {
	c.mu.Lock()
	now := c.now()
	waitUntil := c.next
	if waitUntil.Before(now) {
		waitUntil = now
	}
	c.next = waitUntil.Add(c.interval)
	c.mu.Unlock()
	wait := time.Until(waitUntil)
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
