package mempool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/ethrpc"
)

const maximumUint256Bits = 256

type Source interface {
	PendingTransactions(context.Context) (json.RawMessage, string, error)
}

type SourceError struct {
	State State
	Code  string
	Cause error
}

func (err SourceError) Error() string {
	if err.Code == "" {
		return "mempool RPC request failed"
	}
	return "mempool RPC request failed: " + err.Code
}

func (err SourceError) Unwrap() error { return err.Cause }

type PoolSource struct{ Pool *ethrpc.Pool }

func (source PoolSource) PendingTransactions(ctx context.Context) (json.RawMessage, string, error) {
	if source.Pool == nil {
		return nil, "", SourceError{State: StateUnavailable, Code: "endpoint_unavailable"}
	}
	endpoint, err := source.Pool.Acquire(ethrpc.PurposeMempool)
	if err != nil {
		return nil, "", SourceError{State: StateUnavailable, Code: "endpoint_unavailable", Cause: err}
	}
	var content json.RawMessage
	err = endpoint.CallContext(ctx, &content, "txpool_content")
	if err != nil {
		source.Pool.ReportFailure(endpoint.Name)
		state, code := StateFailed, "rpc_request_failed"
		if ethrpc.IsMethodNotFound(err) {
			state, code = StateUnavailable, "method_not_supported"
		}
		return nil, endpoint.Name, SourceError{
			State: state,
			Code:  code,
			Cause: ethrpc.SanitizeError(err),
		}
	}
	if len(content) == 0 || strings.EqualFold(strings.TrimSpace(string(content)), "null") {
		source.Pool.ReportFailure(endpoint.Name)
		return nil, endpoint.Name, SourceError{State: StateFailed, Code: "null_snapshot"}
	}
	source.Pool.ReportSuccess(endpoint.Name)
	return content, endpoint.Name, nil
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
	source          Source
	store           Store
	options         PollerOptions
	lastFailureCode string
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
			code := pollErrorCode(err)
			level := slog.LevelWarn
			if code == poller.lastFailureCode {
				level = slog.LevelDebug
			}
			poller.options.Logger.LogAttrs(ctx, level, "mempool poll failed; core synchronization remains active",
				slog.String("event", "mempool_poll_failed"),
				slog.String("component", poller.Name()),
				slog.String("error_code", code),
				slog.String("error_type", fmt.Sprintf("%T", err)),
				slog.Int64("retry_in_ms", poller.options.PollInterval.Milliseconds()),
			)
			poller.lastFailureCode = code
		} else if poller.lastFailureCode != "" && ctx.Err() == nil {
			poller.options.Logger.InfoContext(ctx, "mempool polling recovered",
				"event", "mempool_poll_recovered", "component", poller.Name(),
				"previous_error_code", poller.lastFailureCode,
			)
			poller.lastFailureCode = ""
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
	rawContent, endpoint, err := poller.source.PendingTransactions(ctx)
	observedAt := poller.options.Now().UTC()
	if err != nil {
		failure := sourceFailure(err, endpoint, observedAt)
		if storeErr := poller.store.StoreFailure(ctx, failure); storeErr != nil {
			return fmt.Errorf("persist mempool failure status: %w", storeErr)
		}
		return err
	}
	snapshot, err := buildSnapshot(rawContent, endpoint, poller.options, observedAt)
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
	info, err := poller.store.StoreSnapshot(ctx, snapshot)
	if err != nil {
		return fmt.Errorf("persist mempool snapshot: %w", err)
	}
	poller.options.Logger.DebugContext(ctx, "mempool snapshot committed",
		"event", "mempool_snapshot_committed", "component", poller.Name(),
		"snapshot", slog.GroupValue(
			slog.Int64("id", info.ID),
			slog.String("endpoint", info.Endpoint),
			slog.Int("transaction_count", info.TransactionCount),
		),
	)
	return nil
}

