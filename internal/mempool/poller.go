package mempool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
)

const maximumUint256Bits = 256

type Source interface {
	PendingBlock(context.Context) (*ethrpc.Block, string, error)
}

type SourceError struct {
	State State
	Code  string
	Cause error
}

func (err SourceError) Error() string {
	if err.Code == "" {
		return "pending RPC request failed"
	}
	return "pending RPC request failed: " + err.Code
}

func (err SourceError) Unwrap() error { return err.Cause }

type PoolSource struct{ Pool *ethrpc.Pool }

func (source PoolSource) PendingBlock(ctx context.Context) (*ethrpc.Block, string, error) {
	if source.Pool == nil {
		return nil, "", SourceError{State: StateUnavailable, Code: "endpoint_unavailable"}
	}
	endpoint, err := source.Pool.Acquire(ethrpc.PurposeMempool)
	if err != nil {
		return nil, "", SourceError{State: StateUnavailable, Code: "endpoint_unavailable", Cause: err}
	}
	var block *ethrpc.Block
	err = endpoint.Client.Call(ctx, "eth_getBlockByNumber", []any{"pending", true}, &block)
	if err != nil {
		source.Pool.ReportFailure(endpoint.Name)
		state, code := StateFailed, "rpc_request_failed"
		if ethrpc.IsMethodNotFound(err) {
			state, code = StateUnavailable, "method_not_supported"
		}
		return nil, endpoint.Name, SourceError{State: state, Code: code, Cause: err}
	}
	if block == nil {
		source.Pool.ReportFailure(endpoint.Name)
		return nil, endpoint.Name, SourceError{State: StateFailed, Code: "null_snapshot"}
	}
	source.Pool.ReportSuccess(endpoint.Name)
	return block, endpoint.Name, nil
}

type PollerOptions struct {
	ChainID          uint64
	PollInterval     time.Duration
	Retention        time.Duration
	MaxTransactions  int
	MaxResponseBytes int
	Now              func() time.Time
	Logger           *slog.Logger
}

type Poller struct {
	source  Source
	store   Store
	options PollerOptions
}

