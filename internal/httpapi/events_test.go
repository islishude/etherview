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
	"sync"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/events"
)

type durableEventFixture struct {
	mu     sync.Mutex
	events []events.Event
}

func (fixture *durableEventFixture) append(eventType string, data any) events.Event {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	payload, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	event := events.Event{
		ID:   uint64(len(fixture.events) + 1),
		Type: eventType,
		Time: time.Now().UTC(),
		Data: payload,
	}
	fixture.events = append(fixture.events, event)
	return event
}

func (fixture *durableEventFixture) Replay(_ context.Context, after *uint64, limit int) ([]events.Event, error) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	result := make([]events.Event, 0, limit)
	for _, event := range fixture.events {
		if after == nil || event.ID > *after {
			result = append(result, event)
		}
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func TestEventStreamReplaysContinuesAndReconnectsAcrossReorg(t *testing.T) {
	t.Parallel()
	source := &durableEventFixture{}
	head := source.append("head", map[string]string{"number": "10", "hash": "0xhead"})
	broker, err := events.NewDurableBroker(8, source)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(Options{
		Config: config.Default(), Reader: fakeReader{}, Events: broker,
		RequestID: func() string { return "request-events-live" },
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	streamCtx, cancelStream := context.WithCancel(t.Context())
	request, err := http.NewRequestWithContext(streamCtx, http.MethodGet, server.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Last-Event-ID", "0")
	client := server.Client()
	client.Timeout = 3 * time.Second
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" ||
		response.Header.Get("Cache-Control") != "no-cache, no-transform" || response.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("SSE status=%d headers=%v", response.StatusCode, response.Header)
	}
	reader := bufio.NewReader(response.Body)
	assertSSEEvent(t, reader, head.ID, "head")
	reorg := source.append("reorg", map[string]string{"ancestor": "9", "detached": "1"})
	if published, err := broker.PublishStored(reorg); err != nil || !published {
		t.Fatalf("publish stored reorg=%t error=%v", published, err)
	}
	assertSSEEvent(t, reader, reorg.ID, "reorg")
	cancelStream()
	_ = response.Body.Close()

	status := source.append("status", map[string]bool{"ready": true})
	// Recreate the broker and HTTP server to prove that reconnect uses the
	// durable source rather than the first process's memory replay buffer.
	server.Close()
	broker, err = events.NewDurableBroker(8, source)
	if err != nil {
		t.Fatal(err)
	}
	handler, err = New(Options{
		Config: config.Default(), Reader: fakeReader{}, Events: broker,
		RequestID: func() string { return "request-events-reconnect" },
	})
	if err != nil {
		t.Fatal(err)
	}
	server = httptest.NewServer(handler)
	defer server.Close()
	reconnectCtx, cancelReconnect := context.WithCancel(t.Context())
	defer cancelReconnect()
	request, err = http.NewRequestWithContext(reconnectCtx, http.MethodGet, server.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Last-Event-ID", fmt.Sprint(reorg.ID))
	client = server.Client()
	client.Timeout = 3 * time.Second
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	assertSSEEvent(t, bufio.NewReader(response.Body), status.ID, "status")
}

func assertSSEEvent(t *testing.T, reader *bufio.Reader, wantID uint64, wantType string) {
	t.Helper()
	fields := make(map[string]string)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE event: %v", err)
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			break
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			t.Fatalf("malformed SSE line %q", line)
		}
		fields[name] = strings.TrimSpace(value)
	}
	if fields["id"] != fmt.Sprint(wantID) || fields["event"] != wantType || !json.Valid([]byte(fields["data"])) {
		t.Fatalf("SSE fields=%v want id=%d event=%s", fields, wantID, wantType)
	}
}

type replaySourceFunc func(context.Context, *uint64, int) ([]events.Event, error)

func (function replaySourceFunc) Replay(ctx context.Context, after *uint64, limit int) ([]events.Event, error) {
	return function(ctx, after, limit)
}

func TestEventStreamClassifiesCursorAndReplayFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		cursor     string
		replayErr  error
		status     int
		code       string
		mustRedact string
	}{
		{name: "malformed cursor", cursor: "not-a-number", status: http.StatusBadRequest, code: "invalid_event_cursor"},
		{name: "expired cursor", cursor: "1", replayErr: events.ErrExpiredCursor, status: http.StatusBadRequest, code: "invalid_event_cursor"},
		{name: "future cursor", cursor: "2", replayErr: events.ErrFutureCursor, status: http.StatusBadRequest, code: "invalid_event_cursor"},
		{
			name: "replay backend unavailable", cursor: "3", replayErr: errors.New("postgres://user:secret@example.invalid"),
			status: http.StatusServiceUnavailable, code: "event_replay_unavailable", mustRedact: "secret",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sourceCalled := false
			source := replaySourceFunc(func(context.Context, *uint64, int) ([]events.Event, error) {
				sourceCalled = true
				return nil, test.replayErr
			})
			broker, err := events.NewDurableBroker(8, source)
			if err != nil {
				t.Fatal(err)
			}
			handler, err := New(Options{
				Config: config.Default(), Reader: fakeReader{}, Events: broker,
				RequestID: func() string { return "request-events" },
			})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
			request.Header.Set("Last-Event-ID", test.cursor)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if recorder.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.status, recorder.Body.String())
			}
			var response struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Error.Code != test.code {
				t.Fatalf("code=%q want=%q body=%s", response.Error.Code, test.code, recorder.Body.String())
			}
			if test.cursor == "not-a-number" && sourceCalled {
				t.Fatal("malformed cursor reached durable replay source")
			}
			if test.mustRedact != "" && strings.Contains(recorder.Body.String(), test.mustRedact) {
				t.Fatalf("backend detail leaked: %s", recorder.Body.String())
			}
		})
	}
}
