package state

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
)

const stateFakeDriverName = "etherview-state-test"

var (
	stateFakeScripts sync.Map
	stateFakeDSN     atomic.Uint64
)

func init() { sql.Register(stateFakeDriverName, stateFakeDriver{}) }

type stateSQLExpectation struct {
	kind         string
	contains     string
	columns      []string
	rows         [][]driver.Value
	rowsAffected int64
	err          error
	check        func([]driver.NamedValue) error
}

type stateSQLScript struct {
	mu           sync.Mutex
	expectations []stateSQLExpectation
}

func stateTestDatabase(t *testing.T, expectations ...stateSQLExpectation) *sql.DB {
	t.Helper()
	dsn := strconv.FormatUint(stateFakeDSN.Add(1), 10)
	script := &stateSQLScript{expectations: append([]stateSQLExpectation(nil), expectations...)}
	stateFakeScripts.Store(dsn, script)
	db, err := sql.Open(stateFakeDriverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = db.Close()
		stateFakeScripts.Delete(dsn)
		script.mu.Lock()
		defer script.mu.Unlock()
		if len(script.expectations) != 0 {
			t.Errorf(
				"%d state database expectations were not consumed; next %s contains %q",
				len(script.expectations),
				script.expectations[0].kind,
				script.expectations[0].contains,
			)
		}
	})
	return db
}

type stateFakeDriver struct{}

func (stateFakeDriver) Open(name string) (driver.Conn, error) {
	value, exists := stateFakeScripts.Load(name)
	if !exists {
		return nil, fmt.Errorf("unknown state fake database %q", name)
	}
	return &stateFakeConn{script: value.(*stateSQLScript)}, nil
}

type stateFakeConn struct{ script *stateSQLScript }

func (*stateFakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are unsupported by state fake driver")
}

func (*stateFakeConn) Close() error { return nil }

func (*stateFakeConn) Begin() (driver.Tx, error) { return stateFakeTx{}, nil }

func (*stateFakeConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return stateFakeTx{}, nil
}

func (*stateFakeConn) CheckNamedValue(*driver.NamedValue) error { return nil }

func (connection *stateFakeConn) QueryContext(
	_ context.Context,
	query string,
	arguments []driver.NamedValue,
) (driver.Rows, error) {
	expectation, err := connection.next("query", query, arguments)
	if err != nil {
		return nil, err
	}
	if expectation.err != nil {
		return nil, expectation.err
	}
	return &stateFakeRows{columns: expectation.columns, rows: expectation.rows}, nil
}

func (connection *stateFakeConn) ExecContext(
	_ context.Context,
	query string,
	arguments []driver.NamedValue,
) (driver.Result, error) {
	expectation, err := connection.next("exec", query, arguments)
	if err != nil {
		return nil, err
	}
	if expectation.err != nil {
		return nil, expectation.err
	}
	return driver.RowsAffected(expectation.rowsAffected), nil
}

func (connection *stateFakeConn) next(
	kind string,
	query string,
	arguments []driver.NamedValue,
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

type stateFakeTx struct{}

func (stateFakeTx) Commit() error   { return nil }
func (stateFakeTx) Rollback() error { return nil }

type stateFakeRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (rows *stateFakeRows) Columns() []string { return rows.columns }
func (*stateFakeRows) Close() error           { return nil }

func (rows *stateFakeRows) Next(destination []driver.Value) error {
	if rows.index >= len(rows.rows) {
		return io.EOF
	}
	row := rows.rows[rows.index]
	rows.index++
	if len(row) != len(destination) {
		return fmt.Errorf("state fake row has %d values, destination has %d", len(row), len(destination))
	}
	copy(destination, row)
	return nil
}

func compactStateSQL(value string) string { return strings.Join(strings.Fields(value), " ") }
