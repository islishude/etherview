package query

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/ethrpc"
)

type rowScanner interface {
	Scan(dest ...any) error
}

type blockRecord struct {
	Model  gen.Block
	Number uint64
	Hash   ethrpc.Hash
}

func (r *PostgresReader) scanBlock(scanner rowScanner, forceCanonical bool) (blockRecord, error) {
	var raw, hashBytes []byte
	var numberText string
	var canonical bool
	var safeHeight, finalizedHeight sql.NullString
	if err := scanner.Scan(&raw, &numberText, &hashBytes, &canonical, &safeHeight, &finalizedHeight); err != nil {
		return blockRecord{}, fmt.Errorf("scan block: %w", err)
	}
	if forceCanonical && !canonical {
		return blockRecord{}, errors.New("canonical block query returned an orphan")
	}
	number, err := parseDecimalUint64(numberText)
	if err != nil {
		return blockRecord{}, fmt.Errorf("decode block number: %w", err)
	}
	hash, err := decodeHashBytes(hashBytes)
	if err != nil {
		return blockRecord{}, err
	}
	var wire ethrpc.Block
	if err := decodeRawObject(raw, &wire); err != nil {
		return blockRecord{}, fmt.Errorf("decode block raw JSON: %w", err)
	}
	if wire.Number == nil || wire.Hash == nil {
		return blockRecord{}, errors.New("stored block raw JSON has a null number or hash")
	}
	wireNumber, err := wire.Number.Uint64()
	if err != nil || wireNumber != number {
		return blockRecord{}, errors.New("stored block raw number does not match indexed identity")
	}
	if !wire.Hash.Equal(hash) {
		return blockRecord{}, errors.New("stored block raw hash does not match indexed identity")
	}
	if _, err := ethrpc.ParseHash(wire.ParentHash.String()); err != nil {
		return blockRecord{}, fmt.Errorf("stored block raw parent hash is invalid: %w", err)
	}
	timestamp, err := quantityTime(wire.Timestamp)
	if err != nil {
		return blockRecord{}, fmt.Errorf("decode block timestamp: %w", err)
	}
	model := gen.Block{
		Hash:             strings.ToLower(hash.String()),
		Number:           strconv.FormatUint(number, 10),
		ParentHash:       strings.ToLower(wire.ParentHash.String()),
		Timestamp:        timestamp,
		TransactionCount: len(wire.Transactions),
		Canonical:        canonical,
		Completeness:     r.completeness,
	}
	model.Finality, err = classifyFinality(canonical, number, safeHeight, finalizedHeight)
	if err != nil {
		return blockRecord{}, err
	}
	if wire.Miner != nil {
		miner, err := ChecksumAddress(wire.Miner.String())
		if err != nil {
			return blockRecord{}, fmt.Errorf("checksum block miner: %w", err)
		}
		model.Miner = &miner
	}
	if wire.GasUsed != "" {
		value, err := decimalQuantity(wire.GasUsed)
		if err != nil {
			return blockRecord{}, fmt.Errorf("decode block gas used: %w", err)
		}
		model.GasUsed = &value
	}
	if wire.GasLimit != "" {
		value, err := decimalQuantity(wire.GasLimit)
		if err != nil {
			return blockRecord{}, fmt.Errorf("decode block gas limit: %w", err)
		}
		model.GasLimit = &value
	}
	if wire.BaseFeePerGas != nil {
		value, err := decimalQuantity(*wire.BaseFeePerGas)
		if err != nil {
			return blockRecord{}, fmt.Errorf("decode block base fee: %w", err)
		}
		model.BaseFeePerGas = &value
	}
	return blockRecord{Model: model, Number: number, Hash: hash}, nil
}

