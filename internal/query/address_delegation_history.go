package query

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
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
	if err := tx.QueryRowContext(ctx, addressDelegationHistorySQL,
		r.chainID, fmt.Sprint(referenceNumber), referenceHash.Bytes(), address.Bytes(),
	).Scan(&referenceCanonical, &hasHistory); err != nil {
		return false, fmt.Errorf("check address delegation history: %w", err)
	}
	if !referenceCanonical {
		return false, fmt.Errorf("%w: address state reference is no longer canonical", httpapi.ErrNotReady)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit delegation history snapshot: %w", err)
	}
	return hasHistory, nil
}

const addressDelegationHistorySQL = `
SELECT
    EXISTS (
        SELECT 1
        FROM canonical_blocks
        WHERE chain_id = $1::numeric
          AND number = $2::numeric
          AND block_hash = $3
    ) AS reference_canonical,
    EXISTS (
        SELECT 1
        FROM eip7702_authorizations AS authz
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = authz.chain_id
         AND canonical.number = authz.block_number
         AND canonical.block_hash = authz.block_hash
        WHERE authz.chain_id = $1::numeric
          AND authz.authority = $4
          AND authz.application_status = 'applied'
          AND authz.canonical
          AND authz.block_number <= $2::numeric
    ) AS has_history`
