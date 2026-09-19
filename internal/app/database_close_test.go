package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type waitingDatabaseCloser struct {
	started chan struct{}
	release chan struct{}
	closed  atomic.Bool
}

func (closer *waitingDatabaseCloser) Close() {
	close(closer.started)
	<-closer.release
	closer.closed.Store(true)
}

func TestDatabaseCloseUsesOneBudgetAndDoesNotWaitForStuckBorrowers(t *testing.T) {
	t.Parallel()
	writer := &waitingDatabaseCloser{started: make(chan struct{}), release: make(chan struct{})}
	reader := &waitingDatabaseCloser{started: make(chan struct{}), release: make(chan struct{})}
	defer close(writer.release)
	defer close(reader.release)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- closeDatabasePools(ctx, writer, reader) }()
	for _, started := range []chan struct{}{writer.started, reader.started} {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("pool close was serialized behind a stuck borrower")
		}
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, errDatabaseCloseTimeout) || !errors.Is(err, context.Canceled) {
			t.Fatalf("close error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pool close ignored exhausted shutdown budget")
	}
	if writer.closed.Load() || reader.closed.Load() {
		t.Fatal("blocked close was reported complete")
	}
}
