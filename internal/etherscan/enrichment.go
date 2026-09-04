package etherscan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/islishude/etherview/internal/db/gen"
	"math/big"
)

const (
	tokenStage  = "token"
	traceStage  = "trace"
	holderStage = "holder"
)

type enrichmentQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (b *PostgresBackend) beginEnrichmentSnapshot(ctx context.Context) (*sql.Tx, error) {
	return b.beginCanonicalSnapshot(ctx)
}

// requireCanonicalStageRange makes an empty list meaningful: every canonical
// block in the requested, tip-clamped range must have a latest completed stage
// result before callers may report that no matching records exist.
func (b *PostgresBackend) requireCanonicalStageRange(
	ctx context.Context,
	queryer enrichmentQueryer,
	stage, start string,
	end *string,
	unavailable error,
) (string, error) {
	coreTip, err := b.requireCanonicalCoreRange(ctx, queryer, start, end)
	if err != nil {
		return coreTip, err
	}
	var endArgument any
	if end != nil {
		endArgument = *end
	}
	var tip string
	var incompleteNumber, state sql.NullString
	var incompleteHash []byte
	err = queryer.QueryRowContext(ctx, dbgen.EtherscanCanonicalStageRange, b.chain, start, endArgument, stage).Scan(&tip, &incompleteNumber, &incompleteHash, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("canonical stage range lost its proven core tip")
	}
	if err != nil {
		return "", fmt.Errorf("check canonical %s stage range: %w", stage, err)
	}
	tipNumber, err := storedUint256(tip, "canonical tip")
	if err != nil {
		return "", err
	}
	if tip != coreTip {
		return "", errors.New("canonical stage and core coverage tips differ")
	}
	startNumber, err := storedUint256(start, "stage range start")
	if err != nil {
		return "", err
	}
	if startNumber.Cmp(tipNumber) > 0 {
		return tip, ErrNotFound
	}
	if !incompleteNumber.Valid {
		if len(incompleteHash) != 0 || state.Valid {
			return "", errors.New("stored stage range has an invalid complete marker")
		}
		return tip, nil
	}
	if _, err := storedUint256(incompleteNumber.String, "incomplete stage block"); err != nil {
		return "", err
	}
	if len(incompleteHash) != 32 {
		return "", errors.New("stored incomplete stage block hash is invalid")
	}
	if !state.Valid || state.String == "unavailable" || state.String == "failed" {
		return "", unavailable
	}
	return "", fmt.Errorf("stored %s stage has invalid state %q", stage, state.String)
}

func storedUint256(value, name string) (*big.Int, error) {
	parsed, err := parseCanonicalDecimal(value)
	if err != nil {
		return nil, fmt.Errorf("stored %s is invalid: %w", name, err)
	}
	return parsed, nil
}
