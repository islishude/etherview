package maintenance

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

type sqlStep struct {
	kind     string
	contains string
	columns  []string
	rows     [][]any
	affected int64
	err      error
	check    func([]any) error
}

type sqlScript struct {
	mu    sync.Mutex
	steps []sqlStep
}

func maintenanceDatabase(t *testing.T, expectations ...sqlStep) *maintenanceFakeConn {
	t.Helper()
	script := &sqlScript{steps: append([]sqlStep(nil), expectations...)}
	t.Cleanup(func() {
		script.mu.Lock()
		defer script.mu.Unlock()
		if len(script.steps) != 0 {
			t.Errorf("%d database expectations were not consumed; next contains %q", len(script.steps), script.steps[0].contains)
		}
	})
	return &maintenanceFakeConn{script: script}
}

type maintenanceFakeConn struct {
	pgx.Tx
	script *sqlScript
	closed bool
}

func (c *maintenanceFakeConn) BeginTx(_ context.Context, options pgx.TxOptions) (pgx.Tx, error) {

	return &maintenanceFakeConn{script: c.script}, nil
}
func (c *maintenanceFakeConn) Commit(context.Context) error {
	if c.closed {
		return pgx.ErrTxClosed
	}
	c.closed = true
	return nil
}
func (c *maintenanceFakeConn) Rollback(context.Context) error {
	if c.closed {
		return pgx.ErrTxClosed
	}
	c.closed = true
	return nil
}
func (c *maintenanceFakeConn) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return testpgx.Row(c.Query(ctx, query, args...))
}
func (connection *maintenanceFakeConn) Query(_ context.Context, query string, arguments ...any) (pgx.Rows, error) {
	step, err := connection.next("query", query, arguments)
	if err != nil {
		return nil, err
	}
	if step.err != nil {
		return nil, step.err
	}
	return &testpgx.Rows{ColumnNames: step.columns, ValuesList: step.rows}, nil
}
func (connection *maintenanceFakeConn) Exec(_ context.Context, query string, arguments ...any) (pgconn.CommandTag, error) {
	step, err := connection.next("exec", query, arguments)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	if step.err != nil {
		return pgconn.CommandTag{}, step.err
	}
	return testpgx.Affected(step.affected), nil
}
func (connection *maintenanceFakeConn) next(kind, query string, arguments []any) (sqlStep, error) {
	connection.script.mu.Lock()
	defer connection.script.mu.Unlock()
	if len(connection.script.steps) == 0 {
		return sqlStep{}, fmt.Errorf("unexpected %s: %s", kind, compactMaintenanceSQL(query))
	}
	step := connection.script.steps[0]
	connection.script.steps = connection.script.steps[1:]
	if step.kind != kind {
		return sqlStep{}, fmt.Errorf("got %s %q, expected %s %q", kind, compactMaintenanceSQL(query), step.kind, step.contains)
	}
	if !strings.Contains(compactMaintenanceSQL(query), compactMaintenanceSQL(step.contains)) {
		return sqlStep{}, fmt.Errorf("SQL %q does not contain %q", compactMaintenanceSQL(query), compactMaintenanceSQL(step.contains))
	}
	if step.check != nil {
		if err := step.check(arguments); err != nil {
			return sqlStep{}, err
		}
	}
	return step, nil
}
func (c *maintenanceFakeConn) Release()                {}
func (c *maintenanceFakeConn) Discard(context.Context) {}
func compactMaintenanceSQL(value string) string        { return strings.Join(strings.Fields(value), " ") }
func maintenanceColumns(count int) []string {
	columns := make([]string, count)
	for index := range columns {
		columns[index] = fmt.Sprintf("column_%d", index)
	}
	return columns
}
