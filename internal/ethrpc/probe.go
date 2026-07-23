package ethrpc

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"sort"
	"strings"
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
	// ErrHistoryUnavailable identifies an endpoint that definitively cannot
	// return the configured starting block. Transient transport and upstream
	// failures do not match this sentinel because their capability remains
	// unknown and may recover without operator action.
	ErrHistoryUnavailable = errors.New("RPC history is unavailable at the configured start block")
	// ErrHistoryPruned is the narrower classification for an explicit JSON-RPC
	// response stating that historical data was pruned. Every pruned error also
	// matches ErrHistoryUnavailable.
	ErrHistoryPruned = errors.New("RPC history was pruned at the configured start block")
)

type HistoryUnavailableKind string

const (
	HistoryUnavailableResult HistoryUnavailableKind = "history_unavailable"
	HistoryPrunedResult      HistoryUnavailableKind = "history_pruned"
)

// HistoryUnavailableError is a stable, credential-free capability result.
// It intentionally does not retain the upstream error: RPC messages are an
// untrusted boundary and can contain endpoint credentials or provider data.
type HistoryUnavailableError struct {
	Kind       HistoryUnavailableKind
	StartBlock Quantity
}

func (e *HistoryUnavailableError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Kind == HistoryPrunedResult {
		return fmt.Sprintf("%s: %s", ErrHistoryPruned, e.StartBlock)
	}
	return fmt.Sprintf("%s: %s", ErrHistoryUnavailable, e.StartBlock)
}

func (e *HistoryUnavailableError) Is(target error) bool {
	if e == nil {
		return false
	}
	return target == ErrHistoryUnavailable || target == ErrHistoryPruned && e.Kind == HistoryPrunedResult
}

type CapabilityReport struct {
	ChainID            string
	GenesisHash        Hash
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
	GenesisHash Hash
}

type ProbeOptions struct {
	Expected   *ChainIdentity
	StartBlock Quantity
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
	if options.StartBlock == "" {
		options.StartBlock = QuantityFromUint64(0)
	}
	report := CapabilityReport{Methods: make(map[string]Availability)}
	var chainID Quantity
	if err := endpoint.Client.Call(ctx, "eth_chainId", nil, &chainID); err != nil {
		return report, fmt.Errorf("probe eth_chainId on %q: %w", endpoint.Name, err)
	}
	chainIDBig, err := chainID.Big()
	if err != nil {
		return report, fmt.Errorf("probe eth_chainId on %q: %w", endpoint.Name, err)
	}
	report.ChainID = chainIDBig.String()
	var genesis *probeBlock
	genesisErr := endpoint.Client.Call(ctx, "eth_getBlockByNumber", []any{"0x0", false}, &genesis)
	if genesisErr != nil {
		if options.StartBlock == QuantityFromUint64(0) && isExplicitHistoryPruned(genesisErr) {
			issue := setHistoryUnavailable(&report, options.StartBlock, HistoryPrunedResult)
			return report, issue
		}
		return report, fmt.Errorf("probe genesis block on %q: %w", endpoint.Name, genesisErr)
	}
	if genesis == nil || genesis.Hash == nil {
		if options.StartBlock == QuantityFromUint64(0) {
			issue := setHistoryUnavailable(&report, options.StartBlock, HistoryUnavailableResult)
			return report, issue
		}
		return report, fmt.Errorf("probe genesis block on %q: result is null or has no hash", endpoint.Name)
	}
	report.GenesisHash = *genesis.Hash
	if options.Expected != nil {
		if options.Expected.ChainID != "" && options.Expected.ChainID != report.ChainID {
			return report, &IdentityMismatchError{Field: "chain_id", Expected: options.Expected.ChainID, Actual: report.ChainID}
		}
		if options.Expected.GenesisHash != "" && !options.Expected.GenesisHash.Equal(report.GenesisHash) {
			return report, &IdentityMismatchError{Field: "genesis_hash", Expected: options.Expected.GenesisHash.String(), Actual: report.GenesisHash.String()}
		}
	}

	if endpoint.Supports(PurposeHead) {
		probeBlockTag(ctx, endpoint.Client, "safe", CapabilitySafeTag, &report)
		probeBlockTag(ctx, endpoint.Client, "finalized", CapabilityFinalizedTag, &report)
	}
	if endpoint.Supports(PurposeHead) || endpoint.Supports(PurposeHistory) {
		probeHistoricalBlock(ctx, endpoint.Client, options.StartBlock, &report)
		probeBlockReceipts(ctx, endpoint.Client, options.StartBlock, &report)
	}
	if endpoint.Supports(PurposeState) {
		probeHistoricalState(ctx, endpoint.Client, options.StartBlock, &report)
	}
	if endpoint.Supports(PurposeTrace) || endpoint.Supports(PurposeMempool) {
		probeModules(ctx, endpoint.Client, &report)
	}
	return report, nil
}

