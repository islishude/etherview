package state

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/islishude/etherview/internal/testpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type stateSQLExpectation struct {
	kind         string
	contains     string
	columns      []string
	rows         [][]any
	rowsAffected int64
	err          error
	check        func([]any) error
}

type stateSQLScript struct {
	mu           sync.Mutex
	expectations []stateSQLExpectation
}

func stateTestDatabase(t *testing.T, expectations ...stateSQLExpectation) *stateFakeConn {
	t.Helper()
	script := &stateSQLScript{expectations: append([]stateSQLExpectation(nil), expectations...)}
	t.Cleanup(func() {
		script.mu.Lock()
		defer script.mu.Unlock()
		if len(script.expectations) != 0 {
			t.Errorf("%d database expectations were not consumed; next contains %q", len(script.expectations), script.expectations[0].contains)
		}
	})
	return &stateFakeConn{script: script}
}

type stateFakeConn struct {
	pgx.Tx
	script *stateSQLScript
	closed bool
}

func (c *stateFakeConn) BeginTx(_ context.Context, options pgx.TxOptions) (pgx.Tx, error) {

	return &stateFakeConn{script: c.script}, nil
}
func (c *stateFakeConn) Commit(context.Context) error {
	if c.closed {
		return pgx.ErrTxClosed
	}
	c.closed = true
	return nil
}
func (c *stateFakeConn) Rollback(context.Context) error {
	if c.closed {
		return pgx.ErrTxClosed
	}
	c.closed = true
	return nil
}
func (c *stateFakeConn) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return testpgx.Row(c.Query(ctx, query, args...))
}
func (connection *stateFakeConn) Query(
	_ context.Context,
	query string,
	arguments ...any,
) (pgx.Rows, error) {
	expectation, err := connection.next("query", query, arguments)
	if err != nil {
		return nil, err
	}
	if expectation.err != nil {
		return nil, expectation.err
	}
	return &testpgx.Rows{ColumnNames: expectation.columns, ValuesList: expectation.rows}, nil
}
func (connection *stateFakeConn) Exec(
	_ context.Context,
	query string,
	arguments ...any,
) (pgconn.CommandTag, error) {
	expectation, err := connection.next("exec", query, arguments)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	if expectation.err != nil {
		return pgconn.CommandTag{}, expectation.err
	}
	return testpgx.Affected(expectation.rowsAffected), nil
}
func (connection *stateFakeConn) next(
	kind string,
	query string,
	arguments []any,
) (stateSQLExpectation, error) {
	connection.script.mu.Lock()
	defer connection.script.mu.Unlock()
	if len(connection.script.expectations) == 0 {
		return stateSQLExpectation{}, fmt.Errorf("unexpected state %s: %s", kind, compactStateSQL(query))
	}
	expectation := connection.script.expectations[0]
	connection.script.expectations = connection.script.expectations[1:]
	if expectation.kind != kind {
		return stateSQLExpectation{}, fmt.Errorf("state SQL kind=%q, want %q", kind, expectation.kind)
	}
	if !strings.Contains(compactStateSQL(query), compactStateSQL(expectation.contains)) {
		return stateSQLExpectation{}, fmt.Errorf(
			"state query %q does not contain expected %q",
			compactStateSQL(query),
			compactStateSQL(expectation.contains),
		)
	}
	if expectation.check != nil {
		if err := expectation.check(arguments); err != nil {
			return stateSQLExpectation{}, err
		}
	}
	return expectation, nil
}
func compactStateSQL(value string) string { return strings.Join(strings.Fields(value), " ") }
