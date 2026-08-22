package etherscan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/islishude/etherview/internal/db/gen"
)

// ErrCoreUnavailable means the requested canonical block interval has not
// been durably covered by core ingestion. Compatibility callers must not turn
// that incomplete history into either a partial result or a no-records result.
var ErrCoreUnavailable = errors.New("canonical core coverage unavailable")

func (b *PostgresBackend) beginCanonicalSnapshot(ctx context.Context) (*sql.Tx, error) {
	tx, err := b.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin canonical read snapshot: %w", err)
	}
	return tx, nil
}

// requireCanonicalCoreRange proves that one normalized durable coverage range
// contains the complete inclusive request after its upper bound is clamped to
// the canonical tip. An explicit request wholly above the tip has no existing
// blocks and returns ErrNotFound; every other missing proof is unavailable.
func (b *PostgresBackend) requireCanonicalCoreRange(
	ctx context.Context,
	queryer enrichmentQueryer,
	start string,
	end *string,
) (string, error) {
	startNumber, err := storedUint256(start, "core range start")
	if err != nil {
		return "", err
	}
	var endArgument any
	if end != nil {
		endNumber, parseErr := storedUint256(*end, "core range end")
		if parseErr != nil {
			return "", parseErr
		}
		if endNumber.Cmp(startNumber) < 0 {
			return "", errors.New("canonical core range end precedes its start")
		}
		endArgument = *end
	}

	var tip string
	var configuredStart, coveredStart, coveredEnd sql.NullString
	err = queryer.QueryRowContext(ctx, dbgen.EtherscanCanonicalCoreRange, b.chain, start, endArgument).Scan(&tip, &configuredStart, &coveredStart, &coveredEnd)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrCoreUnavailable
	}
	if err != nil {
		return "", fmt.Errorf("check canonical core range: %w", err)
	}
	tipNumber, err := storedUint256(tip, "canonical tip")
	if err != nil {
		return "", err
	}
	if startNumber.Cmp(tipNumber) > 0 {
		return tip, ErrNotFound
	}
	requestedEnd := tipNumber
	if end != nil {
		requestedEnd, err = storedUint256(*end, "core range end")
		if err != nil {
			return "", err
		}
		if requestedEnd.Cmp(tipNumber) > 0 {
			requestedEnd = tipNumber
		}
	}
	if !configuredStart.Valid || !coveredStart.Valid || !coveredEnd.Valid {
		return "", ErrCoreUnavailable
	}
	configured, err := storedUint256(configuredStart.String, "configured index start")
	if err != nil {
		return "", err
	}
	rangeStart, err := storedUint256(coveredStart.String, "core coverage start")
	if err != nil {
		return "", err
	}
	rangeEnd, err := storedUint256(coveredEnd.String, "core coverage end")
	if err != nil {
		return "", err
	}
	if configured.Cmp(tipNumber) > 0 || rangeStart.Cmp(configured) < 0 ||
		rangeEnd.Cmp(rangeStart) < 0 || rangeEnd.Cmp(tipNumber) > 0 {
		return "", errors.New("stored canonical core coverage is inconsistent")
	}
	if rangeStart.Cmp(startNumber) > 0 || rangeEnd.Cmp(requestedEnd) < 0 {
		return "", ErrCoreUnavailable
	}
	return tip, nil
}
