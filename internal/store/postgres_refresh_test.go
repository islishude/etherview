package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	testpgx "github.com/islishude/etherview/internal/testpgx"
	pgx "github.com/jackc/pgx/v5"
	pgconn "github.com/jackc/pgx/v5/pgconn"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/chainbundle"
)

type refreshStep struct {
	check    func([]any) error
	kind     string
	contains string
	columns  int
	rows     [][]any
	affected int64
	err      error
}

type refreshScript struct {
	mu         sync.Mutex
	steps      []refreshStep
	committed  int
	rolledBack int
}

func TestPostgresRefreshCanonicalRollsBackWholeReplacementOnWriteFailure(t *testing.T) {
	t.Parallel()
	writeErr := errors.New("injected block replacement failure")
	bundle := refreshTestBundle(t)
	reference := mustStoreTestRef(t, bundle)
	steps := refreshHappyPathSteps(
		reference.Number,
		reference.Hash,
		reference.ParentHash,
	)
	steps[len(steps)-1].err = writeErr
	db, script := refreshDatabase(t, steps...)
	repository := newRefreshRepository(t, db)
	err := repository.RefreshCanonical(
		context.Background(), "1",
		bundle,
		RefreshOptions{},
	)
	if !errors.Is(err, writeErr) {
		t.Fatalf("error=%v", err)
	}
	assertRefreshTransactions(t, script, 0, 1)
}

func TestPostgresRefreshCanonicalIdentityMismatchDoesNotDeleteFacts(t *testing.T) {
	t.Parallel()
	bundle := refreshTestBundle(t)
	reference := mustStoreTestRef(t, bundle)
	db, script := refreshDatabase(t,
		refreshStep{kind: "exec", contains: "pg_advisory_xact_lock", affected: 1},
		refreshCanonicalRow(
			reference.Number,
			storeTestHash(99),
			reference.ParentHash,
		),
	)
	repository := newRefreshRepository(t, db)
	err := repository.RefreshCanonical(
		context.Background(), "1",
		bundle,
		RefreshOptions{},
	)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error=%v", err)
	}
	assertRefreshTransactions(t, script, 0, 1)
}

