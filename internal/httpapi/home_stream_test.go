package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/events"
)

type homeSnapshotSourceFixture struct {
	channel <-chan HomePublication
	err     error
}

func (fixture homeSnapshotSourceFixture) Subscribe(context.Context) (<-chan HomePublication, error) {
	return fixture.channel, fixture.err
}

func TestHomeSnapshotStreamSendsCurrentCompleteSnapshot(t *testing.T) {
	t.Parallel()
	publications := make(chan HomePublication, 1)
	publications <- HomePublication{
		EventID: 42,
		Data: HomeSnapshot{
			Status: gen.Status{
				ChainId: "1", LatestBlock: "12", IndexedBlock: "12", Lag: "0",
				CoreReady: true, BackfillComplete: true,
				Completeness: gen.Completeness{Core: gen.StageStateComplete},
			},
			Blocks: []gen.Block{{Number: "12"}},
			Transactions: []gen.Transaction{{
				Hash: "0x000000000000000000000000000000000000000000000000000000000000002a",
			}},
		},
		CoverageStart: "0",
		CoverageEnd:   "12",
	}
	cfg := config.Default()
	cfg.Chain.ID = 1
	handler, err := New(Options{
		Config: cfg, Reader: fakeReader{},
		HomeSnapshots: homeSnapshotSourceFixture{channel: publications},
		RequestID:     func() string { return "home-request" },
		Now:           func() time.Time { return time.Unix(42, 0).UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/home/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Timeout = 3 * time.Second
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close home snapshot response: %v", err)
		}
	}()
	if response.StatusCode != http.StatusOK ||
		response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" ||
		response.Header.Get("Cache-Control") != "no-cache, no-transform" ||
		response.Header.Get("X-Accel-Buffering") != "no" ||
		response.Header.Get("X-Content-Type-Options") != "nosniff" ||
		response.Header.Get("Referrer-Policy") != "strict-origin-when-cross-origin" {
		t.Fatalf("SSE status=%d headers=%v", response.StatusCode, response.Header)
	}
	fields := readSSEFields(t, bufio.NewReader(response.Body))
	if fields["id"] != "42" || fields["event"] != "snapshot" {
		t.Fatalf("SSE fields = %#v", fields)
	}
	var payload struct {
		Data HomeSnapshot `json:"data"`
		Meta gen.Meta     `json:"meta"`
	}
	if err := json.Unmarshal([]byte(fields["data"]), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.Status.LatestBlock != "12" ||
		len(payload.Data.Blocks) != 1 || len(payload.Data.Transactions) != 1 ||
		payload.Meta.ChainId != "1" || payload.Meta.RequestId != "home-request" ||
		payload.Meta.CoverageStart == nil || *payload.Meta.CoverageStart != "0" ||
		payload.Meta.CoverageEnd == nil || *payload.Meta.CoverageEnd != "12" {
		t.Fatalf("home snapshot payload = %+v", payload)
	}
}

func TestHomeSnapshotStreamReturnsStableUnavailableErrorBeforeInitialSnapshot(t *testing.T) {
	t.Parallel()
	handler, err := New(Options{
		Config: config.Default(), Reader: fakeReader{},
		HomeSnapshots: homeSnapshotSourceFixture{
			err: errors.New("postgres://user:secret@example.invalid"),
		},
		RequestID: func() string { return "home-unavailable" },
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/home/stream", nil))
	if recorder.Code != http.StatusServiceUnavailable ||
		!strings.Contains(recorder.Body.String(), `"code":"home_snapshot_unavailable"`) ||
		strings.Contains(recorder.Body.String(), "postgres") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func readSSEFields(t *testing.T, reader *bufio.Reader) map[string]string {
	t.Helper()
	fields := make(map[string]string)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE event: %v", err)
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			return fields
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			t.Fatalf("malformed SSE line %q", line)
		}
		fields[name] = strings.TrimSpace(value)
	}
}

func TestHomeSnapshotPublicationRejectsOversizedLists(t *testing.T) {
	t.Parallel()
	feed, err := NewHomeFeed(
		&homeSnapshotReaderFixture{}, events.NewBroker(8),
		HomeFeedOptions{ChainID: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = feed.publication(HomeSnapshotState{
		Blocks: make([]gen.Block, homeSnapshotItemLimit+1),
	})
	if err == nil || !strings.Contains(fmt.Sprint(err), "item limit") {
		t.Fatalf("oversized publication error = %v", err)
	}
}
