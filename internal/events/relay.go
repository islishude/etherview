package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

type PollSource interface {
	Poll(context.Context, uint64, int) ([]Event, error)
}

type RelayOptions struct {
	PollInterval time.Duration
	BatchSize    int
	Wake         <-chan struct{}
	Logger       *slog.Logger
}

// Relay is a read-only PostgreSQL tailer. Each API replica owns one relay and
// never coordinates cursor ownership with another replica.
type Relay struct {
	source       PollSource
	broker       *Broker
	pollInterval time.Duration
	batchSize    int
	wake         <-chan struct{}
	logger       *slog.Logger
	after        uint64
}

func NewRelay(source PollSource, broker *Broker, options RelayOptions) (*Relay, error) {
	if source == nil || broker == nil {
		return nil, errors.New("runtime event relay source and broker are required")
	}
	interval := options.PollInterval
	if interval <= 0 {
		interval = time.Second
	}
	batchSize := options.BatchSize
	if batchSize <= 0 || batchSize > 1024 {
		batchSize = DefaultReplayLimit
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Relay{
		source: source, broker: broker, pollInterval: interval,
		batchSize: batchSize, wake: options.Wake, logger: logger,
	}, nil
}

func (*Relay) Name() string { return "runtime-event-relay" }

func (r *Relay) Run(ctx context.Context) error {
	if r == nil || r.source == nil || r.broker == nil {
		return errors.New("runtime event relay is not fully configured")
	}
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		if err := r.drain(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Database driver errors can contain connection details. The relay is
			// self-healing, so a bounded type is sufficient for routine logs.
			r.logger.WarnContext(ctx, "runtime event relay poll failed; retrying", "error_type", fmt.Sprintf("%T", err))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		case <-r.wake:
		}
	}
}

func (r *Relay) drain(ctx context.Context) error {
	// Bound one drain pass so an always-busy stream cannot starve cancellation.
	for range 64 {
		events, err := r.source.Poll(ctx, r.after, r.batchSize)
		if err != nil {
			return err
		}
		for _, event := range events {
			if event.ID <= r.after {
				return errors.New("runtime event relay source returned a non-monotonic ID")
			}
			if _, err := r.broker.PublishStoredContext(ctx, event); err != nil {
				return fmt.Errorf("publish runtime event %d: %w", event.ID, err)
			}
			r.after = event.ID
		}
		if len(events) < r.batchSize {
			return nil
		}
	}
	return nil
}
