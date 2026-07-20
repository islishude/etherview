package events

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReplayAndLiveDelivery(t *testing.T) {
	t.Parallel()
	broker := NewBroker(2)
	for index := 0; index < 3; index++ {
		if _, err := broker.Publish("head", map[string]int{"number": index}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	channel, err := broker.Subscribe(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []uint64{2, 3} {
		select {
		case got := <-channel:
			if got.ID != want {
				t.Fatalf("got %d, want %d", got.ID, want)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for replay")
		}
	}
	event, err := broker.Publish("reorg", map[string]int{"depth": 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := <-channel; got.ID != event.ID || got.Type != "reorg" {
		t.Fatalf("got %#v", got)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()
	broker := NewBroker(1)
	if _, err := broker.Publish("unknown", nil); err == nil {
		t.Fatal("expected event type rejection")
	}
	if _, err := broker.Subscribe(context.Background(), "bad"); err == nil {
		t.Fatal("expected Last-Event-ID rejection")
	}
	if _, err := broker.Subscribe(context.Background(), "2"); !errors.Is(err, ErrFutureCursor) {
		t.Fatalf("future cursor error = %v", err)
	}
}

func TestBoundedMemoryReplayRejectsExpiredCursor(t *testing.T) {
	t.Parallel()
	broker := NewBroker(2)
	for index := 0; index < 4; index++ {
		if _, err := broker.Publish("head", map[string]int{"number": index}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := broker.Subscribe(context.Background(), "1"); !errors.Is(err, ErrExpiredCursor) {
		t.Fatalf("expired cursor error = %v", err)
	}
}

type replayFixture struct {
	events []Event
	err    error
}

type mutableReplayFixture struct {
	mu     sync.Mutex
	events []Event
}

func (f *mutableReplayFixture) Replay(_ context.Context, after *uint64, limit int) ([]Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]Event, 0, limit)
	for _, event := range f.events {
		if after == nil || event.ID > *after {
			result = append(result, event)
		}
	}
	return result, nil
}

func (f replayFixture) Replay(_ context.Context, after *uint64, limit int) ([]Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	result := make([]Event, 0, limit)
	for _, event := range f.events {
		if after == nil || event.ID > *after {
			result = append(result, event)
		}
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func TestDurableReplayAndRelayDeliveryAreDeduplicated(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	stored := []Event{
		{ID: 41, Type: "head", Time: now, Data: []byte(`{"number":"40"}`)},
		{ID: 42, Type: "status", Time: now, Data: []byte(`{"ready":true}`)},
	}
	broker, err := NewDurableBroker(8, replayFixture{events: stored})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	channel, err := broker.Subscribe(ctx, "41")
	if err != nil {
		t.Fatal(err)
	}
	if got := <-channel; got.ID != 42 {
		t.Fatalf("replayed event = %+v", got)
	}
	if published, err := broker.PublishStored(stored[1]); err != nil || !published {
		t.Fatalf("first relay publish = %t, err=%v", published, err)
	}
	select {
	case duplicate := <-channel:
		t.Fatalf("replayed subscriber received duplicate %+v", duplicate)
	case <-time.After(20 * time.Millisecond):
	}
	next := Event{ID: 43, Type: "reorg", Time: now, Data: []byte(`{"detached_count":"1"}`)}
	if published, err := broker.PublishStored(next); err != nil || !published {
		t.Fatalf("new publish = %t, err=%v", published, err)
	}
	if got := <-channel; got.ID != 43 {
		t.Fatalf("live event = %+v", got)
	}
}

func TestDurableReplayHidesBackendDetails(t *testing.T) {
	t.Parallel()
	broker, err := NewDurableBroker(8, replayFixture{err: errors.New("postgres://user:secret@example")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Subscribe(context.Background(), ""); !errors.Is(err, ErrReplayUnavailable) || err.Error() != ErrReplayUnavailable.Error() {
		t.Fatalf("replay error = %v", err)
	}
}

func TestDurableReplayInvalidatesCacheBeforeExposingStoredEvent(t *testing.T) {
	t.Parallel()
	event := Event{ID: 7, Type: "head", Time: time.Now().UTC(), Data: []byte(`{"number":"7"}`)}
	fail := true
	invalidations := 0
	broker, err := NewDurableBroker(8, replayFixture{events: []Event{event}}, CacheInvalidatorFunc(func(_ context.Context, got Event) error {
		invalidations++
		if got.ID != event.ID {
			t.Fatalf("invalidated event = %+v", got)
		}
		if fail {
			return errors.New("cache safety cannot be established")
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Subscribe(context.Background(), "6"); !errors.Is(err, ErrReplayUnavailable) {
		t.Fatalf("failed invalidation replay error = %v", err)
	}
	fail = false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := broker.Subscribe(ctx, "6")
	if err != nil {
		t.Fatal(err)
	}
	if got := <-stream; got.ID != event.ID {
		t.Fatalf("replayed event = %+v", got)
	}
	if invalidations != 2 {
		t.Fatalf("cache invalidations = %d", invalidations)
	}
}

func TestReplayForNewSubscriberDoesNotSuppressExistingLiveSubscriber(t *testing.T) {
	t.Parallel()
	source := &mutableReplayFixture{}
	broker, err := NewDurableBroker(8, source)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, err := broker.Subscribe(ctx, "41")
	if err != nil {
		t.Fatal(err)
	}
	event := Event{ID: 42, Type: "head", Time: time.Now().UTC(), Data: []byte(`{"number":"42"}`)}
	source.mu.Lock()
	source.events = append(source.events, event)
	source.mu.Unlock()
	second, err := broker.Subscribe(ctx, "41")
	if err != nil {
		t.Fatal(err)
	}
	if got := <-second; got.ID != 42 {
		t.Fatalf("second subscriber replay = %+v", got)
	}
	if published, err := broker.PublishStored(event); err != nil || !published {
		t.Fatalf("relay publish = %t, err=%v", published, err)
	}
	select {
	case got := <-first:
		if got.ID != 42 {
			t.Fatalf("first subscriber event = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("existing subscriber missed event replayed to a new subscriber")
	}
	select {
	case duplicate := <-second:
		t.Fatalf("second subscriber received duplicate event %+v", duplicate)
	case <-time.After(20 * time.Millisecond):
	}
}

type relayFixture struct {
	mu     sync.Mutex
	events []Event
}

type failingPollFixture struct{ called chan struct{} }

func (fixture failingPollFixture) Poll(context.Context, uint64, int) ([]Event, error) {
	select {
	case fixture.called <- struct{}{}:
	default:
	}
	return nil, errors.New("postgres://user:secret@database.example.invalid/runtime")
}

type relayLogHandler struct{ records chan map[string]string }

func (handler *relayLogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (handler *relayLogHandler) Handle(_ context.Context, record slog.Record) error {
	attributes := make(map[string]string)
	record.Attrs(func(attribute slog.Attr) bool {
		attributes[attribute.Key] = attribute.Value.Resolve().String()
		return true
	})
	handler.records <- attributes
	return nil
}
func (handler *relayLogHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }
func (handler *relayLogHandler) WithGroup(string) slog.Handler      { return handler }

func (f *relayFixture) Poll(_ context.Context, after uint64, limit int) ([]Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]Event, 0, limit)
	for _, event := range f.events {
		if event.ID > after {
			result = append(result, event)
		}
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func TestRelayWakeAndContextShutdown(t *testing.T) {
	t.Parallel()
	wake := make(chan struct{}, 1)
	source := &relayFixture{}
	broker := NewBroker(8)
	relay, err := NewRelay(source, broker, RelayOptions{PollInterval: time.Hour, Wake: wake})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	channel, err := broker.Subscribe(ctx, "0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- relay.Run(ctx) }()
	source.mu.Lock()
	source.events = append(source.events, Event{
		ID: 1, Type: "head", Time: time.Now().UTC(), Data: []byte(`{"number":"0"}`),
	})
	source.mu.Unlock()
	wake <- struct{}{}
	select {
	case event := <-channel:
		if event.ID != 1 {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("relay did not publish after wake")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("relay shutdown error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("relay did not stop after context cancellation")
	}
}

func TestRelayInvalidatesCacheBeforePublishAndRetriesWithoutAdvancing(t *testing.T) {
	t.Parallel()
	event := Event{ID: 1, Type: "reorg", Time: time.Now().UTC(), Data: []byte(`{"depth":"1"}`)}
	source := &relayFixture{events: []Event{event}}
	invalidations := 0
	broker, err := NewDurableBroker(8, replayFixture{}, CacheInvalidatorFunc(func(_ context.Context, got Event) error {
		invalidations++
		if got.ID != event.ID || got.Type != event.Type {
			t.Fatalf("invalidated event = %+v", got)
		}
		if invalidations == 1 {
			return errors.New("cache safety cannot be established")
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := broker.Subscribe(ctx, "0")
	if err != nil {
		t.Fatal(err)
	}
	relay, err := NewRelay(source, broker, RelayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := relay.drain(ctx); err == nil || !strings.Contains(err.Error(), "invalidate query cache") {
		t.Fatalf("first drain error = %v", err)
	}
	if relay.after != 0 {
		t.Fatalf("relay advanced past failed invalidation: %d", relay.after)
	}
	select {
	case got := <-stream:
		t.Fatalf("event published before cache invalidation: %+v", got)
	default:
	}
	if err := relay.drain(ctx); err != nil {
		t.Fatal(err)
	}
	if relay.after != event.ID || invalidations != 2 {
		t.Fatalf("relay after=%d invalidations=%d", relay.after, invalidations)
	}
	select {
	case got := <-stream:
		if got.ID != event.ID || got.Type != event.Type {
			t.Fatalf("published event = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("event was not published after cache invalidation succeeded")
	}
}

func TestRelayDoesNotLogCredentialBearingDatabaseError(t *testing.T) {
	t.Parallel()
	called := make(chan struct{}, 1)
	logs := &relayLogHandler{records: make(chan map[string]string, 1)}
	relay, err := NewRelay(failingPollFixture{called: called}, NewBroker(8), RelayOptions{
		PollInterval: time.Hour, Logger: slog.New(logs),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- relay.Run(ctx) }()
	select {
	case attributes := <-logs.records:
		cancel()
		if attributes["error_type"] == "" {
			t.Fatalf("relay log attributes = %+v", attributes)
		}
		for key, value := range attributes {
			if strings.Contains(value, "secret") || strings.Contains(value, "example.invalid") {
				t.Fatalf("credential-bearing database error leaked through %s=%q", key, value)
			}
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("timed out waiting for sanitized relay log")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("relay shutdown error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime event relay did not stop")
	}
}