func NewPoller(source Source, store Store, options PollerOptions) (*Poller, error) {
	if source == nil {
		return nil, errors.New("mempool source is nil")
	}
	if store == nil {
		return nil, errors.New("mempool store is nil")
	}
	if options.ChainID == 0 {
		return nil, errors.New("mempool chain ID must be greater than zero")
	}
	if options.PollInterval <= 0 || options.Retention <= options.PollInterval {
		return nil, errors.New("mempool retention must exceed the positive poll interval")
	}
	if options.MaxTransactions <= 0 || options.MaxResponseBytes <= 0 {
		return nil, errors.New("mempool snapshot limits must be positive")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Poller{source: source, store: store, options: options}, nil
}

func (poller *Poller) Name() string { return "pending-mempool" }

func (poller *Poller) Run(ctx context.Context) error {
	for {
		if err := poller.Cycle(ctx); err != nil && ctx.Err() == nil {
			poller.options.Logger.WarnContext(ctx, "mempool poll failed; core synchronization remains active",
				"error_code", pollErrorCode(err))
		}
		timer := time.NewTimer(poller.options.PollInterval)
		select {
		case <-ctx.Done():
			stopTimer(timer)
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (poller *Poller) Cycle(ctx context.Context) error {
	block, endpoint, err := poller.source.PendingBlock(ctx)
	observedAt := poller.options.Now().UTC()
	if err != nil {
		failure := sourceFailure(err, endpoint, observedAt)
		if storeErr := poller.store.StoreFailure(ctx, failure); storeErr != nil {
			return fmt.Errorf("persist mempool failure status: %w", storeErr)
		}
		return err
	}
	snapshot, err := buildSnapshot(block, endpoint, poller.options, observedAt)
	if err != nil {
		failure := Failure{
			State: StateFailed, Endpoint: endpoint, Code: "invalid_snapshot",
			Message: boundedMessage(err.Error()), ObservedAt: observedAt,
		}
		if storeErr := poller.store.StoreFailure(ctx, failure); storeErr != nil {
			return fmt.Errorf("persist invalid mempool status: %w", storeErr)
		}
		return err
	}
	if _, err := poller.store.StoreSnapshot(ctx, snapshot); err != nil {
		return fmt.Errorf("persist mempool snapshot: %w", err)
	}
	return nil
}

func sourceFailure(err error, endpoint string, observedAt time.Time) Failure {
	state, code, message := StateFailed, "rpc_request_failed", "pending RPC request failed"
	var sourceErr SourceError
	if errors.As(err, &sourceErr) {
		state = sourceErr.State
		code = sourceErr.Code
	}
	switch code {
	case "endpoint_unavailable":
		message = "no HTTP RPC endpoint is available for mempool polling"
	case "method_not_supported":
		message = "pending block RPC is not supported by the selected endpoint"
	case "null_snapshot":
		message = "pending block RPC returned a null snapshot"
	}
	return Failure{State: state, Endpoint: endpoint, Code: code, Message: message, ObservedAt: observedAt}
}

func buildSnapshot(block *ethrpc.Block, endpoint string, options PollerOptions, observedAt time.Time) (Snapshot, error) {
	if block == nil {
		return Snapshot{}, errors.New("pending block is null")
	}
	if block.Hash != nil || block.Number != nil {
		return Snapshot{}, errors.New("pending block has a mined block identity")
	}
	if endpoint == "" || len(endpoint) > 128 {
		return Snapshot{}, errors.New("pending endpoint name is invalid")
	}
	if len(block.Transactions) > options.MaxTransactions {
		return Snapshot{}, fmt.Errorf("pending snapshot has %d transactions, limit is %d", len(block.Transactions), options.MaxTransactions)
	}
	rawBlock, err := json.Marshal(block)
	if err != nil {
		return Snapshot{}, fmt.Errorf("encode pending snapshot: %w", err)
	}
	if len(rawBlock) > options.MaxResponseBytes {
		return Snapshot{}, fmt.Errorf("pending snapshot has %d bytes, limit is %d", len(rawBlock), options.MaxResponseBytes)
	}
	transactions := make([]Transaction, 0, len(block.Transactions))
	seen := make(map[string]struct{}, len(block.Transactions))
	for index, reference := range block.Transactions {
		if !reference.IsFull() {
			return Snapshot{}, fmt.Errorf("pending transaction %d is hash-only", index)
		}
		transaction, err := pendingTransaction(reference, options.ChainID, endpoint, observedAt, observedAt.Add(options.Retention))
		if err != nil {
			return Snapshot{}, fmt.Errorf("pending transaction %d: %w", index, err)
		}
		key := strings.ToLower(transaction.Hash)
		if _, duplicate := seen[key]; duplicate {
			return Snapshot{}, fmt.Errorf("pending transaction %d duplicates hash %s", index, key)
		}
		seen[key] = struct{}{}
		transactions = append(transactions, transaction)
	}
	return Snapshot{
		Endpoint: endpoint, ObservedAt: observedAt, ExpiresAt: observedAt.Add(options.Retention),
		Transactions: transactions,
	}, nil
}

func pendingTransaction(reference ethrpc.TransactionRef, chainID uint64, endpoint string, firstSeen, expires time.Time) (Transaction, error) {
	wire := reference.Transaction
	if wire == nil {
		return Transaction{}, errors.New("transaction object is missing")
	}
	if wire.BlockHash != nil || wire.BlockNumber != nil || wire.TransactionIndex != nil {
		return Transaction{}, errors.New("transaction has a mined block hash, number, or index")
	}
	hashBytes, err := wire.Hash.Bytes()
	if err != nil || len(hashBytes) != 32 {
		return Transaction{}, errors.New("transaction hash is invalid")
	}
	if reference.Hash != "" && !reference.Hash.Equal(wire.Hash) {
		return Transaction{}, errors.New("transaction reference hash does not match the full object")
	}
	from, err := checksumAddress(wire.From)
	if err != nil {
		return Transaction{}, errors.New("transaction sender is invalid")
	}
	var to *string
	if wire.To != nil {
		value, err := checksumAddress(*wire.To)
		if err != nil {
			return Transaction{}, errors.New("transaction recipient is invalid")
		}
		to = &value
	}
	if wire.ChainID != nil {
		actual, err := wire.ChainID.Uint64()
		if err != nil || actual != chainID {
			return Transaction{}, errors.New("transaction chain ID does not match the configured chain")
		}
	}
	nonce, err := decimalQuantity(wire.Nonce)
	if err != nil {
		return Transaction{}, fmt.Errorf("nonce: %w", err)
	}
	value, err := decimalQuantity(wire.Value)
	if err != nil {
		return Transaction{}, fmt.Errorf("value: %w", err)
	}
	gas, err := decimalQuantity(wire.Gas)
	if err != nil {
		return Transaction{}, fmt.Errorf("gas: %w", err)
	}
	gasPrice, err := optionalDecimalQuantity(wire.GasPrice)
	if err != nil {
		return Transaction{}, fmt.Errorf("gas price: %w", err)
	}
	maxFee, err := optionalDecimalQuantity(wire.MaxFeePerGas)
	if err != nil {
		return Transaction{}, fmt.Errorf("max fee per gas: %w", err)
	}
	priorityFee, err := optionalDecimalQuantity(wire.MaxPriorityFeePerGas)
	if err != nil {
		return Transaction{}, fmt.Errorf("max priority fee per gas: %w", err)
	}
	txType, err := optionalDecimalQuantity(wire.Type)
	if err != nil {
		return Transaction{}, fmt.Errorf("type: %w", err)
	}
	inputBytes, err := wire.Input.Bytes()
	if err != nil {
		return Transaction{}, errors.New("transaction input is invalid")
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		return Transaction{}, fmt.Errorf("encode raw transaction: %w", err)
	}
	return Transaction{
		Hash: strings.ToLower(wire.Hash.String()), From: from, To: to,
		Nonce: nonce, Value: value, Gas: gas, GasPrice: gasPrice,
		MaxFeePerGas: maxFee, MaxPriorityFeePerGas: priorityFee, Type: txType,
		Input: ethrpc.DataFromBytes(inputBytes).String(), Raw: raw,
		FirstSeenAt: firstSeen, LastSeenAt: firstSeen, ExpiresAt: expires, Endpoint: endpoint,
	}, nil
}

func decimalQuantity(quantity ethrpc.Quantity) (string, error) {
	value, err := quantity.Big()
	if err != nil || value.Sign() < 0 || value.BitLen() > maximumUint256Bits {
		return "", errors.New("quantity is not an unsigned 256-bit integer")
	}
	return value.String(), nil
}

func optionalDecimalQuantity(quantity *ethrpc.Quantity) (*string, error) {
	if quantity == nil {
		return nil, nil
	}
	value, err := decimalQuantity(*quantity)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func canonicalDecimal(value string) bool {
	if value == "" || len(value) > 78 || len(value) > 1 && value[0] == '0' {
		return false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	parsed, ok := new(big.Int).SetString(value, 10)
	return ok && parsed.Sign() >= 0 && parsed.BitLen() <= maximumUint256Bits
}

func boundedMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return "mempool operation failed"
	}
	if len(message) > 1024 {
		message = message[:1024]
	}
	return message
}

func pollErrorCode(err error) string {
	var sourceErr SourceError
	if errors.As(err, &sourceErr) && sourceErr.Code != "" {
		return sourceErr.Code
	}
	if strings.Contains(err.Error(), "snapshot") {
		return "invalid_snapshot"
	}
	return "storage_or_internal_failure"
}

func stopTimer(timer *time.Timer) {
	if timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}