func (r *PostgresReader) transactionModel(
	transactionJSON, receiptJSON []byte,
	blockNumberText string,
	blockHashBytes []byte,
	transactionIndex int64,
	transactionHashBytes []byte,
	canonical bool,
	safeHeight, finalizedHeight sql.NullString,
) (gen.Transaction, error) {
	blockNumber, err := parseDecimalUint64(blockNumberText)
	if err != nil {
		return gen.Transaction{}, fmt.Errorf("decode transaction block number: %w", err)
	}
	if transactionIndex < 0 || uint64(transactionIndex) > uint64(math.MaxInt) {
		return gen.Transaction{}, fmt.Errorf("transaction index %d exceeds API integer range", transactionIndex)
	}
	blockHash, err := decodeHashBytes(blockHashBytes)
	if err != nil {
		return gen.Transaction{}, err
	}
	transactionHash, err := decodeHashBytes(transactionHashBytes)
	if err != nil {
		return gen.Transaction{}, err
	}
	var wire ethrpc.Transaction
	if err := decodeRawObject(transactionJSON, &wire); err != nil {
		return gen.Transaction{}, fmt.Errorf("decode transaction raw JSON: %w", err)
	}
	if !wire.Hash.Equal(transactionHash) {
		return gen.Transaction{}, errors.New("stored transaction raw hash does not match indexed identity")
	}
	if wire.BlockHash == nil || !wire.BlockHash.Equal(blockHash) {
		return gen.Transaction{}, errors.New("stored transaction raw block hash does not match inclusion")
	}
	if wire.BlockNumber == nil {
		return gen.Transaction{}, errors.New("stored transaction raw block number is null")
	}
	wireBlockNumber, err := wire.BlockNumber.Uint64()
	if err != nil || wireBlockNumber != blockNumber {
		return gen.Transaction{}, errors.New("stored transaction raw block number does not match inclusion")
	}
	if wire.TransactionIndex == nil {
		return gen.Transaction{}, errors.New("stored transaction raw index is null")
	}
	wireIndex, err := wire.TransactionIndex.Uint64()
	if err != nil || wireIndex != uint64(transactionIndex) {
		return gen.Transaction{}, errors.New("stored transaction raw index does not match inclusion")
	}
	from, err := ChecksumAddress(wire.From.String())
	if err != nil {
		return gen.Transaction{}, fmt.Errorf("checksum transaction sender: %w", err)
	}
	if _, err := ethrpc.ParseData(wire.Input.String()); err != nil {
		return gen.Transaction{}, fmt.Errorf("decode transaction input: %w", err)
	}
	model := gen.Transaction{
		Hash:             strings.ToLower(transactionHash.String()),
		BlockHash:        new(strings.ToLower(blockHash.String())),
		BlockNumber:      new(strconv.FormatUint(blockNumber, 10)),
		Canonical:        canonical,
		Completeness:     r.completeness,
		From:             from,
		Input:            wire.Input.String(),
		TransactionIndex: new(int(transactionIndex)),
	}
	model.Finality, err = classifyFinality(canonical, blockNumber, safeHeight, finalizedHeight)
	if err != nil {
		return gen.Transaction{}, err
	}
	if wire.To != nil {
		to, err := ChecksumAddress(wire.To.String())
		if err != nil {
			return gen.Transaction{}, fmt.Errorf("checksum transaction recipient: %w", err)
		}
		model.To = &to
	}
	if model.Nonce, err = decimalQuantity(wire.Nonce); err != nil {
		return gen.Transaction{}, fmt.Errorf("decode transaction nonce: %w", err)
	}
	if model.Value, err = decimalQuantity(wire.Value); err != nil {
		return gen.Transaction{}, fmt.Errorf("decode transaction value: %w", err)
	}
	if model.Gas, err = decimalQuantity(wire.Gas); err != nil {
		return gen.Transaction{}, fmt.Errorf("decode transaction gas: %w", err)
	}
	if wire.GasPrice != nil {
		value, err := decimalQuantity(*wire.GasPrice)
		if err != nil {
			return gen.Transaction{}, fmt.Errorf("decode transaction gas price: %w", err)
		}
		model.GasPrice = &value
	}
	if wire.MaxFeePerGas != nil {
		value, err := decimalQuantity(*wire.MaxFeePerGas)
		if err != nil {
			return gen.Transaction{}, fmt.Errorf("decode transaction max fee: %w", err)
		}
		model.MaxFeePerGas = &value
	}
	if wire.MaxPriorityFeePerGas != nil {
		value, err := decimalQuantity(*wire.MaxPriorityFeePerGas)
		if err != nil {
			return gen.Transaction{}, fmt.Errorf("decode transaction priority fee: %w", err)
		}
		model.MaxPriorityFeePerGas = &value
	}
	if wire.Type != nil {
		value, err := decimalQuantity(*wire.Type)
		if err != nil {
			return gen.Transaction{}, fmt.Errorf("decode transaction type: %w", err)
		}
		model.Type = &value
	}

	var receipt ethrpc.Receipt
	if err := decodeRawObject(receiptJSON, &receipt); err != nil {
		return gen.Transaction{}, fmt.Errorf("decode receipt raw JSON: %w", err)
	}
	if !receipt.TransactionHash.Equal(transactionHash) || !receipt.BlockHash.Equal(blockHash) {
		return gen.Transaction{}, errors.New("stored receipt raw identity does not match transaction inclusion")
	}
	receiptBlockNumber, err := receipt.BlockNumber.Uint64()
	if err != nil || receiptBlockNumber != blockNumber {
		return gen.Transaction{}, errors.New("stored receipt raw block number does not match transaction inclusion")
	}
	receiptIndex, err := receipt.TransactionIndex.Uint64()
	if err != nil || receiptIndex != uint64(transactionIndex) {
		return gen.Transaction{}, errors.New("stored receipt raw index does not match transaction inclusion")
	}
	status := gen.TransactionStatusUnknown
	if receipt.Status != nil {
		statusValue, err := receipt.Status.Big()
		if err != nil {
			return gen.Transaction{}, fmt.Errorf("decode receipt status: %w", err)
		}
		switch {
		case statusValue.Sign() == 0:
			status = gen.TransactionStatusFailed
		case statusValue.Cmp(big.NewInt(1)) == 0:
			status = gen.TransactionStatusSuccess
		}
	}
	model.Status = &status
	return model, nil
}

