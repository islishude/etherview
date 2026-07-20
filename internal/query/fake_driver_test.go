package query

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

const fakeDriverName = "etherview-query-test"

var (
	fakeScripts sync.Map
	fakeDSN     atomic.Uint64
)

func init() { sql.Register(fakeDriverName, fakeSQLDriver{}) }

type queryExpectation struct {
	contains string
	columns  []string
	rows     [][]driver.Value
	err      error
	check    func([]driver.NamedValue) error
}

type queryScript struct {
	mu           sync.Mutex
	expectations []queryExpectation
}

func testDatabase(t *testing.T, expectations ...queryExpectation) *sql.DB {
	t.Helper()
	dsn := strconv.FormatUint(fakeDSN.Add(1), 10)
	script := &queryScript{expectations: append([]queryExpectation(nil), expectations...)}
	fakeScripts.Store(dsn, script)
	db, err := sql.Open(fakeDriverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = db.Close()
		fakeScripts.Delete(dsn)
		script.mu.Lock()
		defer script.mu.Unlock()
		if len(script.expectations) != 0 {
			t.Errorf("%d database expectations were not consumed; next contains %q", len(script.expectations), script.expectations[0].contains)
		}
	})
	return db
}

type fakeSQLDriver struct{}

func (fakeSQLDriver) Open(name string) (driver.Conn, error) {
	value, exists := fakeScripts.Load(name)
	if !exists {
		return nil, fmt.Errorf("unknown fake database %q", name)
	}
	return &fakeSQLConn{script: value.(*queryScript)}, nil
}

type fakeSQLConn struct{ script *queryScript }

func (c *fakeSQLConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are unsupported by fake query driver")
}

func (c *fakeSQLConn) Close() error { return nil }

func (c *fakeSQLConn) Begin() (driver.Tx, error) { return fakeSQLTx{}, nil }

func (c *fakeSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return fakeSQLTx{}, nil
}

func (c *fakeSQLConn) QueryContext(_ context.Context, query string, arguments []driver.NamedValue) (driver.Rows, error) {
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
	return &fakeSQLRows{columns: expectation.columns, rows: expectation.rows}, nil
}

func (c *fakeSQLConn) CheckNamedValue(*driver.NamedValue) error { return nil }

type fakeSQLTx struct{}

func (fakeSQLTx) Commit() error   { return nil }
func (fakeSQLTx) Rollback() error { return nil }

type fakeSQLRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (r *fakeSQLRows) Columns() []string { return r.columns }
func (r *fakeSQLRows) Close() error      { return nil }

func (r *fakeSQLRows) Next(destination []driver.Value) error {
	if r.index >= len(r.rows) {
		return io.EOF
	}
	row := r.rows[r.index]
	r.index++
	if len(row) != len(destination) {
		return fmt.Errorf("fake row has %d values, destination has %d", len(row), len(destination))
	}
	copy(destination, row)
	return nil
}

func compactSQL(value string) string { return strings.Join(strings.Fields(value), " ") }

func columns(count int) []string {
	result := make([]string, count)
	for index := range result {
		result[index] = fmt.Sprintf("column_%d", index)
	}
	return result
}
