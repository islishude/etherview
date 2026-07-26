package x402testnet

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"math/big"
	"net"
	"net/http"
	"time"

	"github.com/islishude/etherview/internal/billing"
	"github.com/islishude/etherview/internal/ethrpc"
)

const (
	maxChainRPCURLBytes            = 4096
	maxChainRPCResponseBytes       = int64(1 << 20)
	maxChainRPCResponseHeaderBytes = int64(64 << 10)
	expectedSettlementCallBytes    = 4 + 9*32
	maxSettlementInputBytes        = 64 << 10
	erc20TransferTopic             = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
)

type ChainOptions struct {
	RPCURL               string
	ChainID              uint64
	TransactionHash      string
	Asset                string
	AmountAtomic         string
	Recipient            string
	Payer                string
	CallDataPrefixBytes  int
	CallDataPrefixSHA256 [sha256.Size]byte
}

type ChainEvidence struct {
	TransactionHash string
	BlockHash       string
	BlockNumber     string
	TransferCount   int
}

type settlementChain interface {
	chainID(context.Context) (string, error)
	transaction(context.Context, string) (*chainTransaction, error)
	receipt(context.Context, string) (*chainReceipt, error)
}

type rpcSettlementChain struct {
	caller ethrpc.Caller
}

type chainTransaction struct {
	Hash        string  `json:"hash"`
	BlockHash   *string `json:"blockHash"`
	BlockNumber *string `json:"blockNumber"`
	ChainID     string  `json:"chainId"`
	To          *string `json:"to"`
	Input       string  `json:"input"`
	Value       string  `json:"value"`
}

type chainReceipt struct {
	TransactionHash string     `json:"transactionHash"`
	BlockHash       string     `json:"blockHash"`
	BlockNumber     string     `json:"blockNumber"`
	Status          string     `json:"status"`
	Logs            []chainLog `json:"logs"`
}

type chainLog struct {
	Address         string   `json:"address"`
	Topics          []string `json:"topics"`
	Data            string   `json:"data"`
	BlockHash       string   `json:"blockHash"`
	BlockNumber     string   `json:"blockNumber"`
	TransactionHash string   `json:"transactionHash"`
	Removed         bool     `json:"removed"`
}

// CheckChain performs the mandatory independent Base Sepolia check before any
// payer authorization is created.
func CheckChain(
	ctx context.Context,
	rpcURL string,
	chainID uint64,
) error {
	if chainID != baseSepoliaChainID {
		return boundaryError("chain_configuration_invalid")
	}
	chain, err := newRPCSettlementChain(rpcURL)
	if err != nil {
		return err
	}
	return checkChainID(ctx, chain, chainID)
}

// VerifyChain rechecks the chain ID, then proves that the settled transaction
// is mined successfully and emitted exactly the expected aggregate ERC-20
// Transfer amount. The transaction sender is intentionally not compared with
// the payer: exact-EVM settlement is submitted by the facilitator.
func VerifyChain(
	ctx context.Context,
	options ChainOptions,
) (ChainEvidence, error) {
	expected, ok := parseChainExpectation(options)
	if !ok {
		return ChainEvidence{}, boundaryError("chain_configuration_invalid")
	}
	chain, err := newRPCSettlementChain(options.RPCURL)
	if err != nil {
		return ChainEvidence{}, err
	}
	return verifyChain(ctx, chain, expected)
}

func newRPCSettlementChain(rawURL string) (settlementChain, error) {
	if !validChainRPCURL(rawURL) {
		return nil, boundaryError("chain_configuration_invalid")
	}
	caller, err := ethrpc.NewHTTPClient(
		rawURL,
		ethrpc.HTTPClientOptions{
			HTTPClient:       newChainHTTPClient(),
			MaxResponseBytes: maxChainRPCResponseBytes,
		},
	)
	if err != nil {
		return nil, boundaryError("chain_configuration_invalid")
	}
	return &rpcSettlementChain{caller: caller}, nil
}

func validChainRPCURL(raw string) bool {
	return len(raw) <= maxChainRPCURLBytes && validRPCURL(raw)
}

func newChainHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.TLSHandshakeTimeout = 5 * time.Second
	transport.ResponseHeaderTimeout = 5 * time.Second
	transport.DisableCompression = true
	transport.MaxResponseHeaderBytes = maxChainRPCResponseHeaderBytes
	transport.MaxConnsPerHost = 2
	transport.MaxIdleConns = 2
	transport.MaxIdleConnsPerHost = 1
	return &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func checkChainID(
	ctx context.Context,
	chain settlementChain,
	expected uint64,
) error {
	if chain == nil {
		return boundaryError("chain_configuration_invalid")
	}
	raw, err := chain.chainID(ctx)
	if err != nil {
		return boundaryError("chain_unavailable")
	}
	chainID, ok := parseHexQuantity(raw)
	if !ok || !chainID.IsUint64() {
		return boundaryError("chain_response_invalid")
	}
	if chainID.Uint64() != expected || expected != baseSepoliaChainID {
		return boundaryError("chain_id_mismatch")
	}
	return nil
}

func verifyChain(
	ctx context.Context,
	chain settlementChain,
	expected chainExpectation,
) (ChainEvidence, error) {
	if err := checkChainID(ctx, chain, expected.chainID); err != nil {
		return ChainEvidence{}, err
	}
	transaction, err := chain.transaction(ctx, expected.transactionHashText)
	if err != nil {
		return ChainEvidence{}, boundaryError("chain_unavailable")
	}
	if transaction == nil {
		return ChainEvidence{}, boundaryError("chain_transaction_not_found")
	}
	transactionHash, ok := parseTransactionHash(transaction.Hash)
	if !ok || transactionHash != expected.transactionHash {
		return ChainEvidence{}, boundaryError("chain_transaction_mismatch")
	}
	if transaction.To == nil {
		return ChainEvidence{}, boundaryError("chain_transaction_mismatch")
	}
	transactionTo, toOK := parseRPCAddress(*transaction.To)
	transactionValue, valueOK := parseHexQuantity(transaction.Value)
	input, inputOK := decodeVariableRPCData(
		transaction.Input,
		maxSettlementInputBytes,
	)
	if !toOK || transactionTo != expected.asset ||
		!valueOK || transactionValue.Sign() != 0 ||
		!inputOK || len(input) < expected.callDataPrefixBytes ||
		sha256.Sum256(input[:expected.callDataPrefixBytes]) !=
			expected.callDataPrefixDigest {
		return ChainEvidence{}, boundaryError("chain_transaction_mismatch")
	}
	if transaction.BlockHash == nil || transaction.BlockNumber == nil {
		return ChainEvidence{}, boundaryError("chain_transaction_pending")
	}
	blockHash, ok := parseTransactionHash(*transaction.BlockHash)
	if !ok {
		return ChainEvidence{}, boundaryError("chain_response_invalid")
	}
	blockNumber, ok := parseHexQuantity(*transaction.BlockNumber)
	if !ok || !blockNumber.IsUint64() {
		return ChainEvidence{}, boundaryError("chain_response_invalid")
	}
	transactionChainID, ok := parseHexQuantity(transaction.ChainID)
	if !ok || !transactionChainID.IsUint64() ||
		transactionChainID.Uint64() != expected.chainID {
		return ChainEvidence{}, boundaryError("chain_transaction_mismatch")
	}

	receipt, err := chain.receipt(ctx, expected.transactionHashText)
	if err != nil {
		return ChainEvidence{}, boundaryError("chain_unavailable")
	}
	if receipt == nil {
		return ChainEvidence{}, boundaryError("chain_receipt_not_found")
	}
	if receipt.Status != "0x1" {
		if status, valid := parseHexQuantity(receipt.Status); valid &&
			status.Sign() == 0 {
			return ChainEvidence{}, boundaryError("chain_receipt_failed")
		}
		return ChainEvidence{}, boundaryError("chain_response_invalid")
	}
	receiptTransactionHash, transactionOK := parseTransactionHash(
		receipt.TransactionHash,
	)
	receiptBlockHash, blockOK := parseTransactionHash(receipt.BlockHash)
	receiptBlockNumber, numberOK := parseHexQuantity(receipt.BlockNumber)
	if !transactionOK || receiptTransactionHash != expected.transactionHash ||
		!blockOK || receiptBlockHash != blockHash ||
		!numberOK || !receiptBlockNumber.IsUint64() ||
		receiptBlockNumber.Uint64() != blockNumber.Uint64() {
		return ChainEvidence{}, boundaryError("chain_receipt_mismatch")
	}

	transferCount, total, ok := matchingTransferTotal(
		receipt,
		expected,
		receiptTransactionHash,
		receiptBlockHash,
		receiptBlockNumber,
	)
	if !ok {
		return ChainEvidence{}, boundaryError("chain_response_invalid")
	}
	if transferCount == 0 || total.Cmp(expected.amount) != 0 {
		return ChainEvidence{}, boundaryError("chain_transfer_mismatch")
	}
	return ChainEvidence{
		TransactionHash: canonicalHash(expected.transactionHash),
		BlockHash:       canonicalHash(blockHash),
		BlockNumber:     blockNumber.String(),
		TransferCount:   transferCount,
	}, nil
}

