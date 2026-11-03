//go:build runtimee2e

package runtimee2e

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/accelerator"
)

// Independent clients prove actual network hints and resubscription after the
// broker restarts. Verification correctness during the outage is tested by the
// surrounding PostgreSQL-backed production workflow.
func (h *harness) natsWakeProbe(ctx context.Context) func() {
	h.t.Helper()
	binding, err := h.project.Run(ctx, "port", "nats", "4222")
	if err != nil {
		h.t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(strings.TrimSpace(string(binding)))
	if err != nil {
		h.t.Fatal(err)
	}
	options := accelerator.NATSWakeOptions{
		Namespace: "recovery-probe", ChainID: 1,
		ConnectTimeout: time.Second, ReconnectWait: 50 * time.Millisecond,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	producer, err := accelerator.NewNATSWake("nats://127.0.0.1:"+port, options)
	if err != nil {
		h.t.Fatal(err)
	}
	consumer, err := accelerator.NewNATSWake("nats://127.0.0.1:"+port, options)
	if err != nil {
		h.t.Fatal(err)
	}
	hints, err := consumer.Subscribe(accelerator.WakeJobs)
	if err != nil {
		h.t.Fatal(err)
	}
	probeCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 2)
	go func() { done <- producer.Run(probeCtx) }()
	go func() { done <- consumer.Run(probeCtx) }()
	h.t.Cleanup(func() {
		cancel()
		for range 2 {
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					h.t.Errorf("NATS probe: %v", err)
				}
			case <-time.After(5 * time.Second):
				h.t.Error("NATS probe did not shut down")
			}
		}
	})
	return func() {
		h.t.Helper()
		// Discard any delayed earlier signal before proving fresh delivery.
		select {
		case <-hints:
		default:
		}
		waitFor(h.t, ctx, "NATS remote jobs wake", func() (bool, string, error) {
			producer.Signal(accelerator.WakeJobs)
			select {
			case <-hints:
				return true, "delivered", nil
			case <-time.After(100 * time.Millisecond):
				return false, "awaiting reconnect and subscription", nil
			}
		})
	}
}
