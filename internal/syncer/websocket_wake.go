package syncer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	defaultWakeInitialBackoff       = 500 * time.Millisecond
	defaultWakeMaxBackoff           = 30 * time.Second
	defaultWakeConnectTimeout       = 10 * time.Second
	defaultWakeSubscriptionTimeout  = 10 * time.Second
	defaultWakeSubscriptionMaxBytes = int64(4 << 10)
	defaultWakeMessageMaxBytes      = int64(64 << 10)
	maxWakeSubscriptionIDBytes      = 512
)

var (
	errWakeBinaryMessage = errors.New("binary WebSocket message is not accepted")
	errWakeMessageTooBig = errors.New("WebSocket message exceeds configured limit")
)

// WakeConnection is the narrow transport contract needed by HeadWake. The
// concrete implementation applies a per-message read limit before any data is
// accepted; tests can replace it without opening sockets.
type WakeConnection interface {
	Write(context.Context, []byte) error
	Read(context.Context) ([]byte, error)
	Close() error
}

// WakeDialer creates one bounded WebSocket connection. Implementations must
// apply maxMessageBytes before returning the connection.
type WakeDialer interface {
	Dial(ctx context.Context, rawURL string, maxMessageBytes int64) (WakeConnection, error)
}

// HeadWakeOptions controls only the optional latency hint path. None of these
// settings changes authoritative polling, gap repair, ancestry, or finality.
type HeadWakeOptions struct {
	Dialer               WakeDialer
	Logger               *slog.Logger
	InitialBackoff       time.Duration
	MaxBackoff           time.Duration
	ConnectTimeout       time.Duration
	SubscriptionTimeout  time.Duration
	MaxSubscriptionBytes int64
	MaxMessageBytes      int64
	Jitter               func(base, maximum time.Duration) time.Duration
	Wait                 func(context.Context, time.Duration) error
}

// HeadWake subscribes to eth_subscribe(newHeads) on independent endpoints and
// coalesces all matching notifications into a single non-blocking signal. It
// deliberately does not decode the notification result as a block header.
type HeadWake struct {
	urls                 []string
	dialer               WakeDialer
	logger               *slog.Logger
	wake                 chan struct{}
	initialBackoff       time.Duration
	maxBackoff           time.Duration
	connectTimeout       time.Duration
	subscriptionTimeout  time.Duration
	maxSubscriptionBytes int64
	maxMessageBytes      int64
	jitter               func(time.Duration, time.Duration) time.Duration
	wait                 func(context.Context, time.Duration) error
}

func NewHeadWake(urls []string, options HeadWakeOptions) (*HeadWake, error) {
	if len(urls) == 0 {
		return nil, errors.New("at least one WebSocket wake URL is required")
	}
	validated := make([]string, len(urls))
	for index, rawURL := range urls {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return nil, fmt.Errorf("WebSocket wake endpoint %d is invalid", index+1)
		}
		scheme := strings.ToLower(parsed.Scheme)
		if (scheme != "ws" && scheme != "wss") || parsed.Host == "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("WebSocket wake endpoint %d is invalid", index+1)
		}
		validated[index] = rawURL
	}

	wake := &HeadWake{
		urls:                 validated,
		dialer:               options.Dialer,
		logger:               options.Logger,
		wake:                 make(chan struct{}, 1),
		initialBackoff:       options.InitialBackoff,
		maxBackoff:           options.MaxBackoff,
		connectTimeout:       options.ConnectTimeout,
		subscriptionTimeout:  options.SubscriptionTimeout,
		maxSubscriptionBytes: options.MaxSubscriptionBytes,
		maxMessageBytes:      options.MaxMessageBytes,
		jitter:               options.Jitter,
		wait:                 options.Wait,
	}
	if wake.dialer == nil {
		wake.dialer = coderWakeDialer{}
	}
	if wake.logger == nil {
		wake.logger = slog.Default()
	}
	if wake.initialBackoff <= 0 {
		wake.initialBackoff = defaultWakeInitialBackoff
	}
	if wake.maxBackoff <= 0 {
		wake.maxBackoff = defaultWakeMaxBackoff
	}
	if wake.maxBackoff < wake.initialBackoff {
		return nil, errors.New("WebSocket wake maximum backoff is below initial backoff")
	}
	if wake.connectTimeout <= 0 {
		wake.connectTimeout = defaultWakeConnectTimeout
	}
	if wake.subscriptionTimeout <= 0 {
		wake.subscriptionTimeout = defaultWakeSubscriptionTimeout
	}
	if wake.maxSubscriptionBytes <= 0 {
		wake.maxSubscriptionBytes = defaultWakeSubscriptionMaxBytes
	}
	if wake.maxMessageBytes <= 0 {
		wake.maxMessageBytes = defaultWakeMessageMaxBytes
	}
	if wake.maxSubscriptionBytes > wake.maxMessageBytes {
		return nil, errors.New("WebSocket subscription response limit exceeds message limit")
	}
	if wake.jitter == nil {
		wake.jitter = boundedWakeJitter
	}
	if wake.wait == nil {
		wake.wait = waitWakeRetry
	}
	return wake, nil
}

