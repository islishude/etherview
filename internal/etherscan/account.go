package etherscan

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"

	"github.com/islishude/etherview/internal/ethrpc"
)

func (b *PostgresBackend) accountTransactions(ctx context.Context, values url.Values) ([]accountTransaction, error) {
	address, _, err := parseAddressParameter(values.Get("address"), "address")
	if err != nil {
		return nil, err
	}
	page, err := parsePagination(values)
	if err != nil {
		return nil, err
	}
	start, end, err := decimalRange(values)
	if err != nil {
		return nil, err
	}
	tx, err := b.beginCanonicalSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := b.requireCanonicalCoreRange(ctx, tx, start, end); err != nil {
		return nil, err
	}

	arguments := []any{b.chain, strings.ToLower(address.String()), start}
	endClause := ""
	if end != nil {
		arguments = append(arguments, *end)
		endClause = fmt.Sprintf("AND inclusion.block_number <= $%d::numeric", len(arguments))
	}
	arguments = append(arguments, page.limit, page.offset)
	query := fmt.Sprintf(accountTransactionsSQL, endClause, page.direction, page.direction, page.direction, len(arguments)-1, len(arguments))
	rows, err := tx.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query account transactions: %w", err)
	}
	defer rows.Close()
	result := make([]accountTransaction, 0, page.limit)
	for rows.Next() {
		item, err := scanAccountTransaction(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account transactions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close account transactions: %w", err)
	}
	if len(result) == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit account transaction snapshot: %w", err)
	}
	return result, nil
}

type rowScanner interface{ Scan(...any) error }

