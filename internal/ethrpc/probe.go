package ethrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"sort"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
)

type Availability string

const (
	AvailabilityUnknown     Availability = "unknown"
	AvailabilityAvailable   Availability = "available"
	AvailabilityUnavailable Availability = "unavailable"
)

const (
	CapabilityBlockReceipts   = "eth_getBlockReceipts"
	CapabilitySafeTag         = "safe_tag"
	CapabilityFinalizedTag    = "finalized_tag"
	CapabilityHistoricalData  = "historical_blocks"
	CapabilityHistoricalState = "historical_state"
	CapabilityDebugTrace      = "debug_trace"
	CapabilityParityTrace     = "trace_module"
	CapabilityTxPool          = "txpool_module"
)

var (
	ErrHistoryUnavailable = errors.New("RPC history is unavailable at the configured start block")
	ErrHistoryPruned      = errors.New("RPC history was pruned at the configured start block")
)

type HistoryUnavailableKind string

const (
	HistoryUnavailableResult HistoryUnavailableKind = "history_unavailable"
	HistoryPrunedResult      HistoryUnavailableKind = "history_pruned"
)

// HistoryUnavailableError is a stable, credential-free capability result. It
// intentionally does not retain the untrusted upstream error.
type HistoryUnavailableError struct {
	Kind       HistoryUnavailableKind
	StartBlock uint64
}

func (e *HistoryUnavailableError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Kind == HistoryPrunedResult {
		return fmt.Sprintf("%s: %s", ErrHistoryPruned, hexutil.EncodeUint64(e.StartBlock))
	}
	return fmt.Sprintf("%s: %s", ErrHistoryUnavailable, hexutil.EncodeUint64(e.StartBlock))
}

func (e *HistoryUnavailableError) Is(target error) bool {
	if e == nil {
		return false
	}
	return target == ErrHistoryUnavailable || target == ErrHistoryPruned && e.Kind == HistoryPrunedResult
}

type CapabilityReport struct {
	ChainID            string
	GenesisHash        common.Hash
	Methods            map[string]Availability
	HistoryUnavailable *HistoryUnavailableError
	Warnings           []string
}

func (r CapabilityReport) Clone() CapabilityReport {
	copy := r
	copy.Methods = make(map[string]Availability, len(r.Methods))
	maps.Copy(copy.Methods, r.Methods)
	if r.HistoryUnavailable != nil {
		historyUnavailable := *r.HistoryUnavailable
		copy.HistoryUnavailable = &historyUnavailable
	}
	copy.Warnings = append([]string(nil), r.Warnings...)
	return copy
}

func (r CapabilityReport) Status(capability string) Availability {
	if status, exists := r.Methods[capability]; exists {
		return status
	}
	return AvailabilityUnknown
}

type ChainIdentity struct {
	ChainID     string
	GenesisHash common.Hash
}

type ProbeOptions struct {
	Expected   *ChainIdentity
	StartBlock uint64
}

type IdentityMismatchError struct {
	Field    string
	Expected string
	Actual   string
}

func (e *IdentityMismatchError) Error() string {
	return fmt.Sprintf("RPC chain identity mismatch for %s: expected %s, got %s", e.Field, e.Expected, e.Actual)
}

