package chainbundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/trie"
)

var (
	requiredHeaderFields = []string{
		"hash", "parentHash", "sha3Uncles", "miner", "stateRoot",
		"transactionsRoot", "receiptsRoot", "logsBloom", "difficulty", "number",
		"gasLimit", "gasUsed", "timestamp", "extraData", "mixHash", "nonce",
	}
	headerHashFields = []string{
		"hash", "parentHash", "sha3Uncles", "transactionsRoot", "stateRoot",
		"receiptsRoot", "mixHash", "withdrawalsRoot", "parentBeaconBlockRoot",
		"requestsHash",
	}
	headerQuantityFields = []string{
		"number", "difficulty", "totalDifficulty", "size", "gasLimit", "gasUsed",
		"timestamp", "baseFeePerGas", "blobGasUsed", "excessBlobGas", "slotNumber",
	}
	headerDataFields          = []string{"nonce", "logsBloom", "extraData"}
	headerAddressFields       = []string{"miner"}
	transactionHashFields     = []string{"hash", "blockHash"}
	transactionDataFields     = []string{"input"}
	transactionAddressFields  = []string{"from", "to"}
	transactionQuantityFields = []string{
		"type", "blockNumber", "transactionIndex", "nonce", "gas", "gasPrice",
		"maxPriorityFeePerGas", "maxFeePerGas", "value", "chainId",
		"maxFeePerBlobGas", "v", "r", "s", "yParity",
	}
	receiptHashFields     = []string{"transactionHash", "blockHash"}
	receiptQuantityFields = []string{
		"transactionIndex", "blockNumber", "cumulativeGasUsed", "gasUsed",
		"status", "type", "effectiveGasPrice", "blobGasUsed", "blobGasPrice",
	}
	receiptDataFields        = []string{"logsBloom", "root"}
	receiptAddressFields     = []string{"from", "to", "contractAddress"}
	logHashFields            = []string{"transactionHash", "blockHash"}
	logQuantityFields        = []string{"logIndex", "transactionIndex", "blockNumber", "blockTimestamp"}
	logDataFields            = []string{"data"}
	logAddressFields         = []string{"address"}
	withdrawalQuantityFields = []string{"index", "validatorIndex", "amount"}
	withdrawalAddressFields  = []string{"address"}
)

// DecodeHeader decodes a mined execution header and verifies the RPC-supplied
// hash against the hash computed by go-ethereum.
func DecodeHeader(raw json.RawMessage) (*types.Header, error) {
	fields, err := decodeObject(raw, "header")
	if err != nil {
		return nil, err
	}
	if err := validateFields(fields, "header", headerHashFields, headerQuantityFields, headerDataFields, headerAddressFields); err != nil {
		return nil, err
	}
	if err := requirePresentFields(fields, "header", requiredHeaderFields...); err != nil {
		return nil, err
	}
	wireHash, err := requiredHash(fields, "hash", "header.hash")
	if err != nil {
		return nil, err
	}
	var header types.Header
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, fmt.Errorf("%w: decode header: %v", ErrInvalidWireValue, err)
	}
	if err := header.SanityCheck(); err != nil {
		return nil, fmt.Errorf("%w: header failed sanity check: %v", ErrInvalidWireValue, err)
	}
	if computed := header.Hash(); computed != wireHash {
		return nil, validation("header.hash", "does not match the go-ethereum header hash")
	}
	return types.CopyHeader(&header), nil
}

// UncleHashes returns the exact ordered uncle identities from a raw block.
func UncleHashes(raw json.RawMessage) ([]common.Hash, error) {
	fields, err := decodeObject(raw, "block")
	if err != nil {
		return nil, err
	}
	items, err := requiredArray(fields, "uncles", "block.uncles")
	if err != nil {
		return nil, err
	}
	hashes := make([]common.Hash, len(items))
	for index := range items {
		hashes[index], err = parseHashJSON(items[index], fmt.Sprintf("block.uncles[%d]", index))
		if err != nil {
			return nil, err
		}
	}
	return hashes, nil
}

// DecodeBlock constructs a geth block from one raw eth_getBlockBy* result. The
// caller supplies raw uncle headers fetched from the same endpoint and block view.
// Receipts are attached separately through WithReceipts.
func DecodeBlock(raw json.RawMessage, rawUncles []json.RawMessage) (Bundle, error) {
	return decodeBlock(raw, rawUncles, false)
}

func decodeBlock(
	raw json.RawMessage,
	rawUncles []json.RawMessage,
	legacyStoredShape bool,
) (Bundle, error) {
	fields, err := decodeObject(raw, "block")
	if err != nil {
		return Bundle{}, err
	}
	header, err := DecodeHeader(raw)
	if err != nil {
		return Bundle{}, err
	}
	if header.Number == nil || !header.Number.IsUint64() {
		return Bundle{}, validation("block.number", "must be a uint64")
	}
	wireHash := header.Hash()
	transactionRaws, err := requiredArray(fields, "transactions", "block.transactions")
	if err != nil {
		return Bundle{}, err
	}
	transactions := make(types.Transactions, len(transactionRaws))
	seenTransactions := make(map[common.Hash]struct{}, len(transactionRaws))
	for index := range transactionRaws {
		transaction, _, decodeErr := decodeTransaction(
			transactionRaws[index],
			fmt.Sprintf("block.transactions[%d]", index),
			wireHash,
			header.Number.Uint64(),
			uint64(index),
		)
		if decodeErr != nil {
			return Bundle{}, decodeErr
		}
		if _, duplicate := seenTransactions[transaction.Hash()]; duplicate {
			return Bundle{}, validation(
				fmt.Sprintf("block.transactions[%d].hash", index),
				"duplicates another transaction in the block",
			)
		}
		seenTransactions[transaction.Hash()] = struct{}{}
		transactions[index] = transaction
	}
	uncleHashes, err := UncleHashes(raw)
	if err != nil {
		return Bundle{}, err
	}
	if len(uncleHashes) != len(rawUncles) {
		return Bundle{}, validation("block.uncles", fmt.Sprintf("header count %d does not match hash count %d", len(rawUncles), len(uncleHashes)))
	}
	uncles := make([]*types.Header, len(rawUncles))
	for index := range rawUncles {
		uncles[index], err = DecodeHeader(rawUncles[index])
		if err != nil {
			return Bundle{}, fmt.Errorf("decode block.uncles[%d]: %w", index, err)
		}
		if uncles[index].Hash() != uncleHashes[index] {
			return Bundle{}, validation(fmt.Sprintf("block.uncles[%d]", index), "header hash does not match block reference")
		}
	}
	withdrawals, withdrawalRaws, err := decodeWithdrawals(
		fields,
		header,
		legacyStoredShape,
	)
	if err != nil {
		return Bundle{}, err
	}
	if root := types.DeriveSha(transactions, trie.NewStackTrie(nil)); root != header.TxHash {
		return Bundle{}, validation("block.transactionsRoot", "does not match decoded transactions")
	}
	if uncleHash := types.CalcUncleHash(uncles); uncleHash != header.UncleHash {
		return Bundle{}, validation("block.sha3Uncles", "does not match decoded uncle headers")
	}
	block := types.NewBlockWithHeader(header).WithBody(types.Body{
		Transactions: transactions,
		Uncles:       uncles,
		Withdrawals:  withdrawals,
	})
	if rawUncles == nil {
		rawUncles = []json.RawMessage{}
	}
	return Bundle{
		Block:                  block,
		RawBlock:               cloneRaw(raw),
		RawTransactions:        cloneRawSlice(transactionRaws),
		RawUncles:              cloneRawSlice(rawUncles),
		RawWithdrawals:         cloneRawSlice(withdrawalRaws),
		legacyStoredBlockShape: legacyStoredShape,
	}, nil
}

