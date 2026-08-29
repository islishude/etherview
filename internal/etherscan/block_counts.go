package etherscan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"

	"github.com/islishude/etherview/internal/db/gen"
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
	defer tx.Rollback() //nolint:errcheck
	if _, err := b.requireCanonicalStageRange(ctx, tx, traceStage, blockText, &blockText, ErrTraceUnavailable); err != nil {
		return blockTransactionCounts{}, err
	}
	if _, err := b.requireCanonicalStageRange(ctx, tx, tokenStage, blockText, &blockText, ErrTokenUnavailable); err != nil {
		return blockTransactionCounts{}, err
	}
	var result blockTransactionCounts
	err = tx.QueryRowContext(ctx, dbgen.EtherscanBlockTransactionCounts, b.chain, blockText).Scan(
		&result.Block, &result.Transactions, &result.Internal,
		&result.ERC20Transfers, &result.ERC721Transfers, &result.ERC1155Transfers,
	)
	if errors.Is(err, sql.ErrNoRows) {
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
	if err := tx.Commit(); err != nil {
		return blockTransactionCounts{}, fmt.Errorf("commit block transaction count snapshot: %w", err)
	}
	return result, nil
}
