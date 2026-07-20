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
	mu            sync.Mutex
	nextID        uint64
	lastPublished uint64
	replayLimit   int
	replay        []Event
	subscribers   map[uint64]subscriber
	nextSubID     uint64
	source        ReplaySource
	invalidator   CacheInvalidator
}

type subscriber struct {
	channel chan Event
	after   uint64
}

func NewBroker(replayLimit int) *Broker {
	if replayLimit <= 0 {
		replayLimit = 128
	}
	return &Broker{replayLimit: replayLimit, subscribers: make(map[uint64]subscriber)}
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
	b.mu.Lock()
	defer b.mu.Unlock()
	var replay []Event
	if b.source != nil {
		var err error
		replay, err = b.source.Replay(ctx, after, b.replayLimit)
		if err != nil {
			if errors.Is(err, ErrInvalidCursor) || errors.Is(err, ErrExpiredCursor) || errors.Is(err, ErrFutureCursor) {
				return nil, err
			}
			return nil, ErrReplayUnavailable
		}
		for _, event := range replay {
			if err := validateEvent(event); err != nil {
				return nil, fmt.Errorf("invalid durable event: %w", err)
			}
			if err := b.invalidate(ctx, event); err != nil {
				return nil, ErrReplayUnavailable
			}
			b.rememberLocked(event)
		}
	} else {
		if err := b.validateMemoryCursorLocked(after); err != nil {
			return nil, err
		}
		replay = b.memoryReplayLocked(after)
	}
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
	go func() {
		<-ctx.Done()
		b.mu.Lock()
		if current, ok := b.subscribers[id]; ok && current.channel == channel {
			delete(b.subscribers, id)
			close(channel)
		}
		b.mu.Unlock()
	}()
	return channel, nil
}

func (b *Broker) invalidate(ctx context.Context, event Event) error {
	if b.invalidator == nil {
		return nil
	}
	return b.invalidator.Invalidate(ctx, event)
}

func (b *Broker) publishLocked(event Event) {
	b.rememberLocked(event)
	for id, subscriber := range b.subscribers {
		if event.ID <= subscriber.after {
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