func sourceFailure(err error, endpoint string, observedAt time.Time) Failure {
	state, code, message := StateFailed, "rpc_request_failed", "txpool RPC request failed"
	if sourceErr, ok := errors.AsType[SourceError](err); ok {
		state = sourceErr.State
		code = sourceErr.Code
	}
	switch code {
	case "endpoint_unavailable":
		message = "no HTTP RPC endpoint is available for mempool polling"
	case "method_not_supported":
		message = "txpool_content RPC is not supported by the selected endpoint"
	case "null_snapshot":
		message = "txpool_content RPC returned a null snapshot"
	}
	return Failure{State: state, Endpoint: endpoint, Code: code, Message: message, ObservedAt: observedAt}
}

type pendingTxpoolProjection struct {
	Pending map[string]map[string]json.RawMessage `json:"pending"`
	Queued  map[string]map[string]json.RawMessage `json:"queued"`
}

type pendingTransactionProjection struct {
	Hash                 *common.Hash    `json:"hash"`
	From                 *common.Address `json:"from"`
	BlockHash            *common.Hash    `json:"blockHash"`
	BlockNumber          *hexutil.Big    `json:"blockNumber"`
	TransactionIndex     *hexutil.Uint64 `json:"transactionIndex"`
	ChainID              *hexutil.Big    `json:"chainId"`
	GasPrice             *hexutil.Big    `json:"gasPrice"`
	MaxFeePerGas         *hexutil.Big    `json:"maxFeePerGas"`
	MaxPriorityFeePerGas *hexutil.Big    `json:"maxPriorityFeePerGas"`
	Type                 *hexutil.Uint64 `json:"type"`
}

