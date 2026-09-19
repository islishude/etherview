package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/events"
)

func TestHomeVersionWaitsForObservedEvent(t *testing.T) {
	broker := events.NewBroker(8)
	fixture := &homeSnapshotDuringRefreshFixture{secondStart: make(chan struct{}), secondFinish: make(chan struct{}), thirdStart: make(chan struct{}), thirdFinish: make(chan struct{})}
	feed, err := NewHomeFeed(fixture, broker, HomeFeedOptions{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = feed.Run(ctx) }()
	subscription := subscribeHomeEventually(t, feed, ctx)
	receiveHomePublication(t, subscription)
	if _, err := broker.Publish("head", map[string]string{"number": "11"}); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, fixture.secondStart, "refresh did not start")
	cfg := config.Default()
	cfg.Chain.ID = 1
	handler, err := New(Options{Config: cfg, Reader: fakeReader{}, HomeSnapshots: feed})
	if err != nil {
		t.Fatal(err)
	}
	const readers = 8
	results := make(chan *httptest.ResponseRecorder, readers)
	for range readers {
		go func() {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/home?min_event_id=1", nil))
			results <- rec
		}()
	}
	select {
	case rec := <-results:
		t.Fatalf("returned old publication: %s", rec.Body)
	case <-time.After(20 * time.Millisecond):
	}
	close(fixture.secondFinish)
	for range readers {
		select {
		case rec := <-results:
			var response struct {
				EventID string       `json:"event_id"`
				Data    HomeSnapshot `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if rec.Code != 200 || response.EventID != "1" || response.Data.Status.LatestBlock != "11" {
				t.Fatalf("response=%s", rec.Body)
			}
		case <-time.After(time.Second):
			t.Fatal("snapshot wait hung")
		}
	}
}

func TestHomeVersionRejectsInvalidOrTimesOutFutureVersion(t *testing.T) {
	cfg := config.Default()
	cfg.Chain.ID = 1
	for _, value := range []string{"", "01", "-1", "+1", "9223372036854775808", "1&min_event_id=2"} {
		t.Run(value, func(t *testing.T) {
			handler, err := New(Options{Config: cfg, Reader: fakeReader{}})
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/home?min_event_id="+value, nil))
			if rec.Code != 400 {
				t.Fatalf("status=%d", rec.Code)
			}
		})
	}
	channel := make(chan HomePublication, 1)
	channel <- HomePublication{EventID: 0}
	handler, err := New(Options{Config: cfg, Reader: fakeReader{}, HomeSnapshots: homeSnapshotSourceFixture{channel: channel}})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	start := time.Now()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/home?min_event_id=9223372036854775807", nil))
	if rec.Code != 503 || time.Since(start) > 3*time.Second {
		t.Fatalf("status=%d duration=%s", rec.Code, time.Since(start))
	}
}

func TestHomeVersionCancellationRemovesSubscription(t *testing.T) {
	feed, err := NewHomeFeed(&homeSnapshotReaderFixture{}, events.NewBroker(8), HomeFeedOptions{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	feed.publish(HomePublication{EventID: 0})
	cfg := config.Default()
	cfg.Chain.ID = 1
	handler, err := New(Options{Config: cfg, Reader: fakeReader{}, HomeSnapshots: feed})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/home?min_event_id=1", nil).WithContext(ctx))
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not stop request")
	}
	deadline := time.Now().Add(time.Second)
	for {
		feed.mu.Lock()
		count := len(feed.subscribers)
		feed.mu.Unlock()
		if count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("subscription leaked")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestHomeVersionFenceSurvivesReplicaSwitch(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Chain.ID = 1
	newReplica := func(version uint64) (*Handler, *HomeFeed) {
		feed, err := NewHomeFeed(&homeSnapshotReaderFixture{}, events.NewBroker(8), HomeFeedOptions{ChainID: 1})
		if err != nil {
			t.Fatal(err)
		}
		feed.publish(HomePublication{EventID: version})
		handler, err := New(Options{Config: cfg, Reader: fakeReader{}, HomeSnapshots: feed})
		if err != nil {
			t.Fatal(err)
		}
		return handler, feed
	}
	fresh, _ := newReplica(11)
	lagging, feed := newReplica(10)
	current := httptest.NewRecorder()
	fresh.ServeHTTP(current, httptest.NewRequest(http.MethodGet, "/api/v1/home?min_event_id=11", nil))
	if current.Code != http.StatusOK {
		t.Fatalf("fresh replica: %s", current.Body)
	}
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		lagging.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/home?min_event_id=11", nil))
		result <- rec
	}()
	deadline := time.Now().Add(time.Second)
	for {
		feed.mu.Lock()
		subscribed := len(feed.subscribers) == 1
		feed.mu.Unlock()
		if subscribed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("lagging replica did not subscribe")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case rec := <-result:
		t.Fatalf("replica switch returned snapshot 10: %s", rec.Body)
	default:
	}
	feed.publish(HomePublication{EventID: 11})
	select {
	case rec := <-result:
		var envelope struct {
			EventID string `json:"event_id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusOK || envelope.EventID != "11" {
			t.Fatalf("lagging replica response=%s", rec.Body)
		}
	case <-time.After(time.Second):
		t.Fatal("replica publication did not wake request")
	}
}
