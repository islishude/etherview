// Package events provides a low-latency in-process fanout for events whose
// identity and replay window are durable in PostgreSQL.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

var (
	ErrInvalidCursor     = errors.New("invalid Last-Event-ID")
	ErrExpiredCursor     = errors.New("Last-Event-ID is outside the retained replay window")
	ErrFutureCursor      = errors.New("Last-Event-ID is ahead of the event stream")
	ErrReplayUnavailable = errors.New("event replay is temporarily unavailable")
)

const maxEventPayloadBytes = 8192

const defaultReplayConcurrency = 4

type Event struct {
	ID   uint64          `json:"id"`
	Type string          `json:"type"`
	Time time.Time       `json:"time"`
	Data json.RawMessage `json:"data"`
}

// ReplaySource returns an ordered, bounded PostgreSQL snapshot. A nil cursor
// requests the most recent window for a new subscriber; a non-nil cursor must
// be rejected when it is older than retention or ahead of the durable stream.
type ReplaySource interface {
	Replay(context.Context, *uint64, int) ([]Event, error)
}

// CacheInvalidator evicts query-cache entries affected by a committed runtime
// event. Implementations must be idempotent because both durable replay and
// relay polling may observe the same event. An adapter backed by optional
// infrastructure must disable or bypass that cache when its backend is down;
// returning an error means the process cannot yet prove that cached reads are
// safe to expose.
type CacheInvalidator interface {
	Invalidate(context.Context, Event) error
}

type CacheInvalidatorFunc func(context.Context, Event) error

func (function CacheInvalidatorFunc) Invalidate(ctx context.Context, event Event) error {
	return function(ctx, event)
}

type Broker struct {
	mu                sync.Mutex
	invalidationMu    sync.Mutex
	nextID            uint64
	lastPublished     uint64
	invalidated       map[uint64]struct{}
	invalidationOrder []uint64
	replayLimit       int
	replay            []Event
	subscribers       map[uint64]subscriber
	nextSubID         uint64
	replaySlots       chan struct{}
	source            ReplaySource
	invalidator       CacheInvalidator
}

type subscriber struct {
	channel   chan Event
	after     uint64
	preparing bool
	pending   []Event
	overflow  bool
}

func NewBroker(replayLimit int) *Broker {
	if replayLimit <= 0 {
		replayLimit = 128
	}
	return &Broker{
		replayLimit: replayLimit, subscribers: make(map[uint64]subscriber),
		invalidated: make(map[uint64]struct{}),
	}
}

func NewDurableBroker(replayLimit int, source ReplaySource, invalidators ...CacheInvalidator) (*Broker, error) {
	if source == nil {
		return nil, errors.New("durable event replay source is nil")
	}
	if len(invalidators) > 1 {
		return nil, errors.New("durable event broker accepts at most one cache invalidator")
	}
	broker := NewBroker(replayLimit)
	broker.source = source
	broker.replaySlots = make(chan struct{}, defaultReplayConcurrency)
	if len(invalidators) == 1 {
		if invalidators[0] == nil {
			return nil, errors.New("durable event cache invalidator is nil")
		}
		broker.invalidator = invalidators[0]
	}
	return broker, nil
}