func scanAccountTransaction(scanner rowScanner) (accountTransaction, error) {
	var transactionJSON, receiptJSON, blockJSON []byte
	var blockNumberText, tipNumberText string
	var blockHashBytes, transactionHashBytes []byte
	var transactionIndex int64
	if err := scanner.Scan(
		&transactionJSON, &receiptJSON, &blockJSON, &blockNumberText,
		&blockHashBytes, &transactionIndex, &transactionHashBytes, &tipNumberText,
	); err != nil {
		return accountTransaction{}, fmt.Errorf("scan account transaction: %w", err)
	}
	if transactionIndex < 0 {
		return accountTransaction{}, errors.New("stored transaction index is negative")
	}
	blockNumber, ok := new(big.Int).SetString(blockNumberText, 10)
	if !ok || blockNumber.Sign() < 0 {
		return accountTransaction{}, errors.New("stored block number is invalid")
	}
	tipNumber, ok := new(big.Int).SetString(tipNumberText, 10)
	if !ok || tipNumber.Cmp(blockNumber) < 0 {
		return accountTransaction{}, errors.New("stored canonical tip is invalid")
	}
	blockHash, err := hashFromBytes(blockHashBytes)
	if err != nil {
		return accountTransaction{}, err
	}
	transactionHash, err := hashFromBytes(transactionHashBytes)
	if err != nil {
		return accountTransaction{}, err
	}

	var transaction ethrpc.Transaction
	if err := decodeRawObject(transactionJSON, &transaction); err != nil {
		return accountTransaction{}, fmt.Errorf("decode transaction raw JSON: %w", err)
	}
	if !transaction.Hash.Equal(transactionHash) || transaction.BlockHash == nil || !transaction.BlockHash.Equal(blockHash) {
		return accountTransaction{}, errors.New("stored transaction raw identity does not match inclusion")
	}
	if transaction.BlockNumber == nil || transaction.TransactionIndex == nil {
		return accountTransaction{}, errors.New("stored transaction raw inclusion fields are null")
	}
	wireBlockNumber, err := transaction.BlockNumber.Big()
	if err != nil || wireBlockNumber.Cmp(blockNumber) != 0 {
		return accountTransaction{}, errors.New("stored transaction raw block number does not match inclusion")
	}
	wireIndex, err := transaction.TransactionIndex.Uint64()
	if err != nil || wireIndex != uint64(transactionIndex) {
		return accountTransaction{}, errors.New("stored transaction raw index does not match inclusion")
	}

	var receipt ethrpc.Receipt
	if err := decodeRawObject(receiptJSON, &receipt); err != nil {
		return accountTransaction{}, fmt.Errorf("decode receipt raw JSON: %w", err)
	}
	if !receipt.TransactionHash.Equal(transactionHash) || !receipt.BlockHash.Equal(blockHash) {
		return accountTransaction{}, errors.New("stored receipt raw identity does not match inclusion")
	}
	receiptBlockNumber, err := receipt.BlockNumber.Big()
	if err != nil || receiptBlockNumber.Cmp(blockNumber) != 0 {
		return accountTransaction{}, errors.New("stored receipt block number does not match inclusion")
	}
	receiptIndex, err := receipt.TransactionIndex.Uint64()
	if err != nil || receiptIndex != uint64(transactionIndex) {
		return accountTransaction{}, errors.New("stored receipt index does not match inclusion")
	}

	var block ethrpc.Block
	if err := decodeRawObject(blockJSON, &block); err != nil {
		return accountTransaction{}, fmt.Errorf("decode block raw JSON: %w", err)
	}
	if block.Number == nil || block.Hash == nil || !block.Hash.Equal(blockHash) {
		return accountTransaction{}, errors.New("stored block raw identity does not match inclusion")
	}
	wireBlock, err := block.Number.Big()
	if err != nil || wireBlock.Cmp(blockNumber) != 0 {
		return accountTransaction{}, errors.New("stored block raw number does not match inclusion")
	}

	from, err := checksumAddress(transaction.From)
	if err != nil {
		return accountTransaction{}, fmt.Errorf("checksum transaction sender: %w", err)
	}
	to := ""
	if transaction.To != nil {
		to, err = checksumAddress(*transaction.To)
		if err != nil {
			return accountTransaction{}, fmt.Errorf("checksum transaction recipient: %w", err)
		}
	}
	contractAddress := ""
	if receipt.ContractAddress != nil {
		contractAddress, err = checksumAddress(*receipt.ContractAddress)
		if err != nil {
			return accountTransaction{}, fmt.Errorf("checksum created contract: %w", err)
		}
	}

	item := accountTransaction{
		BlockNumber: blockNumber.String(), Hash: strings.ToLower(transactionHash.String()),
		BlockHash: strings.ToLower(blockHash.String()), TransactionIndex: strconv.FormatInt(transactionIndex, 10),
		From: from, To: to, ContractAddress: contractAddress, Input: transaction.Input.String(),
		FunctionName: "",
	}
	if item.TimeStamp, err = decimalQuantity(block.Timestamp); err != nil {
		return accountTransaction{}, fmt.Errorf("decode block timestamp: %w", err)
	}
	if item.Nonce, err = decimalQuantity(transaction.Nonce); err != nil {
		return accountTransaction{}, fmt.Errorf("decode transaction nonce: %w", err)
	}
	if item.Value, err = decimalQuantity(transaction.Value); err != nil {
		return accountTransaction{}, fmt.Errorf("decode transaction value: %w", err)
	}
	if item.Gas, err = decimalQuantity(transaction.Gas); err != nil {
		return accountTransaction{}, fmt.Errorf("decode transaction gas: %w", err)
	}
	gasPrice := transaction.GasPrice
	if gasPrice == nil {
		gasPrice = receipt.EffectiveGasPrice
	}
	if gasPrice == nil {
		return accountTransaction{}, errors.New("stored transaction has no effective gas price")
	}
	if item.GasPrice, err = decimalQuantity(*gasPrice); err != nil {
		return accountTransaction{}, fmt.Errorf("decode transaction gas price: %w", err)
	}
	if item.CumulativeGasUsed, err = decimalQuantity(receipt.CumulativeGasUsed); err != nil {
		return accountTransaction{}, fmt.Errorf("decode receipt cumulative gas: %w", err)
	}
	if receipt.GasUsed == nil {
		return accountTransaction{}, errors.New("stored receipt gas used is null")
	}
	if item.GasUsed, err = decimalQuantity(*receipt.GasUsed); err != nil {
		return accountTransaction{}, fmt.Errorf("decode receipt gas used: %w", err)
	}
	if receipt.Status != nil {
		status, err := receipt.Status.Big()
		if err != nil {
			return accountTransaction{}, fmt.Errorf("decode receipt status: %w", err)
		}
		switch status.Sign() {
		case 0:
			item.IsError, item.ReceiptStatus = "1", "0"
		default:
			if status.Cmp(big.NewInt(1)) != 0 {
				return accountTransaction{}, errors.New("stored receipt status is neither zero nor one")
			}
			item.IsError, item.ReceiptStatus = "0", "1"
		}
	}
	confirmations := new(big.Int).Sub(tipNumber, blockNumber)
	confirmations.Add(confirmations, big.NewInt(1))
	item.Confirmations = confirmations.String()
	if len(item.Input) >= 10 {
		item.MethodID = strings.ToLower(item.Input[:10])
	}
	return item, nil
}