// WithReceipts decodes receipt payloads and returns a new fully validated
// bundle. The receiver is left unchanged on every error.
func (b Bundle) WithReceipts(rawReceipts []json.RawMessage) (Bundle, error) {
	return b.withReceipts(rawReceipts, false)
}

// WithStoredReceipts decodes receipt rows written by either the current raw
// codec or the legacy typed codec. The legacy codec could omit
// contractAddress:null and effectiveGasPrice; both are authenticated and
// derived from the transaction and this bundle's block context. No such
// omission is accepted for a fresh RPC response.
func (b Bundle) WithStoredReceipts(rawReceipts []json.RawMessage) (Bundle, error) {
	return b.withReceipts(rawReceipts, true)
}

func (b Bundle) withReceipts(
	rawReceipts []json.RawMessage,
	legacyStoredShape bool,
) (Bundle, error) {
	if b.Block == nil {
		return Bundle{}, validation("block", "must not be nil")
	}
	transactions := b.Block.Transactions()
	if len(rawReceipts) != len(transactions) {
		return Bundle{}, validation("receipts", fmt.Sprintf("count %d does not match transaction count %d", len(rawReceipts), len(transactions)))
	}
	number := b.Block.NumberU64()
	hash := b.Block.Hash()
	receipts := make(types.Receipts, len(rawReceipts))
	rawLogs := make([][]json.RawMessage, len(rawReceipts))
	nextLogIndex := uint64(0)
	previousCumulativeGasUsed := uint64(0)
	options := receiptDecodeOptions{
		baseFee:                       b.Block.BaseFee(),
		effectiveGasPriceContextKnown: true,
		requireEffectiveGasPrice:      !legacyStoredShape,
		legacyStoredShape:             legacyStoredShape,
	}
	for index := range rawReceipts {
		receipt, logs, next, err := decodeReceipt(
			rawReceipts[index],
			fmt.Sprintf("receipts[%d]", index),
			transactions[index],
			hash,
			number,
			uint64(index),
			nextLogIndex,
			&previousCumulativeGasUsed,
			options,
		)
		if err != nil {
			return Bundle{}, err
		}
		receipts[index] = receipt
		rawLogs[index] = logs
		nextLogIndex = next
		previousCumulativeGasUsed = receipt.CumulativeGasUsed
	}
	if previousCumulativeGasUsed != b.Block.GasUsed() {
		return Bundle{}, validation(
			"block.gasUsed",
			"does not match the final receipt cumulativeGasUsed",
		)
	}
	if root := types.DeriveSha(receipts, trie.NewStackTrie(nil)); root != b.Block.ReceiptHash() {
		return Bundle{}, validation("block.receiptsRoot", "does not match decoded receipts")
	}
	if bloom := types.MergeBloom(receipts); bloom != b.Block.Bloom() {
		return Bundle{}, validation("block.logsBloom", "does not match decoded receipts")
	}
	result := b
	result.Receipts = receipts
	result.RawReceipts = cloneRawSlice(rawReceipts)
	result.RawLogs = cloneNestedRawSlice(rawLogs)
	result.legacyStoredBlockShape = b.legacyStoredBlockShape
	result.storedReceiptShape = legacyStoredShape
	return result, nil
}

// DecodeTransaction decodes one persisted or freshly fetched mined
// transaction, validates all inclusion fields, verifies its computed hash, and
// returns the recovered sender after checking the wire "from" value.
func DecodeTransaction(
	raw json.RawMessage,
	blockHash common.Hash,
	blockNumber uint64,
	index uint64,
) (*types.Transaction, common.Address, error) {
	return decodeTransaction(raw, "transaction", blockHash, blockNumber, index)
}