type chainExpectation struct {
	chainID              uint64
	transactionHash      billing.TransactionHash
	transactionHashText  string
	asset                billing.Address
	amount               *big.Int
	recipient            billing.Address
	payer                billing.Address
	callDataPrefixBytes  int
	callDataPrefixDigest [sha256.Size]byte
}

func parseChainExpectation(
	options ChainOptions,
) (chainExpectation, bool) {
	var result chainExpectation
	if options.ChainID != baseSepoliaChainID ||
		!validChainRPCURL(options.RPCURL) ||
		options.CallDataPrefixBytes != expectedSettlementCallBytes ||
		zeroDigest(options.CallDataPrefixSHA256) {
		return result, false
	}
	transactionHash, ok := parseTransactionHash(options.TransactionHash)
	if !ok {
		return result, false
	}
	asset, ok := parseExpectedAddress(options.Asset)
	if !ok {
		return result, false
	}
	amount, ok := parseAmount(options.AmountAtomic)
	if !ok {
		return result, false
	}
	recipient, ok := parseExpectedAddress(options.Recipient)
	if !ok {
		return result, false
	}
	payer, ok := parseExpectedAddress(options.Payer)
	if !ok {
		return result, false
	}
	return chainExpectation{
		chainID:              options.ChainID,
		transactionHash:      transactionHash,
		transactionHashText:  canonicalHash(transactionHash),
		asset:                asset,
		amount:               amount,
		recipient:            recipient,
		payer:                payer,
		callDataPrefixBytes:  options.CallDataPrefixBytes,
		callDataPrefixDigest: options.CallDataPrefixSHA256,
	}, true
}

func matchingTransferTotal(
	receipt *chainReceipt,
	expected chainExpectation,
	transactionHash billing.TransactionHash,
	blockHash billing.TransactionHash,
	blockNumber *big.Int,
) (int, *big.Int, bool) {
	total := new(big.Int)
	count := 0
	for _, log := range receipt.Logs {
		asset, validAsset := parseRPCAddress(log.Address)
		if !validAsset || log.Removed ||
			!validLogData(log.Data) ||
			!validLogTopics(log.Topics) {
			return 0, nil, false
		}
		logTransactionHash, transactionOK := parseTransactionHash(
			log.TransactionHash,
		)
		logBlockHash, blockOK := parseTransactionHash(log.BlockHash)
		logBlockNumber, numberOK := parseHexQuantity(log.BlockNumber)
		if !transactionOK || logTransactionHash != transactionHash ||
			!blockOK || logBlockHash != blockHash ||
			!numberOK || logBlockNumber.Cmp(blockNumber) != 0 {
			return 0, nil, false
		}
		if asset != expected.asset {
			continue
		}
		if len(log.Topics) == 0 ||
			log.Topics[0] != erc20TransferTopic {
			continue
		}
		if len(log.Topics) != 3 {
			return 0, nil, false
		}
		from, ok := parseAddressTopic(log.Topics[1])
		if !ok {
			return 0, nil, false
		}
		to, ok := parseAddressTopic(log.Topics[2])
		if !ok {
			return 0, nil, false
		}
		value, ok := parseDataWord(log.Data)
		if !ok {
			return 0, nil, false
		}
		if from != expected.payer || to != expected.recipient {
			continue
		}
		total.Add(total, value)
		if total.Cmp(maximumTestnetUint256) > 0 {
			return 0, nil, false
		}
		count++
	}
	return count, total, true
}

