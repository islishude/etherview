package httpapi

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/events"
)

type homeSnapshotReaderFixture struct {
	mu     sync.Mutex
	states []HomeSnapshotState
	errs   []error
	calls  chan struct{}
}

func (fixture *homeSnapshotReaderFixture) HomeSnapshot(context.Context) (HomeSnapshotState, error) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	select {
	case fixture.calls <- struct{}{}:
	default:
	}
	if len(fixture.errs) > 0 {
		err := fixture.errs[0]
		fixture.errs = fixture.errs[1:]
		if err != nil {
			return HomeSnapshotState{}, err
		}
	}
	if len(fixture.states) == 0 {
		return HomeSnapshotState{}, errors.New("unexpected home snapshot read")
	}
	state := fixture.states[0]
	if len(fixture.states) > 1 {
		fixture.states = fixture.states[1:]
	}
	return state, nil
}

func TestHomeFeedPublishesCompleteSnapshotsAndDisconnectsSlowSubscribers(t *testing.T) {
	t.Parallel()
	broker := events.NewBroker(8)
	fixture := &homeSnapshotReaderFixture{
		states: []HomeSnapshotState{
			homeState(0, "10"),
			homeState(1, "11"),
		},
		calls: make(chan struct{}, 4),
	}
	feed, err := NewHomeFeed(fixture, broker, HomeFeedOptions{
		ChainID: 1, InitialBackoff: time.Millisecond, MaximumBackoff: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- feed.Run(ctx) }()
	waitHomeRead(t, fixture.calls)

	activeCtx, cancelActive := context.WithCancel(t.Context())
	defer cancelActive()
	active := subscribeHomeEventually(t, feed, activeCtx)
	slowCtx, cancelSlow := context.WithCancel(t.Context())
	defer cancelSlow()
	slow := subscribeHomeEventually(t, feed, slowCtx)
	if initial := receiveHomePublication(t, active); initial.EventID != 0 ||
		initial.Data.Status.LatestBlock != "10" {
		t.Fatalf("initial publication = %+v", initial)
	}
	// Leave the slow subscriber's initial publication buffered so the next
	// complete snapshot disconnects it rather than blocking every subscriber.
	if _, err := broker.Publish("head", map[string]string{"number": "11"}); err != nil {
		t.Fatal(err)
	}
	updated := receiveHomePublication(t, active)
	if updated.EventID != 1 || updated.Data.Status.LatestBlock != "11" ||
		len(updated.Data.Blocks) != 1 || len(updated.Data.Transactions) != 1 {
		t.Fatalf("updated publication = %+v", updated)
	}
	// Receiving from the active channel proves that publish reached that
	// subscriber, but map iteration may still be processing the slow one. Wait
	// for the complete fanout while its initial publication remains buffered.
	feed.mu.Lock()
	remainingSubscribers := len(feed.subscribers)
	feed.mu.Unlock()
	if remainingSubscribers != 1 {
		t.Fatalf("home feed subscribers after fanout = %d, want 1", remainingSubscribers)
	}
	if first, open := <-slow; !open || first.EventID != 0 {
		t.Fatalf("slow initial publication = %+v open=%t", first, open)
	}
	if _, open := <-slow; open {
		t.Fatal("slow subscriber remained connected")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("feed stopped with %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("home feed did not stop")
	}
}

func TestHomeFeedRetriesWithoutPublishingFailedRefresh(t *testing.T) {
	t.Parallel()
	broker := events.NewBroker(8)
	fixture := &homeSnapshotReaderFixture{
		states: []HomeSnapshotState{homeState(0, "10"), homeState(1, "11")},
		errs:   []error{errors.New("postgres unavailable"), nil, errors.New("refresh failed"), nil},
		calls:  make(chan struct{}, 8),
	}
	feed, err := NewHomeFeed(fixture, broker, HomeFeedOptions{
		ChainID: 1, InitialBackoff: 30 * time.Millisecond, MaximumBackoff: 30 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = feed.Run(ctx) }()
	waitHomeRead(t, fixture.calls)
	if _, err := feed.Subscribe(t.Context()); !errors.Is(err, ErrHomeSnapshotUnavailable) {
		t.Fatalf("subscribe before first snapshot error = %v", err)
	}
	channel := subscribeHomeEventually(t, feed, t.Context())
	waitHomeRead(t, fixture.calls)
	if initial := receiveHomePublication(t, channel); initial.EventID != 0 {
		t.Fatalf("initial publication = %+v", initial)
	}
	if _, err := broker.Publish("status", map[string]bool{"ready": true}); err != nil {
		t.Fatal(err)
	}
	waitHomeRead(t, fixture.calls)
	select {
	case unexpected := <-channel:
		t.Fatalf("failed refresh published %+v", unexpected)
	case <-time.After(10 * time.Millisecond):
	}
	waitHomeRead(t, fixture.calls)
	if updated := receiveHomePublication(t, channel); updated.EventID != 1 ||
		updated.Data.Status.LatestBlock != "11" {
		t.Fatalf("retried publication = %+v", updated)
	}
}

type homeSnapshotDuringRefreshFixture struct {
	mu           sync.Mutex
	call         int
	secondStart  chan struct{}
	secondFinish chan struct{}
	thirdStart   chan struct{}
	thirdFinish  chan struct{}
}

func (fixture *homeSnapshotDuringRefreshFixture) HomeSnapshot(
	ctx context.Context,
) (HomeSnapshotState, error) {
	fixture.mu.Lock()
	fixture.call++
	call := fixture.call
	fixture.mu.Unlock()
	switch call {
	case 1:
		return homeState(0, "10"), nil
	case 2:
		close(fixture.secondStart)
		select {
		case <-fixture.secondFinish:
			return homeState(1, "11"), nil
		case <-ctx.Done():
			return HomeSnapshotState{}, ctx.Err()
		}
	case 3:
		close(fixture.thirdStart)
		select {
		case <-fixture.thirdFinish:
			return homeState(2, "12"), nil
		case <-ctx.Done():
			return HomeSnapshotState{}, ctx.Err()
		}
	default:
		return HomeSnapshotState{}, errors.New("unexpected home snapshot read")
	}
}

func TestHomeFeedRefreshesAgainWhenAnEventArrivesDuringAQuery(t *testing.T) {
	t.Parallel()
	broker := events.NewBroker(8)
	fixture := &homeSnapshotDuringRefreshFixture{
		secondStart: make(chan struct{}), secondFinish: make(chan struct{}),
		thirdStart: make(chan struct{}), thirdFinish: make(chan struct{}),
	}
	feed, err := NewHomeFeed(fixture, broker, HomeFeedOptions{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = feed.Run(ctx) }()
	channel := subscribeHomeEventually(t, feed, t.Context())
	if initial := receiveHomePublication(t, channel); initial.EventID != 0 {
		t.Fatalf("initial publication = %+v", initial)
	}

	if _, err := broker.Publish("head", map[string]string{"number": "11"}); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, fixture.secondStart, "second home snapshot did not start")
	if _, err := broker.Publish("reorg", map[string]string{"number": "12"}); err != nil {
		t.Fatal(err)
	}
	close(fixture.secondFinish)
	if second := receiveHomePublication(t, channel); second.EventID != 1 {
		t.Fatalf("second publication = %+v", second)
	}
	waitSignal(t, fixture.thirdStart, "queued event did not trigger another snapshot")
	close(fixture.thirdFinish)
	if third := receiveHomePublication(t, channel); third.EventID != 2 ||
		third.Data.Status.LatestBlock != "12" {
		t.Fatalf("third publication = %+v", third)
	}
}

func homeState(eventID uint64, latest gen.Quantity) HomeSnapshotState {
	return HomeSnapshotState{
		EventID: eventID,
		Status: StatusSnapshot{
			LatestBlock: eventID + 10, IndexedBlock: eventID + 10,
			HighestCoveredBlock: eventID + 10, HighestCoveredKnown: true,
			BackfillComplete: true, CoverageStart: 0, CoverageEnd: eventID + 10,
			CoreReady: true, Completeness: gen.Completeness{Core: gen.StageStateComplete},
		},
		Blocks: []gen.Block{{Number: latest}},
		Transactions: []gen.Transaction{{
			Hash: "0x0000000000000000000000000000000000000000000000000000000000000001",
		}},
	}
}

func waitSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func waitHomeRead(t *testing.T, calls <-chan struct{}) {
	t.Helper()
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("home snapshot reader was not called")
	}
}

func receiveHomePublication(t *testing.T, channel <-chan HomePublication) HomePublication {
	t.Helper()
	select {
	case publication, open := <-channel:
		if !open {
			t.Fatal("home publication channel closed")
		}
		return publication
	case <-time.After(time.Second):
		t.Fatal("home publication was not received")
		return HomePublication{}
	}
}

func subscribeHomeEventually(
	t *testing.T,
	feed *HomeFeed,
	ctx context.Context,
) <-chan HomePublication {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		channel, err := feed.Subscribe(ctx)
		if err == nil {
			return channel
		}
		if !errors.Is(err, ErrHomeSnapshotUnavailable) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("home snapshot did not become available")
		}
		time.Sleep(time.Millisecond)
	}
}