func decodeTransaction(
	raw json.RawMessage,
	path string,
	blockHash common.Hash,
	blockNumber uint64,
	index uint64,
) (*types.Transaction, common.Address, error) {
	fields, err := decodeObject(raw, path)
	if err != nil {
		return nil, common.Address{}, err
	}
	if err := validateFields(fields, path, transactionHashFields, transactionQuantityFields, transactionDataFields, transactionAddressFields); err != nil {
		return nil, common.Address{}, err
	}
	if _, exists := fields["to"]; !exists {
		return nil, common.Address{}, validation(path+".to", "must be present, using null for contract creation")
	}
	if typeRaw, exists := fields["type"]; exists && !isNull(typeRaw) {
		typeNumber, typeErr := parseQuantityJSON(typeRaw, path+".type")
		if typeErr != nil {
			return nil, common.Address{}, typeErr
		}
		if !typeNumber.IsUint64() || !supportedTransactionType(typeNumber.Uint64()) {
			return nil, common.Address{}, fmt.Errorf("%w at %s", ErrUnsupportedTransactionType, path)
		}
	}
	if err := validateTransactionQuantityBounds(fields, path); err != nil {
		return nil, common.Address{}, err
	}
	if err := validateTransactionCollections(fields, path); err != nil {
		return nil, common.Address{}, err
	}
	wireHash, err := requiredHash(fields, "hash", path+".hash")
	if err != nil {
		return nil, common.Address{}, err
	}
	wireFrom, err := requiredAddress(fields, "from", path+".from")
	if err != nil {
		return nil, common.Address{}, err
	}
	includedHash, err := requiredHash(fields, "blockHash", path+".blockHash")
	if err != nil {
		return nil, common.Address{}, err
	}
	includedNumber, err := requiredUint64(fields, "blockNumber", path+".blockNumber")
	if err != nil {
		return nil, common.Address{}, err
	}
	includedIndex, err := requiredUint64(fields, "transactionIndex", path+".transactionIndex")
	if err != nil {
		return nil, common.Address{}, err
	}
	if includedHash != blockHash {
		return nil, common.Address{}, validation(path+".blockHash", "does not match block hash")
	}
	if includedNumber != blockNumber {
		return nil, common.Address{}, validation(path+".blockNumber", "does not match block number")
	}
	if includedIndex != index {
		return nil, common.Address{}, validation(path+".transactionIndex", fmt.Sprintf("must equal %d", index))
	}
	var transaction types.Transaction
	if err := json.Unmarshal(raw, &transaction); err != nil {
		if errors.Is(err, types.ErrTxTypeNotSupported) {
			return nil, common.Address{}, fmt.Errorf("%w at %s", ErrUnsupportedTransactionType, path)
		}
		return nil, common.Address{}, fmt.Errorf("%w: decode %s: %v", ErrInvalidWireValue, path, err)
	}
	if !supportedTransactionType(uint64(transaction.Type())) {
		return nil, common.Address{}, fmt.Errorf("%w at %s", ErrUnsupportedTransactionType, path)
	}
	if computed := transaction.Hash(); computed != wireHash {
		return nil, common.Address{}, validation(path+".hash", "does not match the go-ethereum transaction hash")
	}
	sender, err := types.Sender(types.LatestSignerForChainID(transaction.ChainId()), &transaction)
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("%w: recover %s.from: %v", ErrInvalidWireValue, path, err)
	}
	if sender != wireFrom {
		return nil, common.Address{}, validation(path+".from", "does not match the go-ethereum recovered sender")
	}
	return &transaction, sender, nil
}

// DecodeReceipt decodes one fresh RPC receipt and validates its
// transaction/block/log inclusion and effective gas price. A nil baseFee is a
// known pre-London block context, not an unknown context.
func DecodeReceipt(
	raw json.RawMessage,
	transaction *types.Transaction,
	blockHash common.Hash,
	blockNumber uint64,
	transactionIndex uint64,
	nextLogIndex uint64,
	baseFee *big.Int,
) (*types.Receipt, []json.RawMessage, uint64, error) {
	if transaction == nil {
		return nil, nil, nextLogIndex, validation("receipt.transaction", "must not be nil")
	}
	return decodeSingleReceipt(
		raw,
		transaction,
		blockHash,
		blockNumber,
		transactionIndex,
		nextLogIndex,
		receiptDecodeOptions{
			baseFee:                       baseFee,
			effectiveGasPriceContextKnown: true,
			requireEffectiveGasPrice:      true,
		},
	)
}

// DecodeStoredReceipt accepts the one legacy persisted-row omission supported
// by WithStoredReceipts. Without an authenticated block context, legacy and
// access-list gas prices are verified from the transaction, while type 2-4
// EffectiveGasPrice is deliberately cleared instead of exposing an
// unauthenticated receipt field.
func DecodeStoredReceipt(
	raw json.RawMessage,
	transaction *types.Transaction,
	blockHash common.Hash,
	blockNumber uint64,
	transactionIndex uint64,
	nextLogIndex uint64,
) (*types.Receipt, []json.RawMessage, uint64, error) {
	if transaction == nil {
		return nil, nil, nextLogIndex, validation("receipt.transaction", "must not be nil")
	}
	return decodeSingleReceipt(
		raw,
		transaction,
		blockHash,
		blockNumber,
		transactionIndex,
		nextLogIndex,
		receiptDecodeOptions{legacyStoredShape: true},
	)
}

// DecodeStoredReceiptWithBlock authenticates a stored receipt's effective gas
// price against a validated go-ethereum block context. Legacy omission remains
// readable and is filled from the transaction and block.
func DecodeStoredReceiptWithBlock(
	raw json.RawMessage,
	transaction *types.Transaction,
	block *types.Block,
	transactionIndex uint64,
	nextLogIndex uint64,
) (*types.Receipt, []json.RawMessage, uint64, error) {
	if transaction == nil {
		return nil, nil, nextLogIndex, validation("receipt.transaction", "must not be nil")
	}
	if block == nil || block.Number() == nil || !block.Number().IsUint64() {
		return nil, nil, nextLogIndex, validation("receipt.block", "must have a uint64 block number")
	}
	transactions := block.Transactions()
	if transactionIndex >= uint64(len(transactions)) ||
		transactions[transactionIndex].Hash() != transaction.Hash() {
		return nil, nil, nextLogIndex, validation("receipt.transaction", "does not match the block transaction index")
	}
	return decodeSingleReceipt(
		raw,
		transaction,
		block.Hash(),
		block.NumberU64(),
		transactionIndex,
		nextLogIndex,
		receiptDecodeOptions{
			baseFee:                       block.BaseFee(),
			effectiveGasPriceContextKnown: true,
			legacyStoredShape:             true,
		},
	)
}

