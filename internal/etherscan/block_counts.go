package etherscan

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/jackc/pgx/v5/pgtype"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"

	dbgen "github.com/islishude/etherview/internal/db/gen"
)

func (b *PostgresBackend) blockTransactionCounts(ctx context.Context, values url.Values) (blockTransactionCounts, error) {
	block, err := parseDecimal(values.Get("blockno"), "blockno")
	if err != nil {
		return blockTransactionCounts{}, err
	}
	blockText := block.String()
	tx, err := b.beginEnrichmentSnapshot(ctx)
	if err != nil {
		return blockTransactionCounts{}, err
	}
	defer dbaccess.Rollback(ctx, tx)
	if _, err := b.requireCanonicalStageRange(ctx, tx, traceStage, blockText, &blockText, ErrTraceUnavailable); err != nil {
		return blockTransactionCounts{}, err
	}
	if _, err := b.requireCanonicalStageRange(ctx, tx, tokenStage, blockText, &blockText, ErrTokenUnavailable); err != nil {
		return blockTransactionCounts{}, err
	}
	var result blockTransactionCounts
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(b.chain); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(blockText); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).EtherscanBlockTransactionCounts(ctx, queryValue0, queryValue1)
		if err != nil {
			return err
		}
		result.Block = queryRow.CanonicalNumber
		result.Transactions = queryRow.TransactionCount
		result.Internal = queryRow.InternalCount
		result.ERC20Transfers = queryRow.Erc20Count
		result.ERC721Transfers = queryRow.Erc721Count
		result.ERC1155Transfers = queryRow.Erc1155Count
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		return blockTransactionCounts{}, ErrNotFound
	}
	if err != nil {
		return blockTransactionCounts{}, fmt.Errorf("query block transaction counts: %w", err)
	}
	for name, value := range map[string]string{
		"block number": result.Block, "transaction count": result.Transactions,
		"internal transaction count": result.Internal, "ERC-20 transfer count": result.ERC20Transfers,
		"ERC-721 transfer count": result.ERC721Transfers, "ERC-1155 transfer count": result.ERC1155Transfers,
	} {
		if _, err := storedUint256(value, name); err != nil {
			return blockTransactionCounts{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return blockTransactionCounts{}, fmt.Errorf("commit block transaction count snapshot: %w", err)
	}
	return result, nil
}
