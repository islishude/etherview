package syncer

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const testWakeURL = "wss://wake.example/ws"

type wakeDialFunc func(context.Context, string, int64) (WakeConnection, error)

func (f wakeDialFunc) Dial(ctx context.Context, rawURL string, maximum int64) (WakeConnection, error) {
	return f(ctx, rawURL, maximum)
}

type fakeWakeRead struct {
	message []byte
	err     error
}

type fakeWakeConnection struct {
	reads     chan fakeWakeRead
	readCalls chan struct{}
	writes    chan []byte
	closed    chan struct{}
	closeOnce sync.Once
}

func newFakeWakeConnection(reads ...fakeWakeRead) *fakeWakeConnection {
	conn := &fakeWakeConnection{
		reads: make(chan fakeWakeRead, len(reads)), readCalls: make(chan struct{}, len(reads)+1),
		writes: make(chan []byte, 1), closed: make(chan struct{}),
	}
	for _, read := range reads {
		conn.reads <- read
	}
	return conn
}

func (c *fakeWakeConnection) Write(ctx context.Context, message []byte) error {
	select {
	case c.writes <- slices.Clone(message):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *fakeWakeConnection) Read(ctx context.Context) ([]byte, error) {
	select {
	case read := <-c.reads:
		c.readCalls <- struct{}{}
		return slices.Clone(read.message), read.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *fakeWakeConnection) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func TestHeadWakeCoalescesNotificationsWithoutParsingHeaders(t *testing.T) {
	t.Parallel()
	conn := newFakeWakeConnection(
		fakeWakeRead{message: subscriptionAccepted("sub-1")},
		// Neither result is a valid Ethereum header. Both are valid wake hints
		// because canonical data must be fetched by the polling Source.
		fakeWakeRead{message: headNotificationFor("sub-1", `"not-a-header"`)},
		fakeWakeRead{message: headNotificationFor("sub-1", `{"number":17}`)},
	)
	wake, err := NewHeadWake([]string{testWakeURL}, HeadWakeOptions{
		Dialer: wakeDialFunc(func(context.Context, string, int64) (WakeConnection, error) {
			return conn, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- wake.Run(ctx) }()

	for range 3 {
		select {
		case <-conn.readCalls:
		case <-time.After(time.Second):
			t.Fatal("wake reader did not consume scripted message")
		}
	}
	if got := len(wake.wake); got != 1 {
		t.Fatalf("coalesced wake signals = %d, want 1", got)
	}
	select {
	case request := <-conn.writes:
		want := `{"jsonrpc":"2.0","id":1,"method":"eth_subscribe","params":["newHeads"]}`
		if string(request) != want {
			t.Fatalf("subscription request = %s", request)
		}
	case <-time.After(time.Second):
		t.Fatal("subscription request was not written")
	}
	<-wake.Signal()
	select {
	case <-wake.Signal():
		t.Fatal("duplicate notification escaped signal coalescing")
	default:
	}

	cancel()
	if err := awaitWakeRun(done); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestHeadWakeReconnectsWithBoundedBackoffAndRedactedLogs(t *testing.T) {
	t.Parallel()
	const secret = "top-secret-token"
	rawURL := "wss://operator:" + secret + "@wake.example/ws?key=" + secret
	conn := newFakeWakeConnection(
		fakeWakeRead{message: subscriptionAccepted("sub-reconnected")},
		fakeWakeRead{message: headNotificationFor("sub-reconnected", `{}`)},
	)
	var attempts atomic.Int32
	var logOutput bytes.Buffer
	var waitsMu sync.Mutex
	var waits []time.Duration
	wake, err := NewHeadWake([]string{rawURL}, HeadWakeOptions{
		Dialer: wakeDialFunc(func(_ context.Context, gotURL string, maximum int64) (WakeConnection, error) {
			if gotURL != rawURL {
				t.Errorf("dial URL = %q", gotURL)
			}
			if maximum != defaultWakeMessageMaxBytes {
				t.Errorf("read limit = %d", maximum)
			}
			if attempts.Add(1) <= 4 {
				return nil, errors.New("provider echoed " + rawURL)
			}
			return conn, nil
		}),
		Logger:         slog.New(slog.NewTextHandler(&logOutput, nil)),
		InitialBackoff: 5 * time.Millisecond,
		MaxBackoff:     20 * time.Millisecond,
		Jitter:         func(base, _ time.Duration) time.Duration { return base },
		Wait: func(ctx context.Context, delay time.Duration) error {
			waitsMu.Lock()
			waits = append(waits, delay)
			waitsMu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return nil
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- wake.Run(ctx) }()
	select {
	case <-wake.Signal():
	case <-time.After(time.Second):
		t.Fatal("reconnected endpoint did not emit a wake signal")
	}
	cancel()
	if err := awaitWakeRun(done); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v", err)
	}
	waitsMu.Lock()
	gotWaits := slices.Clone(waits)
	waitsMu.Unlock()
	wantWaits := []time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond, 20 * time.Millisecond}
	if !slices.Equal(gotWaits, wantWaits) {
		t.Fatalf("retry waits = %v, want %v", gotWaits, wantWaits)
	}
	if output := logOutput.String(); strings.Contains(output, secret) || strings.Contains(output, rawURL) {
		t.Fatalf("WebSocket credentials leaked into logs: %s", output)
	}
}

func TestHeadWakeRejectsOversizedAndInvalidMessages(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		reads      []fakeWakeRead
		wantReason wakeFailure
	}{
		{
			name:       "oversized subscription response",
			reads:      []fakeWakeRead{{message: bytes.Repeat([]byte("x"), 65)}},
			wantReason: wakeFailureOversize,
		},
		{
			name:       "wrong subscription response id",
			reads:      []fakeWakeRead{{message: []byte(`{"jsonrpc":"2.0","id":2,"result":"sub-1"}`)}},
			wantReason: wakeFailureProtocol,
		},
		{
			name: "oversized notification",
			reads: []fakeWakeRead{
				{message: subscriptionAccepted("s")},
				{message: bytes.Repeat([]byte("x"), 257)},
			},
			wantReason: wakeFailureOversize,
		},
		{
			name: "unknown notification field",
			reads: []fakeWakeRead{
				{message: subscriptionAccepted("s")},
				{message: []byte(`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"s","result":{}},"unexpected":true}`)},
			},
			wantReason: wakeFailureProtocol,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			conn := newFakeWakeConnection(test.reads...)
			wake, err := NewHeadWake([]string{testWakeURL}, HeadWakeOptions{
				Dialer: wakeDialFunc(func(context.Context, string, int64) (WakeConnection, error) {
					return conn, nil
				}),
				MaxSubscriptionBytes: 64,
				MaxMessageBytes:      256,
			})
			if err != nil {
				t.Fatal(err)
			}
			reason, notified := wake.session(context.Background(), testWakeURL)
			if reason != test.wantReason || notified {
				t.Fatalf("session() = (%q, %t), want (%q, false)", reason, notified, test.wantReason)
			}
			select {
			case <-wake.Signal():
				t.Fatal("invalid message emitted a wake signal")
			default:
			}
		})
	}
}

func TestHeadWakeCancellationInterruptsDialAndRead(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		dialer WakeDialer
	}{
		{
			name: "dial",
			dialer: wakeDialFunc(func(ctx context.Context, _ string, _ int64) (WakeConnection, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			}),
		},
		{
			name: "read",
			dialer: wakeDialFunc(func(context.Context, string, int64) (WakeConnection, error) {
				return newFakeWakeConnection(fakeWakeRead{message: subscriptionAccepted("sub-1")}), nil
			}),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			wake, err := NewHeadWake([]string{testWakeURL}, HeadWakeOptions{Dialer: test.dialer})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- wake.Run(ctx) }()
			cancel()
			if err := awaitWakeRun(done); !errors.Is(err, context.Canceled) {
				t.Fatalf("Run() error = %v", err)
			}
		})
	}
}

func TestHeadWakeRunsEndpointsIndependently(t *testing.T) {
	t.Parallel()
	urls := []string{"wss://one.example/ws", "ws://two.example/ws"}
	connections := map[string]*fakeWakeConnection{
		urls[0]: newFakeWakeConnection(
			fakeWakeRead{message: subscriptionAccepted("one")},
			fakeWakeRead{message: headNotificationFor("one", `{}`)},
		),
		urls[1]: newFakeWakeConnection(
			fakeWakeRead{message: subscriptionAccepted("two")},
			fakeWakeRead{message: headNotificationFor("two", `{}`)},
		),
	}
	dialed := make(chan string, len(urls))
	wake, err := NewHeadWake(urls, HeadWakeOptions{
		Dialer: wakeDialFunc(func(_ context.Context, rawURL string, _ int64) (WakeConnection, error) {
			dialed <- rawURL
			return connections[rawURL], nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- wake.Run(ctx) }()
	seen := make(map[string]bool)
	for range urls {
		select {
		case rawURL := <-dialed:
			seen[rawURL] = true
		case <-time.After(time.Second):
			t.Fatal("not all WebSocket endpoints were dialed")
		}
	}
	for _, rawURL := range urls {
		if !seen[rawURL] {
			t.Fatalf("endpoint %q was not dialed", rawURL)
		}
	}
	cancel()
	if err := awaitWakeRun(done); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestNewHeadWakeRejectsUnsafeConfigurationWithoutEchoingURL(t *testing.T) {
	t.Parallel()
	_, err := NewHeadWake([]string{"https://operator:secret@example.com"}, HeadWakeOptions{})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("NewHeadWake() error = %v", err)
	}
	_, err = NewHeadWake([]string{testWakeURL}, HeadWakeOptions{
		InitialBackoff: 2 * time.Second,
		MaxBackoff:     time.Second,
	})
	if err == nil {
		t.Fatal("NewHeadWake() accepted an inverted backoff range")
	}
}

func TestCoderWakeDialerEnforcesMessageLimit(t *testing.T) {
	t.Parallel()
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(writer, request, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		if _, _, err := conn.Read(request.Context()); err != nil {
			serverErr <- err
			return
		}
		if err := conn.Write(request.Context(), websocket.MessageText, subscriptionAccepted("s")); err != nil {
			serverErr <- err
			return
		}
		if err := conn.Write(request.Context(), websocket.MessageText, bytes.Repeat([]byte("x"), 128)); err != nil {
			serverErr <- err
			return
		}
		serverErr <- nil
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := (coderWakeDialer{}).Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), 64)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.Write(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_subscribe","params":["newHeads"]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Read(ctx); err != nil {
		t.Fatalf("read subscription response: %v", err)
	}
	if _, err := conn.Read(ctx); !errors.Is(err, errWakeMessageTooBig) {
		t.Fatalf("oversized read error = %v, want %v", err, errWakeMessageTooBig)
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("WebSocket server: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WebSocket server did not finish")
	}
}

func awaitWakeRun(done <-chan error) error {
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		return errors.New("WebSocket wake service did not stop after cancellation")
	}
}

func subscriptionAccepted(id string) []byte {
	return []byte(`{"jsonrpc":"2.0","id":1,"result":"` + id + `"}`)
}

func headNotificationFor(id, result string) []byte {
	return []byte(`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"` + id + `","result":` + result + `}}`)
}