// DecodeStoredReceiptWithHeader authenticates a stored receipt's effective gas
// price against a validated header when the caller does not need the full
// block body.
func DecodeStoredReceiptWithHeader(
	raw json.RawMessage,
	transaction *types.Transaction,
	header *types.Header,
	transactionIndex uint64,
	nextLogIndex uint64,
) (*types.Receipt, []json.RawMessage, uint64, error) {
	if transaction == nil {
		return nil, nil, nextLogIndex, validation("receipt.transaction", "must not be nil")
	}
	if header == nil || header.Number == nil || !header.Number.IsUint64() {
		return nil, nil, nextLogIndex, validation("receipt.header", "must have a uint64 block number")
	}
	return decodeSingleReceipt(
		raw,
		transaction,
		header.Hash(),
		header.Number.Uint64(),
		transactionIndex,
		nextLogIndex,
		receiptDecodeOptions{
			baseFee:                       newBigOrNil(header.BaseFee),
			effectiveGasPriceContextKnown: true,
			legacyStoredShape:             true,
		},
	)
}

// DecodeStoredReceiptWithBaseFee authenticates a stored receipt's effective
// gas price from the exact normalized block identity and base-fee projection.
// It is the narrow read-side equivalent of DecodeStoredReceiptWithHeader for
// callers that do not need to transfer or reconstruct a complete header.
func DecodeStoredReceiptWithBaseFee(
	raw json.RawMessage,
	transaction *types.Transaction,
	blockHash common.Hash,
	blockNumber uint64,
	transactionIndex uint64,
	nextLogIndex uint64,
	baseFee *big.Int,
) (*types.Receipt, []json.RawMessage, uint64, error) {
	if transaction == nil {
		return nil, nil, nextLogIndex, validation("receipt.transaction", "must not be nil")
	}
	return decodeSingleReceipt(
		raw,
		transaction,
		blockHash,
		blockNumber,
		transactionIndex,
		nextLogIndex,
		receiptDecodeOptions{
			baseFee:                       newBigOrNil(baseFee),
			effectiveGasPriceContextKnown: true,
			legacyStoredShape:             true,
		},
	)
}

type receiptDecodeOptions struct {
	baseFee                       *big.Int
	effectiveGasPriceContextKnown bool
	requireEffectiveGasPrice      bool
	legacyStoredShape             bool
}

func newBigOrNil(value *big.Int) *big.Int {
	if value == nil {
		return nil
	}
	return new(big.Int).Set(value)
}

func decodeSingleReceipt(
	raw json.RawMessage,
	transaction *types.Transaction,
	blockHash common.Hash,
	blockNumber uint64,
	transactionIndex uint64,
	nextLogIndex uint64,
	options receiptDecodeOptions,
) (*types.Receipt, []json.RawMessage, uint64, error) {
	var previousCumulativeGasUsed *uint64
	if transactionIndex == 0 {
		zero := uint64(0)
		previousCumulativeGasUsed = &zero
	}
	return decodeReceipt(
		raw,
		"receipt",
		transaction,
		blockHash,
		blockNumber,
		transactionIndex,
		nextLogIndex,
		previousCumulativeGasUsed,
		options,
	)
}