func (w *HeadWake) Name() string { return "new-head-wake" }

// Signal returns a receive-only, capacity-one notification channel. A nil
// Signal is never needed: deployments without WebSocket endpoints simply do
// not construct HeadWake and leave Service.Wake nil.
func (w *HeadWake) Signal() <-chan struct{} {
	if w == nil {
		return nil
	}
	return w.wake
}

func (w *HeadWake) Run(ctx context.Context) error {
	if w == nil {
		return errors.New("WebSocket wake service is nil")
	}
	var workers sync.WaitGroup
	workers.Add(len(w.urls))
	for index, rawURL := range w.urls {
		go func() {
			defer workers.Done()
			w.watch(ctx, index+1, rawURL)
		}()
	}
	<-ctx.Done()
	workers.Wait()
	return ctx.Err()
}

type wakeFailure string

const (
	wakeFailureDial      wakeFailure = "dial"
	wakeFailureSubscribe wakeFailure = "subscribe"
	wakeFailureResponse  wakeFailure = "subscription_response"
	wakeFailureRead      wakeFailure = "read"
	wakeFailureOversize  wakeFailure = "oversize"
	wakeFailureProtocol  wakeFailure = "protocol"
)

func (w *HeadWake) watch(ctx context.Context, endpoint int, rawURL string) {
	backoff := w.initialBackoff
	for ctx.Err() == nil {
		failure, receivedNotification := w.session(ctx, rawURL)
		if ctx.Err() != nil {
			return
		}
		if receivedNotification {
			backoff = w.initialBackoff
		}
		delay := w.jitter(backoff, w.maxBackoff)
		if delay <= 0 {
			delay = time.Nanosecond
		}
		if delay > w.maxBackoff {
			delay = w.maxBackoff
		}
		// Do not log rawURL or the underlying error: either may contain URL
		// user-info, query credentials, or provider-generated echo text.
		w.logger.WarnContext(ctx, "WebSocket new-head wake unavailable; authoritative polling remains active",
			"endpoint", endpoint, "reason", failure, "retry_in", delay)
		if err := w.wait(ctx, delay); err != nil {
			return
		}
		backoff = nextWakeBackoff(backoff, w.maxBackoff)
	}
}