func ProbeEndpoint(ctx context.Context, endpoint *Endpoint, options ProbeOptions) (CapabilityReport, error) {
	if endpoint == nil || endpoint.Client == nil {
		return CapabilityReport{}, errors.New("cannot probe a nil RPC endpoint")
	}
	report := CapabilityReport{Methods: make(map[string]Availability)}
	var chainID hexutil.Big
	if err := endpoint.CallContext(ctx, &chainID, "eth_chainId"); err != nil {
		return report, fmt.Errorf("probe eth_chainId on %q: %w", endpoint.Name, SanitizeError(err))
	}
	report.ChainID = (*big.Int)(&chainID).String()
	var genesis *types.Header
	genesisErr := endpoint.CallContext(ctx, &genesis, "eth_getBlockByNumber", "0x0", false)
	if genesisErr != nil {
		if options.StartBlock == 0 && isExplicitHistoryPruned(genesisErr) {
			issue := setHistoryUnavailable(&report, options.StartBlock, HistoryPrunedResult)
			return report, issue
		}
		return report, fmt.Errorf("probe genesis block on %q: %w", endpoint.Name, SanitizeError(genesisErr))
	}
	if genesis == nil {
		if options.StartBlock == 0 {
			issue := setHistoryUnavailable(&report, options.StartBlock, HistoryUnavailableResult)
			return report, issue
		}
		return report, fmt.Errorf("probe genesis block on %q: result is null", endpoint.Name)
	}
	report.GenesisHash = genesis.Hash()
	if options.Expected != nil {
		if options.Expected.ChainID != "" && options.Expected.ChainID != report.ChainID {
			return report, &IdentityMismatchError{Field: "chain_id", Expected: options.Expected.ChainID, Actual: report.ChainID}
		}
		if options.Expected.GenesisHash != (common.Hash{}) && options.Expected.GenesisHash != report.GenesisHash {
			return report, &IdentityMismatchError{
				Field:    "genesis_hash",
				Expected: options.Expected.GenesisHash.Hex(),
				Actual:   report.GenesisHash.Hex(),
			}
		}
	}

	if endpoint.Supports(PurposeHead) {
		probeBlockTag(ctx, endpoint, "safe", CapabilitySafeTag, &report)
		probeBlockTag(ctx, endpoint, "finalized", CapabilityFinalizedTag, &report)
	}
	if endpoint.Supports(PurposeHead) || endpoint.Supports(PurposeHistory) {
		probeHistoricalBlock(ctx, endpoint, options.StartBlock, &report)
		probeBlockReceipts(ctx, endpoint, options.StartBlock, &report)
	}
	if endpoint.Supports(PurposeState) {
		probeHistoricalState(ctx, endpoint, options.StartBlock, &report)
	}
	if endpoint.Supports(PurposeTrace) || endpoint.Supports(PurposeMempool) {
		probeModules(ctx, endpoint, &report)
	}
	if endpoint.Supports(PurposeTrace) {
		probeDebugTraceBlockByHash(ctx, endpoint, &report)
	}
	return report, nil
}

func probeBlockTag(ctx context.Context, endpoint *Endpoint, tag, capability string, report *CapabilityReport) {
	var header *types.Header
	err := endpoint.CallContext(ctx, &header, "eth_getBlockByNumber", tag, false)
	switch {
	case err == nil && header != nil:
		report.Methods[capability] = AvailabilityAvailable
	case err == nil:
		report.Methods[capability] = AvailabilityUnavailable
	case IsMethodNotFound(err):
		report.Methods[capability] = AvailabilityUnavailable
	default:
		report.Methods[capability] = AvailabilityUnknown
		report.Warnings = append(report.Warnings, capability+" probe failed")
	}
}

func probeHistoricalBlock(ctx context.Context, endpoint *Endpoint, start uint64, report *CapabilityReport) {
	var header *types.Header
	err := endpoint.CallContext(ctx, &header, "eth_getBlockByNumber", hexutil.EncodeUint64(start), false)
	switch {
	case err == nil && header != nil:
		report.Methods[CapabilityHistoricalData] = AvailabilityAvailable
	case err == nil:
		_ = setHistoryUnavailable(report, start, HistoryUnavailableResult)
	case isExplicitHistoryPruned(err):
		_ = setHistoryUnavailable(report, start, HistoryPrunedResult)
	default:
		report.Methods[CapabilityHistoricalData] = AvailabilityUnknown
		report.Warnings = append(report.Warnings, "historical block probe returned an indeterminate error")
	}
}

func setHistoryUnavailable(
	report *CapabilityReport,
	start uint64,
	kind HistoryUnavailableKind,
) *HistoryUnavailableError {
	report.Methods[CapabilityHistoricalData] = AvailabilityUnavailable
	issue := &HistoryUnavailableError{Kind: kind, StartBlock: start}
	report.HistoryUnavailable = issue
	return issue
}

func isExplicitHistoryPruned(err error) bool {
	var rpcErr rpc.Error
	if !errors.As(err, &rpcErr) || rpcErr == nil {
		return false
	}
	return strings.Contains(strings.ToLower(rpcErr.Error()), "pruned")
}

