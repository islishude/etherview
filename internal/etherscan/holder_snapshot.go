package etherscan

import (
	"context"
	"errors"
	"fmt"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (b *PostgresBackend) completedHolderTip(ctx context.Context, queryer enrichmentQueryer, address []byte) (string, error) {
	var chain, number pgtype.Numeric
	if err := chain.Scan(b.chain); err != nil {
		return "", err
	}
	row, err := dbgen.New(queryer).CatalogHolderReadSnapshot(ctx, chain, address)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrStateUnavailable
	}
	if err != nil {
		return "", fmt.Errorf("read completed holder snapshot: %w", err)
	}
	if _, err := parseCanonicalDecimal(row.Number); err != nil || len(row.BlockHash) != 32 {
		return "", ErrStateUnavailable
	}
	if err := number.Scan(row.Number); err != nil {
		return "", err
	}
	token, err := dbgen.New(queryer).CatalogTokenContract(ctx, chain, address, number)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrStateUnavailable
	}
	if err != nil {
		return "", fmt.Errorf("read holder token classification: %w", err)
	}
	if token.Standard != "erc20" || token.Confidence != "high" && token.Confidence != "verified" {
		return "", ErrStateUnavailable
	}
	return row.Number, nil
}
