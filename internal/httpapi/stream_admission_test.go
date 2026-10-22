package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/events"
)

type admissionWriter struct {
	*httptest.ResponseRecorder
	ready chan struct{}
	once  sync.Once
	fail  bool
}

func (w *admissionWriter) SetWriteDeadline(time.Time) error { return nil }
func (w *admissionWriter) FlushError() error {
	w.once.Do(func() { close(w.ready) })
	if w.fail {
		return errors.New("write failed")
	}
	return nil
}

type admissionHome struct {
	calls int
	ctx   context.Context
}

func (s *admissionHome) Subscribe(ctx context.Context) (<-chan HomePublication, error) {
	s.calls++
	s.ctx = ctx
	return make(chan HomePublication), nil
}

func TestStreamAdmissionIsSharedAndReleasedOnExit(t *testing.T) {
	h, err := New(Options{Config: config.Default(), Reader: fakeReader{}, Events: events.NewBroker(8)})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default().Security
	cfg.SSEMaxConnections = 2
	cfg.SSEMaxConnectionsPerClient = 1
	proxies, err := auth.NewTrustedProxySet([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	h.streamAdmission = newStreamAdmission(cfg, proxies)
	home := &admissionHome{}
	h.homeSnapshots = home
	request := func(path, client string) *http.Request {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = "10.0.0.1:1234"
		r.Header.Set("X-Forwarded-For", client)
		return r
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w := &admissionWriter{ResponseRecorder: httptest.NewRecorder(), ready: make(chan struct{})}
	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(w, request("/api/v1/events", "198.51.100.1").WithContext(ctx)) }()
	select {
	case <-w.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not open")
	}
	// Same client cannot evade the permit by selecting the other stream route.
	rejected := httptest.NewRecorder()
	h.ServeHTTP(rejected, request("/api/v1/home/stream", "198.51.100.1"))
	if rejected.Code != 429 || home.calls != 0 || !strings.Contains(rejected.Body.String(), "stream_limit_exceeded") {
		t.Fatalf("%d %s calls=%d", rejected.Code, rejected.Body.String(), home.calls)
	}
	// A second client may subscribe, but a write failure must cancel its source.
	failed := &admissionWriter{ResponseRecorder: httptest.NewRecorder(), ready: make(chan struct{}), fail: true}
	h.ServeHTTP(failed, request("/api/v1/home/stream", "198.51.100.2"))
	if home.calls != 1 || home.ctx.Err() == nil {
		t.Fatal("failed stream retained source")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not exit")
	}
	h.streamAdmission.mu.Lock()
	defer h.streamAdmission.mu.Unlock()
	if h.streamAdmission.total != 0 || len(h.streamAdmission.clients) != 0 {
		t.Fatal("permits leaked")
	}
}

func TestStreamAdmissionBoundsGlobalConcurrencyAndRejectsSpoofing(t *testing.T) {
	cfg := config.Default().Security
	cfg.SSEMaxConnections = 8
	cfg.SSEMaxConnectionsPerClient = 1
	a := newStreamAdmission(cfg, auth.TrustedProxySet{})
	var wg sync.WaitGroup
	releases := make(chan func(), 64)
	for i := range 64 {
		wg.Go(func() {
			r := httptest.NewRequest("GET", "/api/v1/events", nil)
			r.RemoteAddr = "192.0.2." + strconv.Itoa(i+1) + ":1"
			if release, ok := a.acquire(r); ok {
				releases <- release
			}
		})
	}
	wg.Wait()
	close(releases)
	if a.total != 8 {
		t.Fatalf("active=%d", a.total)
	}
	for release := range releases {
		release()
		release()
	}
	if a.total != 0 || len(a.clients) != 0 {
		t.Fatal("release not idempotent")
	}
	r := httptest.NewRequest("GET", "/api/v1/events", nil)
	r.RemoteAddr = "192.0.2.1:1"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	release, ok := a.acquire(r)
	if !ok {
		t.Fatal("first refused")
	}
	defer release()
	r.Header.Set("X-Forwarded-For", "198.51.100.2")
	if _, ok := a.acquire(r); ok {
		t.Fatal("untrusted proxy bypassed per-client cap")
	}
}

func TestStreamAdmissionReleasesOnReplayFailure(t *testing.T) {
	h, err := New(Options{Config: config.Default(), Reader: fakeReader{}, Events: events.NewBroker(8)})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		w := &admissionWriter{ResponseRecorder: httptest.NewRecorder(), ready: make(chan struct{})}
		r := httptest.NewRequest("GET", "/api/v1/events", nil)
		r.Header.Set("Last-Event-ID", "bad")
		h.ServeHTTP(w, r)
		if w.Code != 400 || h.streamAdmission.total != 0 {
			t.Fatalf("status=%d active=%d", w.Code, h.streamAdmission.total)
		}
	}
}
