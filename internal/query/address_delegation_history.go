package query

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/publicquery"
)

// HasAddressDelegationHistory reports whether an address has at least one
// applied EIP-7702 authorization on the canonical chain at the supplied state
// reference. The reference and the history lookup share one PostgreSQL
// snapshot so a reorg cannot turn an uncertain result into a false negative.
func (r *PostgresReader) HasAddressDelegationHistory(
	ctx context.Context,
	rawAddress string,
	referenceNumber uint64,
	referenceHash common.Hash,
) (bool, error) {
	address, err := ethrpc.ParseAddress(rawAddress)
	if err != nil {
		return false, fmt.Errorf("invalid delegation history address: %w", err)
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return false, fmt.Errorf("begin delegation history snapshot: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var referenceCanonical, hasHistory bool
	if err := tx.QueryRowContext(ctx, dbgen.GetAddressDelegationHistory,
		r.chainID, fmt.Sprint(referenceNumber), referenceHash.Bytes(), address.Bytes(),
	).Scan(&referenceCanonical, &hasHistory); err != nil {
		return false, fmt.Errorf("check address delegation history: %w", err)
	}
	if !referenceCanonical {
		return false, fmt.Errorf("%w: address state reference is no longer canonical", publicquery.ErrNotReady)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit delegation history snapshot: %w", err)
	}
	return hasHistory, nil
}