func buildSnapshot(rawContent json.RawMessage, endpoint string, options PollerOptions, observedAt time.Time) (Snapshot, error) {
	if len(rawContent) == 0 || strings.EqualFold(strings.TrimSpace(string(rawContent)), "null") {
		return Snapshot{}, errors.New("txpool snapshot is null")
	}
	if len(rawContent) > options.MaxResponseBytes {
		return Snapshot{}, fmt.Errorf("txpool snapshot has %d bytes, limit is %d", len(rawContent), options.MaxResponseBytes)
	}
	var content pendingTxpoolProjection
	if err := json.Unmarshal(rawContent, &content); err != nil {
		return Snapshot{}, fmt.Errorf("decode txpool snapshot: %w", err)
	}
	if endpoint == "" || len(endpoint) > 128 {
		return Snapshot{}, errors.New("mempool endpoint name is invalid")
	}
	transactions := collectTxpoolTransactions(content.Pending)
	transactions = append(transactions, collectTxpoolTransactions(content.Queued)...)
	if len(transactions) > options.MaxTransactions {
		return Snapshot{}, fmt.Errorf("txpool snapshot has %d transactions, limit is %d", len(transactions), options.MaxTransactions)
	}
	parsed := make([]Transaction, 0, len(transactions))
	seen := make(map[string]struct{}, len(transactions))
	for index, rawTransaction := range transactions {
		if len(rawTransaction) == 0 || rawTransaction[0] != '{' {
			return Snapshot{}, fmt.Errorf("txpool transaction %d is hash-only", index)
		}
		transaction, err := pendingTransaction(rawTransaction, options.ChainID, endpoint, observedAt, observedAt.Add(options.Retention))
		if err != nil {
			return Snapshot{}, fmt.Errorf("txpool transaction %d: %w", index, err)
		}
		key := strings.ToLower(transaction.Hash)
		if _, duplicate := seen[key]; duplicate {
			return Snapshot{}, fmt.Errorf("txpool transaction %d duplicates hash %s", index, key)
		}
		seen[key] = struct{}{}
		parsed = append(parsed, transaction)
	}
	snapshot := Snapshot{
		Endpoint: endpoint, ObservedAt: observedAt, ExpiresAt: observedAt.Add(options.Retention),
		Transactions: parsed,
	}
	if err := validateSnapshotForStorage(snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func pendingTransaction(raw json.RawMessage, chainID uint64, endpoint string, firstSeen, expires time.Time) (Transaction, error) {
	var projection pendingTransactionProjection
	if err := json.Unmarshal(raw, &projection); err != nil {
		return Transaction{}, fmt.Errorf("decode transaction object: %w", err)
	}
	if projection.Hash == nil || projection.From == nil {
		return Transaction{}, errors.New("transaction hash or sender is missing")
	}
	if projection.BlockHash != nil || projection.BlockNumber != nil || projection.TransactionIndex != nil {
		return Transaction{}, errors.New("transaction has a mined block hash, number, or index")
	}
	var wire types.Transaction
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Transaction{}, fmt.Errorf("decode geth transaction: %w", err)
	}
	if wire.Hash() != *projection.Hash {
		return Transaction{}, errors.New("transaction hash does not match the full object")
	}
	sender, err := types.Sender(types.LatestSignerForChainID(wire.ChainId()), &wire)
	if err != nil || sender != *projection.From {
		return Transaction{}, errors.New("transaction sender is invalid")
	}
	from := checksumAddress(sender)
	var to *string
	if wire.To() != nil {
		value := checksumAddress(*wire.To())
		to = &value
	}
	if projection.ChainID != nil {
		actual := projection.ChainID.ToInt()
		if !actual.IsUint64() || actual.Uint64() != chainID || wire.ChainId().Cmp(actual) != 0 {
			return Transaction{}, errors.New("transaction chain ID does not match the configured chain")
		}
	}
	value, err := decimalQuantity(wire.Value())
	if err != nil {
		return Transaction{}, fmt.Errorf("value: %w", err)
	}
	gasPrice, err := optionalDecimalQuantity(projection.GasPrice)
	if err != nil {
		return Transaction{}, fmt.Errorf("gas price: %w", err)
	}
	maxFee, err := optionalDecimalQuantity(projection.MaxFeePerGas)
	if err != nil {
		return Transaction{}, fmt.Errorf("max fee per gas: %w", err)
	}
	priorityFee, err := optionalDecimalQuantity(projection.MaxPriorityFeePerGas)
	if err != nil {
		return Transaction{}, fmt.Errorf("max priority fee per gas: %w", err)
	}
	txType := optionalUint64Quantity(projection.Type)
	return Transaction{
		Hash: strings.ToLower(wire.Hash().Hex()), From: from, To: to,
		Nonce: strconv.FormatUint(wire.Nonce(), 10), Value: value, Gas: strconv.FormatUint(wire.Gas(), 10), GasPrice: gasPrice,
		MaxFeePerGas: maxFee, MaxPriorityFeePerGas: priorityFee, Type: txType,
		Input: hexutil.Encode(wire.Data()), Raw: append(json.RawMessage(nil), raw...),
		FirstSeenAt: firstSeen, LastSeenAt: firstSeen, ExpiresAt: expires, Endpoint: endpoint,
	}, nil
}

func decimalQuantity(value *big.Int) (string, error) {
	if value == nil || value.Sign() < 0 || value.BitLen() > maximumUint256Bits {
		return "", errors.New("quantity is not an unsigned 256-bit integer")
	}
	return value.String(), nil
}

func optionalDecimalQuantity(quantity *hexutil.Big) (*string, error) {
	if quantity == nil {
		return nil, nil
	}
	value, err := decimalQuantity(quantity.ToInt())
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func optionalUint64Quantity(quantity *hexutil.Uint64) *string {
	if quantity == nil {
		return nil
	}
	value := strconv.FormatUint(uint64(*quantity), 10)
	return &value
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

func collectTxpoolTransactions(pools map[string]map[string]json.RawMessage) []json.RawMessage {
	if len(pools) == 0 {
		return nil
	}
	transactions := make([]json.RawMessage, 0)
	for _, byAddress := range pools {
		for _, byNonce := range byAddress {
			transactions = append(transactions, byNonce)
		}
	}
	return transactions
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
