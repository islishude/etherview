// Package catalog exposes validated, read-only views of enrichment data.
// PostgreSQL remains the source of truth; callers never infer optional-stage
// availability from an empty result set.
package catalog

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound      = errors.New("catalog resource not found")
	ErrUnavailable   = errors.New("catalog stage unavailable")
	ErrInvalidCursor = errors.New("catalog cursor is invalid or stale")
	ErrInvalidInput  = errors.New("catalog input is invalid")
	ErrCorruptData   = errors.New("catalog data is inconsistent")
	ErrLimitExceeded = errors.New("catalog result exceeds configured limit")
)

type Stage string

const (
	StageCore  Stage = "core"
	StageToken Stage = "token"
	StageStats Stage = "stats"
	StageTrace Stage = "trace"
)

func (stage Stage) Version() int {
	switch stage {
	case StageStats:
		return 2
	case StageCore, StageToken, StageTrace:
		return 1
	default:
		return 0
	}
}

type StageState string

const (
	StageComplete    StageState = "complete"
	StageMissing     StageState = "missing"
	StageUnavailable StageState = "unavailable"
	StageFailed      StageState = "failed"
)

// StageUnavailableError gives API adapters a stable machine-readable reason
// without exposing worker or RPC error text stored in PostgreSQL.
type StageUnavailableError struct {
	Stage       Stage
	State       StageState
	BlockNumber string
	BlockHash   string
}

func (err StageUnavailableError) Error() string { return "catalog stage unavailable" }
func (err StageUnavailableError) Unwrap() error { return ErrUnavailable }

type Snapshot struct {
	ChainID     string
	BlockNumber string
	BlockHash   string
}

type TokenContract struct {
	ChainID             string
	Address             string
	CodeHash            string
	Standard            string
	Confidence          string
	Name                *string
	Symbol              *string
	Decimals            *uint8
	TotalSupply         *string
	MetadataState       string
	ObservedBlockNumber string
	ObservedBlockHash   string
	UpdatedAt           time.Time
}

type TokenListRequest struct {
	ChainID string
	Cursor  string
	Limit   int
}

type TokenPage struct {
	Items      []TokenContract
	NextCursor string
	Snapshot   Snapshot
}

type TokenEvent struct {
	ChainID         string
	BlockNumber     string
	BlockHash       string
	LogIndex        string
	SubIndex        string
	TransactionHash string
	TokenAddress    string
	Standard        string
	Kind            string
	Operator        *string
	From            *string
	To              *string
	TokenID         *string
	Amount          *string
	Confidence      string
}

type TokenEventRequest struct {
	ChainID      string
	TokenAddress string
	Cursor       string
	Limit        int
}

type TokenEventPage struct {
	Items      []TokenEvent
	NextCursor string
	Snapshot   Snapshot
}

type NFTOwnership struct {
	ChainID      string
	TokenAddress string
	TokenID      string
	Owner        string
	Balance      string
	Confidence   string
	Snapshot     Snapshot
}

type NFTBalance struct {
	ChainID      string
	Owner        string
	TokenAddress string
	TokenID      string
	Balance      string
	Confidence   string
}

const NFTStateConfidenceRPCExact = "rpc_exact"

type NFTOwnerObservation struct {
	Exists     bool
	Owner      string
	Confidence string
}

type NFTBalanceCandidate struct {
	Standard     string
	TokenAddress string
	TokenID      string
}

type NFTBalanceObservation struct {
	Balance    string
	Confidence string
}

// NFTStateReconciler promotes event-derived candidates only after exact
// ownerOf/balanceOf observations at the supplied canonical block hash.
type NFTStateReconciler interface {
	Owner(context.Context, Snapshot, string, string) (NFTOwnerObservation, error)
	Balances(context.Context, Snapshot, string, []NFTBalanceCandidate) ([]NFTBalanceObservation, error)
}

type NFTBalanceRequest struct {
	ChainID string
	Owner   string
	Cursor  string
	Limit   int
}

type NFTBalancePage struct {
	Items      []NFTBalance
	NextCursor string
	Snapshot   Snapshot
}

type BlockStatsRequest struct {
	ChainID   string
	FromBlock string
	ToBlock   string
}

type BlockStat struct {
	ChainID               string
	BlockNumber           string
	BlockHash             string
	TransactionCount      string
	GasUsed               string
	GasLimit              string
	BaseFeePerGas         *string
	BlobGasUsed           *string
	ExcessBlobGas         *string
	BlobBaseFeePerGas     *string
	BurnedWei             *string
	BlobBurnedWei         *string
	BlockTimestamp        string
	BlockIntervalSeconds  *string
	TransactionsPerSecond *string
	TokenEventCount       string
	TokenTransferCount    string
	NFTTransferCount      string
	ComputedAt            time.Time
}

type AggregateStatsRequest struct {
	ChainID   string
	FromBlock string
	ToBlock   string
}

type AggregateStats struct {
	ChainID            string
	FromBlock          string
	ToBlock            string
	Snapshot           Snapshot
	BlockCount         string
	TransactionCount   string
	GasUsed            string
	BurnedWei          string
	BlobBurnedWei      string
	TokenEventCount    string
	TokenTransferCount string
	NFTTransferCount   string
	AverageTPS         *string
	CoreComplete       bool
	StatsComplete      bool
	TokenComplete      bool
}

type TraceFrame struct {
	Path           []uint32
	ParentPath     []uint32
	Depth          uint32
	CallType       string
	From           *string
	To             *string
	CreatedAddress *string
	Value          *string
	Gas            *string
	GasUsed        *string
	Input          *string
	Output         *string
	Error          *string
	Reverted       bool
}

type TransactionTrace struct {
	ChainID          string
	BlockNumber      string
	BlockHash        string
	TransactionHash  string
	TransactionIndex string
	State            StageState
	Frames           []TraceFrame
}

type Reader interface {
	TokenContract(context.Context, string, string) (TokenContract, error)
	TokenContracts(context.Context, TokenListRequest) (TokenPage, error)
	TokenEvents(context.Context, TokenEventRequest) (TokenEventPage, error)
	NFTOwner(context.Context, string, string, string) (NFTOwnership, error)
	NFTBalances(context.Context, NFTBalanceRequest) (NFTBalancePage, error)
	BlockStats(context.Context, BlockStatsRequest) ([]BlockStat, error)
	AggregateStats(context.Context, AggregateStatsRequest) (AggregateStats, error)
	TransactionTrace(context.Context, string, string) (TransactionTrace, error)
}
