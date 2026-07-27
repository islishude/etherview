// Package testfixture builds internally consistent raw-first bundles for tests
// outside package chainbundle. It never permits callers to assign a synthetic
// block or transaction hash; all identities are computed by go-ethereum.
package testfixture

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/holiman/uint256"
	"github.com/islishude/etherview/internal/chainbundle"
)

type Options struct {
	Number             uint64
	ParentHash         common.Hash
	Timestamp          uint64
	ExtraData          []byte
	TransactionTypes   []uint8
	ContractCreations  []bool
	FailedTransactions []bool
	LogsPerTransaction int
	Withdrawals        []*types.Withdrawal
	BaseFee            *big.Int
	BlobGasUsed        *uint64
	ExcessBlobGas      *uint64
	Uncles             []*types.Header
}

// New returns a bundle that has passed the same production raw decoder used by
// RPC ingestion. Distinct branches can be produced by changing ExtraData.
func New(options Options) (chainbundle.Bundle, error) {
	if options.LogsPerTransaction < 0 {
		return chainbundle.Bundle{}, errors.New("logs per transaction must not be negative")
	}
	if len(options.ContractCreations) != 0 &&
		len(options.ContractCreations) != len(options.TransactionTypes) {
		return chainbundle.Bundle{}, errors.New(
			"contract creation flags must align with transaction types",
		)
	}
	if len(options.FailedTransactions) != 0 &&
		len(options.FailedTransactions) != len(options.TransactionTypes) {
		return chainbundle.Bundle{}, errors.New(
			"failed transaction flags must align with transaction types",
		)
	}
	if options.Timestamp == 0 {
		options.Timestamp = 1_700_000_000 + options.Number
	}
	key, err := crypto.HexToECDSA("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		return chainbundle.Bundle{}, err
	}
	chainID := big.NewInt(1)
	signer := types.LatestSignerForChainID(chainID)
	sender := crypto.PubkeyToAddress(key.PublicKey)
	destination := common.Address{19: 0x02}
	transactions := make(types.Transactions, len(options.TransactionTypes))
	receipts := make(types.Receipts, len(options.TransactionTypes))
	nextLogIndex := uint(0)
	var cumulativeGas uint64
	for index, transactionType := range options.TransactionTypes {
		transactionTo := &destination
		if len(options.ContractCreations) != 0 &&
			options.ContractCreations[index] {
			transactionTo = nil
		}
		unsigned, err := newTransaction(
			transactionType,
			chainID,
			uint64(index),
			transactionTo,
		)
		if err != nil {
			return chainbundle.Bundle{}, err
		}
		transaction, err := types.SignTx(unsigned, signer, key)
		if err != nil {
			return chainbundle.Bundle{}, fmt.Errorf("sign fixture transaction %d: %w", index, err)
		}
		transactions[index] = transaction
		cumulativeGas += 21_000
		logs := make([]*types.Log, options.LogsPerTransaction)
		for logIndex := range logs {
			logs[logIndex] = &types.Log{
				Address: common.Address{18: byte(index), 19: byte(logIndex + 1)},
				Topics:  []common.Hash{{31: byte(logIndex + 1)}},
				Data:    []byte{byte(index), byte(logIndex)},
				TxHash:  transaction.Hash(),
				TxIndex: uint(index),
				Index:   nextLogIndex,
			}
			nextLogIndex++
		}
		status := types.ReceiptStatusSuccessful
		if len(options.FailedTransactions) != 0 &&
			options.FailedTransactions[index] {
			status = types.ReceiptStatusFailed
		}
		receipt := &types.Receipt{
			Type:              transaction.Type(),
			Status:            status,
			CumulativeGasUsed: cumulativeGas,
			Logs:              logs,
			TxHash:            transaction.Hash(),
			GasUsed:           21_000,
			TransactionIndex:  uint(index),
		}
		receipt.EffectiveGasPrice, err = chainbundle.TransactionEffectiveGasPrice(
			transaction,
			options.BaseFee,
		)
		if err != nil {
			return chainbundle.Bundle{}, fmt.Errorf(
				"derive fixture transaction %d effective gas price: %w",
				index,
				err,
			)
		}
		if transaction.To() == nil &&
			status == types.ReceiptStatusSuccessful {
			receipt.ContractAddress = crypto.CreateAddress(
				sender,
				transaction.Nonce(),
			)
		}
		receipt.Bloom = types.CreateBloom(receipt)
		receipts[index] = receipt
	}
	uncles, err := cloneHeaders(options.Uncles)
	if err != nil {
		return chainbundle.Bundle{}, err
	}
	header := &types.Header{
		ParentHash:    options.ParentHash,
		UncleHash:     types.CalcUncleHash(uncles),
		Coinbase:      common.Address{19: 0x03},
		Root:          common.Hash{31: 0x04},
		TxHash:        types.DeriveSha(transactions, trie.NewStackTrie(nil)),
		ReceiptHash:   types.DeriveSha(receipts, trie.NewStackTrie(nil)),
		Bloom:         types.MergeBloom(receipts),
		Difficulty:    big.NewInt(0),
		Number:        new(big.Int).SetUint64(options.Number),
		GasLimit:      30_000_000,
		GasUsed:       cumulativeGas,
		Time:          options.Timestamp,
		Extra:         common.CopyBytes(options.ExtraData),
		BaseFee:       cloneBig(options.BaseFee),
		BlobGasUsed:   cloneUint64(options.BlobGasUsed),
		ExcessBlobGas: cloneUint64(options.ExcessBlobGas),
	}
	withdrawals := cloneWithdrawals(options.Withdrawals)
	if options.Withdrawals != nil {
		root := types.DeriveSha(types.Withdrawals(withdrawals), trie.NewStackTrie(nil))
		header.WithdrawalsHash = &root
	}
	blockHash := header.Hash()
	rawTransactions := make([]json.RawMessage, len(transactions))
	rawReceipts := make([]json.RawMessage, len(receipts))
	for index := range transactions {
		rawTransactions[index], err = transactionJSON(
			transactions[index],
			sender,
			blockHash,
			options.Number,
			uint64(index),
		)
		if err != nil {
			return chainbundle.Bundle{}, err
		}
		for _, log := range receipts[index].Logs {
			log.BlockHash = blockHash
			log.BlockNumber = options.Number
		}
		receipts[index].BlockHash = blockHash
		receipts[index].BlockNumber = new(big.Int).SetUint64(options.Number)
		rawReceipts[index], err = receiptJSON(receipts[index], sender, transactions[index].To())
		if err != nil {
			return chainbundle.Bundle{}, err
		}
	}
	rawUncles := make([]json.RawMessage, len(uncles))
	for index := range uncles {
		rawUncles[index], err = json.Marshal(uncles[index])
		if err != nil {
			return chainbundle.Bundle{}, fmt.Errorf("encode fixture uncle %d: %w", index, err)
		}
	}
	rawBlock, err := blockJSON(header, rawTransactions, uncles, withdrawals)
	if err != nil {
		return chainbundle.Bundle{}, err
	}
	bundle, err := chainbundle.DecodeBlock(rawBlock, rawUncles)
	if err != nil {
		return chainbundle.Bundle{}, err
	}
	return bundle.WithReceipts(rawReceipts)
}

