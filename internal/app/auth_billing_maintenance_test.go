package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
)

type appHousekeepingRecorder struct {
	mu     sync.Mutex
	calls  chan struct{}
	times  []time.Time
	limits []int
	err    error
}

func (recorder *appHousekeepingRecorder) sweep(
	_ context.Context,
	observedAt time.Time,
	limit int,
) error {
	recorder.mu.Lock()
	recorder.times = append(recorder.times, observedAt)
	recorder.limits = append(recorder.limits, limit)
	recorder.mu.Unlock()
	select {
	case recorder.calls <- struct{}{}:
	default:
	}
	return recorder.err
}

func TestAuthBillingHousekeeperIsPeriodicRedactedAndSupervisorBounded(
	t *testing.T,
) {
	t.Parallel()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	recorder := &appHousekeepingRecorder{
		calls: make(chan struct{}, 4),
		err:   errors.New("postgres://operator:secret@writer/private"),
	}
	now := time.Date(2026, 7, 26, 1, 2, 3, 0, time.FixedZone("test", 3600))
	service, err := newAuthBillingHousekeeper(
		recorder.sweep,
		logger,
		authBillingHousekeeperOptions{
			ServiceName: "test-auth-cleanup", FailureCode: "test_cleanup_failed",
			Interval: 40 * time.Millisecond, RetryInterval: 5 * time.Millisecond,
			Batch: 73, Now: func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := components.NewLifecycle()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- components.RunWithOptions(
			ctx,
			[]components.Service{service},
			components.RunOptions{
				Lifecycle: lifecycle, ShutdownTimeout: time.Second,
			},
		)
	}()
	waitAppLifecycleReady(t, lifecycle)
	for range 2 {
		select {
		case <-recorder.calls:
		case <-time.After(time.Second):
			t.Fatal("housekeeper did not retry its bounded sweep")
		}
	}
	if !lifecycle.Ready() {
		t.Fatal("retryable housekeeping failure withdrew readiness")
	}
	recorder.mu.Lock()
	times := slices.Clone(recorder.times)
	limits := slices.Clone(recorder.limits)
	recorder.mu.Unlock()
	if len(times) < 2 || !times[0].Equal(now.UTC()) ||
		!times[1].Equal(now.UTC()) {
		t.Fatalf("housekeeping times=%v", times)
	}
	for _, limit := range limits {
		if limit != 73 {
			t.Fatalf("housekeeping limits=%v", limits)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("graceful housekeeping shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("housekeeper did not stop with the supervisor")
	}
	if lifecycle.Ready() {
		t.Fatal("housekeeping lifecycle remained ready after shutdown")
	}
	logged := logs.String()
	if !strings.Contains(logged, "test_cleanup_failed") ||
		strings.Contains(logged, "operator") ||
		strings.Contains(logged, "secret") ||
		strings.Contains(logged, "private") {
		t.Fatalf("housekeeping log was not stable and redacted: %s", logged)
	}
}

func TestAuthBillingHousekeeperUsesNormalIntervalAfterSuccess(t *testing.T) {
	t.Parallel()
	recorder := &appHousekeepingRecorder{calls: make(chan struct{}, 3)}
	service, err := newAuthBillingHousekeeper(
		recorder.sweep,
		nil,
		authBillingHousekeeperOptions{
			ServiceName: "test-billing-expiry",
			FailureCode: "test_billing_expiry_failed",
			Interval:    25 * time.Millisecond,
			Batch:       1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	startedAt := time.Now()
	go func() { done <- service.Run(ctx) }()
	for range 2 {
		select {
		case <-recorder.calls:
		case <-time.After(time.Second):
			t.Fatal("housekeeper did not run on its configured interval")
		}
	}
	if time.Since(startedAt) < 20*time.Millisecond {
		t.Fatal("successful housekeeping loop did not wait for the normal interval")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("housekeeper cancellation=%v", err)
	}
}

func TestRegisterAuthBillingHousekeepersIsFeatureAwareAndBounded(t *testing.T) {
	t.Parallel()
	disabled := config.Default()
	if err := registerAuthBillingHousekeepers(nil, nil, disabled, nil); err != nil {
		t.Fatalf("feature-off registration touched dependencies: %v", err)
	}

	tests := []struct {
		name string
		auth bool
		x402 bool
		want []string
	}{
		{
			name: "user authentication only", auth: true,
			want: []string{"47-user-auth-cleanup"},
		},
		{
			name: "x402 billing only", x402: true,
			want: []string{"48-x402-billing-expiry"},
		},
		{
			name: "both features", auth: true, x402: true,
			want: []string{
				"47-user-auth-cleanup",
				"48-x402-billing-expiry",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Features.UserAuth = test.auth
			cfg.Features.X402Billing = test.x402
			registry := components.NewRegistry()
			if err := registerAuthBillingHousekeepers(
				registry, &sql.DB{}, cfg, nil,
			); err != nil {
				t.Fatal(err)
			}
			keys, err := registry.Keys(
				[]components.Role{components.RoleMaintenance},
			)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(keys, test.want) {
				t.Fatalf("registered keys=%v want=%v", keys, test.want)
			}
			services, err := registry.Build(
				[]components.Role{components.RoleMaintenance},
			)
			if err != nil {
				t.Fatal(err)
			}
			for _, service := range services {
				housekeeper, ok := service.(*authBillingHousekeeper)
				if !ok {
					t.Fatalf("service type=%T", service)
				}
				if housekeeper.options.Batch !=
					maximumAuthBillingMaintenanceBatch {
					t.Fatalf(
						"housekeeping batch=%d",
						housekeeper.options.Batch,
					)
				}
			}
		})
	}
}

func TestNewAuthBillingHousekeeperRejectsUnboundedOptions(t *testing.T) {
	t.Parallel()
	for _, options := range []authBillingHousekeeperOptions{
		{
			ServiceName: "test", FailureCode: "test_failed",
			Interval: time.Second, Batch: 0,
		},
		{
			ServiceName: "test", FailureCode: "test_failed",
			Interval: time.Second,
			Batch:    maximumAuthBillingMaintenanceBatch + 1,
		},
		{
			ServiceName: "test", FailureCode: "test_failed",
			Interval: time.Second, RetryInterval: 2 * time.Second,
			Batch: 1,
		},
	} {
		if _, err := newAuthBillingHousekeeper(
			func(context.Context, time.Time, int) error { return nil },
			nil,
			options,
		); err == nil {
			t.Fatalf("unbounded options passed validation: %#v", options)
		}
	}
}
