// Package accelerator implements optional latency and storage accelerators.
// PostgreSQL remains the only correctness source; every operation in this
// package is therefore lossy or has a caller-owned durable fallback.
package accelerator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

type WakeTopic string

const (
	WakeRuntime WakeTopic = "runtime"
	WakeOutbox  WakeTopic = "outbox"
	WakeJobs    WakeTopic = "jobs"
)

var wakeTopics = map[WakeTopic]struct{}{
	WakeRuntime: {},
	WakeOutbox:  {},
	WakeJobs:    {},
}

type NATSWakeOptions struct {
	Namespace      string
	ChainID        uint64
	ConnectTimeout time.Duration
	ReconnectWait  time.Duration
	Logger         *slog.Logger
}

// NATSWake is a best-effort broadcast hint. Signal also wakes local
// subscribers, while network delivery may be dropped at any time. Consumers
// must always retain their PostgreSQL polling path.
type NATSWake struct {
	url            string
	subjectPrefix  string
	connectTimeout time.Duration
	reconnectWait  time.Duration
	logger         *slog.Logger
	outbound       chan WakeTopic

	mu          sync.RWMutex
	subscribers map[WakeTopic][]chan struct{}
}

func NewNATSWake(rawURL string, options NATSWakeOptions) (*NATSWake, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, errors.New("NATS wake URL is empty")
	}
	if options.Namespace == "" {
		options.Namespace = "etherview"
	}
	if options.ChainID == 0 {
		return nil, errors.New("NATS wake chain ID is zero")
	}
	if options.ConnectTimeout <= 0 {
		options.ConnectTimeout = 2 * time.Second
	}
	if options.ReconnectWait <= 0 {
		options.ReconnectWait = time.Second
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &NATSWake{
		url:            rawURL,
		subjectPrefix:  fmt.Sprintf("%s.%d.wake", options.Namespace, options.ChainID),
		connectTimeout: options.ConnectTimeout,
		reconnectWait:  options.ReconnectWait,
		logger:         options.Logger,
		outbound:       make(chan WakeTopic, 64),
		subscribers:    make(map[WakeTopic][]chan struct{}),
	}, nil
}

func (*NATSWake) Name() string { return "optional-nats-wake" }

// Subscribe returns a coalescing wake channel. Each subscriber receives an
// independent notification, including when several consumers share a process.
func (wake *NATSWake) Subscribe(topic WakeTopic) (<-chan struct{}, error) {
	if wake == nil {
		return nil, errors.New("subscribe using nil NATS wake")
	}
	if _, ok := wakeTopics[topic]; !ok {
		return nil, fmt.Errorf("unsupported NATS wake topic %q", topic)
	}
	channel := make(chan struct{}, 1)
	if err := wake.SubscribeInto(topic, channel); err != nil {
		return nil, err
	}
	return channel, nil
}

// SubscribeInto registers a caller-owned coalescing channel. It is useful
// when a durable poller already has an in-process wake channel.
func (wake *NATSWake) SubscribeInto(topic WakeTopic, channel chan struct{}) error {
	if wake == nil {
		return errors.New("subscribe using nil NATS wake")
	}
	if channel == nil {
		return errors.New("NATS wake subscriber channel is nil")
	}
	if _, ok := wakeTopics[topic]; !ok {
		return fmt.Errorf("unsupported NATS wake topic %q", topic)
	}
	wake.mu.Lock()
	wake.subscribers[topic] = append(wake.subscribers[topic], channel)
	wake.mu.Unlock()
	return nil
}

// Signal never blocks a correctness path. A full outbound buffer means only
// that the next durable poll, rather than NATS, performs the wake-up.
func (wake *NATSWake) Signal(topic WakeTopic) {
	if wake == nil {
		return
	}
	if _, ok := wakeTopics[topic]; !ok {
		return
	}
	wake.dispatch(topic)
	select {
	case wake.outbound <- topic:
	default:
	}
}

func (wake *NATSWake) dispatch(topic WakeTopic) {
	wake.mu.RLock()
	defer wake.mu.RUnlock()
	for _, channel := range wake.subscribers[topic] {
		select {
		case channel <- struct{}{}:
		default:
		}
	}
}

// Run keeps retrying an unavailable broker and exits only when the shared
// supervisor cancels it. Ordinary NATS outages therefore never withdraw
// process readiness.
func (wake *NATSWake) Run(ctx context.Context) error {
	if wake == nil {
		return errors.New("run nil NATS wake")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		connection, err := wake.connect()
		if err != nil {
			wake.logFailure(ctx, "optional NATS connect failed; PostgreSQL polling remains active", err)
			if err := waitOptional(ctx, wake.reconnectWait); err != nil {
				return err
			}
			continue
		}
		if err := wake.runConnected(ctx, connection); err != nil && ctx.Err() == nil {
			wake.logFailure(ctx, "optional NATS session failed; PostgreSQL polling remains active", err)
		}
		connection.Close()
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

func (wake *NATSWake) connect() (*nats.Conn, error) {
	return nats.Connect(
		wake.url,
		nats.Name("etherview-optional-wake"),
		nats.Timeout(wake.connectTimeout),
		nats.ReconnectWait(wake.reconnectWait),
		nats.MaxReconnects(-1),
		nats.RetryOnFailedConnect(true),
		nats.NoCallbacksAfterClientClose(),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			wake.logFailure(context.Background(), "optional NATS callback failed; hint discarded", err)
		}),
	)
}

func (wake *NATSWake) runConnected(ctx context.Context, connection *nats.Conn) error {
	subscription, err := connection.Subscribe(wake.subjectPrefix+".*", func(message *nats.Msg) {
		prefix := wake.subjectPrefix + "."
		if !strings.HasPrefix(message.Subject, prefix) {
			return
		}
		topic := WakeTopic(strings.TrimPrefix(message.Subject, prefix))
		if _, ok := wakeTopics[topic]; ok {
			wake.dispatch(topic)
		}
	})
	if err != nil {
		return fmt.Errorf("subscribe NATS wake: %w", err)
	}
	defer subscription.Unsubscribe()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case topic := <-wake.outbound:
			// The payload is deliberately content-free. PostgreSQL determines all
			// identities and generations after the consumer wakes.
			if err := connection.Publish(wake.subjectPrefix+"."+string(topic), nil); err != nil {
				wake.logFailure(ctx, "optional NATS publish failed; hint discarded", err)
			}
		}
	}
}

func (wake *NATSWake) logFailure(ctx context.Context, message string, err error) {
	if wake.logger != nil && err != nil {
		wake.logger.WarnContext(ctx, message, "error_type", fmt.Sprintf("%T", err))
	}
}

func waitOptional(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
