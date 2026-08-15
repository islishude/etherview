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
	ErrNotApplicable = errors.New("catalog resource is not applicable")
	ErrCorruptData   = errors.New("catalog data is inconsistent")
	ErrLimitExceeded = errors.New("catalog result exceeds configured limit")
)

type Stage string

const (
	StageCore      Stage = "core"
	StageToken     Stage = "token"
	StageStats     Stage = "stats"
	StageTrace     Stage = "trace"
	StageStateDiff Stage = "state_diff"
)

func (stage Stage) Version() int {
	switch stage {
	case StageStats:
		return 3
	case StageTrace:
		return 3
	case StageStateDiff:
		return 3
	case StageCore, StageToken:
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
	Decimals        *uint8
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

type AddressActivityRequest struct {
	ChainID string
	Address string
	Cursor  string
	Limit   int
}

type AddressInternalTransaction struct {
	BlockNumber      string
	BlockHash        string
	BlockTimestamp   time.Time
	TransactionHash  string
	TransactionIndex string
	Path             []uint32
	Depth            uint32
	CallType         string
	From             *string
	To               *string
	CreatedAddress   *string
	Value            *string
	Gas              *string
	GasUsed          *string
	Input            *string
	Error            *string
	Reverted         bool
}

type AddressInternalTransactionPage struct {
	Items      []AddressInternalTransaction
	NextCursor string
	Snapshot   Snapshot
}

type AddressTokenTransfer struct {
	BlockNumber      string
	BlockHash        string
	BlockTimestamp   time.Time
	TransactionHash  string
	TransactionIndex string
	LogIndex         string
	SubIndex         string
	TokenAddress     string
	Standard         string
	Kind             string
	From             *string
	To               *string
	TokenID          *string
	Amount           *string
	Decimals         *uint8
	Confidence       string
}

type AddressTokenTransferPage struct {
	Items      []AddressTokenTransfer
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

type ERC20Balance struct {
	ChainID      string
	Owner        string
	TokenAddress string
	Balance      string
	Confidence   string
	Name         *string
	Symbol       *string
	Decimals     *uint8
}

type ERC20BalanceCandidate struct {
	TokenAddress string
}

type ERC20BalanceObservation struct {
	Balance    string
	Confidence string
}

type ERC20StateReconciler interface {
	ERC20Balances(context.Context, Snapshot, string, []ERC20BalanceCandidate) ([]ERC20BalanceObservation, error)
}

type ERC20BalanceRequest struct {
	ChainID string
	Owner   string
	Cursor  string
	Limit   int
}

type ERC20BalancePage struct {
	Items      []ERC20Balance
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
	DirectReverted bool
	Reverted       bool
	Execution      *TraceExecution
	Decoding       *TraceCallDecoding
}

type TraceExecution struct {
	ContextAddress string
	Address        string
	CodeHash       string
	Resolution     string
}

type ABIValue struct {
	Name  string
	Type  string
	Value any
}

type TransactionCalldataParameter struct {
	Name         string
	Type         string
	InternalType string
	Components   []TransactionCalldataParameter
}

type TransactionCalldataInput struct {
	Name         string
	Type         string
	Value        any
	InternalType string
	Components   []TransactionCalldataParameter
}

type ABISource struct {
	Kind     string
	Address  string
	CodeHash string
}

type TransactionLogABISource = ABISource

type TraceCallDecoding struct {
	Kind         string
	Status       string
	FunctionName string
	Signature    string
	Inputs       []ABIValue
	OutputStatus string
	Outputs      []ABIValue
	Revert       *TraceRevertDecoding
	Candidates   []string
	ABISource    *ABISource
	Confidence   string
	Warning      string
}

type TraceRevertDecoding struct {
	Status     string
	ErrorName  string
	Signature  string
	Arguments  []ABIValue
	Candidates []string
	ABISource  *ABISource
	Confidence string
	Warning    string
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

type TransactionCalldata struct {
	Identity  TransactionResourceIdentity
	Input     string
	Execution TraceExecution
	Decoding  TransactionCalldataDecoding
}

type TransactionCalldataDecoding struct {
	Status       string
	FunctionName string
	Signature    string
	Inputs       []TransactionCalldataInput
	Candidates   []string
	ABISource    *ABISource
	Confidence   string
	Warning      string
}

type TransactionResourceRequest struct {
	ChainID         string
	TransactionHash string
	Cursor          string
	Limit           int
}

type TransactionResourceIdentity struct {
	ChainID          string
	BlockNumber      string
	BlockHash        string
	TransactionHash  string
	TransactionIndex string
	State            StageState
}

type TransactionLog struct {
	Address  string
	LogIndex string
	Topics   []string
	Data     string
	Decoding TransactionLogDecoding
}

type TransactionLogDecoding struct {
	Status      string
	EventName   string
	Signature   string
	Arguments   []TransactionLogArgument
	Candidates  []string
	ABISource   *ABISource
	Attribution TransactionLogAttribution
	Confidence  string
	Warning     string
}

type TransactionLogArgument struct {
	Name    string
	Type    string
	Indexed bool
	Hashed  bool
	Value   any
}

type TransactionLogAttribution struct {
	Mode             string
	TracePath        []uint32
	ExecutionAddress string
}

type TransactionLogPage struct {
	Identity   TransactionResourceIdentity
	Items      []TransactionLog
	NextCursor string
}

type TransactionTokenEventPage struct {
	Identity   TransactionResourceIdentity
	Items      []TokenEvent
	NextCursor string
}

type TransactionInternalTransaction struct {
	Path           []uint32
	Depth          uint32
	CallType       string
	From           string
	To             *string
	CreatedAddress *string
	Value          string
}

type TransactionInternalTransactionPage struct {
	Identity   TransactionResourceIdentity
	Items      []TransactionInternalTransaction
	NextCursor string
}

type TransactionStateChange struct {
	Address    string
	Kind       string
	StorageKey *string
	Before     *string
	After      *string
}

type TransactionStateChangePage struct {
	Identity   TransactionResourceIdentity
	Items      []TransactionStateChange
	NextCursor string
}

type EIP7702Authorization struct {
	Index             string
	ChainID           string
	Nonce             string
	Delegate          string
	YParity           int
	R                 string
	S                 string
	Authority         *string
	SignatureStatus   string
	ApplicationStatus string
	SkipReason        *string
}

type TransactionAuthorizationPage struct {
	Identity   TransactionResourceIdentity
	Items      []EIP7702Authorization
	NextCursor string
}

type AddressDelegationRequest struct {
	ChainID string
	Address string
	Cursor  string
	Limit   int
}

type DelegationHistoryItem struct {
	Authority          string
	Kind               string
	Delegate           string
	PreviousDelegate   *string
	BlockNumber        string
	BlockHash          string
	TransactionHash    string
	TransactionIndex   string
	AuthorizationIndex string
}

type DelegationHistoryPage struct {
	Items      []DelegationHistoryItem
	NextCursor string
	Snapshot   Snapshot
}

type Reader interface {
	TokenContract(context.Context, string, string) (TokenContract, error)
	TokenContracts(context.Context, TokenListRequest) (TokenPage, error)
	TokenEvents(context.Context, TokenEventRequest) (TokenEventPage, error)
	NFTOwner(context.Context, string, string, string) (NFTOwnership, error)
	NFTBalances(context.Context, NFTBalanceRequest) (NFTBalancePage, error)
	ERC20Balances(context.Context, ERC20BalanceRequest) (ERC20BalancePage, error)
	BlockStats(context.Context, BlockStatsRequest) ([]BlockStat, error)
	AggregateStats(context.Context, AggregateStatsRequest) (AggregateStats, error)
	TransactionTrace(context.Context, string, string) (TransactionTrace, error)
	TransactionCalldata(context.Context, string, string) (TransactionCalldata, error)
	TransactionInternalTransactions(context.Context, TransactionResourceRequest) (TransactionInternalTransactionPage, error)
	TransactionTokenEvents(context.Context, TransactionResourceRequest) (TransactionTokenEventPage, error)
	TransactionLogs(context.Context, TransactionResourceRequest) (TransactionLogPage, error)
	TransactionStateChanges(context.Context, TransactionResourceRequest) (TransactionStateChangePage, error)
}
