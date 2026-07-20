package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/islishude/etherview/internal/ethrpc"
)

const refreshDriverName = "etherview-store-refresh-test"

var (
	refreshScripts sync.Map
	refreshDSN     atomic.Uint64
)

func init() { sql.Register(refreshDriverName, refreshDriver{}) }

type refreshStep struct {
	kind     string
	contains string
	columns  int
	rows     [][]driver.Value
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
	steps := refreshHappyPathSteps(1, storeTestHash(2), storeTestHash(1))
	steps[len(steps)-1].err = writeErr
	db, script := refreshDatabase(t, steps...)
	repository := newRefreshRepository(t, db)
	err := repository.RefreshCanonical(
		context.Background(), "1",
		storeTestBundle(1, storeTestHash(2), storeTestHash(1)),
		RefreshOptions{},
	)
	if !errors.Is(err, writeErr) {
		t.Fatalf("error=%v", err)
	}
	assertRefreshTransactions(t, script, 0, 1)
}

func TestPostgresRefreshCanonicalIdentityMismatchDoesNotDeleteFacts(t *testing.T) {
	t.Parallel()
	db, script := refreshDatabase(t,
		refreshStep{kind: "exec", contains: "pg_advisory_xact_lock", affected: 1},
		refreshCanonicalRow(1, storeTestHash(99), storeTestHash(1)),
	)
	repository := newRefreshRepository(t, db)
	err := repository.RefreshCanonical(
		context.Background(), "1",
		storeTestBundle(1, storeTestHash(2), storeTestHash(1)),
		RefreshOptions{},
	)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error=%v", err)
	}
	assertRefreshTransactions(t, script, 0, 1)
}

func TestPostgresRefreshCanonicalIsIdempotentAndDoesNotMoveCanonicalState(t *testing.T) {
	t.Parallel()
	steps := append(
		refreshHappyPathSteps(1, storeTestHash(2), storeTestHash(1)),
		refreshHappyPathSteps(1, storeTestHash(2), storeTestHash(1))...,
	)
	db, script := refreshDatabase(t, steps...)
	repository := newRefreshRepository(t, db)
	bundle := storeTestBundle(1, storeTestHash(2), storeTestHash(1))
	for range 2 {
		if err := repository.RefreshCanonical(context.Background(), "1", bundle, RefreshOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	// The strict script would reject any canonical_blocks or index_checkpoints
	// write not listed in refreshHappyPathSteps.
	assertRefreshTransactions(t, script, 2, 0)
}

func refreshHappyPathSteps(number uint64, hash, parentHash ethrpc.Hash) []refreshStep {
	steps := []refreshStep{
		{kind: "exec", contains: "pg_advisory_xact_lock", affected: 1},
		refreshCanonicalRow(number, hash, parentHash),
	}
	if number > 0 {
		steps = append(steps, refreshCanonicalRow(number-1, parentHash, storeTestHash(0)))
	}
	steps = append(steps, refreshStep{kind: "query", contains: "FROM chain_finality", columns: 5})
	for _, table := range []string{
		"block_stage_results", "abi_decodings", "contract_abis", "token_balance_deltas", "token_events",
		"normalized_traces", "address_activities", "block_statistics",
		"block_journals", "logs", "receipts", "transaction_inclusions", "withdrawals",
	} {
		steps = append(steps, refreshStep{kind: "exec", contains: "DELETE FROM " + table, affected: 0})
	}
	steps = append(steps, refreshStep{kind: "exec", contains: "INSERT INTO blocks", affected: 1})
	return steps
}

func newRefreshRepository(t *testing.T, db *sql.DB) *PostgresRepository {
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

func refreshCanonicalRow(number uint64, hash, parentHash ethrpc.Hash) refreshStep {
	return refreshStep{
		kind: "query", contains: "FROM canonical_blocks cb", columns: 3,
		rows: [][]driver.Value{{
			strconv.FormatUint(number, 10),
			mustHashBytes(hash),
			mustHashBytes(parentHash),
		}},
	}
}

func refreshDatabase(t *testing.T, steps ...refreshStep) (*sql.DB, *refreshScript) {
	t.Helper()
	dsn := strconv.FormatUint(refreshDSN.Add(1), 10)
	script := &refreshScript{steps: append([]refreshStep(nil), steps...)}
	refreshScripts.Store(dsn, script)
	db, err := sql.Open(refreshDriverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = db.Close()
		refreshScripts.Delete(dsn)
		script.mu.Lock()
		defer script.mu.Unlock()
		if len(script.steps) != 0 {
			t.Errorf("%d refresh SQL steps were not consumed; next %s contains %q", len(script.steps), script.steps[0].kind, script.steps[0].contains)
		}
	})
	return db, script
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

type refreshDriver struct{}

func (refreshDriver) Open(name string) (driver.Conn, error) {
	value, exists := refreshScripts.Load(name)
	if !exists {
		return nil, fmt.Errorf("unknown refresh database %q", name)
	}
	return &refreshConnection{script: value.(*refreshScript)}, nil
}

type refreshConnection struct{ script *refreshScript }

func (*refreshConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("refresh test driver does not support prepared statements")
}
func (*refreshConnection) Close() error { return nil }
func (connection *refreshConnection) Begin() (driver.Tx, error) {
	return &refreshTransaction{script: connection.script}, nil
}
func (connection *refreshConnection) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return &refreshTransaction{script: connection.script}, nil
}
func (*refreshConnection) CheckNamedValue(*driver.NamedValue) error { return nil }

func (connection *refreshConnection) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	step, err := connection.next("exec", query)
	if err != nil {
		return nil, err
	}
	if step.err != nil {
		return nil, step.err
	}
	return driver.RowsAffected(step.affected), nil
}

func (connection *refreshConnection) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	step, err := connection.next("query", query)
	if err != nil {
		return nil, err
	}
	if step.err != nil {
		return nil, step.err
	}
	columns := make([]string, step.columns)
	for index := range columns {
		columns[index] = fmt.Sprintf("column_%d", index)
	}
	return &refreshRows{columns: columns, rows: step.rows}, nil
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

type refreshTransaction struct{ script *refreshScript }

func (tx *refreshTransaction) Commit() error {
	tx.script.mu.Lock()
	defer tx.script.mu.Unlock()
	tx.script.committed++
	return nil
}

func (tx *refreshTransaction) Rollback() error {
	tx.script.mu.Lock()
	defer tx.script.mu.Unlock()
	tx.script.rolledBack++
	return nil
}

type refreshRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (rows *refreshRows) Columns() []string { return rows.columns }
func (*refreshRows) Close() error           { return nil }
func (rows *refreshRows) Next(destination []driver.Value) error {
	if rows.index >= len(rows.rows) {
		return io.EOF
	}
	copy(destination, rows.rows[rows.index])
	rows.index++
	return nil
}

func compactRefreshSQL(value string) string { return strings.Join(strings.Fields(value), " ") }