func decodeReceipt(
	raw json.RawMessage,
	path string,
	transaction *types.Transaction,
	blockHash common.Hash,
	blockNumber uint64,
	transactionIndex uint64,
	nextLogIndex uint64,
	previousCumulativeGasUsed *uint64,
	options receiptDecodeOptions,
) (*types.Receipt, []json.RawMessage, uint64, error) {
	fields, err := decodeObject(raw, path)
	if err != nil {
		return nil, nil, nextLogIndex, err
	}
	if err := validateFields(fields, path, receiptHashFields, receiptQuantityFields, receiptDataFields, receiptAddressFields); err != nil {
		return nil, nil, nextLogIndex, err
	}
	if err := requirePresentFields(
		fields,
		path,
		"transactionHash",
		"transactionIndex",
		"blockHash",
		"blockNumber",
		"cumulativeGasUsed",
		"gasUsed",
		"logs",
		"logsBloom",
	); err != nil {
		return nil, nil, nextLogIndex, err
	}
	_, contractAddressPresent := fields["contractAddress"]
	if !contractAddressPresent && !options.legacyStoredShape {
		return nil, nil, nextLogIndex, validation(path+".contractAddress", "must be present, using null when no contract was created")
	}
	statusPresent := presentValue(fields, "status")
	rootPresent := presentValue(fields, "root")
	if !statusPresent && !rootPresent {
		return nil, nil, nextLogIndex, validation(path, "must contain either status or pre-Byzantium root")
	}
	if statusPresent {
		status, parseErr := parseQuantityJSON(fields["status"], path+".status")
		if parseErr != nil {
			return nil, nil, nextLogIndex, parseErr
		}
		if !status.IsUint64() || status.Uint64() > types.ReceiptStatusSuccessful {
			return nil, nil, nextLogIndex, validation(path+".status", "must be 0x0 or 0x1")
		}
	}
	if rootPresent {
		root, parseErr := parseDataJSON(fields["root"], path+".root")
		if parseErr != nil {
			return nil, nil, nextLogIndex, parseErr
		}
		if len(root) != 0 && len(root) != common.HashLength {
			return nil, nil, nextLogIndex, validation(path+".root", "must contain 32 bytes")
		}
		rootPresent = len(root) != 0
		if !statusPresent && !rootPresent {
			return nil, nil, nextLogIndex, validation(path, "must contain either status or pre-Byzantium root")
		}
	}
	if typeRaw, exists := fields["type"]; exists && !isNull(typeRaw) {
		typeNumber, typeErr := parseQuantityJSON(typeRaw, path+".type")
		if typeErr != nil {
			return nil, nil, nextLogIndex, typeErr
		}
		if !typeNumber.IsUint64() || !supportedTransactionType(typeNumber.Uint64()) {
			return nil, nil, nextLogIndex, fmt.Errorf("%w at %s", ErrUnsupportedTransactionType, path)
		}
	}
	wireHash, err := requiredHash(fields, "transactionHash", path+".transactionHash")
	if err != nil {
		return nil, nil, nextLogIndex, err
	}
	includedHash, err := requiredHash(fields, "blockHash", path+".blockHash")
	if err != nil {
		return nil, nil, nextLogIndex, err
	}
	includedNumber, err := requiredUint64(fields, "blockNumber", path+".blockNumber")
	if err != nil {
		return nil, nil, nextLogIndex, err
	}
	includedIndex, err := requiredUint64(fields, "transactionIndex", path+".transactionIndex")
	if err != nil {
		return nil, nil, nextLogIndex, err
	}
	logRaws, err := requiredArray(fields, "logs", path+".logs")
	if err != nil {
		return nil, nil, nextLogIndex, err
	}
	var receipt types.Receipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return nil, nil, nextLogIndex, fmt.Errorf("%w: decode %s: %v", ErrInvalidWireValue, path, err)
	}
	if wireHash != transaction.Hash() || receipt.TxHash != transaction.Hash() {
		return nil, nil, nextLogIndex, validation(path+".transactionHash", "does not match transaction hash")
	}
	if includedHash != blockHash || receipt.BlockHash != blockHash {
		return nil, nil, nextLogIndex, validation(path+".blockHash", "does not match block hash")
	}
	if includedNumber != blockNumber || receipt.BlockNumber == nil || !receipt.BlockNumber.IsUint64() || receipt.BlockNumber.Uint64() != blockNumber {
		return nil, nil, nextLogIndex, validation(path+".blockNumber", "does not match block number")
	}
	if includedIndex != transactionIndex || uint64(receipt.TransactionIndex) != transactionIndex {
		return nil, nil, nextLogIndex, validation(path+".transactionIndex", fmt.Sprintf("must equal %d", transactionIndex))
	}
	if receipt.Type != transaction.Type() {
		return nil, nil, nextLogIndex, validation(path+".type", "does not match transaction type")
	}
	if err := validateReceiptEffectiveGasPrice(
		path,
		fields,
		transaction,
		&receipt,
		options,
	); err != nil {
		return nil, nil, nextLogIndex, err
	}
	if err := validateReceiptContractAddress(
		path,
		transaction,
		&receipt,
		contractAddressPresent,
		statusPresent,
		options.legacyStoredShape,
	); err != nil {
		return nil, nil, nextLogIndex, err
	}
	if receipt.GasUsed > receipt.CumulativeGasUsed {
		return nil, nil, nextLogIndex, validation(
			path+".gasUsed",
			"exceeds cumulativeGasUsed",
		)
	}
	if previousCumulativeGasUsed != nil {
		if receipt.CumulativeGasUsed < *previousCumulativeGasUsed {
			return nil, nil, nextLogIndex, validation(
				path+".cumulativeGasUsed",
				"is less than the previous receipt",
			)
		}
		expectedGasUsed := receipt.CumulativeGasUsed - *previousCumulativeGasUsed
		if receipt.GasUsed != expectedGasUsed {
			return nil, nil, nextLogIndex, validation(
				path+".gasUsed",
				"does not match the cumulativeGasUsed delta",
			)
		}
	}
	if len(receipt.Logs) != len(logRaws) {
		return nil, nil, nextLogIndex, validation(path+".logs", "typed log count does not match raw log count")
	}
	for logPosition := range logRaws {
		next, validateErr := validateLog(
			logRaws[logPosition],
			fmt.Sprintf("%s.logs[%d]", path, logPosition),
			receipt.Logs[logPosition],
			transaction.Hash(),
			blockHash,
			blockNumber,
			transactionIndex,
			nextLogIndex,
		)
		if validateErr != nil {
			return nil, nil, nextLogIndex, validateErr
		}
		nextLogIndex = next
	}
	return &receipt, cloneRawSlice(logRaws), nextLogIndex, nil
}

func validateReceiptEffectiveGasPrice(
	path string,
	fields map[string]json.RawMessage,
	transaction *types.Transaction,
	receipt *types.Receipt,
	options receiptDecodeOptions,
) error {
	raw, present := fields["effectiveGasPrice"]
	present = present && !isNull(raw)
	if options.requireEffectiveGasPrice && !present {
		return validation(path+".effectiveGasPrice", "must not be missing or null")
	}

	canAuthenticate := options.effectiveGasPriceContextKnown ||
		transaction.Type() == types.LegacyTxType ||
		transaction.Type() == types.AccessListTxType
	if !canAuthenticate {
		receipt.EffectiveGasPrice = nil
		return nil
	}
	expected, err := TransactionEffectiveGasPrice(
		transaction,
		options.baseFee,
	)
	if err != nil {
		return validation(path+".effectiveGasPrice", err.Error())
	}
	if present {
		wire, err := parseQuantityJSON(raw, path+".effectiveGasPrice")
		if err != nil {
			return err
		}
		if wire.Cmp(expected) != 0 ||
			receipt.EffectiveGasPrice == nil ||
			receipt.EffectiveGasPrice.Cmp(expected) != 0 {
			return validation(
				path+".effectiveGasPrice",
				"does not match the transaction and block base fee",
			)
		}
	}
	receipt.EffectiveGasPrice = expected
	return nil
}

// TransactionEffectiveGasPrice derives the receipt effectiveGasPrice without
// mutating the transaction or base fee. A nil base fee follows go-ethereum's
// pre-London receipt derivation and returns the fee cap for type 2-4.
func TransactionEffectiveGasPrice(
	transaction *types.Transaction,
	baseFee *big.Int,
) (*big.Int, error) {
	if transaction == nil {
		return nil, errors.New("transaction is nil")
	}
	switch transaction.Type() {
	case types.LegacyTxType, types.AccessListTxType:
		return transaction.GasPrice(), nil
	case types.DynamicFeeTxType, types.BlobTxType, types.SetCodeTxType:
		if baseFee == nil {
			return transaction.GasFeeCap(), nil
		}
		if baseFee.Sign() < 0 || baseFee.BitLen() > 256 {
			return nil, errors.New("block base fee is not an unsigned 256-bit integer")
		}
		return new(big.Int).Add(
			new(big.Int).Set(baseFee),
			transaction.EffectiveGasTipValue(baseFee),
		), nil
	default:
		return nil, fmt.Errorf(
			"unsupported transaction type %d",
			transaction.Type(),
		)
	}
}

