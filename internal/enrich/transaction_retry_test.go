package enrich

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestAbortedQueueTransactionRetryPolicy(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"40P01", "40001", "23505", "08006", "57014"} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			failure := fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: code})
			calls := 0
			err := retryAbortedQueueTransaction(t.Context(), func() error {
				calls++
				return failure
			})
			want := 1
			if code == "40P01" || code == "40001" {
				want = queueTransactionAttempts
			}
			if calls != want || err != failure {
				t.Fatalf("calls=%d want=%d err=%v", calls, want, err)
			}
		})
	}
	for _, failure := range []error{ErrLeaseLost, errors.New("ambiguous commit"), nil} {
		calls := 0
		err := retryAbortedQueueTransaction(t.Context(), func() error { calls++; return failure })
		if calls != 1 || err != failure {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	}
}

func TestAbortedQueueTransactionRetryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	err := retryAbortedQueueTransaction(ctx, func() error {
		calls++
		cancel()
		return &pgconn.PgError{Code: "40P01"}
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestAbortedQueueTransactionRechecksLease(t *testing.T) {
	for _, next := range []error{nil, ErrLeaseLost} {
		calls := 0
		err := retryAbortedQueueTransaction(t.Context(), func() error {
			calls++
			if calls == 1 {
				return &pgconn.PgError{Code: "40P01"}
			}
			return next
		})
		if calls != 2 || err != next {
			t.Fatalf("calls=%d err=%v want=%v", calls, err, next)
		}
	}
}
