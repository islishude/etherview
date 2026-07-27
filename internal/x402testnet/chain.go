package x402testnet

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"math/big"
	"net"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/ethrpc"
)

const (
	maxChainRPCURLBytes            = 4096
	maxChainRPCResponseBytes       = int64(1 << 20)
	maxChainRPCResponseHeaderBytes = int64(64 << 10)
	expectedSettlementCallBytes    = 4 + 9*32
	maxSettlementInputBytes        = 64 << 10
)

var erc20TransferTopic = common.HexToHash(
	"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
)

var errInvalidChainResponse = errors.New("invalid settlement chain response")

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
	chainID(context.Context) (*big.Int, error)
	transaction(
		context.Context,
		common.Hash,
	) (*types.Transaction, bool, error)
	receipt(context.Context, common.Hash) (*types.Receipt, error)
	header(context.Context, common.Hash) (*types.Header, error)
	includedTransaction(
		context.Context,
		common.Hash,
		uint,
	) (*types.Transaction, error)
}

type rpcSettlementChain struct {
	client *ethclient.Client
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
	chain, err := newRPCSettlementChain(ctx, rpcURL)
	if err != nil {
		return err
	}
	defer chain.close()
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
	chain, err := newRPCSettlementChain(ctx, options.RPCURL)
	if err != nil {
		return ChainEvidence{}, err
	}
	defer chain.close()
	return verifyChain(ctx, chain, expected)
}

func newRPCSettlementChain(
	ctx context.Context,
	rawURL string,
) (*rpcSettlementChain, error) {
	if !validChainRPCURL(rawURL) {
		return nil, boundaryError("chain_configuration_invalid")
	}
	client, err := ethrpc.NewClient(
		ctx,
		rawURL,
		ethrpc.ClientOptions{
			HTTPClient:       newChainHTTPClient(),
			MaxResponseBytes: maxChainRPCResponseBytes,
		},
	)
	if err != nil {
		return nil, boundaryError("chain_configuration_invalid")
	}
	return &rpcSettlementChain{client: ethclient.NewClient(client)}, nil
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
	chainID, err := chain.chainID(ctx)
	if err != nil {
		if errors.Is(err, errInvalidChainResponse) {
			return boundaryError("chain_response_invalid")
		}
		return boundaryError("chain_unavailable")
	}
	if chainID == nil || chainID.Sign() < 0 || !chainID.IsUint64() {
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
	transaction, pending, err := chain.transaction(
		ctx,
		expected.transactionHash,
	)
	if err != nil {
		if errors.Is(err, errInvalidChainResponse) {
			return ChainEvidence{}, boundaryError("chain_response_invalid")
		}
		return ChainEvidence{}, boundaryError("chain_unavailable")
	}
	if transaction == nil {
		return ChainEvidence{}, boundaryError("chain_transaction_not_found")
	}
	if transaction.Type() > types.SetCodeTxType {
		return ChainEvidence{}, boundaryError("chain_response_invalid")
	}
	transactionHash := transaction.Hash()
	if transactionHash != expected.transactionHash {
		return ChainEvidence{}, boundaryError("chain_transaction_mismatch")
	}
	transactionTo := transaction.To()
	transactionValue := transaction.Value()
	input := transaction.Data()
	if transactionTo == nil || transactionValue == nil ||
		*transactionTo != expected.asset ||
		transactionValue.Sign() != 0 ||
		len(input) > maxSettlementInputBytes ||
		len(input) < expected.callDataPrefixBytes ||
		sha256.Sum256(input[:expected.callDataPrefixBytes]) !=
			expected.callDataPrefixDigest {
		return ChainEvidence{}, boundaryError("chain_transaction_mismatch")
	}
	if pending {
		return ChainEvidence{}, boundaryError("chain_transaction_pending")
	}
	transactionChainID := transaction.ChainId()
	if transactionChainID == nil ||
		!transactionChainID.IsUint64() ||
		transactionChainID.Uint64() != expected.chainID {
		return ChainEvidence{}, boundaryError("chain_transaction_mismatch")
	}

	receipt, err := chain.receipt(ctx, expected.transactionHash)
	if err != nil {
		if errors.Is(err, errInvalidChainResponse) {
			return ChainEvidence{}, boundaryError("chain_response_invalid")
		}
		return ChainEvidence{}, boundaryError("chain_unavailable")
	}
	if receipt == nil {
		return ChainEvidence{}, boundaryError("chain_receipt_not_found")
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		if receipt.Status == types.ReceiptStatusFailed {
			return ChainEvidence{}, boundaryError("chain_receipt_failed")
		}
		return ChainEvidence{}, boundaryError("chain_response_invalid")
	}
	if receipt.Type != transaction.Type() ||
		receipt.TxHash != expected.transactionHash ||
		receipt.BlockHash == (common.Hash{}) ||
		receipt.BlockNumber == nil ||
		receipt.BlockNumber.Sign() < 0 ||
		!receipt.BlockNumber.IsUint64() {
		return ChainEvidence{}, boundaryError("chain_receipt_mismatch")
	}
	header, err := chain.header(ctx, receipt.BlockHash)
	if err != nil {
		if errors.Is(err, errInvalidChainResponse) {
			return ChainEvidence{}, boundaryError("chain_response_invalid")
		}
		return ChainEvidence{}, boundaryError("chain_unavailable")
	}
	if header == nil || header.Number == nil ||
		header.Number.Sign() < 0 ||
		header.Hash() != receipt.BlockHash ||
		header.Number.Cmp(receipt.BlockNumber) != 0 {
		return ChainEvidence{}, boundaryError("chain_receipt_mismatch")
	}
	includedTransaction, err := chain.includedTransaction(
		ctx,
		receipt.BlockHash,
		receipt.TransactionIndex,
	)
	if err != nil {
		if errors.Is(err, errInvalidChainResponse) {
			return ChainEvidence{}, boundaryError("chain_response_invalid")
		}
		return ChainEvidence{}, boundaryError("chain_unavailable")
	}
	if includedTransaction == nil ||
		includedTransaction.Hash() != expected.transactionHash {
		return ChainEvidence{}, boundaryError("chain_receipt_mismatch")
	}

	transferCount, total, ok := matchingTransferTotal(
		receipt,
		expected,
		receipt.TxHash,
		receipt.BlockHash,
		receipt.BlockNumber.Uint64(),
	)
	if !ok {
		return ChainEvidence{}, boundaryError("chain_response_invalid")
	}
	if transferCount == 0 || total.Cmp(expected.amount) != 0 {
		return ChainEvidence{}, boundaryError("chain_transfer_mismatch")
	}
	return ChainEvidence{
		TransactionHash: canonicalHash(expected.transactionHash),
		BlockHash:       canonicalHash(receipt.BlockHash),
		BlockNumber:     receipt.BlockNumber.String(),
		TransferCount:   transferCount,
	}, nil
}

type chainExpectation struct {
	chainID              uint64
	transactionHash      common.Hash
	asset                common.Address
	amount               *big.Int
	recipient            common.Address
	payer                common.Address
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
		asset:                asset,
		amount:               amount,
		recipient:            recipient,
		payer:                payer,
		callDataPrefixBytes:  options.CallDataPrefixBytes,
		callDataPrefixDigest: options.CallDataPrefixSHA256,
	}, true
}

