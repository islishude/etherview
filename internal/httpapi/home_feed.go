package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/events"
	"github.com/islishude/etherview/internal/publicquery"
)

const (
	homeSnapshotItemLimit = 6
	maxHomeSnapshotBytes  = 2 << 20
)

var ErrHomeSnapshotUnavailable = errors.New("home snapshot is temporarily unavailable")

type HomeSnapshotState = publicquery.HomeSnapshotState
type HomeSnapshotReader = publicquery.HomeSnapshotReader

type HomePublication struct {
	EventID       uint64
	Data          HomeSnapshot
	EncodedData   json.RawMessage
	CoverageStart gen.Quantity
	CoverageEnd   gen.Quantity
}

type HomeSnapshot = gen.HomeSnapshot

type HomeSnapshotSource interface {
	Subscribe(context.Context) (<-chan HomePublication, error)
}

type HomeFeedOptions struct {
	ChainID        uint64
	InitialBackoff time.Duration
	MaximumBackoff time.Duration
	Logger         *slog.Logger
}

type HomeFeed struct {
	reader         HomeSnapshotReader
	events         *events.Broker
	chainID        uint64
	initialBackoff time.Duration
	maximumBackoff time.Duration
	logger         *slog.Logger

	mu          sync.Mutex
	current     *HomePublication
	subscribers map[uint64]chan HomePublication
	nextID      uint64
}

