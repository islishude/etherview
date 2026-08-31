package etherscan

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/ethrpc"
)

type storedBlockContext struct {
	Timestamp uint64
	BaseFee   *big.Int
}

func decodeStoredBlockContext(
	timestampText string,
	baseFeeText sql.NullString,
) (storedBlockContext, error) {
	timestamp, err := storedUint256(timestampText, "block timestamp")
	if err != nil || !timestamp.IsUint64() {
		return storedBlockContext{}, errors.New("stored block timestamp is invalid")
	}
	context := storedBlockContext{Timestamp: timestamp.Uint64()}
	if baseFeeText.Valid {
		context.BaseFee, err = ethrpc.ParseQuantity(baseFeeText.String)
		if err != nil {
			return storedBlockContext{}, errors.New("stored block base fee is invalid")
		}
	}
	return context, nil
}

func decodeStoredBlockMiner(value sql.NullString) (common.Address, error) {
	if !value.Valid {
		return common.Address{}, errors.New("stored block miner is missing")
	}
	miner, err := ethrpc.ParseAddress(value.String)
	if err != nil {
		return common.Address{}, errors.New("stored block miner is invalid")
	}
	return miner, nil
}

func decodeStoredTransaction(
	raw json.RawMessage,
	blockHash common.Hash,
	blockNumber *big.Int,
	transactionIndex int64,
) (*types.Transaction, common.Address, error) {
	if !blockNumber.IsUint64() || transactionIndex < 0 {
		return nil, common.Address{}, errors.New("stored transaction inclusion exceeds supported range")
	}
	return chainbundle.DecodeTransaction(raw, blockHash, blockNumber.Uint64(), uint64(transactionIndex))
}

func decodeStoredReceipt(
	raw json.RawMessage,
	transaction *types.Transaction,
	blockHash common.Hash,
	blockNumber *big.Int,
	transactionIndex int64,
) (*types.Receipt, error) {
	if !blockNumber.IsUint64() || transactionIndex < 0 {
		return nil, errors.New("stored receipt inclusion exceeds supported range")
	}
	firstLogIndex, err := storedReceiptFirstLogIndex(raw)
	if err != nil {
		return nil, err
	}
	receipt, _, _, err := chainbundle.DecodeStoredReceipt(
		raw, transaction, blockHash, blockNumber.Uint64(), uint64(transactionIndex), firstLogIndex,
	)
	return receipt, err
}

func decodeStoredReceiptWithBlockContext(
	raw json.RawMessage,
	transaction *types.Transaction,
	blockHash common.Hash,
	blockNumber *big.Int,
	transactionIndex int64,
	baseFee *big.Int,
) (*types.Receipt, error) {
	if transaction == nil || !blockNumber.IsUint64() || transactionIndex < 0 {
		return nil, errors.New("stored receipt inclusion exceeds supported range")
	}
	firstLogIndex, err := storedReceiptFirstLogIndex(raw)
	if err != nil {
		return nil, err
	}
	receipt, _, _, err := chainbundle.DecodeStoredReceiptWithBaseFee(
		raw,
		transaction,
		blockHash,
		blockNumber.Uint64(),
		uint64(transactionIndex),
		firstLogIndex,
		baseFee,
	)
	return receipt, err
}

func decodeStoredLog(
	raw json.RawMessage,
	transactionHash, blockHash common.Hash,
	blockNumber *big.Int,
	transactionIndex, logIndex int64,
) (*types.Log, error) {
	if !blockNumber.IsUint64() || transactionIndex < 0 || logIndex < 0 {
		return nil, errors.New("stored log inclusion exceeds supported range")
	}
	return chainbundle.DecodeLog(
		raw, transactionHash, blockHash, blockNumber.Uint64(), uint64(transactionIndex), uint64(logIndex),
	)
}

func decodeStandaloneReceipt(
	raw json.RawMessage,
	transactionHash, blockHash common.Hash,
	blockNumber *big.Int,
	transactionIndex int64,
) (*types.Receipt, bool, error) {
	if transactionIndex < 0 || !blockNumber.IsUint64() {
		return nil, false, errors.New("stored receipt inclusion exceeds supported range")
	}
	var receipt types.Receipt
	if err := decodeRawObject(raw, &receipt); err != nil {
		return nil, false, err
	}
	if receipt.TxHash != transactionHash || receipt.BlockHash != blockHash ||
		receipt.BlockNumber == nil || receipt.BlockNumber.Cmp(blockNumber) != 0 ||
		uint64(receipt.TransactionIndex) != uint64(transactionIndex) {
		return nil, false, errors.New("stored receipt raw identity does not match indexed row")
	}
	var fields map[string]json.RawMessage
	if err := decodeRawObject(raw, &fields); err != nil {
		return nil, false, err
	}
	statusRaw := strings.TrimSpace(string(fields["status"]))
	statusPresent := statusRaw != "" && !strings.EqualFold(statusRaw, "null")
	return &receipt, statusPresent, nil
}

func storedReceiptFirstLogIndex(raw json.RawMessage) (uint64, error) {
	var projection struct {
		Logs []struct {
			Index *hexutil.Uint64 `json:"logIndex"`
		} `json:"logs"`
	}
	if err := decodeRawObject(raw, &projection); err != nil {
		return 0, err
	}
	if len(projection.Logs) == 0 {
		return 0, nil
	}
	if projection.Logs[0].Index == nil {
		return 0, errors.New("stored receipt first log index is null")
	}
	return uint64(*projection.Logs[0].Index), nil
}

func decimalBig(value *big.Int) (string, error) {
	if value == nil || value.Sign() < 0 || value.BitLen() > 256 {
		return "", errors.New("quantity is not an unsigned 256-bit integer")
	}
	return value.String(), nil
}

func decimalUint64(value uint64) string {
	return new(big.Int).SetUint64(value).String()
}

func effectiveGasPrice(transaction *types.Transaction, receipt *types.Receipt) (*big.Int, error) {
	if receipt != nil && receipt.EffectiveGasPrice != nil {
		return new(big.Int).Set(receipt.EffectiveGasPrice), nil
	}
	if transaction == nil {
		return nil, fmt.Errorf("stored transaction has no effective gas price")
	}
	switch transaction.Type() {
	case types.LegacyTxType, types.AccessListTxType:
		return transaction.GasPrice(), nil
	default:
		return nil, errors.New(
			"stored dynamic-fee transaction has no authenticated effective gas price",
		)
	}
}