func matchingTransferTotal(
	receipt *types.Receipt,
	expected chainExpectation,
	transactionHash common.Hash,
	blockHash common.Hash,
	blockNumber uint64,
) (int, *big.Int, bool) {
	total := new(big.Int)
	count := 0
	for _, log := range receipt.Logs {
		if log == nil || log.Removed ||
			log.TxHash != transactionHash ||
			log.BlockHash != blockHash ||
			log.BlockNumber != blockNumber {
			return 0, nil, false
		}
		if log.Address != expected.asset {
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

func parseAddressTopic(value common.Hash) (common.Address, bool) {
	var result common.Address
	for _, prefix := range value[:12] {
		if prefix != 0 {
			return result, false
		}
	}
	copy(result[:], value[12:])
	return result, true
}

func parseDataWord(value []byte) (*big.Int, bool) {
	if len(value) != common.HashLength {
		return nil, false
	}
	return new(big.Int).SetBytes(value), true
}

func canonicalHash(value common.Hash) string {
	return value.Hex()
}

func (chain *rpcSettlementChain) chainID(
	ctx context.Context,
) (*big.Int, error) {
	chainID, err := chain.client.ChainID(ctx)
	return chainID, normalizeChainRPCError(err)
}

func (chain *rpcSettlementChain) transaction(
	ctx context.Context,
	transactionHash common.Hash,
) (*types.Transaction, bool, error) {
	transaction, pending, err := chain.client.TransactionByHash(
		ctx,
		transactionHash,
	)
	if errors.Is(err, ethereum.NotFound) {
		return nil, false, nil
	}
	return transaction, pending, normalizeChainRPCError(err)
}

func (chain *rpcSettlementChain) receipt(
	ctx context.Context,
	transactionHash common.Hash,
) (*types.Receipt, error) {
	receipt, err := chain.client.TransactionReceipt(
		ctx,
		transactionHash,
	)
	if errors.Is(err, ethereum.NotFound) {
		return nil, nil
	}
	return receipt, normalizeChainRPCError(err)
}

func (chain *rpcSettlementChain) header(
	ctx context.Context,
	blockHash common.Hash,
) (*types.Header, error) {
	header, err := chain.client.HeaderByHash(ctx, blockHash)
	if errors.Is(err, ethereum.NotFound) {
		return nil, nil
	}
	return header, normalizeChainRPCError(err)
}

func (chain *rpcSettlementChain) includedTransaction(
	ctx context.Context,
	blockHash common.Hash,
	index uint,
) (*types.Transaction, error) {
	transaction, err := chain.client.TransactionInBlock(
		ctx,
		blockHash,
		index,
	)
	if errors.Is(err, ethereum.NotFound) {
		return nil, nil
	}
	return transaction, normalizeChainRPCError(err)
}

func (chain *rpcSettlementChain) close() {
	chain.client.Close()
}

func normalizeChainRPCError(err error) error {
	if err == nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, ethrpc.ErrTransport) {
		return err
	}
	var rpcError rpc.Error
	var httpError rpc.HTTPError
	if errors.As(err, &rpcError) || errors.As(err, &httpError) {
		return ethrpc.SanitizeError(err)
	}
	return errInvalidChainResponse
}
