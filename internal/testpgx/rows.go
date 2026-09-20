package testpgx

import (
	"fmt"
	"reflect"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Rows supplies deterministic native query results in repository unit tests.
// It is not codec acceptance evidence; those tests run against PostgreSQL.
type Rows struct {
	pgx.Rows
	ColumnNames []string
	ValuesList  [][]any
	index       int
	closed      bool
	err         error
}

func (rows *Rows) Close()     { rows.closed = true }
func (rows *Rows) Err() error { return rows.err }
func (rows *Rows) Next() bool {
	if rows.closed || rows.index >= len(rows.ValuesList) {
		rows.Close()
		return false
	}
	rows.index++
	return true
}
func (rows *Rows) Scan(destinations ...any) error {
	if rows.index == 0 || rows.closed {
		return fmt.Errorf("scan without current row")
	}
	values := rows.ValuesList[rows.index-1]
	if len(values) != len(destinations) {
		return fmt.Errorf("row has %d columns, scan has %d", len(values), len(destinations))
	}
	for index, value := range values {
		if err := assign(destinations[index], value); err != nil {
			rows.err = fmt.Errorf("column %d: %w", index, err)
			rows.Close()
			return rows.err
		}
	}
	return nil
}
func assign(destination, value any) error {
	if destination == nil {
		return nil
	}
	if scanner, ok := destination.(interface{ Scan(any) error }); ok {
		return scanner.Scan(value)
	}
	target := reflect.ValueOf(destination)
	if target.Kind() != reflect.Pointer || target.IsNil() {
		return fmt.Errorf("invalid scan target %T", destination)
	}
	target = target.Elem()
	if value == nil {
		switch target.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Interface:
			target.SetZero()
			return nil
		}
		return fmt.Errorf("NULL into %T", destination)
	}
	if target.Kind() == reflect.Pointer {
		target.Set(reflect.New(target.Type().Elem()))
		return assign(target.Interface(), value)
	}
	source := reflect.ValueOf(value)
	if source.Type().AssignableTo(target.Type()) {
		target.Set(source)
		return nil
	}
	switch target.Kind() {
	case reflect.String:
		switch source.Kind() {
		case reflect.String:
			target.SetString(source.String())
			return nil
		case reflect.Slice:
			if data, ok := value.([]byte); ok {
				target.SetString(string(data))
				return nil
			}
		case reflect.Int, reflect.Int32, reflect.Int64:
			target.SetString(strconv.FormatInt(source.Int(), 10))
			return nil
		}
	case reflect.Int, reflect.Int32, reflect.Int64:
		number, err := strconv.ParseInt(fmt.Sprint(value), 10, target.Type().Bits())
		if err != nil {
			return err
		}
		target.SetInt(number)
		return nil
	case reflect.Uint, reflect.Uint32, reflect.Uint64:
		number, err := strconv.ParseUint(fmt.Sprint(value), 10, target.Type().Bits())
		if err != nil {
			return err
		}
		target.SetUint(number)
		return nil
	case reflect.Bool:
		boolean, err := strconv.ParseBool(fmt.Sprint(value))
		if err != nil {
			return err
		}
		target.SetBool(boolean)
		return nil
	case reflect.Slice:
		if text, ok := value.(string); ok {
			if target.Type().Elem().Kind() == reflect.Uint8 {
				target.SetBytes([]byte(text))
				return nil
			}
			if target.Type().Elem().Kind() == reflect.String {
				return pgtype.NewMap().Scan(pgtype.TextArrayOID, pgtype.TextFormatCode, []byte(text), destination)
			}
		}
	}
	return fmt.Errorf("cannot assign %T to %T", value, destination)
}

type row struct {
	rows pgx.Rows
	err  error
}

func Row(rows pgx.Rows, err error) pgx.Row { return row{rows, err} }
func (value row) Scan(destinations ...any) error {
	if value.err != nil {
		return value.err
	}
	defer value.rows.Close()
	if !value.rows.Next() {
		if err := value.rows.Err(); err != nil {
			return err
		}
		return pgx.ErrNoRows
	}
	return value.rows.Scan(destinations...)
}
func Affected(count int64) pgconn.CommandTag {
	return pgconn.NewCommandTag("UPDATE " + strconv.FormatInt(count, 10))
}

// NumericEquals asserts a native, finite NUMERIC parameter, including its exact
// decimal representation. It does not accept strings or floating-point values.
func NumericEquals(value any, expected string) bool {
	numeric, ok := value.(pgtype.Numeric)
	if !ok || !numeric.Valid || numeric.NaN || numeric.InfinityModifier != pgtype.Finite {
		return false
	}
	codec := pgtype.NumericCodec{}
	data, err := codec.PlanEncode(nil, pgtype.NumericOID, pgtype.TextFormatCode, numeric).Encode(numeric, nil)
	return err == nil && string(data) == expected
}

func NumericNull(value any) bool {
	numeric, ok := value.(pgtype.Numeric)
	return ok && !numeric.Valid
}

// TextPointerEquals checks the nullable text representation emitted by sqlc.
func TextPointerEquals(value any, expected string) bool {
	pointer, ok := value.(*string)
	return ok && pointer != nil && *pointer == expected
}