type probeBlock struct {
	Hash   *Hash     `json:"hash"`
	Number *Quantity `json:"number"`
}

func probeBlockTag(ctx context.Context, caller Caller, tag, capability string, report *CapabilityReport) {
	var block *probeBlock
	err := caller.Call(ctx, "eth_getBlockByNumber", []any{tag, false}, &block)
	switch {
	case err == nil && block != nil && block.Hash != nil:
		report.Methods[capability] = AvailabilityAvailable
	case err == nil:
		report.Methods[capability] = AvailabilityUnavailable
	case IsMethodNotFound(err):
		report.Methods[capability] = AvailabilityUnavailable
	default:
		report.Methods[capability] = AvailabilityUnknown
		report.Warnings = append(report.Warnings, fmt.Sprintf("%s probe failed: %v", capability, err))
	}
}

func probeHistoricalBlock(ctx context.Context, caller Caller, start Quantity, report *CapabilityReport) {
	var block *probeBlock
	err := caller.Call(ctx, "eth_getBlockByNumber", []any{start.String(), false}, &block)
	switch {
	case err == nil && block != nil && block.Hash != nil:
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
	start Quantity,
	kind HistoryUnavailableKind,
) *HistoryUnavailableError {
	report.Methods[CapabilityHistoricalData] = AvailabilityUnavailable
	issue := &HistoryUnavailableError{Kind: kind, StartBlock: start}
	report.HistoryUnavailable = issue
	return issue
}

func isExplicitHistoryPruned(err error) bool {
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr == nil {
		return false
	}
	return strings.Contains(strings.ToLower(rpcErr.Message), "pruned")
}

func probeBlockReceipts(ctx context.Context, caller Caller, start Quantity, report *CapabilityReport) {
	var receipts []Receipt
	err := caller.Call(ctx, CapabilityBlockReceipts, []any{start.String()}, &receipts)
	switch err {
	case nil:
		report.Methods[CapabilityBlockReceipts] = AvailabilityAvailable
	default:
		if IsMethodNotFound(err) {
			report.Methods[CapabilityBlockReceipts] = AvailabilityUnavailable
		} else {
			report.Methods[CapabilityBlockReceipts] = AvailabilityUnknown
			report.Warnings = append(report.Warnings, fmt.Sprintf("block receipt probe failed: %v", err))
		}
	}
}

func probeHistoricalState(ctx context.Context, caller Caller, start Quantity, report *CapabilityReport) {
	var balance Quantity
	err := caller.Call(ctx, "eth_getBalance", []any{"0x0000000000000000000000000000000000000000", start.String()}, &balance)
	switch err {
	case nil:
		report.Methods[CapabilityHistoricalState] = AvailabilityAvailable
	default:
		report.Methods[CapabilityHistoricalState] = AvailabilityUnavailable
		report.Warnings = append(report.Warnings, fmt.Sprintf("historical state probe failed: %v", err))
	}
}

func probeModules(ctx context.Context, caller Caller, report *CapabilityReport) {
	var modules map[string]string
	err := caller.Call(ctx, "rpc_modules", nil, &modules)
	if err != nil {
		report.Methods[CapabilityDebugTrace] = AvailabilityUnknown
		report.Methods[CapabilityParityTrace] = AvailabilityUnknown
		report.Methods[CapabilityTxPool] = AvailabilityUnknown
		report.Warnings = append(report.Warnings, fmt.Sprintf("rpc_modules probe failed: %v", err))
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

func NormalizeChainID(value string) (string, error) {
	if value == "" {
		return "", errors.New("chain ID is empty")
	}
	if strings.HasPrefix(value, "0x") {
		quantity, err := ParseQuantity(value)
		if err != nil {
			return "", err
		}
		integer, err := quantity.Big()
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