func validateReceiptContractAddress(
	path string,
	transaction *types.Transaction,
	receipt *types.Receipt,
	contractAddressPresent bool,
	statusPresent bool,
	legacyStoredShape bool,
) error {
	zero := common.Address{}
	if transaction.To() != nil {
		if receipt.ContractAddress != zero {
			return validation(
				path+".contractAddress",
				"must be null or zero for a non-creation transaction",
			)
		}
		return nil
	}
	sender, err := types.Sender(
		types.LatestSignerForChainID(transaction.ChainId()),
		transaction,
	)
	if err != nil {
		return fmt.Errorf(
			"%w: recover %s contract creator: %v",
			ErrInvalidWireValue,
			path,
			err,
		)
	}
	expected := crypto.CreateAddress(sender, transaction.Nonce())
	if statusPresent {
		if receipt.Status == types.ReceiptStatusSuccessful {
			if receipt.ContractAddress != expected {
				return validation(
					path+".contractAddress",
					"does not match the top-level CREATE address",
				)
			}
		} else if receipt.ContractAddress != zero {
			return validation(
				path+".contractAddress",
				"must be null or zero for a failed creation",
			)
		}
	} else if receipt.ContractAddress != zero &&
		receipt.ContractAddress != expected {
		return validation(
			path+".contractAddress",
			"does not match the possible pre-Byzantium CREATE address",
		)
	}
	if !contractAddressPresent &&
		!legacyStoredShape {
		return validation(
			path+".contractAddress",
			"must be present, using null when no contract was created",
		)
	}
	return nil
}

// DecodeLog decodes and validates a single stored log without defining a
// second log wire model.
func DecodeLog(
	raw json.RawMessage,
	transactionHash common.Hash,
	blockHash common.Hash,
	blockNumber uint64,
	transactionIndex uint64,
	logIndex uint64,
) (*types.Log, error) {
	var log types.Log
	if err := json.Unmarshal(raw, &log); err != nil {
		return nil, fmt.Errorf("%w: decode log: %v", ErrInvalidWireValue, err)
	}
	if _, err := validateLog(
		raw,
		"log",
		&log,
		transactionHash,
		blockHash,
		blockNumber,
		transactionIndex,
		logIndex,
	); err != nil {
		return nil, err
	}
	return &log, nil
}

func validateLog(
	raw json.RawMessage,
	path string,
	log *types.Log,
	transactionHash common.Hash,
	blockHash common.Hash,
	blockNumber uint64,
	transactionIndex uint64,
	expectedLogIndex uint64,
) (uint64, error) {
	if log == nil {
		return expectedLogIndex, validation(path, "typed log is nil")
	}
	fields, err := decodeObject(raw, path)
	if err != nil {
		return expectedLogIndex, err
	}
	if err := validateFields(fields, path, logHashFields, logQuantityFields, logDataFields, logAddressFields); err != nil {
		return expectedLogIndex, err
	}
	if err := requirePresentFields(
		fields,
		path,
		"removed",
		"logIndex",
		"transactionIndex",
		"transactionHash",
		"blockHash",
		"blockNumber",
		"address",
		"data",
		"topics",
	); err != nil {
		return expectedLogIndex, err
	}
	topics, err := requiredArray(fields, "topics", path+".topics")
	if err != nil {
		return expectedLogIndex, err
	}
	for index := range topics {
		if _, err := parseHashJSON(topics[index], fmt.Sprintf("%s.topics[%d]", path, index)); err != nil {
			return expectedLogIndex, err
		}
	}
	wireTxHash, err := requiredHash(fields, "transactionHash", path+".transactionHash")
	if err != nil {
		return expectedLogIndex, err
	}
	wireBlockHash, err := requiredHash(fields, "blockHash", path+".blockHash")
	if err != nil {
		return expectedLogIndex, err
	}
	wireBlockNumber, err := requiredUint64(fields, "blockNumber", path+".blockNumber")
	if err != nil {
		return expectedLogIndex, err
	}
	wireTxIndex, err := requiredUint64(fields, "transactionIndex", path+".transactionIndex")
	if err != nil {
		return expectedLogIndex, err
	}
	wireLogIndex, err := requiredUint64(fields, "logIndex", path+".logIndex")
	if err != nil {
		return expectedLogIndex, err
	}
	if log.Removed {
		return expectedLogIndex, validation(path+".removed", "must be false for a block receipt")
	}
	if wireTxHash != transactionHash || log.TxHash != transactionHash {
		return expectedLogIndex, validation(path+".transactionHash", "does not match transaction hash")
	}
	if wireBlockHash != blockHash || log.BlockHash != blockHash {
		return expectedLogIndex, validation(path+".blockHash", "does not match block hash")
	}
	if wireBlockNumber != blockNumber || log.BlockNumber != blockNumber {
		return expectedLogIndex, validation(path+".blockNumber", "does not match block number")
	}
	if wireTxIndex != transactionIndex || uint64(log.TxIndex) != transactionIndex {
		return expectedLogIndex, validation(path+".transactionIndex", "does not match transaction index")
	}
	if wireLogIndex != expectedLogIndex || uint64(log.Index) != expectedLogIndex {
		return expectedLogIndex, validation(path+".logIndex", fmt.Sprintf("must equal %d", expectedLogIndex))
	}
	return expectedLogIndex + 1, nil
}

