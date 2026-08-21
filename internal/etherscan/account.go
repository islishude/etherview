package etherscan

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/islishude/etherview/internal/db/gen"
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
	defer tx.Rollback() //nolint:errcheck
	if _, err := b.requireCanonicalCoreRange(ctx, tx, start, end); err != nil {
		return nil, err
	}

	var endArgument any
	if end != nil {
		endArgument = *end
	}
	rows, err := tx.QueryContext(ctx, dbgen.EtherscanAccountTransactions,
		b.chain, strings.ToLower(address.Hex()), start, endArgument,
		page.limit, page.offset, page.direction,
	)
	if err != nil {
		return nil, fmt.Errorf("query account transactions: %w", err)
	}
	defer rows.Close() //nolint:errcheck
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

	transaction, sender, err := decodeStoredTransaction(transactionJSON, blockHash, blockNumber, transactionIndex)
	if err != nil {
		return accountTransaction{}, fmt.Errorf("decode transaction raw JSON: %w", err)
	}
	if transaction.Hash() != transactionHash {
		return accountTransaction{}, errors.New("stored transaction raw identity does not match inclusion")
	}

	receipt, err := decodeStoredReceiptWithBlockContext(
		receiptJSON,
		blockJSON,
		transaction,
		blockHash,
		blockNumber,
		transactionIndex,
	)
	if err != nil {
		return accountTransaction{}, fmt.Errorf("decode receipt raw JSON: %w", err)
	}

	block, err := decodeStoredBlockProjection(blockJSON, blockHash, blockNumber)
	if err != nil {
		return accountTransaction{}, fmt.Errorf("decode block raw JSON: %w", err)
	}

	from, err := checksumAddress(sender)
	if err != nil {
		return accountTransaction{}, fmt.Errorf("checksum transaction sender: %w", err)
	}
	to := ""
	if transaction.To() != nil {
		to, err = checksumAddress(*transaction.To())
		if err != nil {
			return accountTransaction{}, fmt.Errorf("checksum transaction recipient: %w", err)
		}
	}
	contractAddress := ""
	if receipt.ContractAddress != (common.Address{}) {
		contractAddress, err = checksumAddress(receipt.ContractAddress)
		if err != nil {
			return accountTransaction{}, fmt.Errorf("checksum created contract: %w", err)
		}
	}

	item := accountTransaction{
		BlockNumber: blockNumber.String(), Hash: strings.ToLower(transactionHash.Hex()),
		BlockHash: strings.ToLower(blockHash.Hex()), TransactionIndex: strconv.FormatInt(transactionIndex, 10),
		From: from, To: to, ContractAddress: contractAddress, Input: hexutil.Encode(transaction.Data()),
		FunctionName: "",
	}
	item.TimeStamp = decimalUint64(uint64(*block.Timestamp))
	item.Nonce = decimalUint64(transaction.Nonce())
	if item.Value, err = decimalBig(transaction.Value()); err != nil {
		return accountTransaction{}, fmt.Errorf("decode transaction value: %w", err)
	}
	item.Gas = decimalUint64(transaction.Gas())
	gasPrice, err := effectiveGasPrice(transaction, receipt)
	if err != nil {
		return accountTransaction{}, err
	}
	if item.GasPrice, err = decimalBig(gasPrice); err != nil {
		return accountTransaction{}, fmt.Errorf("decode transaction gas price: %w", err)
	}
	item.CumulativeGasUsed = decimalUint64(receipt.CumulativeGasUsed)
	item.GasUsed = decimalUint64(receipt.GasUsed)
	if len(receipt.PostState) == 0 {
		switch receipt.Status {
		case 0:
			item.IsError, item.ReceiptStatus = "1", "0"
		case 1:
			item.IsError, item.ReceiptStatus = "0", "1"
		default:
			return accountTransaction{}, errors.New("stored receipt status is neither zero nor one")
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
	defer tx.Rollback() //nolint:errcheck
	if _, err := b.requireCanonicalCoreRange(ctx, tx, "0", nil); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, dbgen.EtherscanMinedBlocks,
		b.chain, strings.ToLower(address.Hex()), page.limit, page.offset, page.direction,
	)
	if err != nil {
		return nil, fmt.Errorf("query mined blocks: %w", err)
	}
	defer rows.Close() //nolint:errcheck
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
		block, err := decodeStoredBlockProjection(raw, hash, number)
		if err != nil {
			return nil, fmt.Errorf("decode mined block raw JSON: %w", err)
		}
		if block.Miner == nil || *block.Miner != address {
			return nil, errors.New("stored mined block raw identity does not match indexed row")
		}
		timestamp := decimalUint64(uint64(*block.Timestamp))
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