func NewHomeFeed(
	reader HomeSnapshotReader,
	broker *events.Broker,
	options HomeFeedOptions,
) (*HomeFeed, error) {
	if reader == nil || broker == nil {
		return nil, errors.New("home snapshot reader and event broker are required")
	}
	if options.ChainID == 0 {
		return nil, errors.New("home snapshot chain ID must be greater than zero")
	}
	initialBackoff := options.InitialBackoff
	if initialBackoff <= 0 {
		initialBackoff = 100 * time.Millisecond
	}
	maximumBackoff := options.MaximumBackoff
	if maximumBackoff <= 0 {
		maximumBackoff = 5 * time.Second
	}
	if maximumBackoff < initialBackoff {
		return nil, errors.New("home snapshot maximum backoff is below initial backoff")
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &HomeFeed{
		reader: reader, events: broker, chainID: options.ChainID,
		initialBackoff: initialBackoff, maximumBackoff: maximumBackoff,
		logger: logger, subscribers: make(map[uint64]chan HomePublication),
	}, nil
}

func (*HomeFeed) Name() string { return "home-snapshot-feed" }

func (feed *HomeFeed) Run(ctx context.Context) error {
	if feed == nil || feed.reader == nil || feed.events == nil {
		return errors.New("home snapshot feed is not fully configured")
	}
	defer feed.closeSubscribers()
	backoff := feed.initialBackoff
	for {
		err := feed.runConnected(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		feed.logger.WarnContext(
			ctx,
			"home snapshot refresh failed; retrying",
			"event", "home_snapshot_refresh_failed",
			"component", feed.Name(),
			"error_code", "home_snapshot_refresh_failed",
			"error_type", fmt.Sprintf("%T", err),
			"retry_in_ms", backoff.Milliseconds(),
		)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
		backoff = min(backoff*2, feed.maximumBackoff)
	}
}

func (feed *HomeFeed) runConnected(ctx context.Context) error {
	state, err := feed.reader.HomeSnapshot(ctx)
	if err != nil {
		return err
	}
	publication, err := feed.publication(state)
	if err != nil {
		return err
	}
	feed.publish(publication)

	subscriptionCtx, cancelSubscription := context.WithCancel(ctx)
	defer cancelSubscription()
	channel, err := feed.events.Subscribe(subscriptionCtx, strconv.FormatUint(state.EventID, 10))
	if err != nil {
		return err
	}
	for {
		event, open := <-channel
		if !open {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("home snapshot event subscription closed")
		}
		if event.ID <= state.EventID {
			continue
		}
		for {
			select {
			case queued, queuedOpen := <-channel:
				if !queuedOpen {
					return errors.New("home snapshot event subscription closed")
				}
				if queued.ID > event.ID {
					event = queued
				}
			default:
				goto refresh
			}
		}
	refresh:
		state, err = feed.reader.HomeSnapshot(ctx)
		if err != nil {
			return err
		}
		if state.EventID < event.ID {
			return errors.New("home snapshot event identity is behind its trigger")
		}
		publication, err = feed.publication(state)
		if err != nil {
			return err
		}
		feed.publish(publication)
	}
}

func (feed *HomeFeed) publication(state HomeSnapshotState) (HomePublication, error) {
	if len(state.Blocks) > homeSnapshotItemLimit ||
		len(state.Transactions) > homeSnapshotItemLimit {
		return HomePublication{}, errors.New("home snapshot exceeds its item limit")
	}
	data := gen.HomeSnapshot{
		Status:       statusModel(feed.chainID, state.Status),
		Blocks:       nonNilBlocks(state.Blocks),
		Transactions: nonNilTransactions(state.Transactions),
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return HomePublication{}, fmt.Errorf("encode home snapshot: %w", err)
	}
	if len(encoded) > maxHomeSnapshotBytes {
		return HomePublication{}, errors.New("home snapshot exceeds its encoded size limit")
	}
	return HomePublication{
		EventID: state.EventID, Data: data, EncodedData: encoded,
		CoverageStart: quantity(state.Status.CoverageStart),
		CoverageEnd:   quantity(state.Status.CoverageEnd),
	}, nil
}

func (feed *HomeFeed) Subscribe(ctx context.Context) (<-chan HomePublication, error) {
	if feed == nil || ctx == nil {
		return nil, ErrHomeSnapshotUnavailable
	}
	feed.mu.Lock()
	if feed.current == nil {
		feed.mu.Unlock()
		return nil, ErrHomeSnapshotUnavailable
	}
	feed.nextID++
	id := feed.nextID
	channel := make(chan HomePublication, 1)
	channel <- *feed.current
	feed.subscribers[id] = channel
	feed.mu.Unlock()

	go func() {
		<-ctx.Done()
		feed.mu.Lock()
		if current, ok := feed.subscribers[id]; ok && current == channel {
			delete(feed.subscribers, id)
			close(channel)
		}
		feed.mu.Unlock()
	}()
	return channel, nil
}

func (feed *HomeFeed) publish(publication HomePublication) {
	feed.mu.Lock()
	defer feed.mu.Unlock()
	if feed.current != nil && publication.EventID <= feed.current.EventID {
		return
	}
	copy := publication
	feed.current = &copy
	for id, subscriber := range feed.subscribers {
		select {
		case subscriber <- publication:
		default:
			close(subscriber)
			delete(feed.subscribers, id)
		}
	}
}

func (feed *HomeFeed) closeSubscribers() {
	feed.mu.Lock()
	defer feed.mu.Unlock()
	for id, subscriber := range feed.subscribers {
		close(subscriber)
		delete(feed.subscribers, id)
	}
}

func nonNilBlocks(items []gen.Block) []gen.Block {
	if items == nil {
		return []gen.Block{}
	}
	return items
}

func nonNilTransactions(items []gen.Transaction) []gen.Transaction {
	if items == nil {
		return []gen.Transaction{}
	}
	return items
}

func statusModel(chainID uint64, snapshot StatusSnapshot) gen.Status {
	data := gen.Status{
		ChainId:          quantity(chainID),
		CoreReady:        snapshot.CoreReady,
		LatestBlock:      quantity(snapshot.LatestBlock),
		IndexedBlock:     quantity(snapshot.IndexedBlock),
		BackfillComplete: snapshot.BackfillComplete,
		Lag:              quantity(saturatingSub(snapshot.LatestBlock, snapshot.IndexedBlock)),
		Completeness:     snapshot.Completeness,
	}
	if snapshot.HighestCoveredKnown {
		value := quantity(snapshot.HighestCoveredBlock)
		data.HighestCoveredBlock = &value
	}
	if snapshot.SafeBlock != nil {
		value := quantity(*snapshot.SafeBlock)
		data.SafeBlock = &value
	}
	if snapshot.FinalizedBlock != nil {
		value := quantity(*snapshot.FinalizedBlock)
		data.FinalizedBlock = &value
	}
	return data
}
