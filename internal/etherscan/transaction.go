package etherscan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"net/url"

	"github.com/islishude/etherview/internal/ethrpc"
)

func (b *PostgresBackend) transactionStatus(ctx context.Context, values url.Values, receiptOnly bool) (any, error) {
	hash, hashBytes, err := parseHashParameter(values.Get("txhash"), "txhash")
	if err != nil {
		return nil, err
	}
	tx, err := b.beginCanonicalSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var raw []byte
	var storedHash, blockHash []byte
	var blockNumberText string
	var transactionIndex int64
	err = tx.QueryRowContext(ctx, transactionStatusSQL, b.chain, hashBytes).Scan(
		&raw, &storedHash, &blockHash, &blockNumberText, &transactionIndex,
	)
	if err == sql.ErrNoRows {
		if _, coverageErr := b.requireCanonicalCoreRange(ctx, tx, "0", nil); coverageErr != nil {
			return nil, coverageErr
		}
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query transaction status: %w", err)
	}
	if transactionIndex < 0 {
		return nil, errors.New("stored receipt index is negative")
	}
	indexedHash, err := hashFromBytes(storedHash)
	if err != nil {
		return nil, err
	}
	indexedBlockHash, err := hashFromBytes(blockHash)
	if err != nil {
		return nil, err
	}
	blockNumber, ok := new(big.Int).SetString(blockNumberText, 10)
	if !ok || blockNumber.Sign() < 0 {
		return nil, errors.New("stored receipt block number is invalid")
	}
	if !indexedHash.Equal(hash) {
		return nil, errors.New("stored receipt hash does not match requested transaction")
	}
	var receipt ethrpc.Receipt
	if err := decodeRawObject(raw, &receipt); err != nil {
		return nil, fmt.Errorf("decode receipt raw JSON: %w", err)
	}
	if !receipt.TransactionHash.Equal(indexedHash) || !receipt.BlockHash.Equal(indexedBlockHash) {
		return nil, errors.New("stored receipt raw identity does not match indexed row")
	}
	wireBlock, err := receipt.BlockNumber.Big()
	if err != nil || wireBlock.Cmp(blockNumber) != 0 {
		return nil, errors.New("stored receipt raw block number does not match indexed row")
	}
	wireIndex, err := receipt.TransactionIndex.Uint64()
	if err != nil || wireIndex != uint64(transactionIndex) {
		return nil, errors.New("stored receipt raw index does not match indexed row")
	}
	if receipt.Status == nil {
		return nil, ErrStatusUnavailable
	}
	status, err := receipt.Status.Big()
	if err != nil {
		return nil, fmt.Errorf("decode receipt status: %w", err)
	}
	if status.Sign() != 0 && status.Cmp(big.NewInt(1)) != 0 {
		return nil, errors.New("stored receipt status is neither zero nor one")
	}
	statusText := status.String()
	var result any
	if receiptOnly {
		result = transactionReceiptStatus{Status: statusText}
	} else {
		statusResult := transactionErrorStatus{IsError: "0"}
		if status.Sign() == 0 {
			statusResult.IsError = "1"
			statusResult.ErrDescription = "execution failed"
		}
		result = statusResult
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction status snapshot: %w", err)
	}
	return result, nil
}

const transactionStatusSQL = `
SELECT receipt.raw, receipt.tx_hash, receipt.block_hash,
       receipt.block_number::text, receipt.tx_index
FROM receipts AS receipt
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = receipt.chain_id
 AND canonical.number = receipt.block_number
 AND canonical.block_hash = receipt.block_hash
WHERE receipt.chain_id = $1::numeric
  AND receipt.tx_hash = $2
LIMIT 1`