func (w *HeadWake) session(ctx context.Context, rawURL string) (wakeFailure, bool) {
	dialCtx, cancelDial := context.WithTimeout(ctx, w.connectTimeout)
	conn, err := w.dialer.Dial(dialCtx, rawURL, w.maxMessageBytes)
	cancelDial()
	if err != nil {
		return wakeFailureDial, false
	}
	defer func() { _ = conn.Close() }()

	subscribeCtx, cancelSubscribe := context.WithTimeout(ctx, w.subscriptionTimeout)
	err = conn.Write(subscribeCtx, []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_subscribe","params":["newHeads"]}`))
	if err != nil {
		cancelSubscribe()
		return wakeFailureSubscribe, false
	}
	response, err := conn.Read(subscribeCtx)
	cancelSubscribe()
	if err != nil {
		if errors.Is(err, errWakeMessageTooBig) {
			return wakeFailureOversize, false
		}
		if errors.Is(err, errWakeBinaryMessage) {
			return wakeFailureProtocol, false
		}
		return wakeFailureResponse, false
	}
	if int64(len(response)) > w.maxSubscriptionBytes {
		return wakeFailureOversize, false
	}
	subscriptionID, err := validateSubscriptionResponse(response)
	if err != nil {
		return wakeFailureProtocol, false
	}

	receivedNotification := false
	for {
		message, readErr := conn.Read(ctx)
		if readErr != nil {
			if errors.Is(readErr, errWakeMessageTooBig) {
				return wakeFailureOversize, receivedNotification
			}
			if errors.Is(readErr, errWakeBinaryMessage) {
				return wakeFailureProtocol, receivedNotification
			}
			return wakeFailureRead, receivedNotification
		}
		if int64(len(message)) > w.maxMessageBytes {
			return wakeFailureOversize, receivedNotification
		}
		matches, validateErr := validateHeadNotification(message, subscriptionID)
		if validateErr != nil {
			return wakeFailureProtocol, receivedNotification
		}
		if !matches {
			continue
		}
		receivedNotification = true
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
}

type subscriptionResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  string          `json:"result"`
	Error   json.RawMessage `json:"error"`
}

func validateSubscriptionResponse(message []byte) (string, error) {
	var response subscriptionResponse
	if err := decodeStrictJSON(message, &response); err != nil {
		return "", err
	}
	if response.JSONRPC != "2.0" || !bytes.Equal(bytes.TrimSpace(response.ID), []byte("1")) {
		return "", errors.New("unexpected WebSocket subscription response identity")
	}
	if len(response.Error) != 0 || response.Result == "" || len(response.Result) > maxWakeSubscriptionIDBytes {
		return "", errors.New("WebSocket subscription was not accepted")
	}
	return response.Result, nil
}

type headNotification struct {
	JSONRPC string                 `json:"jsonrpc"`
	Method  string                 `json:"method"`
	Params  headNotificationParams `json:"params"`
}

type headNotificationParams struct {
	Subscription string          `json:"subscription"`
	Result       json.RawMessage `json:"result"`
}

func validateHeadNotification(message []byte, subscriptionID string) (bool, error) {
	var notification headNotification
	if err := decodeStrictJSON(message, &notification); err != nil {
		return false, err
	}
	if notification.JSONRPC != "2.0" || notification.Method != "eth_subscription" || len(notification.Params.Result) == 0 {
		return false, errors.New("invalid WebSocket subscription notification")
	}
	// The result is intentionally left as json.RawMessage. Polling retrieves
	// and validates the actual canonical head after this function returns.
	return notification.Params.Subscription == subscriptionID, nil
}

func decodeStrictJSON(message []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(message))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not accepted")
		}
		return err
	}
	return nil
}

func nextWakeBackoff(current, maximum time.Duration) time.Duration {
	if current >= maximum || current > maximum/2 {
		return maximum
	}
	return current * 2
}

func boundedWakeJitter(base, maximum time.Duration) time.Duration {
	// A 20 percent symmetric jitter keeps reconnecting replicas from moving
	// in lockstep while the final clamp preserves the hard maximum.
	factor := 0.8 + rand.Float64()*0.4
	delay := time.Duration(float64(base) * factor)
	if delay > maximum {
		return maximum
	}
	return delay
}

func waitWakeRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type coderWakeDialer struct{}

func (coderWakeDialer) Dial(ctx context.Context, rawURL string, maxMessageBytes int64) (WakeConnection, error) {
	conn, _, err := websocket.Dial(ctx, rawURL, nil)
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(maxMessageBytes)
	return &coderWakeConnection{conn: conn}, nil
}

type coderWakeConnection struct {
	conn *websocket.Conn
}

func (c *coderWakeConnection) Write(ctx context.Context, message []byte) error {
	return c.conn.Write(ctx, websocket.MessageText, message)
}

func (c *coderWakeConnection) Read(ctx context.Context) ([]byte, error) {
	messageType, message, err := c.conn.Read(ctx)
	if err != nil {
		if errors.Is(err, websocket.ErrMessageTooBig) {
			return nil, errWakeMessageTooBig
		}
		return nil, err
	}
	if messageType != websocket.MessageText {
		return nil, errWakeBinaryMessage
	}
	return message, nil
}

func (c *coderWakeConnection) Close() error {
	return c.conn.CloseNow()
}
