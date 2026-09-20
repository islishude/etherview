package etherscan

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	dbaccess "github.com/islishude/etherview/internal/db"

	dbgen "github.com/islishude/etherview/internal/db/gen"
	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"
)

const (
	tokenStage  = "token"
	traceStage  = "trace"
	holderStage = "holder"
)

type enrichmentQueryer = dbgen.DBTX

func (b *PostgresBackend) beginEnrichmentSnapshot(ctx context.Context) (pgx.Tx, error) {
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
	endArgument := end
	var tip string
	var incompleteNumber, state pgtype.Text
	var incompleteHash []byte
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(b.chain); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(start); err != nil {
			return err
		}
		var queryValue2 pgtype.Numeric
		if endArgument != nil {
			if err := queryValue2.Scan(*endArgument); err != nil {
				return err
			}
		}
		queryRow, err := dbgen.New(queryer).EtherscanCanonicalStageRange(ctx, dbgen.EtherscanCanonicalStageRangeParams{ChainID: queryValue0, MinNumber: queryValue1, RangeEnd: queryValue2, Stage: stage})
		if err != nil {
			return err
		}
		tip = queryRow.TipNumber
		resultValue1, err := dbaccess.NumericText(queryRow.Number)
		if err != nil {
			return err
		}
		incompleteNumber = resultValue1
		incompleteHash = queryRow.BlockHash
		var resultValue4 pgtype.Text
		if queryRow.State != nil {
			resultValue4 = pgtype.Text{String: *queryRow.State, Valid: true}
		}
		state = resultValue4
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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
