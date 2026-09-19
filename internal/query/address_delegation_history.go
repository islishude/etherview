package query

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"

	"github.com/ethereum/go-ethereum/common"
	dbgen "github.com/islishude/etherview/internal/db/gen"
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
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return false, fmt.Errorf("begin delegation history snapshot: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)

	var referenceCanonical, hasHistory bool
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(fmt.Sprint(referenceNumber)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).GetAddressDelegationHistory(ctx, dbgen.GetAddressDelegationHistoryParams{ChainID: queryValue0, ReferenceNumber: queryValue1, ReferenceHash: referenceHash.Bytes(), Authority: address.Bytes()})
		if err != nil {
			return err
		}
		referenceCanonical = queryRow.ReferenceCanonical
		hasHistory = queryRow.HasHistory
		return nil
	}(); err != nil {
		return false, fmt.Errorf("check address delegation history: %w", err)
	}
	if !referenceCanonical {
		return false, fmt.Errorf("%w: address state reference is no longer canonical", publicquery.ErrNotReady)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit delegation history snapshot: %w", err)
	}
	return hasHistory, nil
}
