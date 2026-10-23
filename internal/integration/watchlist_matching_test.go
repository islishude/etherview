//go:build integration

package integration_test

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/watchlist"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pushRecorder struct {
	stop     func()
	onSend   func(context.Context) error
	mu       sync.Mutex
	payloads [][]byte
	status   int
}

func (s *pushRecorder) Send(ctx context.Context, _, _, _ string, payload []byte) (int, error) {
	if s.onSend != nil {
		if err := s.onSend(ctx); err != nil {
			return 0, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.payloads = append(s.payloads, append([]byte(nil), payload...))
	return s.status, nil
}
func (s *pushRecorder) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.payloads) }
func startPushWorker(t *testing.T, db *pgxpool.Pool, user string) *pushRecorder {
	t.Helper()
	session := uuid.NewString()
	execFixture(t, t.Context(), db, `INSERT INTO user_sessions(id,user_id,token_digest,csrf_digest,created_at,expires_at,last_used_at) VALUES($1,$2,$3,$4,now(),now()+interval '1 hour',now())`, session, user, []byte(strings.Repeat("t", 32)), []byte(strings.Repeat("c", 32)))
	key, e := ecdh.P256().GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	service, e := watchlist.New(db, 1, watchlist.PushOptions{Enabled: true, PublicKey: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())})
	if e != nil {
		t.Fatal(e)
	}
	_, e = service.SavePushSubscription(t.Context(), user, session, gen.PushSubscriptionInput{Endpoint: "https://push.example/" + session, P256dh: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), Auth: base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("a", 16))), Language: "en"})
	if e != nil {
		t.Fatal(e)
	}
	sender := &pushRecorder{status: 201}
	runPushWorker(t, db, sender)
	return sender
}
func runPushWorker(t *testing.T, db *pgxpool.Pool, sender *pushRecorder) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	worker, e := watchlist.NewPushWorker(db, 1, sender, nil)
	if e != nil {
		t.Fatal(e)
	}
	go func() { defer close(done); _ = worker.Run(ctx) }()
	sender.stop = func() { cancel(); <-done }
	t.Cleanup(sender.stop)
	// Observe the public leader lock, then allow its initial boundary snapshot.
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, e := db.Acquire(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		var locked bool
		e = conn.QueryRow(t.Context(), `SELECT pg_try_advisory_lock(hashtext('etherview:browser-push'),hashtext('1'))`).Scan(&locked)
		if locked {
			_, _ = conn.Exec(t.Context(), `SELECT pg_advisory_unlock(hashtext('etherview:browser-push'),hashtext('1'))`)
		}
		conn.Release()
		if e != nil {
			t.Fatal(e)
		}
		if !locked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("push worker did not acquire leader lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
}
func waitPushCount(t *testing.T, s *pushRecorder, want int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if s.count() == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("push deliveries=%d want=%d", s.count(), want)
}
func assertPushQuiet(t *testing.T, s *pushRecorder, want int) {
	t.Helper()
	time.Sleep(2200 * time.Millisecond)
	if s.count() != want {
		t.Fatalf("unexpected repeated push: %d want %d", s.count(), want)
	}
}