func probeBlockReceipts(ctx context.Context, endpoint *Endpoint, start uint64, report *CapabilityReport) {
	var receipts types.Receipts
	err := endpoint.CallContext(ctx, &receipts, CapabilityBlockReceipts, hexutil.EncodeUint64(start))
	switch err {
	case nil:
		report.Methods[CapabilityBlockReceipts] = AvailabilityAvailable
	default:
		if IsMethodNotFound(err) {
			report.Methods[CapabilityBlockReceipts] = AvailabilityUnavailable
		} else {
			report.Methods[CapabilityBlockReceipts] = AvailabilityUnknown
			report.Warnings = append(report.Warnings, "block receipt probe failed")
		}
	}
}

func probeHistoricalState(ctx context.Context, endpoint *Endpoint, start uint64, report *CapabilityReport) {
	var balance hexutil.Big
	err := endpoint.CallContext(
		ctx,
		&balance,
		"eth_getBalance",
		common.Address{}.Hex(),
		hexutil.EncodeUint64(start),
	)
	switch err {
	case nil:
		report.Methods[CapabilityHistoricalState] = AvailabilityAvailable
	default:
		report.Methods[CapabilityHistoricalState] = AvailabilityUnavailable
		report.Warnings = append(report.Warnings, "historical state probe failed")
	}
}

func probeModules(ctx context.Context, endpoint *Endpoint, report *CapabilityReport) {
	var modules map[string]string
	err := endpoint.CallContext(ctx, &modules, "rpc_modules")
	if err != nil {
		report.Methods[CapabilityDebugTrace] = AvailabilityUnknown
		report.Methods[CapabilityParityTrace] = AvailabilityUnknown
		report.Methods[CapabilityTxPool] = AvailabilityUnknown
		report.Warnings = append(report.Warnings, "rpc_modules probe failed")
		return
	}
	for capability, module := range map[string]string{
		CapabilityDebugTrace:  "debug",
		CapabilityParityTrace: "trace",
		CapabilityTxPool:      "txpool",
	} {
		if _, exists := modules[module]; exists {
			report.Methods[capability] = AvailabilityAvailable
		} else {
			report.Methods[capability] = AvailabilityUnavailable
		}
	}
}

func probeDebugTraceBlockByHash(ctx context.Context, endpoint *Endpoint, report *CapabilityReport) {
	var result json.RawMessage
	err := endpoint.CallContext(
		ctx,
		&result,
		"debug_traceBlockByHash",
		report.GenesisHash,
		map[string]any{},
	)
	switch {
	case err == nil:
		report.Methods[CapabilityDebugTrace] = AvailabilityAvailable
	case IsMethodNotFound(err):
		report.Methods[CapabilityDebugTrace] = AvailabilityUnavailable
	default:
		report.Methods[CapabilityDebugTrace] = AvailabilityUnknown
		report.Warnings = append(report.Warnings, "debug_traceBlockByHash probe failed")
	}
}

func NormalizeChainID(value string) (string, error) {
	if value == "" {
		return "", errors.New("chain ID is empty")
	}
	if strings.HasPrefix(value, "0x") {
		integer, err := ParseQuantity(value)
		if err != nil {
			return "", err
		}
		return integer.String(), nil
	}
	integer, ok := new(big.Int).SetString(value, 10)
	if !ok || integer.Sign() < 0 {
		return "", fmt.Errorf("invalid decimal chain ID %q", value)
	}
	return integer.String(), nil
}

func SortedCapabilities(report CapabilityReport) []string {
	capabilities := make([]string, 0, len(report.Methods))
	for capability := range report.Methods {
		capabilities = append(capabilities, capability)
	}
	sort.Strings(capabilities)
	return capabilities
}

// SanitizeError removes provider-controlled message, data, body, and URL text
// while retaining only stable local categories needed by callers.
func SanitizeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if status, ok := errors.AsType[rpc.HTTPError](err); ok {
		return fmt.Errorf("JSON-RPC HTTP status %d", status.StatusCode)
	}
	if rpcErr, ok := errors.AsType[rpc.Error](err); ok {
		return fmt.Errorf("JSON-RPC error code %d", rpcErr.ErrorCode())
	}
	if errors.Is(err, ErrInvalidResponse) {
		return ErrInvalidResponse
	}
	if errors.Is(err, ErrResponseTooLarge) {
		return ErrResponseTooLarge
	}
	return ErrTransport
}