func (b *Broker) Publish(eventType string, data any) (Event, error) {
	if b == nil {
		return Event{}, errors.New("event broker is nil")
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return Event{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.source != nil {
		return Event{}, errors.New("durable event broker rejects non-durable publish")
	}
	b.nextID++
	event := Event{ID: b.nextID, Type: eventType, Time: time.Now().UTC(), Data: payload}
	if err := validateEvent(event); err != nil {
		b.nextID--
		return Event{}, err
	}
	b.lastPublished = event.ID
	b.publishLocked(event)
	return event, nil
}

// PublishStored fans out a committed PostgreSQL event. Duplicate and stale
// deliveries are ignored, which makes simultaneous wakeups and polling safe.
func (b *Broker) PublishStored(event Event) (bool, error) {
	return b.PublishStoredContext(context.Background(), event)
}

// PublishStoredContext invalidates local query caches before making a durable
// event observable to live subscribers. The relay advances its cursor only
// after this method succeeds.
func (b *Broker) PublishStoredContext(ctx context.Context, event Event) (bool, error) {
	if b == nil {
		return false, errors.New("event broker is nil")
	}
	if ctx == nil {
		return false, errors.New("event publish context is nil")
	}
	if err := validateEvent(event); err != nil {
		return false, err
	}
	if err := b.invalidate(ctx, event); err != nil {
		return false, fmt.Errorf("invalidate query cache for runtime event %d: %w", event.ID, err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if event.ID <= b.lastPublished {
		return false, nil
	}
	b.lastPublished = event.ID
	if event.ID > b.nextID {
		b.nextID = event.ID
	}
	b.publishLocked(event)
	return true, nil
}

func (b *Broker) Subscribe(ctx context.Context, afterID string) (<-chan Event, error) {
	if b == nil {
		return nil, errors.New("event broker is nil")
	}
	if ctx == nil {
		return nil, errors.New("event subscription context is nil")
	}
	var after *uint64
	if afterID != "" {
		parsed, err := strconv.ParseUint(afterID, 10, 64)
		if err != nil {
			return nil, ErrInvalidCursor
		}
		after = &parsed
	}
	if b.source != nil {
		return b.subscribeDurable(ctx, after)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.validateMemoryCursorLocked(after); err != nil {
		return nil, err
	}
	return b.registerLocked(ctx, after, b.memoryReplayLocked(after)), nil
}

func (b *Broker) subscribeDurable(ctx context.Context, after *uint64) (<-chan Event, error) {
	if !b.acquireReplaySlot(ctx) {
		return nil, ErrReplayUnavailable
	}
	defer b.releaseReplaySlot()

	b.mu.Lock()
	b.nextSubID++
	id := b.nextSubID
	subscriberAfter := uint64(0)
	if after != nil {
		subscriberAfter = *after
	}
	b.subscribers[id] = subscriber{
		after: subscriberAfter, preparing: true,
		pending: make([]Event, 0, min(b.replayLimit, 16)),
	}
	b.mu.Unlock()

	registered := true
	defer func() {
		if !registered {
			b.removePreparingSubscriber(id)
		}
	}()

	replay, err := b.source.Replay(ctx, after, b.replayLimit)
	if err != nil {
		registered = false
		if errors.Is(err, ErrInvalidCursor) || errors.Is(err, ErrExpiredCursor) || errors.Is(err, ErrFutureCursor) {
			return nil, err
		}
		return nil, ErrReplayUnavailable
	}
	for index, event := range replay {
		if err := validateEvent(event); err != nil {
			registered = false
			return nil, fmt.Errorf("invalid durable event: %w", err)
		}
		if index > 0 && replay[index-1].ID >= event.ID {
			registered = false
			return nil, errors.New("durable event replay is not strictly ordered")
		}
		if err := b.invalidate(ctx, event); err != nil {
			registered = false
			return nil, ErrReplayUnavailable
		}
	}

	b.mu.Lock()
	current, exists := b.subscribers[id]
	if !exists || !current.preparing || current.overflow {
		if exists {
			delete(b.subscribers, id)
		}
		b.mu.Unlock()
		registered = false
		return nil, ErrReplayUnavailable
	}
	for _, event := range replay {
		b.rememberLocked(event)
	}
	delivery := mergeEvents(replay, current.pending)
	channel := make(chan Event, len(delivery)+16)
	current.channel = channel
	current.preparing = false
	current.pending = nil
	for _, event := range delivery {
		if event.ID <= current.after {
			continue
		}
		channel <- event
		current.after = event.ID
	}
	if after == nil && len(delivery) == 0 {
		current.after = b.nextID
	}
	b.subscribers[id] = current
	b.mu.Unlock()
	registered = true
	b.removeOnCancellation(ctx, id, channel)
	return channel, nil
}

func (b *Broker) registerLocked(ctx context.Context, after *uint64, replay []Event) <-chan Event {
	b.nextSubID++
	id := b.nextSubID
	channel := make(chan Event, len(replay)+16)
	subscriberAfter := uint64(0)
	if after != nil {
		subscriberAfter = *after
	}
	for _, event := range replay {
		channel <- event
		subscriberAfter = event.ID
	}
	if after == nil && len(replay) == 0 {
		subscriberAfter = b.nextID
	}
	b.subscribers[id] = subscriber{channel: channel, after: subscriberAfter}
	b.removeOnCancellation(ctx, id, channel)
	return channel
}

func (b *Broker) removeOnCancellation(ctx context.Context, id uint64, channel chan Event) {
	go func() {
		<-ctx.Done()
		b.mu.Lock()
		if current, ok := b.subscribers[id]; ok && current.channel == channel {
			delete(b.subscribers, id)
			close(channel)
		}
		b.mu.Unlock()
	}()
}

func (b *Broker) invalidate(ctx context.Context, event Event) error {
	if b.invalidator == nil {
		return nil
	}
	b.invalidationMu.Lock()
	defer b.invalidationMu.Unlock()
	b.mu.Lock()
	_, specificallyInvalidated := b.invalidated[event.ID]
	alreadyInvalidated := event.ID <= b.lastPublished || specificallyInvalidated
	b.mu.Unlock()
	if alreadyInvalidated {
		return nil
	}
	if err := b.invalidator.Invalidate(ctx, event); err != nil {
		return err
	}
	b.mu.Lock()
	if _, exists := b.invalidated[event.ID]; !exists {
		b.invalidated[event.ID] = struct{}{}
		b.invalidationOrder = append(b.invalidationOrder, event.ID)
		if len(b.invalidationOrder) > b.replayLimit {
			oldest := b.invalidationOrder[0]
			b.invalidationOrder = b.invalidationOrder[1:]
			delete(b.invalidated, oldest)
		}
	}
	b.mu.Unlock()
	return nil
}

func (b *Broker) publishLocked(event Event) {
	b.rememberLocked(event)
	for id, subscriber := range b.subscribers {
		if event.ID <= subscriber.after {
			continue
		}
		if subscriber.preparing {
			if len(subscriber.pending) >= b.replayLimit {
				subscriber.overflow = true
			} else {
				subscriber.pending = append(subscriber.pending, event)
			}
			b.subscribers[id] = subscriber
			continue
		}
		select {
		case subscriber.channel <- event:
			subscriber.after = event.ID
			b.subscribers[id] = subscriber
		default:
			close(subscriber.channel)
			delete(b.subscribers, id)
		}
	}
}

func (b *Broker) acquireReplaySlot(ctx context.Context) bool {
	if b.replaySlots == nil {
		return true
	}
	select {
	case b.replaySlots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (b *Broker) releaseReplaySlot() {
	if b.replaySlots != nil {
		<-b.replaySlots
	}
}

func (b *Broker) removePreparingSubscriber(id uint64) {
	b.mu.Lock()
	if current, ok := b.subscribers[id]; ok && current.preparing {
		delete(b.subscribers, id)
	}
	b.mu.Unlock()
}

func mergeEvents(replay, live []Event) []Event {
	merged := make([]Event, 0, len(replay)+len(live))
	left, right := 0, 0
	for left < len(replay) || right < len(live) {
		switch {
		case right >= len(live):
			merged = append(merged, replay[left])
			left++
		case left >= len(replay):
			merged = append(merged, live[right])
			right++
		case replay[left].ID < live[right].ID:
			merged = append(merged, replay[left])
			left++
		case live[right].ID < replay[left].ID:
			merged = append(merged, live[right])
			right++
		default:
			merged = append(merged, replay[left])
			left++
			right++
		}
	}
	return merged
}

func (b *Broker) rememberLocked(event Event) {
	if event.ID > b.nextID {
		b.nextID = event.ID
	}
	if len(b.replay) == 0 || event.ID > b.replay[len(b.replay)-1].ID {
		copy := event
		copy.Data = append(json.RawMessage(nil), event.Data...)
		b.replay = append(b.replay, copy)
		if len(b.replay) > b.replayLimit {
			b.replay = append([]Event(nil), b.replay[len(b.replay)-b.replayLimit:]...)
		}
	}
}

func (b *Broker) validateMemoryCursorLocked(after *uint64) error {
	if after == nil {
		return nil
	}
	if *after > b.nextID {
		return ErrFutureCursor
	}
	if len(b.replay) > 0 {
		oldest := b.replay[0].ID
		if oldest > 0 && *after < oldest-1 {
			return ErrExpiredCursor
		}
	}
	return nil
}

func (b *Broker) memoryReplayLocked(after *uint64) []Event {
	replay := make([]Event, 0, len(b.replay))
	for _, event := range b.replay {
		if after == nil || event.ID > *after {
			replay = append(replay, event)
		}
	}
	return replay
}

func validateEvent(event Event) error {
	if event.ID == 0 {
		return errors.New("event ID must be greater than zero")
	}
	if event.Type != "head" && event.Type != "reorg" && event.Type != "status" {
		return errors.New("unsupported live event type")
	}
	if len(event.Data) == 0 || len(event.Data) > maxEventPayloadBytes || !json.Valid(event.Data) {
		return errors.New("event payload is not valid bounded JSON")
	}
	if event.Time.IsZero() {
		return errors.New("event timestamp is required")
	}
	return nil
}
