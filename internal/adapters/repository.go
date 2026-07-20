// Package adapters implements bounded, optional external name and price
// capabilities. PostgreSQL observations are the public availability source;
// external transport failures never become core indexing dependencies.
package adapters

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"time"

	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrUnavailable = errors.New("external adapter capability unavailable")

type CapabilityError struct {
	Capability string
	State      string
	Code       string
}

func (CapabilityError) Error() string { return "external adapter capability unavailable" }
func (CapabilityError) Unwrap() error { return ErrUnavailable }

type observation struct {
	State      string
	Code       string
	Value      []byte
	ObservedAt time.Time
	ExpiresAt  time.Time
}

type repository struct {
	db      *sql.DB
	chain   uint64
	chainID pgtype.Numeric
}

func newRepository(db *sql.DB, chainID uint64) (repository, error) {
	if db == nil {
		return repository{}, errors.New("adapter repository database is nil")
	}
	if chainID == 0 {
		return repository{}, errors.New("adapter chain ID must be greater than zero")
	}
	return repository{db: db, chain: chainID, chainID: uint64Numeric(chainID)}, nil
}

func (r repository) fresh(ctx context.Context, capability, key string, now time.Time) (observation, bool, error) {
	var row dbgen.GetFreshAdapterObservationRow
	err := dbaccess.WithQueries(ctx, r.db, func(queries *dbgen.Queries) error {
		var queryErr error
		row, queryErr = queries.GetFreshAdapterObservation(ctx, dbgen.GetFreshAdapterObservationParams{
			ChainID: r.chainID, Capability: capability, ObservationKey: key,
			NowAt: timestamptz(now),
		})
		return queryErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return observation{}, false, nil
	}
	if err != nil {
		return observation{}, false, fmt.Errorf("read fresh %s adapter observation: %w", capability, err)
	}
	if !row.ObservedAt.Valid || !row.ExpiresAt.Valid || !row.ExpiresAt.Time.After(now) {
		return observation{}, false, errors.New("adapter observation has invalid freshness bounds")
	}
	result := observation{
		State: row.State, Value: append([]byte(nil), row.Value...),
		ObservedAt: row.ObservedAt.Time.UTC(), ExpiresAt: row.ExpiresAt.Time.UTC(),
	}
	if row.Code != nil {
		result.Code = *row.Code
	}
	return result, true, nil
}

func (r repository) failure(
	ctx context.Context,
	capability, key, state, code string,
	observedAt, expiresAt time.Time,
) error {
	return dbaccess.WithQueries(ctx, r.db, func(queries *dbgen.Queries) error {
		return queries.RecordAdapterFailure(ctx, dbgen.RecordAdapterFailureParams{
			ChainID: r.chainID, Capability: capability, ObservationKey: key,
			State: state, Code: &code, ObservedAt: timestamptz(observedAt),
			ExpiresAt: timestamptz(expiresAt),
		})
	})
}

func uint64Numeric(value uint64) pgtype.Numeric {
	integer := new(big.Int).SetUint64(value)
	return pgtype.Numeric{Int: integer, Valid: true}
}

func decimalNumeric(value string) (pgtype.Numeric, error) {
	if value == "" || len(value) > 78 || len(value) > 1 && value[0] == '0' {
		return pgtype.Numeric{}, errors.New("invalid non-negative decimal quantity")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return pgtype.Numeric{}, errors.New("invalid non-negative decimal quantity")
		}
	}
	integer := new(big.Int)
	if _, ok := integer.SetString(value, 10); !ok {
		return pgtype.Numeric{}, errors.New("invalid non-negative decimal quantity")
	}
	return pgtype.Numeric{Int: integer, Valid: true}, nil
}

func timestamptz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}
