package maintenance

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type catalogSweepCall struct {
	chainID              uint64
	retentionGenerations int64
	deleteBatch          int
	now                  time.Time
}

type scriptedCatalogCleaner struct {
	mu      sync.Mutex
	errors  []error
	calls   []catalogSweepCall
	arrived chan int
}

func (cleaner *scriptedCatalogCleaner) Sweep(
	_ context.Context,
	chainID uint64,
	retentionGenerations int64,
	deleteBatch int,
	now time.Time,
) (CatalogCleanupResult, error) {
	cleaner.mu.Lock()
	index := len(cleaner.calls)
	cleaner.calls = append(cleaner.calls, catalogSweepCall{
		chainID: chainID, retentionGenerations: retentionGenerations,
		deleteBatch: deleteBatch, now: now,
	})
	var err error
	if index < len(cleaner.errors) {
		err = cleaner.errors[index]
	}
	cleaner.mu.Unlock()
	if cleaner.arrived != nil {
		select {
		case cleaner.arrived <- index + 1:
		default:
		}
	}
	return CatalogCleanupResult{Ran: err == nil}, err
}

func (cleaner *scriptedCatalogCleaner) snapshot() []catalogSweepCall {
	cleaner.mu.Lock()
	defer cleaner.mu.Unlock()
	return append([]catalogSweepCall(nil), cleaner.calls...)
}

func TestCatalogHousekeeperRetriesWithoutLeakingFailureAndStops(t *testing.T) {
	now := time.Date(2026, time.July, 20, 1, 2, 3, 0, time.FixedZone("test", 8*60*60))
	cleaner := &scriptedCatalogCleaner{
		errors:  []error{errors.New("upstream secret one"), errors.New("upstream secret two")},
		arrived: make(chan int, 8),
	}
	var logs bytes.Buffer
	housekeeper, err := NewCatalogHousekeeper(cleaner, slog.New(slog.NewTextHandler(&logs, nil)), CatalogHousekeeperOptions{
		ChainID: 1, Interval: 100 * time.Millisecond, RetryInterval: time.Millisecond,
		RetentionGenerations: 2000, AdapterDeleteBatch: 17, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- housekeeper.Run(ctx) }()
	waitForCatalogSweep(t, cleaner.arrived, 3)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("catalog housekeeper did not stop after cancellation")
	}

	calls := cleaner.snapshot()
	if len(calls) < 3 {
		t.Fatalf("sweep calls=%d", len(calls))
	}
	for _, call := range calls[:3] {
		if call.chainID != 1 || call.retentionGenerations != 2000 || call.deleteBatch != 17 || !call.now.Equal(now.UTC()) {
			t.Fatalf("sweep call=%+v", call)
		}
	}
	output := logs.String()
	if strings.Count(output, "error_code=catalog_maintenance_failed") != 2 {
		t.Fatalf("maintenance logs=%q", output)
	}
	if strings.Contains(output, "upstream secret") {
		t.Fatalf("maintenance log leaked nested failure: %q", output)
	}
}

func TestCatalogHousekeeperRunsImmediatelyAfterEachRestart(t *testing.T) {
	cleaner := &scriptedCatalogCleaner{arrived: make(chan int, 4)}
	for restart := 1; restart <= 2; restart++ {
		housekeeper, err := NewCatalogHousekeeper(cleaner, nil, CatalogHousekeeperOptions{
			ChainID: 1, Interval: time.Hour, RetentionGenerations: 1000, AdapterDeleteBatch: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- housekeeper.Run(ctx) }()
		waitForCatalogSweep(t, cleaner.arrived, restart)
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("restart %d run error=%v", restart, err)
		}
	}
	if calls := cleaner.snapshot(); len(calls) != 2 {
		t.Fatalf("restart sweep calls=%d", len(calls))
	}
}

func TestCatalogHousekeeperValidatesDependenciesAndBounds(t *testing.T) {
	cleaner := &scriptedCatalogCleaner{}
	for _, test := range []struct {
		cleaner CatalogCleaner
		options CatalogHousekeeperOptions
	}{
		{nil, CatalogHousekeeperOptions{ChainID: 1}},
		{cleaner, CatalogHousekeeperOptions{ChainID: 0}},
		{cleaner, CatalogHousekeeperOptions{ChainID: 1, Interval: time.Second, RetryInterval: 2 * time.Second}},
		{cleaner, CatalogHousekeeperOptions{ChainID: 1, RetentionGenerations: 999}},
		{cleaner, CatalogHousekeeperOptions{ChainID: 1, AdapterDeleteBatch: 10_001}},
	} {
		if _, err := NewCatalogHousekeeper(test.cleaner, nil, test.options); err == nil {
			t.Fatalf("configuration=%+v cleaner=%T was accepted", test.options, test.cleaner)
		}
	}
	if _, err := NewPostgresCatalogCleaner(nil); err == nil {
		t.Fatal("nil PostgreSQL catalog cleaner database was accepted")
	}
}

func waitForCatalogSweep(t *testing.T, arrived <-chan int, want int) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case got := <-arrived:
			if got >= want {
				return
			}
		case <-deadline.C:
			t.Fatalf("catalog sweep %d did not arrive", want)
		}
	}
}