func (b *PostgresBackend) minedBlocks(ctx context.Context, values url.Values) ([]minedBlock, error) {
	blockType := strings.ToLower(strings.TrimSpace(values.Get("blocktype")))
	if blockType == "uncles" {
		return nil, ErrUncleUnavailable
	}
	if blockType != "" && blockType != "blocks" {
		return nil, invalidParameter("blocktype must be blocks or uncles")
	}
	address, _, err := parseAddressParameter(values.Get("address"), "address")
	if err != nil {
		return nil, err
	}
	page, err := parsePagination(values)
	if err != nil {
		return nil, err
	}
	tx, err := b.beginCanonicalSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := b.requireCanonicalCoreRange(ctx, tx, "0", nil); err != nil {
		return nil, err
	}
	query := fmt.Sprintf(minedBlocksSQL, page.direction, page.direction)
	rows, err := tx.QueryContext(ctx, query, b.chain, strings.ToLower(address.String()), page.limit, page.offset)
	if err != nil {
		return nil, fmt.Errorf("query mined blocks: %w", err)
	}
	defer rows.Close()
	result := make([]minedBlock, 0, page.limit)
	for rows.Next() {
		var raw []byte
		var numberText string
		var hashBytes []byte
		if err := rows.Scan(&raw, &numberText, &hashBytes); err != nil {
			return nil, fmt.Errorf("scan mined block: %w", err)
		}
		number, ok := new(big.Int).SetString(numberText, 10)
		if !ok || number.Sign() < 0 {
			return nil, errors.New("stored block number is invalid")
		}
		hash, err := hashFromBytes(hashBytes)
		if err != nil {
			return nil, err
		}
		var block ethrpc.Block
		if err := decodeRawObject(raw, &block); err != nil {
			return nil, fmt.Errorf("decode mined block raw JSON: %w", err)
		}
		if block.Number == nil || block.Hash == nil || block.Miner == nil || !block.Hash.Equal(hash) || !block.Miner.Equal(address) {
			return nil, errors.New("stored mined block raw identity does not match indexed row")
		}
		wireNumber, err := block.Number.Big()
		if err != nil || wireNumber.Cmp(number) != 0 {
			return nil, errors.New("stored mined block raw number does not match indexed row")
		}
		timestamp, err := decimalQuantity(block.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("decode mined block timestamp: %w", err)
		}
		result = append(result, minedBlock{BlockNumber: number.String(), TimeStamp: timestamp})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mined blocks: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close mined blocks: %w", err)
	}
	if len(result) == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit mined block snapshot: %w", err)
	}
	return result, nil
}

const accountTransactionsSQL = `
WITH tip AS (
    SELECT number
    FROM canonical_blocks
    WHERE chain_id = $1::numeric
    ORDER BY number DESC
    LIMIT 1
)
SELECT inclusion.raw, receipt.raw, block.raw, inclusion.block_number::text,
       inclusion.block_hash, inclusion.tx_index, inclusion.tx_hash, tip.number::text
FROM transaction_inclusions AS inclusion
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = inclusion.chain_id
 AND canonical.number = inclusion.block_number
 AND canonical.block_hash = inclusion.block_hash
JOIN receipts AS receipt
  ON receipt.chain_id = inclusion.chain_id
 AND receipt.block_number = inclusion.block_number
 AND receipt.block_hash = inclusion.block_hash
 AND receipt.tx_index = inclusion.tx_index
JOIN blocks AS block
  ON block.chain_id = inclusion.chain_id
 AND block.number = inclusion.block_number
 AND block.hash = inclusion.block_hash
CROSS JOIN tip
WHERE inclusion.chain_id = $1::numeric
  AND (lower(inclusion.raw->>'from') = $2 OR lower(inclusion.raw->>'to') = $2)
  AND inclusion.block_number >= $3::numeric
  %s
ORDER BY inclusion.block_number %s, inclusion.tx_index %s, inclusion.tx_hash %s
LIMIT $%d OFFSET $%d`

const minedBlocksSQL = `
SELECT block.raw, block.number::text, block.hash
FROM blocks AS block
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = block.chain_id
 AND canonical.number = block.number
 AND canonical.block_hash = block.hash
WHERE block.chain_id = $1::numeric
  AND lower(block.raw->>'miner') = $2
ORDER BY block.number %s, block.hash %s
LIMIT $3 OFFSET $4`