func cloneUint64(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneBig(value *big.Int) *big.Int {
	if value == nil {
		return nil
	}
	return new(big.Int).Set(value)
}

func newTransaction(
	transactionType uint8,
	chainID *big.Int,
	nonce uint64,
	to *common.Address,
) (*types.Transaction, error) {
	switch transactionType {
	case types.LegacyTxType:
		return types.NewTx(&types.LegacyTx{
			Nonce: nonce, GasPrice: big.NewInt(1), Gas: 21_000, To: to,
			Value: big.NewInt(int64(nonce + 1)), Data: []byte{byte(nonce)},
		}), nil
	case types.AccessListTxType:
		return types.NewTx(&types.AccessListTx{
			ChainID: chainID, Nonce: nonce, GasPrice: big.NewInt(1), Gas: 21_000,
			To: to, Value: big.NewInt(int64(nonce + 1)), Data: []byte{byte(nonce)},
			AccessList: types.AccessList{},
		}), nil
	case types.DynamicFeeTxType:
		return types.NewTx(&types.DynamicFeeTx{
			ChainID: chainID, Nonce: nonce, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2),
			Gas: 21_000, To: to, Value: big.NewInt(int64(nonce + 1)),
			Data: []byte{byte(nonce)}, AccessList: types.AccessList{},
		}), nil
	case types.BlobTxType:
		if to == nil {
			return nil, errors.New("blob transaction cannot create a contract")
		}
		return types.NewTx(&types.BlobTx{
			ChainID: uint256.MustFromBig(chainID), Nonce: nonce,
			GasTipCap: uint256.NewInt(1), GasFeeCap: uint256.NewInt(2), Gas: 21_000,
			To: *to, Value: uint256.NewInt(nonce + 1), Data: []byte{byte(nonce)},
			AccessList: types.AccessList{}, BlobFeeCap: uint256.NewInt(3),
			BlobHashes: []common.Hash{{0: 0x01, 31: byte(nonce + 1)}},
		}), nil
	case types.SetCodeTxType:
		if to == nil {
			return nil, errors.New("set-code transaction cannot create a contract")
		}
		return types.NewTx(&types.SetCodeTx{
			ChainID: uint256.MustFromBig(chainID), Nonce: nonce,
			GasTipCap: uint256.NewInt(1), GasFeeCap: uint256.NewInt(2), Gas: 21_000,
			To: *to, Value: uint256.NewInt(nonce + 1), Data: []byte{byte(nonce)},
			AccessList: types.AccessList{}, AuthList: []types.SetCodeAuthorization{},
		}), nil
	default:
		return nil, fmt.Errorf("%w: %d", chainbundle.ErrUnsupportedTransactionType, transactionType)
	}
}

