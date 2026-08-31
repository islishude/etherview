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

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/ethrpc"
)

type rowScanner interface {
	Scan(dest ...any) error
}

type blockRecord struct {
	Model  gen.Block
	Number uint64
	Hash   common.Hash
}

type storedWithdrawalProjection struct {
	Index          string `json:"index"`
	ValidatorIndex string `json:"validator_index"`
	Address        string `json:"address"`
	Amount         string `json:"amount"`
}

func (r *PostgresReader) scanBlock(scanner rowScanner, forceCanonical bool) (blockRecord, error) {
	var hashBytes, parentHashBytes, withdrawalsJSON []byte
	var numberText, timestampText string
	var minerText, gasUsedText, gasLimitText, baseFeeText sql.NullString
	var expectedTransactionCount, withdrawalCount sql.NullInt64
	var normalizedTransactionCount int64
	var withdrawalsPresent sql.NullBool
	var canonical bool
	var safeHeight, finalizedHeight sql.NullString
	if err := scanner.Scan(
		&numberText, &hashBytes, &parentHashBytes, &timestampText,
		&minerText, &gasUsedText, &gasLimitText, &baseFeeText,
		&expectedTransactionCount, &normalizedTransactionCount,
		&withdrawalsPresent, &withdrawalCount, &withdrawalsJSON,
		&canonical, &safeHeight, &finalizedHeight,
	); err != nil {
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
	parentHash, err := decodeHashBytes(parentHashBytes)
	if err != nil {
		return blockRecord{}, fmt.Errorf("decode block parent hash: %w", err)
	}
	timestampSeconds, err := parseDecimalUint64(timestampText)
	if err != nil {
		return blockRecord{}, fmt.Errorf("decode block timestamp: %w", err)
	}
	timestamp, err := quantityTime(timestampSeconds)
	if err != nil {
		return blockRecord{}, fmt.Errorf("decode block timestamp: %w", err)
	}
	if !expectedTransactionCount.Valid || expectedTransactionCount.Int64 < 0 ||
		expectedTransactionCount.Int64 > int64(math.MaxInt) {
		return blockRecord{}, errors.New("stored block transaction count is invalid")
	}
	if normalizedTransactionCount != expectedTransactionCount.Int64 {
		return blockRecord{}, errors.New("stored block transaction count does not match normalized inclusions")
	}
	model := gen.Block{
		Hash:             strings.ToLower(hash.Hex()),
		Number:           strconv.FormatUint(number, 10),
		ParentHash:       strings.ToLower(parentHash.Hex()),
		Timestamp:        timestamp,
		TransactionCount: int(expectedTransactionCount.Int64),
		Canonical:        canonical,
		Completeness:     r.completeness,
	}
	model.Finality, err = classifyFinality(canonical, number, safeHeight, finalizedHeight)
	if err != nil {
		return blockRecord{}, err
	}
	if minerText.Valid {
		miner, err := ChecksumAddress(minerText.String)
		if err != nil {
			return blockRecord{}, fmt.Errorf("checksum block miner: %w", err)
		}
		model.Miner = &miner
	}
	model.GasUsed, err = blockUint64Quantity(gasUsedText, "gas used")
	if err != nil {
		return blockRecord{}, err
	}
	model.GasLimit, err = blockUint64Quantity(gasLimitText, "gas limit")
	if err != nil {
		return blockRecord{}, err
	}
	model.BaseFeePerGas, err = blockBigQuantity(baseFeeText, "base fee per gas")
	if err != nil {
		return blockRecord{}, err
	}
	withdrawals, err := decodeStoredWithdrawals(
		withdrawalsJSON, withdrawalsPresent, withdrawalCount,
	)
	if err != nil {
		return blockRecord{}, err
	}
	model.Withdrawals = withdrawals
	return blockRecord{Model: model, Number: number, Hash: hash}, nil
}

func blockUint64Quantity(value sql.NullString, field string) (*string, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := ethrpc.ParseQuantity(value.String)
	if err != nil || !parsed.IsUint64() {
		return nil, fmt.Errorf("decode block %s: invalid uint64 quantity", field)
	}
	decimal := parsed.String()
	return &decimal, nil
}

func blockBigQuantity(value sql.NullString, field string) (*string, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := ethrpc.ParseQuantity(value.String)
	if err != nil {
		return nil, fmt.Errorf("decode block %s: invalid quantity", field)
	}
	decimal := parsed.String()
	return &decimal, nil
}

func decodeStoredWithdrawals(
	raw []byte,
	present sql.NullBool,
	expectedCount sql.NullInt64,
) (*[]gen.BlockWithdrawal, error) {
	if !present.Valid {
		return nil, errors.New("stored block withdrawals presence is invalid")
	}
	var projected []storedWithdrawalProjection
	if err := decodeRawObject(raw, &projected); err != nil {
		return nil, fmt.Errorf("decode normalized block withdrawals: %w", err)
	}
	if !present.Bool {
		if expectedCount.Valid || len(projected) != 0 {
			return nil, errors.New("stored block without withdrawals has normalized withdrawal rows")
		}
		return nil, nil
	}
	if !expectedCount.Valid || expectedCount.Int64 < 0 ||
		expectedCount.Int64 != int64(len(projected)) {
		return nil, errors.New("stored block withdrawal count does not match normalized withdrawals")
	}
	withdrawals := make([]gen.BlockWithdrawal, len(projected))
	for index, withdrawal := range projected {
		withdrawalIndex, err := parseDecimalUint64(withdrawal.Index)
		if err != nil {
			return nil, fmt.Errorf("decode block withdrawal %d index: %w", index, err)
		}
		validatorIndex, err := parseDecimalUint64(withdrawal.ValidatorIndex)
		if err != nil {
			return nil, fmt.Errorf("decode block withdrawal %d validator: %w", index, err)
		}
		amount, err := parseDecimalUint64(withdrawal.Amount)
		if err != nil {
			return nil, fmt.Errorf("decode block withdrawal %d amount: %w", index, err)
		}
		address, err := ChecksumAddress(withdrawal.Address)
		if err != nil {
			return nil, fmt.Errorf("checksum block withdrawal %d address: %w", index, err)
		}
		withdrawals[index] = gen.BlockWithdrawal{
			Index:          strconv.FormatUint(withdrawalIndex, 10),
			ValidatorIndex: strconv.FormatUint(validatorIndex, 10),
			Address:        address,
			Amount:         strconv.FormatUint(amount, 10),
		}
	}
	return &withdrawals, nil
}

func (r *PostgresReader) transactionModel(
	transactionJSON, receiptJSON []byte,
	blockTimestampText string,
	blockBaseFeeText sql.NullString,
	blockNumberText string,
	blockHashBytes []byte,
	transactionIndex int64,
	transactionHashBytes []byte,
	canonical bool,
	safeHeight, finalizedHeight sql.NullString,
	tipNumber uint64,
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
	wire, sender, err := chainbundle.DecodeTransaction(
		transactionJSON, blockHash, blockNumber, uint64(transactionIndex),
	)
	if err != nil {
		return gen.Transaction{}, fmt.Errorf("decode transaction raw JSON: %w", err)
	}
	if wire.Hash() != transactionHash {
		return gen.Transaction{}, errors.New("stored transaction raw hash does not match indexed identity")
	}
	from, err := ChecksumAddress(sender.Hex())
	if err != nil {
		return gen.Transaction{}, fmt.Errorf("checksum transaction sender: %w", err)
	}
	model := gen.Transaction{
		Hash:             strings.ToLower(transactionHash.Hex()),
		BlockHash:        new(strings.ToLower(blockHash.Hex())),
		BlockNumber:      new(strconv.FormatUint(blockNumber, 10)),
		Canonical:        canonical,
		Completeness:     r.completeness,
		From:             from,
		Input:            hexutil.Encode(wire.Data()),
		TransactionIndex: new(int(transactionIndex)),
	}
	model.Finality, err = classifyFinality(canonical, blockNumber, safeHeight, finalizedHeight)
	if err != nil {
		return gen.Transaction{}, err
	}
	if wire.To() != nil {
		to, err := ChecksumAddress(wire.To().Hex())
		if err != nil {
			return gen.Transaction{}, fmt.Errorf("checksum transaction recipient: %w", err)
		}
		model.To = &to
	}
	model.Nonce = strconv.FormatUint(wire.Nonce(), 10)
	model.Value = wire.Value().String()
	model.Gas = strconv.FormatUint(wire.Gas(), 10)
	switch wire.Type() {
	case types.LegacyTxType, types.AccessListTxType:
		value := wire.GasPrice().String()
		model.GasPrice = &value
	default:
		value := wire.GasFeeCap().String()
		model.MaxFeePerGas = &value
		value = wire.GasTipCap().String()
		model.MaxPriorityFeePerGas = &value
	}
	if transactionTypePresent(transactionJSON) {
		value := strconv.FormatUint(uint64(wire.Type()), 10)
		model.Type = &value
	}
	if wire.Type() >= types.AccessListTxType && wire.Type() <= types.SetCodeTxType {
		accessList := wire.AccessList()
		entries := make([]gen.TransactionAccessListEntry, len(accessList))
		for index, entry := range accessList {
			address, err := ChecksumAddress(entry.Address.Hex())
			if err != nil {
				return gen.Transaction{}, fmt.Errorf("checksum transaction access-list address: %w", err)
			}
			storageKeys := make([]gen.Hash, len(entry.StorageKeys))
			for keyIndex, key := range entry.StorageKeys {
				storageKeys[keyIndex] = gen.Hash(strings.ToLower(key.Hex()))
			}
			entries[index] = gen.TransactionAccessListEntry{Address: address, StorageKeys: storageKeys}
		}
		model.AccessList = &entries
	}
	if wire.Type() == types.BlobTxType {
		blobHashes := wire.BlobHashes()
		hashes := make([]gen.Hash, len(blobHashes))
		for index, hash := range blobHashes {
			hashes[index] = gen.Hash(strings.ToLower(hash.Hex()))
		}
		model.BlobVersionedHashes = &hashes
		if feeCap := wire.BlobGasFeeCap(); feeCap != nil {
			value := feeCap.String()
			model.MaxFeePerBlobGas = &value
		}
	}

	firstLogIndex, err := receiptFirstLogIndex(receiptJSON)
	if err != nil {
		return gen.Transaction{}, fmt.Errorf("decode receipt raw JSON: %w", err)
	}
	blockBaseFee, err := parseBlockBaseFee(blockBaseFeeText)
	if err != nil {
		return gen.Transaction{}, fmt.Errorf("decode transaction block base fee: %w", err)
	}
	receipt, _, _, err := chainbundle.DecodeStoredReceiptWithBaseFee(
		receiptJSON, wire, blockHash, blockNumber, uint64(transactionIndex),
		firstLogIndex, blockBaseFee,
	)
	if err != nil {
		return gen.Transaction{}, fmt.Errorf("decode receipt raw JSON: %w", err)
	}
	status := gen.TransactionStatusUnknown
	if len(receipt.PostState) == 0 {
		switch receipt.Status {
		case types.ReceiptStatusFailed:
			status = gen.TransactionStatusFailed
		case types.ReceiptStatusSuccessful:
			status = gen.TransactionStatusSuccess
		}
	}
	model.Status = &status
	if wire.To() == nil && receipt.ContractAddress != (common.Address{}) {
		contractAddress, err := ChecksumAddress(receipt.ContractAddress.Hex())
		if err != nil {
			return gen.Transaction{}, fmt.Errorf("checksum created contract address: %w", err)
		}
		model.ContractAddress = &contractAddress
	}
	model.GasUsed = ptrQuantity(strconv.FormatUint(receipt.GasUsed, 10))
	if receipt.BlobGasPrice != nil {
		model.BlobBaseFeePerGas = ptrQuantity(receipt.BlobGasPrice.String())
	}

	blockTimestampSeconds, err := parseDecimalUint64(blockTimestampText)
	if err != nil {
		return gen.Transaction{}, fmt.Errorf("decode transaction block timestamp: %w", err)
	}
	blockTimestamp, err := quantityTime(blockTimestampSeconds)
	if err != nil {
		return gen.Transaction{}, fmt.Errorf("decode transaction block timestamp: %w", err)
	}
	model.BlockTimestamp = &blockTimestamp
	if blockBaseFee != nil {
		model.BaseFeePerGas = ptrQuantity(blockBaseFee.String())
	}
	if canonical && tipNumber >= blockNumber {
		confirmations := strconv.FormatUint((tipNumber - blockNumber + 1), 10)
		model.Confirmations = &confirmations
	}
	effectiveGasPrice, err := chainbundle.TransactionEffectiveGasPrice(wire, blockBaseFee)
	if err != nil {
		return gen.Transaction{}, fmt.Errorf("derive transaction effective gas price: %w", err)
	}
	if effectiveGasPrice != nil {
		model.EffectiveGasPrice = ptrQuantity(effectiveGasPrice.String())
		txFeeWei := new(big.Int).Mul(effectiveGasPrice, new(big.Int).SetUint64(receipt.GasUsed))
		model.TxFeeWei = ptrQuantity(txFeeWei.String())
	}
	if blockBaseFee != nil {
		burnedWei := new(big.Int).Mul(blockBaseFee, new(big.Int).SetUint64(receipt.GasUsed))
		model.BurnedWei = ptrQuantity(burnedWei.String())
	} else {
		model.BurnedWei = ptrQuantity("0")
	}

	return model, nil
}

func parseBlockBaseFee(value sql.NullString) (*big.Int, error) {
	if !value.Valid {
		return nil, nil
	}
	return ethrpc.ParseQuantity(value.String)
}

func ptrQuantity(value string) *gen.Quantity {
	quantity := gen.Quantity(value)
	return &quantity
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

func transactionTypePresent(raw []byte) bool {
	var fields map[string]json.RawMessage
	return json.Unmarshal(raw, &fields) == nil && len(fields["type"]) != 0 &&
		!strings.EqualFold(strings.TrimSpace(string(fields["type"])), "null")
}

func receiptFirstLogIndex(raw []byte) (uint64, error) {
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

func quantityTime(value uint64) (time.Time, error) {
	if value > math.MaxInt64 {
		return time.Time{}, fmt.Errorf("timestamp %d is outside time.Time Unix range", value)
	}
	return time.Unix(int64(value), 0).UTC(), nil
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
