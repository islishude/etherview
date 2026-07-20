package etherscan

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

const etherscanFakeDriverName = "etherview-etherscan-test"

var (
	etherscanFakeScripts sync.Map
	etherscanFakeDSN     atomic.Uint64
)

func init() { sql.Register(etherscanFakeDriverName, etherscanFakeDriver{}) }

type sqlExpectation struct {
	contains string
	columns  []string
	rows     [][]driver.Value
	err      error
	check    func([]driver.NamedValue) error
}

type sqlScript struct {
	mu           sync.Mutex
	expectations []sqlExpectation
}

func fakeDatabase(t *testing.T, expectations ...sqlExpectation) *sql.DB {
	t.Helper()
	dsn := strconv.FormatUint(etherscanFakeDSN.Add(1), 10)
	script := &sqlScript{expectations: append([]sqlExpectation(nil), expectations...)}
	etherscanFakeScripts.Store(dsn, script)
	db, err := sql.Open(etherscanFakeDriverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = db.Close()
		etherscanFakeScripts.Delete(dsn)
		script.mu.Lock()
		defer script.mu.Unlock()
		if len(script.expectations) != 0 {
			t.Errorf("%d database expectations were not consumed; next contains %q", len(script.expectations), script.expectations[0].contains)
		}
	})
	return db
}

type etherscanFakeDriver struct{}

func (etherscanFakeDriver) Open(name string) (driver.Conn, error) {
	value, exists := etherscanFakeScripts.Load(name)
	if !exists {
		return nil, fmt.Errorf("unknown fake database %q", name)
	}
	return &etherscanFakeConn{script: value.(*sqlScript)}, nil
}

type etherscanFakeConn struct{ script *sqlScript }

func (c *etherscanFakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are unsupported by fake driver")
}
func (c *etherscanFakeConn) Close() error              { return nil }
func (c *etherscanFakeConn) Begin() (driver.Tx, error) { return etherscanFakeTx{}, nil }

func (c *etherscanFakeConn) BeginTx(_ context.Context, options driver.TxOptions) (driver.Tx, error) {
	if options.Isolation != driver.IsolationLevel(sql.LevelRepeatableRead) || !options.ReadOnly {
		return nil, fmt.Errorf("unexpected transaction options: %+v", options)
	}
	return etherscanFakeTx{}, nil
}
func (c *etherscanFakeConn) CheckNamedValue(*driver.NamedValue) error { return nil }
func (c *etherscanFakeConn) QueryContext(_ context.Context, query string, arguments []driver.NamedValue) (driver.Rows, error) {
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
	return &etherscanFakeRows{columns: expectation.columns, rows: expectation.rows}, nil
}

type etherscanFakeTx struct{}

func (etherscanFakeTx) Commit() error   { return nil }
func (etherscanFakeTx) Rollback() error { return nil }

type etherscanFakeRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (r *etherscanFakeRows) Columns() []string { return r.columns }
func (r *etherscanFakeRows) Close() error      { return nil }
func (r *etherscanFakeRows) Next(destination []driver.Value) error {
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

func fakeColumns(count int) []string {
	result := make([]string, count)
	for index := range result {
		result[index] = fmt.Sprintf("column_%d", index)
	}
	return result
}
