//go:build integration

package integration_test

import (
	"encoding/json"
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/holiman/uint256"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
)

type integrationTransactionOptions struct {
	Type             uint8
	To               *common.Address
	ContractCreation bool
	Data             []byte
	Value            *big.Int
	GasPrice         *big.Int
	Authorizations   []types.SetCodeAuthorization
	Logs             []*types.Log
	RawExtra         map[string]any
	ReceiptRawExtra  map[string]any
}

type integrationBundleOptions struct {
	Number        uint64
	ParentHash    common.Hash
	Timestamp     uint64
	ExtraData     []byte
	Coinbase      common.Address
	Transactions  []integrationTransactionOptions
	Withdrawals   []*types.Withdrawal
	BlobGasUsed   *uint64
	ExcessBlobGas *uint64
	RawExtra      map[string]any
}

func newIntegrationBundle(options integrationBundleOptions) (chainbundle.Bundle, error) {
	if options.Timestamp == 0 {
		options.Timestamp = 1_700_000_000 + options.Number
	}
	if options.Coinbase == (common.Address{}) {
		options.Coinbase = testAddress(3)
	}
	key, err := crypto.HexToECDSA("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		return chainbundle.Bundle{}, err
	}
	sender := crypto.PubkeyToAddress(key.PublicKey)
	chainID := big.NewInt(1)
	signer := types.LatestSignerForChainID(chainID)
	transactions := make(types.Transactions, len(options.Transactions))
	receipts := make(types.Receipts, len(options.Transactions))
	var cumulativeGas uint64
	var nextLogIndex uint
	for index, transactionOptions := range options.Transactions {
		unsigned, err := newIntegrationTransaction(chainID, uint64(index), transactionOptions)
		if err != nil {
			return chainbundle.Bundle{}, fmt.Errorf("build transaction %d: %w", index, err)
		}
		transaction, err := types.SignTx(unsigned, signer, key)
		if err != nil {
			return chainbundle.Bundle{}, fmt.Errorf("sign transaction %d: %w", index, err)
		}
		transactions[index] = transaction
		cumulativeGas += 21_000
		logs := cloneIntegrationLogs(transactionOptions.Logs)
		for _, log := range logs {
			log.TxHash = transaction.Hash()
			log.TxIndex = uint(index)
			log.Index = nextLogIndex
			nextLogIndex++
		}
		effectiveGasPrice := transactionOptions.GasPrice
		if effectiveGasPrice == nil {
			effectiveGasPrice = big.NewInt(1)
		}
		receipt := &types.Receipt{
			Type:              transaction.Type(),
			Status:            types.ReceiptStatusSuccessful,
			CumulativeGasUsed: cumulativeGas,
			Logs:              logs,
			TxHash:            transaction.Hash(),
			GasUsed:           21_000,
			EffectiveGasPrice: new(big.Int).Set(effectiveGasPrice),
			TransactionIndex:  uint(index),
		}
		if transaction.To() == nil {
			receipt.ContractAddress = crypto.CreateAddress(sender, transaction.Nonce())
		}
		receipt.Bloom = types.CreateBloom(receipt)
		receipts[index] = receipt
	}
	header := &types.Header{
		ParentHash:    options.ParentHash,
		UncleHash:     types.EmptyUncleHash,
		Coinbase:      options.Coinbase,
		Root:          testHash(4),
		TxHash:        types.DeriveSha(transactions, trie.NewStackTrie(nil)),
		ReceiptHash:   types.DeriveSha(receipts, trie.NewStackTrie(nil)),
		Bloom:         types.MergeBloom(receipts),
		Difficulty:    big.NewInt(0),
		Number:        new(big.Int).SetUint64(options.Number),
		GasLimit:      30_000_000,
		GasUsed:       cumulativeGas,
		Time:          options.Timestamp,
		Extra:         common.CopyBytes(options.ExtraData),
		BlobGasUsed:   cloneIntegrationUint64(options.BlobGasUsed),
		ExcessBlobGas: cloneIntegrationUint64(options.ExcessBlobGas),
	}
	withdrawals := cloneIntegrationWithdrawals(options.Withdrawals)
	if options.Withdrawals != nil {
		root := types.DeriveSha(types.Withdrawals(withdrawals), trie.NewStackTrie(nil))
		header.WithdrawalsHash = &root
	}
	blockHash := header.Hash()
	rawTransactions := make([]json.RawMessage, len(transactions))
	rawReceipts := make([]json.RawMessage, len(receipts))
	for index := range transactions {
		rawTransactions[index], err = integrationTransactionJSON(
			transactions[index],
			sender,
			blockHash,
			options.Number,
			uint64(index),
			options.Transactions[index].RawExtra,
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
		rawReceipts[index], err = integrationReceiptJSON(
			receipts[index],
			sender,
			transactions[index].To(),
			options.Transactions[index].ReceiptRawExtra,
		)
		if err != nil {
			return chainbundle.Bundle{}, err
		}
	}
	rawBlock, err := integrationBlockJSON(header, rawTransactions, withdrawals, options.RawExtra)
	if err != nil {
		return chainbundle.Bundle{}, err
	}
	bundle, err := chainbundle.DecodeBlock(rawBlock, []json.RawMessage{})
	if err != nil {
		return chainbundle.Bundle{}, err
	}
	return bundle.WithReceipts(rawReceipts)
}

func integrationContractAddress(nonce uint64) common.Address {
	key, err := crypto.HexToECDSA("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		panic(err)
	}
	return crypto.CreateAddress(crypto.PubkeyToAddress(key.PublicKey), nonce)
}

func newIntegrationTransaction(
	chainID *big.Int,
	nonce uint64,
	options integrationTransactionOptions,
) (*types.Transaction, error) {
	to := options.To
	if !options.ContractCreation && to == nil {
		defaultTo := testAddress(2)
		to = &defaultTo
	}
	value := options.Value
	if value == nil {
		value = big.NewInt(int64(nonce + 1))
	}
	gasPrice := options.GasPrice
	if gasPrice == nil {
		gasPrice = big.NewInt(1)
	}
	switch options.Type {
	case types.LegacyTxType:
		return types.NewTx(&types.LegacyTx{
			Nonce: nonce, GasPrice: gasPrice, Gas: 21_000, To: to,
			Value: value, Data: common.CopyBytes(options.Data),
		}), nil
	case types.AccessListTxType:
		return types.NewTx(&types.AccessListTx{
			ChainID: chainID, Nonce: nonce, GasPrice: gasPrice, Gas: 21_000,
			To: to, Value: value, Data: common.CopyBytes(options.Data),
			AccessList: types.AccessList{},
		}), nil
	case types.DynamicFeeTxType:
		return types.NewTx(&types.DynamicFeeTx{
			ChainID: chainID, Nonce: nonce, GasTipCap: big.NewInt(1),
			GasFeeCap: gasPrice, Gas: 21_000, To: to, Value: value,
			Data: common.CopyBytes(options.Data), AccessList: types.AccessList{},
		}), nil
	case types.BlobTxType:
		if to == nil {
			return nil, fmt.Errorf("blob transaction cannot create a contract")
		}
		return types.NewTx(&types.BlobTx{
			ChainID: uint256.MustFromBig(chainID), Nonce: nonce,
			GasTipCap: uint256.NewInt(1), GasFeeCap: uint256.MustFromBig(gasPrice),
			Gas: 21_000, To: *to, Value: uint256.MustFromBig(value),
			Data: common.CopyBytes(options.Data), AccessList: types.AccessList{},
			BlobFeeCap: uint256.NewInt(3),
			BlobHashes: []common.Hash{{0: 0x01, 31: byte(nonce + 1)}},
		}), nil
	case types.SetCodeTxType:
		if to == nil {
			return nil, fmt.Errorf("set-code transaction cannot create a contract")
		}
		return types.NewTx(&types.SetCodeTx{
			ChainID: uint256.MustFromBig(chainID), Nonce: nonce,
			GasTipCap: uint256.NewInt(1), GasFeeCap: uint256.MustFromBig(gasPrice),
			Gas: 21_000, To: *to, Value: uint256.MustFromBig(value),
			Data: common.CopyBytes(options.Data), AccessList: types.AccessList{},
			AuthList: append([]types.SetCodeAuthorization(nil), options.Authorizations...),
		}), nil
	default:
		return nil, fmt.Errorf("%w: %d", chainbundle.ErrUnsupportedTransactionType, options.Type)
	}
}

func integrationTransactionJSON(
	transaction *types.Transaction,
	from common.Address,
	blockHash common.Hash,
	blockNumber uint64,
	index uint64,
	extra map[string]any,
) (json.RawMessage, error) {
	fields, err := marshalIntegrationFields(transaction)
	if err != nil {
		return nil, err
	}
	setIntegrationJSON(fields, "from", from)
	setIntegrationJSON(fields, "blockHash", blockHash)
	setIntegrationJSON(fields, "blockNumber", fmt.Sprintf("0x%x", blockNumber))
	setIntegrationJSON(fields, "transactionIndex", fmt.Sprintf("0x%x", index))
	if transaction.Type() == types.SetCodeTxType {
		setIntegrationJSON(fields, "authorizationList", transaction.SetCodeAuthorizations())
	}
	for name, value := range extra {
		setIntegrationJSON(fields, name, value)
	}
	return json.Marshal(fields)
}

func integrationReceiptJSON(
	receipt *types.Receipt,
	from common.Address,
	to *common.Address,
	extra map[string]any,
) (json.RawMessage, error) {
	fields, err := marshalIntegrationFields(receipt)
	if err != nil {
		return nil, err
	}
	setIntegrationJSON(fields, "from", from)
	setIntegrationJSON(fields, "to", to)
	if receipt.ContractAddress == (common.Address{}) {
		fields["contractAddress"] = json.RawMessage("null")
	}
	for name, value := range extra {
		setIntegrationJSON(fields, name, value)
	}
	return json.Marshal(fields)
}

func integrationBlockJSON(
	header *types.Header,
	transactions []json.RawMessage,
	withdrawals []*types.Withdrawal,
	extra map[string]any,
) (json.RawMessage, error) {
	fields, err := marshalIntegrationFields(header)
	if err != nil {
		return nil, err
	}
	setIntegrationJSON(fields, "transactions", transactions)
	setIntegrationJSON(fields, "uncles", []common.Hash{})
	if withdrawals != nil {
		setIntegrationJSON(fields, "withdrawals", withdrawals)
	}
	for name, value := range extra {
		setIntegrationJSON(fields, name, value)
	}
	return json.Marshal(fields)
}

func marshalIntegrationFields(value any) (map[string]json.RawMessage, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

func setIntegrationJSON(fields map[string]json.RawMessage, name string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	fields[name] = data
}

func cloneIntegrationLogs(source []*types.Log) []*types.Log {
	result := make([]*types.Log, len(source))
	for index, log := range source {
		if log == nil {
			continue
		}
		copy := *log
		copy.Topics = append([]common.Hash(nil), log.Topics...)
		copy.Data = common.CopyBytes(log.Data)
		result[index] = &copy
	}
	return result
}

func cloneIntegrationWithdrawals(source []*types.Withdrawal) []*types.Withdrawal {
	if source == nil {
		return nil
	}
	result := make([]*types.Withdrawal, len(source))
	for index, withdrawal := range source {
		if withdrawal != nil {
			copy := *withdrawal
			result[index] = &copy
		}
	}
	return result
}

func cloneIntegrationUint64(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func withIntegrationBlockRawField(
	bundle chainbundle.Bundle,
	name string,
	value any,
) (chainbundle.Bundle, error) {
	fields, err := marshalIntegrationFields(json.RawMessage(bundle.RawBlock))
	if err != nil {
		return chainbundle.Bundle{}, err
	}
	setIntegrationJSON(fields, name, value)
	rawBlock, err := json.Marshal(fields)
	if err != nil {
		return chainbundle.Bundle{}, err
	}
	decoded, err := chainbundle.DecodeBlock(rawBlock, bundle.RawUncles)
	if err != nil {
		return chainbundle.Bundle{}, err
	}
	return decoded.WithReceipts(bundle.RawReceipts)
}

func newIntegrationRPCClient(t *testing.T, namespace string, service any) *rpc.Client {
	return newIntegrationRPCClientServices(t, map[string]any{namespace: service})
}

func newIntegrationRPCClientServices(t *testing.T, services map[string]any) *rpc.Client {
	t.Helper()
	server := rpc.NewServer()
	for namespace, service := range services {
		if err := server.RegisterName(namespace, service); err != nil {
			t.Fatalf("register %s integration RPC service: %v", namespace, err)
		}
	}
	client := rpc.DialInProc(server)
	t.Cleanup(func() {
		client.Close()
		server.Stop()
	})
	return client
}

func registerFixtureHash(seed, actual common.Hash) {
	fixtureHashes.Lock()
	if symbolic, ok := fixtureHashes.reverse[seed]; ok {
		seed = symbolic
	}
	fixtureHashes.forward[seed] = actual
	fixtureHashes.reverse[actual] = seed
	fixtureHashes.Unlock()
}

type integrationRPCError struct {
	code    int
	message string
	data    any
}

func (e *integrationRPCError) Error() string  { return e.message }
func (e *integrationRPCError) ErrorCode() int { return e.code }
func (e *integrationRPCError) ErrorData() any { return e.data }

func TestIntegrationFixturesUseGethComputedIdentitiesAndRoots(t *testing.T) {
	resetFixtureHashes()
	shared, err := testfixture.New(testfixture.Options{
		Number:             7,
		TransactionTypes:   []uint8{types.DynamicFeeTxType},
		LogsPerTransaction: 1,
	})
	if err != nil {
		t.Fatalf("build shared chainbundle fixture: %v", err)
	}
	if err := chainbundle.Validate(shared); err != nil {
		t.Fatalf("validate shared chainbundle fixture: %v", err)
	}
	parent := testBundle(0, testHash(990_000), testHash(0), testHash(990_100), "geth-parent")
	child := coreProtocolBundle(t, 1, parent.Block.Hash(), 991_000)
	if err := chainbundle.Validate(parent); err != nil {
		t.Fatalf("validate parent fixture: %v", err)
	}
	if err := chainbundle.Validate(child); err != nil {
		t.Fatalf("validate child fixture: %v", err)
	}
	if err := chainbundle.ValidateParent(child, parent); err != nil {
		t.Fatalf("validate fixture parent link: %v", err)
	}
	for index, transaction := range child.Block.Transactions() {
		signer := types.LatestSignerForChainID(transaction.ChainId())
		if _, err := types.Sender(signer, transaction); err != nil {
			t.Fatalf("recover transaction %d sender: %v", index, err)
		}
		if transaction.Hash() != child.Receipts[index].TxHash {
			t.Fatalf("transaction %d hash does not match its receipt", index)
		}
	}
}

func TestIntegrationRPCServerAcceptsExactNFTCall(t *testing.T) {
	service := &exactNFTCaller{owner: testAddress(1)}
	client := newIntegrationRPCClient(t, "eth", service)
	selector := rpc.BlockNumberOrHashWithHash(testHash(2), true)
	callData := append([]byte{0x63, 0x52, 0x21, 0x1e}, make([]byte, 32)...)
	var result hexutil.Bytes
	err := client.CallContext(
		t.Context(),
		&result,
		"eth_call",
		map[string]any{"to": testAddress(3), "data": hexutil.Bytes(callData)},
		selector,
	)
	if err != nil {
		t.Fatalf("call in-process exact NFT service: %v", err)
	}
	if len(result) != common.HashLength {
		t.Fatalf("exact NFT result length=%d want=%d", len(result), common.HashLength)
	}
}
