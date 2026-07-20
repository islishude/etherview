package ethrpc

import (
	"context"
	"errors"
	"testing"
)

type rateTestCaller struct{ calls int }

func (c *rateTestCaller) Call(context.Context, string, []any, any) error {
	c.calls++
	return nil
}

func TestRateClientHonorsCancellationWhileWaiting(t *testing.T) {
	t.Parallel()
	base := &rateTestCaller{}
	client, err := NewRateClient(base, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Call(context.Background(), "first", nil, new(any)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.Call(ctx, "second", nil, new(any)); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation", err)
	}
	if base.calls != 1 {
		t.Fatalf("underlying calls = %d", base.calls)
	}
}

func TestNewRateClientValidatesArguments(t *testing.T) {
	t.Parallel()
	if _, err := NewRateClient(nil, 1); err == nil {
		t.Fatal("expected nil-client error")
	}
	if _, err := NewRateClient(&rateTestCaller{}, 0); err == nil {
		t.Fatal("expected invalid-rate error")
	}
}
