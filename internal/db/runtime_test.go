package dbaccess

import (
	"context"
	"errors"
	"testing"

	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
)

type transactionDatabase struct {
	dbgen.DBTX
	transaction *recordingTransaction
	options     pgx.TxOptions
	begins      int
}

func (database *transactionDatabase) BeginTx(_ context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	database.begins++
	database.options = options
	return database.transaction, nil
}

type recordingTransaction struct {
	pgx.Tx
	commits            int
	rollbacks          int
	commitError        error
	cleanupError       error
	cleanupHasDeadline bool
}

func (transaction *recordingTransaction) Commit(context.Context) error {
	transaction.commits++
	return transaction.commitError
}
func (transaction *recordingTransaction) Rollback(ctx context.Context) error {
	transaction.rollbacks++
	transaction.cleanupError = ctx.Err()
	_, transaction.cleanupHasDeadline = ctx.Deadline()
	return nil
}

func TestMissingInputs(t *testing.T) {
	t.Parallel()
	callback := func(*dbgen.Queries) error { return nil }
	if err := WithQueries(t.Context(), nil, callback); err == nil {
		t.Fatal("nil database accepted")
	}
	if err := WithQueries(t.Context(), &transactionDatabase{}, nil); err == nil {
		t.Fatal("nil callback accepted")
	}
	if err := WithTransaction(t.Context(), nil, callback); err == nil {
		t.Fatal("nil database accepted")
	}
	if err := WithTransaction(t.Context(), &transactionDatabase{}, nil); err == nil {
		t.Fatal("nil callback accepted")
	}
}

func TestTransactionCancellationRollsBackWithLiveBoundedContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	transaction := &recordingTransaction{}
	database := &transactionDatabase{transaction: transaction}
	options := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	err := WithTransactionOptions(ctx, database, options, func(*dbgen.Queries) error {
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) || transaction.commits != 0 || transaction.rollbacks != 1 {
		t.Fatalf("cancellation failed: error=%v commits=%d rollbacks=%d", err, transaction.commits, transaction.rollbacks)
	}
	if transaction.cleanupError != nil || !transaction.cleanupHasDeadline || database.options != options {
		t.Fatalf("invalid cleanup or isolation: %+v %+v", transaction, database.options)
	}
}

func TestTransactionDoesNotRetryUnknownCommit(t *testing.T) {
	t.Parallel()
	unknown := errors.New("commit response lost")
	transaction := &recordingTransaction{commitError: unknown}
	database := &transactionDatabase{transaction: transaction}
	err := WithTransaction(t.Context(), database, func(*dbgen.Queries) error { return nil })
	if !errors.Is(err, unknown) || database.begins != 1 || transaction.commits != 1 || transaction.rollbacks != 1 {
		t.Fatalf("unknown commit was not preserved: %v %+v", err, transaction)
	}
}

func TestTransactionPanicStillRollsBack(t *testing.T) {
	t.Parallel()
	transaction := &recordingTransaction{}
	database := &transactionDatabase{transaction: transaction}
	defer func() {
		if recover() != "callback panic" || transaction.rollbacks != 1 || transaction.commits != 0 {
			t.Fatalf("callback panic leaked transaction: %+v", transaction)
		}
	}()
	_ = WithTransaction(t.Context(), database, func(*dbgen.Queries) error { panic("callback panic") })
}
