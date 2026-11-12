//go:build integration

package integration_test

import (
	"github.com/islishude/etherview/internal/httpapi"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRequestBudgetCancelsPostgresPoolAndLockWaits(t *testing.T) {
	for _, kind := range []string{"pool", "lock"} {
		t.Run(kind, func(t *testing.T) {
			db := newMigratedPostgres(t)
			var held []*pgxpool.Conn
			count := 1
			if kind == "pool" {
				count = int(db.Config().MaxConns)
			}
			for range count {
				conn, err := db.Acquire(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				held = append(held, conn)
			}
			defer func() {
				for _, conn := range held {
					conn.Release()
				}
			}()
			if kind == "lock" {
				if _, err := held[0].Exec(t.Context(), `SELECT pg_advisory_lock(979134)`); err != nil {
					t.Fatal(err)
				}
				defer func() { _, _ = held[0].Exec(t.Context(), `SELECT pg_advisory_unlock(979134)`) }()
			}
			response := httptest.NewRecorder()
			started := time.Now()
			httpapi.RequestBudget(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := db.Exec(r.Context(), `SELECT pg_advisory_xact_lock(979134)`)
				if err == nil {
					t.Error("database wait was not cancelled")
				}
				w.WriteHeader(http.StatusInternalServerError)
			}), 30*time.Millisecond).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/blocks", nil))
			if response.Code != 504 || !strings.Contains(response.Body.String(), "request_timeout") {
				t.Fatalf("response=%d %s", response.Code, response.Body)
			}
			if time.Since(started) > time.Second {
				t.Fatal("database wait outlived request budget")
			}
		})
	}
}
