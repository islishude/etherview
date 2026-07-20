package accelerator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestNATSWakeBroadcastsLocallyAndCoalesces(t *testing.T) {
	t.Parallel()
	wake, err := NewNATSWake("nats://127.0.0.1:4222", NATSWakeOptions{ChainID: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	first, err := wake.Subscribe(WakeJobs)
	if err != nil {
		t.Fatal(err)
	}
	second, err := wake.Subscribe(WakeJobs)
	if err != nil {
		t.Fatal(err)
	}
	wake.Signal(WakeJobs)
	wake.Signal(WakeJobs)
	for index, channel := range []<-chan struct{}{first, second} {
		select {
		case <-channel:
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d was not woken", index)
		}
		select {
		case <-channel:
			t.Fatalf("subscriber %d received an uncoalesced duplicate", index)
		default:
		}
	}
}

func TestNATSWakeUnavailableBrokerDoesNotExitService(t *testing.T) {
	t.Parallel()
	wake, err := NewNATSWake("nats://127.0.0.1:1", NATSWakeOptions{
		ChainID: 1, ConnectTimeout: 10 * time.Millisecond, ReconnectWait: 10 * time.Millisecond,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- wake.Run(ctx) }()
	select {
	case err := <-done:
		t.Fatalf("optional NATS outage stopped the service: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("NATS wake did not honor cancellation")
	}
}

func TestNATSWakeRejectsUnknownTopic(t *testing.T) {
	t.Parallel()
	wake, err := NewNATSWake("nats://127.0.0.1:4222", NATSWakeOptions{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wake.Subscribe("credential"); err == nil {
		t.Fatal("unknown wake topic was accepted")
	}
}