func decodeWithdrawals(
	fields map[string]json.RawMessage,
	header *types.Header,
	legacyStoredShape bool,
) ([]*types.Withdrawal, []json.RawMessage, error) {
	raw, exists := fields["withdrawals"]
	if !exists || isNull(raw) {
		if header.WithdrawalsHash != nil {
			if legacyStoredShape &&
				*header.WithdrawalsHash == types.EmptyWithdrawalsHash {
				return []*types.Withdrawal{}, nil, nil
			}
			return nil, nil, validation("block.withdrawals", "is absent while withdrawalsRoot is present")
		}
		return nil, nil, nil
	}
	if header.WithdrawalsHash == nil {
		return nil, nil, validation("block.withdrawalsRoot", "is absent while withdrawals are present")
	}
	items, err := decodeArray(raw, "block.withdrawals")
	if err != nil {
		return nil, nil, err
	}
	withdrawals := make([]*types.Withdrawal, len(items))
	var previous uint64
	for index := range items {
		path := fmt.Sprintf("block.withdrawals[%d]", index)
		object, objectErr := decodeObject(items[index], path)
		if objectErr != nil {
			return nil, nil, objectErr
		}
		if objectErr = validateFields(object, path, nil, withdrawalQuantityFields, nil, withdrawalAddressFields); objectErr != nil {
			return nil, nil, objectErr
		}
		var withdrawal types.Withdrawal
		if objectErr = json.Unmarshal(items[index], &withdrawal); objectErr != nil {
			return nil, nil, fmt.Errorf("%w: decode %s: %v", ErrInvalidWireValue, path, objectErr)
		}
		if index > 0 && withdrawal.Index != previous+1 {
			return nil, nil, validation(path+".index", "must immediately follow the previous withdrawal index")
		}
		previous = withdrawal.Index
		withdrawals[index] = &withdrawal
	}
	if root := types.DeriveSha(types.Withdrawals(withdrawals), trie.NewStackTrie(nil)); root != *header.WithdrawalsHash {
		return nil, nil, validation("block.withdrawalsRoot", "does not match decoded withdrawals")
	}
	return withdrawals, cloneRawSlice(items), nil
}