func transactionJSON(
	transaction *types.Transaction,
	from common.Address,
	blockHash common.Hash,
	blockNumber uint64,
	index uint64,
) (json.RawMessage, error) {
	data, err := json.Marshal(transaction)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	setJSON(fields, "from", from)
	setJSON(fields, "blockHash", blockHash)
	setJSON(fields, "blockNumber", fmt.Sprintf("0x%x", blockNumber))
	setJSON(fields, "transactionIndex", fmt.Sprintf("0x%x", index))
	if transaction.Type() == types.SetCodeTxType {
		fields["authorizationList"] = json.RawMessage("[]")
	}
	return json.Marshal(fields)
}

func receiptJSON(receipt *types.Receipt, from common.Address, to *common.Address) (json.RawMessage, error) {
	data, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	setJSON(fields, "from", from)
	setJSON(fields, "to", to)
	if receipt.ContractAddress == (common.Address{}) {
		fields["contractAddress"] = json.RawMessage("null")
	}
	return json.Marshal(fields)
}

func blockJSON(
	header *types.Header,
	transactions []json.RawMessage,
	uncles []*types.Header,
	withdrawals []*types.Withdrawal,
) (json.RawMessage, error) {
	data, err := json.Marshal(header)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	transactionData, err := json.Marshal(transactions)
	if err != nil {
		return nil, err
	}
	fields["transactions"] = transactionData
	uncleHashes := make([]common.Hash, len(uncles))
	for index := range uncles {
		uncleHashes[index] = uncles[index].Hash()
	}
	fields["uncles"], err = json.Marshal(uncleHashes)
	if err != nil {
		return nil, err
	}
	if withdrawals != nil {
		withdrawalData, err := json.Marshal(withdrawals)
		if err != nil {
			return nil, err
		}
		fields["withdrawals"] = withdrawalData
	}
	return json.Marshal(fields)
}

func cloneHeaders(input []*types.Header) ([]*types.Header, error) {
	if input == nil {
		return []*types.Header{}, nil
	}
	result := make([]*types.Header, len(input))
	for index := range input {
		if input[index] == nil {
			return nil, fmt.Errorf("uncle %d is nil", index)
		}
		result[index] = types.CopyHeader(input[index])
	}
	return result, nil
}

func setJSON(fields map[string]json.RawMessage, name string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	fields[name] = data
}

func cloneWithdrawals(input []*types.Withdrawal) []*types.Withdrawal {
	if input == nil {
		return nil
	}
	result := make([]*types.Withdrawal, len(input))
	for index := range input {
		if input[index] != nil {
			copy := *input[index]
			result[index] = &copy
		}
	}
	return result
}
