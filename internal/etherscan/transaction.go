package etherscan

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	pgx "github.com/jackc/pgx/v5"
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
	defer dbaccess.Rollback(ctx, tx)
	var raw []byte
	var storedHash, blockHash []byte
	var blockNumberText string
	var transactionIndex int64
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(b.chain); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).EtherscanTransactionStatus(ctx, queryValue0, hashBytes)
		if err != nil {
			return err
		}
		raw = queryRow.Raw
		storedHash = queryRow.TxHash
		blockHash = queryRow.BlockHash
		blockNumberText = queryRow.ReceiptBlockNumber
		transactionIndex = queryRow.TxIndex
		return nil
	}()
	if err == pgx.ErrNoRows {
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
	if indexedHash != hash {
		return nil, errors.New("stored receipt hash does not match requested transaction")
	}
	receipt, statusPresent, err := decodeStandaloneReceipt(
		raw, indexedHash, indexedBlockHash, blockNumber, transactionIndex,
	)
	if err != nil {
		return nil, fmt.Errorf("decode receipt raw JSON: %w", err)
	}
	if !statusPresent {
		return nil, ErrStatusUnavailable
	}
	if receipt.Status != 0 && receipt.Status != 1 {
		return nil, errors.New("stored receipt status is neither zero nor one")
	}
	statusText := strconv.FormatUint(receipt.Status, 10)
	var result any
	if receiptOnly {
		result = transactionReceiptStatus{Status: statusText}
	} else {
		statusResult := transactionErrorStatus{IsError: "0"}
		if receipt.Status == 0 {
			statusResult.IsError = "1"
			statusResult.ErrDescription = "execution failed"
		}
		result = statusResult
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit transaction status snapshot: %w", err)
	}
	return result, nil
}