func validateTransactionCollections(fields map[string]json.RawMessage, path string) error {
	if raw, exists := fields["blobVersionedHashes"]; exists && !isNull(raw) {
		items, err := decodeArray(raw, path+".blobVersionedHashes")
		if err != nil {
			return err
		}
		for index := range items {
			if _, err := parseHashJSON(items[index], fmt.Sprintf("%s.blobVersionedHashes[%d]", path, index)); err != nil {
				return err
			}
		}
	}
	if raw, exists := fields["accessList"]; exists && !isNull(raw) {
		items, err := decodeArray(raw, path+".accessList")
		if err != nil {
			return err
		}
		for index := range items {
			entryPath := fmt.Sprintf("%s.accessList[%d]", path, index)
			entry, err := decodeObject(items[index], entryPath)
			if err != nil {
				return err
			}
			if value, exists := entry["address"]; exists && !isNull(value) {
				if _, err := parseAddressJSON(value, entryPath+".address"); err != nil {
					return err
				}
			}
			if value, exists := entry["storageKeys"]; exists && !isNull(value) {
				keys, err := decodeArray(value, entryPath+".storageKeys")
				if err != nil {
					return err
				}
				for keyIndex := range keys {
					if _, err := parseHashJSON(keys[keyIndex], fmt.Sprintf("%s.storageKeys[%d]", entryPath, keyIndex)); err != nil {
						return err
					}
				}
			}
		}
	}
	if raw, exists := fields["authorizationList"]; exists && !isNull(raw) {
		items, err := decodeArray(raw, path+".authorizationList")
		if err != nil {
			return err
		}
		for index := range items {
			entryPath := fmt.Sprintf("%s.authorizationList[%d]", path, index)
			entry, err := decodeObject(items[index], entryPath)
			if err != nil {
				return err
			}
			if err := validateFields(
				entry,
				entryPath,
				nil,
				[]string{"chainId", "nonce", "yParity", "v", "r", "s"},
				nil,
				[]string{"address"},
			); err != nil {
				return err
			}
			if err := validateAuthorizationQuantityBounds(entry, entryPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateTransactionQuantityBounds(
	fields map[string]json.RawMessage,
	path string,
) error {
	if err := validateUint64Fields(fields, path, "nonce", "gas"); err != nil {
		return err
	}
	return validateUint256Fields(
		fields,
		path,
		"chainId",
		"gasPrice",
		"maxPriorityFeePerGas",
		"maxFeePerGas",
		"maxFeePerBlobGas",
		"value",
		"v",
		"r",
		"s",
	)
}

func validateAuthorizationQuantityBounds(
	fields map[string]json.RawMessage,
	path string,
) error {
	if err := validateUint64Fields(fields, path, "nonce", "v"); err != nil {
		return err
	}
	if err := validateUint8Fields(fields, path, "yParity"); err != nil {
		return err
	}
	return validateUint256Fields(fields, path, "chainId", "r", "s")
}

func validateUint64Fields(
	fields map[string]json.RawMessage,
	path string,
	names ...string,
) error {
	for _, name := range names {
		raw, exists := fields[name]
		if !exists || isNull(raw) {
			continue
		}
		value, err := parseQuantityJSON(raw, path+"."+name)
		if err != nil {
			return err
		}
		if !value.IsUint64() {
			return validation(path+"."+name, "exceeds uint64")
		}
	}
	return nil
}

func validateUint8Fields(
	fields map[string]json.RawMessage,
	path string,
	names ...string,
) error {
	for _, name := range names {
		raw, exists := fields[name]
		if !exists || isNull(raw) {
			continue
		}
		value, err := parseQuantityJSON(raw, path+"."+name)
		if err != nil {
			return err
		}
		if value.BitLen() > 8 {
			return validation(path+"."+name, "exceeds uint8")
		}
	}
	return nil
}

func validateUint256Fields(
	fields map[string]json.RawMessage,
	path string,
	names ...string,
) error {
	for _, name := range names {
		raw, exists := fields[name]
		if !exists || isNull(raw) {
			continue
		}
		value, err := parseQuantityJSON(raw, path+"."+name)
		if err != nil {
			return err
		}
		if value.BitLen() > 256 {
			return validation(path+"."+name, "exceeds uint256")
		}
	}
	return nil
}

func supportedTransactionType(value uint64) bool {
	switch value {
	case types.LegacyTxType, types.AccessListTxType, types.DynamicFeeTxType, types.BlobTxType, types.SetCodeTxType:
		return true
	default:
		return false
	}
}

func validateFields(
	fields map[string]json.RawMessage,
	path string,
	hashFields, quantityFields, dataFields, addressFields []string,
) error {
	for _, name := range hashFields {
		if raw, exists := fields[name]; exists && !isNull(raw) {
			if _, err := parseHashJSON(raw, path+"."+name); err != nil {
				return err
			}
		}
	}
	for _, name := range quantityFields {
		if raw, exists := fields[name]; exists && !isNull(raw) {
			if _, err := parseQuantityJSON(raw, path+"."+name); err != nil {
				return err
			}
		}
	}
	for _, name := range dataFields {
		if raw, exists := fields[name]; exists && !isNull(raw) {
			if _, err := parseDataJSON(raw, path+"."+name); err != nil {
				return err
			}
		}
	}
	for _, name := range addressFields {
		if raw, exists := fields[name]; exists && !isNull(raw) {
			if _, err := parseAddressJSON(raw, path+"."+name); err != nil {
				return err
			}
		}
	}
	return nil
}

func requirePresentFields(fields map[string]json.RawMessage, path string, names ...string) error {
	for _, name := range names {
		raw, exists := fields[name]
		if !exists || isNull(raw) {
			return validation(path+"."+name, "must not be missing or null")
		}
	}
	return nil
}

func requiredHash(fields map[string]json.RawMessage, name, path string) (common.Hash, error) {
	raw, exists := fields[name]
	if !exists || isNull(raw) {
		return common.Hash{}, validation(path, "must not be missing or null")
	}
	return parseHashJSON(raw, path)
}

func requiredAddress(fields map[string]json.RawMessage, name, path string) (common.Address, error) {
	raw, exists := fields[name]
	if !exists || isNull(raw) {
		return common.Address{}, validation(path, "must not be missing or null")
	}
	return parseAddressJSON(raw, path)
}

func requiredUint64(fields map[string]json.RawMessage, name, path string) (uint64, error) {
	raw, exists := fields[name]
	if !exists || isNull(raw) {
		return 0, validation(path, "must not be missing or null")
	}
	value, err := parseQuantityJSON(raw, path)
	if err != nil {
		return 0, err
	}
	if !value.IsUint64() {
		return 0, validation(path, "exceeds uint64")
	}
	return value.Uint64(), nil
}

func requiredArray(fields map[string]json.RawMessage, name, path string) ([]json.RawMessage, error) {
	raw, exists := fields[name]
	if !exists || isNull(raw) {
		return nil, validation(path, "must not be missing or null")
	}
	return decodeArray(raw, path)
}

func decodeObject(raw json.RawMessage, path string) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 || isNull(raw) {
		return nil, validation(path, "must not be empty or null")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("%w: %s must be an object", ErrInvalidWireValue, path)
	}
	return fields, nil
}

func decodeArray(raw json.RawMessage, path string) ([]json.RawMessage, error) {
	if isNull(raw) {
		return nil, validation(path, "must not be null")
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return nil, fmt.Errorf("%w: %s must be an array", ErrInvalidWireValue, path)
	}
	return values, nil
}

func parseQuantityJSON(raw json.RawMessage, path string) (*big.Int, error) {
	value, err := decodeJSONString(raw, path)
	if err != nil {
		return nil, err
	}
	if len(value) < 3 || !strings.HasPrefix(value, "0x") {
		return nil, fmt.Errorf("%w: %s is not a canonical quantity", ErrInvalidWireValue, path)
	}
	digits := value[2:]
	if len(digits) > 1 && digits[0] == '0' {
		return nil, fmt.Errorf("%w: %s has a leading zero", ErrInvalidWireValue, path)
	}
	for _, digit := range digits {
		if !isLowerHex(digit) {
			return nil, fmt.Errorf("%w: %s contains a non-lowercase-hex digit", ErrInvalidWireValue, path)
		}
	}
	if len(digits) > 64 {
		return nil, validation(path, "exceeds uint256")
	}
	decoded, err := hexutil.DecodeBig(value)
	if err != nil || hexutil.EncodeBig(decoded) != value {
		return nil, fmt.Errorf("%w: %s is not a canonical quantity", ErrInvalidWireValue, path)
	}
	return decoded, nil
}

func parseDataJSON(raw json.RawMessage, path string) ([]byte, error) {
	value, err := decodeJSONString(raw, path)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(value, "0x") || (len(value)-2)%2 != 0 {
		return nil, fmt.Errorf("%w: %s is not canonical data", ErrInvalidWireValue, path)
	}
	for _, digit := range value[2:] {
		if !isHex(digit) {
			return nil, fmt.Errorf("%w: %s contains a non-hex digit", ErrInvalidWireValue, path)
		}
	}
	decoded, err := hexutil.Decode(value)
	if err != nil {
		return nil, fmt.Errorf("%w: %s is not canonical data", ErrInvalidWireValue, path)
	}
	return decoded, nil
}

func parseHashJSON(raw json.RawMessage, path string) (common.Hash, error) {
	data, err := parseDataJSON(raw, path)
	if err != nil {
		return common.Hash{}, err
	}
	if len(data) != common.HashLength {
		return common.Hash{}, validation(path, fmt.Sprintf("expected 32 bytes, got %d", len(data)))
	}
	var hash common.Hash
	copy(hash[:], data)
	return hash, nil
}

func parseAddressJSON(raw json.RawMessage, path string) (common.Address, error) {
	data, err := parseDataJSON(raw, path)
	if err != nil {
		return common.Address{}, err
	}
	if len(data) != common.AddressLength {
		return common.Address{}, validation(path, fmt.Sprintf("expected 20 bytes, got %d", len(data)))
	}
	var address common.Address
	copy(address[:], data)
	return address, nil
}

func decodeJSONString(raw json.RawMessage, path string) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%w: %s must be a string", ErrInvalidWireValue, path)
	}
	return value, nil
}

func presentValue(fields map[string]json.RawMessage, name string) bool {
	raw, exists := fields[name]
	return exists && !isNull(raw)
}

func isNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func cloneRawSlice(values []json.RawMessage) []json.RawMessage {
	if values == nil {
		return nil
	}
	result := make([]json.RawMessage, len(values))
	for index := range values {
		result[index] = cloneRaw(values[index])
	}
	return result
}

func cloneNestedRawSlice(values [][]json.RawMessage) [][]json.RawMessage {
	if values == nil {
		return nil
	}
	result := make([][]json.RawMessage, len(values))
	for index := range values {
		result[index] = cloneRawSlice(values[index])
	}
	return result
}

func isLowerHex(value rune) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f'
}

func isHex(value rune) bool {
	return isLowerHex(value) || value >= 'A' && value <= 'F'
}
