package maintenance

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

const maintenanceFakeDriverName = "etherview-maintenance-test"

var (
	maintenanceScripts sync.Map
	maintenanceDSN     atomic.Uint64
)

func init() { sql.Register(maintenanceFakeDriverName, maintenanceFakeDriver{}) }

type sqlStep struct {
	kind     string
	contains string
	columns  []string
	rows     [][]driver.Value
	affected int64
	err      error
	check    func([]driver.NamedValue) error
}

type sqlScript struct {
	mu    sync.Mutex
	steps []sqlStep
}

func maintenanceDatabase(t *testing.T, steps ...sqlStep) *sql.DB {
	t.Helper()
	dsn := strconv.FormatUint(maintenanceDSN.Add(1), 10)
	script := &sqlScript{steps: append([]sqlStep(nil), steps...)}
	maintenanceScripts.Store(dsn, script)
	db, err := sql.Open(maintenanceFakeDriverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = db.Close()
		maintenanceScripts.Delete(dsn)
		script.mu.Lock()
		defer script.mu.Unlock()
		if len(script.steps) != 0 {
			t.Errorf("%d SQL steps were not consumed; next %s contains %q", len(script.steps), script.steps[0].kind, script.steps[0].contains)
		}
	})
	return db
}

type maintenanceFakeDriver struct{}

func (maintenanceFakeDriver) Open(name string) (driver.Conn, error) {
	value, exists := maintenanceScripts.Load(name)
	if !exists {
		return nil, fmt.Errorf("unknown fake database %q", name)
	}
	return &maintenanceFakeConn{script: value.(*sqlScript)}, nil
}

type maintenanceFakeConn struct{ script *sqlScript }

func (connection *maintenanceFakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are unsupported by maintenance fake driver")
}
func (connection *maintenanceFakeConn) Close() error              { return nil }
func (connection *maintenanceFakeConn) Begin() (driver.Tx, error) { return maintenanceFakeTx{}, nil }
func (connection *maintenanceFakeConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return maintenanceFakeTx{}, nil
}
func (connection *maintenanceFakeConn) CheckNamedValue(*driver.NamedValue) error { return nil }

func (connection *maintenanceFakeConn) QueryContext(_ context.Context, query string, arguments []driver.NamedValue) (driver.Rows, error) {
	step, err := connection.next("query", query, arguments)
	if err != nil {
		return nil, err
	}
	if step.err != nil {
		return nil, step.err
	}
	return &maintenanceFakeRows{columns: step.columns, rows: step.rows}, nil
}

func (connection *maintenanceFakeConn) ExecContext(_ context.Context, query string, arguments []driver.NamedValue) (driver.Result, error) {
	step, err := connection.next("exec", query, arguments)
	if err != nil {
		return nil, err
	}
	if step.err != nil {
		return nil, step.err
	}
	return driver.RowsAffected(step.affected), nil
}

func (connection *maintenanceFakeConn) next(kind, query string, arguments []driver.NamedValue) (sqlStep, error) {
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

type maintenanceFakeTx struct{}

func (maintenanceFakeTx) Commit() error   { return nil }
func (maintenanceFakeTx) Rollback() error { return nil }

type maintenanceFakeRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (rows *maintenanceFakeRows) Columns() []string { return rows.columns }
func (rows *maintenanceFakeRows) Close() error      { return nil }
func (rows *maintenanceFakeRows) Next(destination []driver.Value) error {
	if rows.index >= len(rows.rows) {
		return io.EOF
	}
	row := rows.rows[rows.index]
	rows.index++
	if len(row) != len(destination) {
		return fmt.Errorf("fake row has %d values, destination has %d", len(row), len(destination))
	}
	copy(destination, row)
	return nil
}

func compactMaintenanceSQL(value string) string { return strings.Join(strings.Fields(value), " ") }

func maintenanceColumns(count int) []string {
	columns := make([]string, count)
	for index := range columns {
		columns[index] = fmt.Sprintf("column_%d", index)
	}
	return columns
}
