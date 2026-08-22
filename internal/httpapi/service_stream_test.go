package httpapi

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/events"
)

func TestServiceSSEWriteDeadlineIsPerFrameForHTTP1AndHTTP2(t *testing.T) {
	for _, tlsEnabled := range []bool{false, true} {
		name := "http1"
		if tlsEnabled {
			name = "http2"
		}
		t.Run(name, func(t *testing.T) {
			source := &durableEventFixture{}
			first := source.append("head", map[string]string{"number": "1"})
			broker, err := events.NewDurableBroker(8, source)
			if err != nil {
				t.Fatal(err)
			}
			cfg := config.Default()
			cfg.Server.WriteTimeout = 50 * time.Millisecond
			// Go's HTTP/2 graceful GOAWAY drain is one second even after the
			// final stream exits, so retain headroom above that protocol budget.
			cfg.Server.ShutdownTimeout = 2 * time.Second
			handler, err := New(Options{Config: cfg, Reader: fakeReader{}, Events: broker})
			if err != nil {
				t.Fatal(err)
			}
			baseURL, client, cancelService, done := startStreamTestService(t, cfg, handler, tlsEnabled)
			streamCtx, cancelStream := context.WithCancel(t.Context())
			defer cancelStream()
			request, err := http.NewRequestWithContext(streamCtx, http.MethodGet, baseURL+"/api/v1/events", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Last-Event-ID", "0")
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			if tlsEnabled && response.ProtoMajor != 2 {
				t.Fatalf("protocol=%s", response.Proto)
			}
			reader := bufio.NewReader(response.Body)
			assertSSEEvent(t, reader, first.ID, first.Type)

			// The connection remains idle longer than the configured timeout.
			time.Sleep(3 * cfg.Server.WriteTimeout)
			second := source.append("status", map[string]bool{"ready": true})
			if published, err := broker.PublishStored(second); err != nil || !published {
				t.Fatalf("publish=%t err=%v", published, err)
			}
			assertSSEEvent(t, reader, second.ID, second.Type)
			cancelStream()
			_ = response.Body.Close()
			cancelService()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("service shutdown=%v", err)
			}
		})
	}
}

func TestServiceCancellationClosesActiveHTTP2EventAndHomeStreams(t *testing.T) {
	source := &durableEventFixture{}
	event := source.append("head", map[string]string{"number": "1"})
	broker, err := events.NewDurableBroker(8, source)
	if err != nil {
		t.Fatal(err)
	}
	publications := make(chan HomePublication, 1)
	publications <- HomePublication{
		EventID: 1,
		Data: HomeSnapshot{
			Status: gen.Status{
				ChainId: "1", LatestBlock: "1", IndexedBlock: "1", Lag: "0",
				CoreReady: true, BackfillComplete: true,
				Completeness: gen.Completeness{Core: gen.StageStateComplete},
			},
			Blocks: []gen.Block{}, Transactions: []gen.Transaction{},
		},
		CoverageStart: "0", CoverageEnd: "1",
	}
	cfg := config.Default()
	cfg.Server.ShutdownTimeout = 2 * time.Second
	handler, err := New(Options{
		Config: cfg, Reader: fakeReader{}, Events: broker,
		HomeSnapshots: homeSnapshotSourceFixture{channel: publications},
	})
	if err != nil {
		t.Fatal(err)
	}
	baseURL, client, cancelService, done := startStreamTestService(t, cfg, handler, true)

	eventResponse, err := client.Get(baseURL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eventResponse.Body.Close() }()
	assertSSEEvent(t, bufio.NewReader(eventResponse.Body), event.ID, event.Type)
	homeResponse, err := client.Get(baseURL + "/api/v1/home/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = homeResponse.Body.Close() }()
	if fields := readSSEFields(t, bufio.NewReader(homeResponse.Body)); fields["event"] != "snapshot" {
		t.Fatalf("home stream fields=%v", fields)
	}
	if eventResponse.ProtoMajor != 2 || homeResponse.ProtoMajor != 2 {
		t.Fatalf("protocols event=%s home=%s", eventResponse.Proto, homeResponse.Proto)
	}

	started := time.Now()
	cancelService()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("service shutdown=%v", err)
		}
	case <-time.After(cfg.Server.ShutdownTimeout):
		t.Fatal("active streams exhausted the service shutdown budget")
	}
	if elapsed := time.Since(started); elapsed >= cfg.Server.ShutdownTimeout {
		t.Fatalf("stream shutdown took %s", elapsed)
	}
}

func startStreamTestService(
	t *testing.T,
	cfg config.Config,
	handler http.Handler,
	tlsEnabled bool,
) (string, *http.Client, context.CancelFunc, <-chan error) {
	t.Helper()
	if tlsEnabled {
		cfg.Server.TLSCertFile, cfg.Server.TLSKeyFile = writeTestTLSKeyPair(t)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(cfg, handler)
	service.listen = func(_, _ string) (net.Listener, error) { return listener, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	transport := &http.Transport{}
	scheme := "http"
	if tlsEnabled {
		scheme = "https"
		transport.ForceAttemptHTTP2 = true
		transport.TLSClientConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			// The certificate is generated only for this local listener.
			InsecureSkipVerify: true,
		}
	}
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	t.Cleanup(func() {
		cancel()
		transport.CloseIdleConnections()
	})
	return scheme + "://" + listener.Addr().String(), client, cancel, done
}
