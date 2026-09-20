package query

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

type queryExpectation struct {
	contains string
	columns  []string
	rows     [][]any
	err      error
	check    func([]any) error
}

type queryScript struct {
	mu           sync.Mutex
	expectations []queryExpectation
}

func testDatabase(t *testing.T, expectations ...queryExpectation) *fakeSQLConn {
	t.Helper()
	script := &queryScript{expectations: append([]queryExpectation(nil), expectations...)}
	t.Cleanup(func() {
		script.mu.Lock()
		defer script.mu.Unlock()
		if len(script.expectations) != 0 {
			t.Errorf("%d database expectations were not consumed; next contains %q", len(script.expectations), script.expectations[0].contains)
		}
	})
	return &fakeSQLConn{script: script}
}

type fakeSQLConn struct {
	pgx.Tx
	script *queryScript
	closed bool
}

func (c *fakeSQLConn) BeginTx(_ context.Context, options pgx.TxOptions) (pgx.Tx, error) {

	return &fakeSQLConn{script: c.script}, nil
}
func (c *fakeSQLConn) Commit(context.Context) error {
	if c.closed {
		return pgx.ErrTxClosed
	}
	c.closed = true
	return nil
}
func (c *fakeSQLConn) Rollback(context.Context) error {
	if c.closed {
		return pgx.ErrTxClosed
	}
	c.closed = true
	return nil
}
func (c *fakeSQLConn) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return testpgx.Row(c.Query(ctx, query, args...))
}
func (c *fakeSQLConn) Query(_ context.Context, query string, arguments ...any) (pgx.Rows, error) {
	c.script.mu.Lock()
	defer c.script.mu.Unlock()
	if len(c.script.expectations) == 0 {
		return nil, fmt.Errorf("unexpected query: %s", compactSQL(query))
	}
	expectation := c.script.expectations[0]
	c.script.expectations = c.script.expectations[1:]
	if !strings.Contains(compactSQL(query), compactSQL(expectation.contains)) {
		return nil, fmt.Errorf("query %q does not contain expected %q", compactSQL(query), compactSQL(expectation.contains))
	}
	if expectation.check != nil {
		if err := expectation.check(arguments); err != nil {
			return nil, err
		}
	}
	if expectation.err != nil {
		return nil, expectation.err
	}
	return &testpgx.Rows{ColumnNames: expectation.columns, ValuesList: expectation.rows}, nil
}
func (c *fakeSQLConn) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, fmt.Errorf("unexpected exec")
}
func compactSQL(value string) string { return strings.Join(strings.Fields(value), " ") }
func columns(count int) []string {
	result := make([]string, count)
	for index := range result {
		result[index] = fmt.Sprintf("column_%d", index)
	}
	return result
}