func validLogTopics(topics []string) bool {
	for _, topic := range topics {
		if _, ok := decodeFixedHex(topic, 32); !ok {
			return false
		}
	}
	return true
}

func validLogData(value string) bool {
	if len(value) < 2 || value[:2] != "0x" || (len(value)-2)%2 != 0 {
		return false
	}
	decoded, err := hex.DecodeString(value[2:])
	return err == nil && hex.EncodeToString(decoded) == value[2:]
}

func parseAddressTopic(value string) (billing.Address, bool) {
	var result billing.Address
	decoded, ok := decodeFixedHex(value, 32)
	if !ok {
		return result, false
	}
	for _, prefix := range decoded[:12] {
		if prefix != 0 {
			return result, false
		}
	}
	copy(result[:], decoded[12:])
	return result, true
}

func parseRPCAddress(value string) (billing.Address, bool) {
	var result billing.Address
	decoded, ok := decodeFixedHex(value, len(result))
	if !ok {
		return result, false
	}
	copy(result[:], decoded)
	return result, true
}

func parseDataWord(value string) (*big.Int, bool) {
	decoded, ok := decodeFixedHex(value, 32)
	if !ok {
		return nil, false
	}
	return new(big.Int).SetBytes(decoded), true
}

func decodeFixedHex(value string, size int) ([]byte, bool) {
	if len(value) != 2+size*2 || value[:2] != "0x" {
		return nil, false
	}
	decoded, err := hex.DecodeString(value[2:])
	return decoded, err == nil &&
		len(decoded) == size &&
		hex.EncodeToString(decoded) == value[2:]
}

func decodeVariableRPCData(value string, maximumBytes int) ([]byte, bool) {
	if len(value) < 2 || value[:2] != "0x" ||
		len(value[2:])%2 != 0 ||
		len(value[2:])/2 > maximumBytes {
		return nil, false
	}
	decoded, err := hex.DecodeString(value[2:])
	return decoded, err == nil &&
		hex.EncodeToString(decoded) == value[2:]
}

func parseHexQuantity(value string) (*big.Int, bool) {
	// JSON-RPC QUANTITY is the shortest lowercase hexadecimal representation:
	// zero is 0x0 and every non-zero value has no leading zero.
	if len(value) < 3 || value[:2] != "0x" ||
		(len(value) > 3 && value[2] == '0') {
		return nil, false
	}
	for _, character := range value[2:] {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return nil, false
		}
	}
	result, ok := new(big.Int).SetString(value[2:], 16)
	if !ok || result.Sign() < 0 || result.Cmp(maximumTestnetUint256) > 0 {
		return nil, false
	}
	return result, true
}

func canonicalHash(value billing.TransactionHash) string {
	return "0x" + hex.EncodeToString(value[:])
}

func (chain *rpcSettlementChain) chainID(
	ctx context.Context,
) (string, error) {
	var result string
	if err := chain.caller.Call(ctx, "eth_chainId", nil, &result); err != nil {
		return "", err
	}
	return result, nil
}

func (chain *rpcSettlementChain) transaction(
	ctx context.Context,
	transactionHash string,
) (*chainTransaction, error) {
	var result *chainTransaction
	if err := chain.caller.Call(
		ctx,
		"eth_getTransactionByHash",
		[]any{transactionHash},
		&result,
	); err != nil {
		return nil, err
	}
	return result, nil
}

func (chain *rpcSettlementChain) receipt(
	ctx context.Context,
	transactionHash string,
) (*chainReceipt, error) {
	var result *chainReceipt
	if err := chain.caller.Call(
		ctx,
		"eth_getTransactionReceipt",
		[]any{transactionHash},
		&result,
	); err != nil {
		return nil, err
	}
	return result, nil
}