func decodeRawObject(raw []byte, destination any) error {
	if len(raw) == 0 || destination == nil {
		return errors.New("raw JSON and destination are required")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("raw JSON contains multiple values")
		}
		return err
	}
	return nil
}

func decimalQuantity(quantity ethrpc.Quantity) (string, error) {
	value, err := quantity.Big()
	if err != nil {
		return "", err
	}
	return value.String(), nil
}

func quantityTime(quantity ethrpc.Quantity) (time.Time, error) {
	value, err := quantity.Big()
	if err != nil {
		return time.Time{}, err
	}
	if value.Sign() < 0 || !value.IsInt64() {
		return time.Time{}, fmt.Errorf("timestamp %s is outside time.Time Unix range", value)
	}
	return time.Unix(value.Int64(), 0).UTC(), nil
}

func classifyFinality(canonical bool, number uint64, safeHeight, finalizedHeight sql.NullString) (gen.Finality, error) {
	if !canonical {
		return gen.FinalityOrphan, nil
	}
	safe, finalized, err := finalityNumbers(safeHeight, finalizedHeight)
	if err != nil {
		return "", err
	}
	if finalized != nil {
		if number <= *finalized {
			return gen.FinalityFinalized, nil
		}
	}
	if safe != nil {
		if number <= *safe {
			return gen.FinalitySafe, nil
		}
	}
	return gen.FinalityLatest, nil
}

func finalityNumbers(safeHeight, finalizedHeight sql.NullString) (*uint64, *uint64, error) {
	var safe, finalized *uint64
	if safeHeight.Valid {
		value, err := parseDecimalUint64(safeHeight.String)
		if err != nil {
			return nil, nil, fmt.Errorf("decode safe height: %w", err)
		}
		safe = &value
	}
	if finalizedHeight.Valid {
		value, err := parseDecimalUint64(finalizedHeight.String)
		if err != nil {
			return nil, nil, fmt.Errorf("decode finalized height: %w", err)
		}
		finalized = &value
	}
	if safe != nil && finalized != nil && *finalized > *safe {
		return nil, nil, errors.New("finalized height exceeds safe height")
	}
	return safe, finalized, nil
}