func TestPostgresRefreshCanonicalIsIdempotentAndDoesNotMoveCanonicalState(t *testing.T) {
	t.Parallel()
	bundle := refreshTestBundle(t)
	reference := mustStoreTestRef(t, bundle)
	steps := append(
		refreshHappyPathSteps(
			reference.Number,
			reference.Hash,
			reference.ParentHash,
		),
		refreshHappyPathSteps(
			reference.Number,
			reference.Hash,
			reference.ParentHash,
		)...,
	)
	db, script := refreshDatabase(t, steps...)
	repository := newRefreshRepository(t, db)
	for range 2 {
		if err := repository.RefreshCanonical(context.Background(), "1", bundle, RefreshOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	// The strict script would reject any canonical_blocks or index_checkpoints
	// write not listed in refreshHappyPathSteps.
	assertRefreshTransactions(t, script, 2, 0)
}

func refreshHappyPathSteps(number uint64, hash, parentHash common.Hash) []refreshStep {
	steps := []refreshStep{
		{kind: "exec", contains: "pg_advisory_xact_lock", affected: 1},
		refreshCanonicalRow(number, hash, parentHash),
	}
	if number > 0 {
		steps = append(steps, refreshCanonicalRow(number-1, parentHash, common.Hash{}))
	}
	steps = append(steps, refreshStep{kind: "query", contains: "FROM chain_finality", columns: 5})
	steps = append(steps,
		refreshStep{kind: "exec", contains: "-- name: StoreDeleteDerivedBlockFacts :exec", affected: 0},
		refreshStep{kind: "exec", contains: "DELETE FROM block_journals", affected: 0},
		refreshStep{kind: "exec", contains: "-- name: StoreDeleteCoreBlockFacts :exec", affected: 0},
	)
	steps = append(steps, refreshStep{kind: "exec", contains: "INSERT INTO blocks", affected: 1})
	return steps
}

func refreshTestBundle(t *testing.T) chainbundle.Bundle {
	t.Helper()
	genesis := storeTestBundle(0, common.Hash{}, 1)
	return storeTestBundle(
		1,
		mustStoreTestRef(t, genesis).Hash,
		2,
	)
}

func newRefreshRepository(t *testing.T, db *refreshConnection) *PostgresRepository {
	t.Helper()
	repository, err := NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	// This focused SQL script models a database whose migration-owned initial
	// range has already been catalog-checked by startup provisioning.
	repository.partitions.add(0)
	return repository
}

func refreshCanonicalRow(number uint64, hash, parentHash common.Hash) refreshStep {
	return refreshStep{
		kind: "query", contains: "-- name: StoreLockCanonicalBlock :one", columns: 3,
		rows: [][]any{{
			strconv.FormatUint(number, 10),
			mustHashBytes(hash),
			mustHashBytes(parentHash),
		}},
	}
}

func refreshDatabase(t *testing.T, steps ...refreshStep) (*refreshConnection, *refreshScript) {
	t.Helper()
	script := &refreshScript{steps: append([]refreshStep(nil), steps...)}
	t.Cleanup(func() {
		script.mu.Lock()
		defer script.mu.Unlock()
		if len(script.steps) != 0 {
			t.Errorf("%d refresh steps not consumed; next %q", len(script.steps), script.steps[0].contains)
		}
	})
	return &refreshConnection{script: script}, script
}

func assertRefreshTransactions(t *testing.T, script *refreshScript, committed, rolledBack int) {
	t.Helper()
	script.mu.Lock()
	defer script.mu.Unlock()
	if script.committed != committed || script.rolledBack != rolledBack {
		t.Fatalf(
			"transactions committed=%d rolled_back=%d, want %d/%d",
			script.committed, script.rolledBack, committed, rolledBack,
		)
	}
}

type refreshConnection struct {
	pgx.Tx
	script *refreshScript
	done   bool
}

func (connection *refreshConnection) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return &refreshConnection{script: connection.script}, nil
}
func (connection *refreshConnection) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return testpgx.Row(connection.Query(ctx, query, args...))
}
func (connection *refreshConnection) Commit(context.Context) error {
	connection.script.mu.Lock()
	defer connection.script.mu.Unlock()
	if connection.done {
		return pgx.ErrTxClosed
	}
	connection.done = true
	connection.script.committed++
	return nil
}
func (connection *refreshConnection) Rollback(context.Context) error {
	connection.script.mu.Lock()
	defer connection.script.mu.Unlock()
	if connection.done {
		return pgx.ErrTxClosed
	}
	connection.done = true
	connection.script.rolledBack++
	return nil
}
func (connection *refreshConnection) Exec(_ context.Context, query string, _ ...any) (pgconn.CommandTag, error) {
	step, err := connection.next("exec", query)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	if step.err != nil {
		return pgconn.CommandTag{}, step.err
	}
	return testpgx.Affected(step.affected), nil
}
func (connection *refreshConnection) Query(_ context.Context, query string, arguments ...any) (pgx.Rows, error) {
	step, err := connection.next("query", query)
	if err != nil {
		return nil, err
	}
	if step.err != nil {
		return nil, step.err
	}
	if step.check != nil {
		if err := step.check(arguments); err != nil {
			return nil, err
		}
	}
	columns := make([]string, step.columns)
	for index := range columns {
		columns[index] = fmt.Sprintf("column_%d", index)
	}
	return &testpgx.Rows{ColumnNames: columns, ValuesList: step.rows}, nil
}
func (connection *refreshConnection) next(kind, query string) (refreshStep, error) {
	connection.script.mu.Lock()
	defer connection.script.mu.Unlock()
	if len(connection.script.steps) == 0 {
		return refreshStep{}, fmt.Errorf("unexpected refresh %s: %s", kind, compactRefreshSQL(query))
	}
	step := connection.script.steps[0]
	connection.script.steps = connection.script.steps[1:]
	if step.kind != kind || !strings.Contains(compactRefreshSQL(query), compactRefreshSQL(step.contains)) {
		return refreshStep{}, fmt.Errorf(
			"refresh %s %q does not match expected %s containing %q",
			kind, compactRefreshSQL(query), step.kind, compactRefreshSQL(step.contains),
		)
	}
	return step, nil
}
func compactRefreshSQL(value string) string { return strings.Join(strings.Fields(value), " ") }
