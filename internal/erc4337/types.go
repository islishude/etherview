package erc4337

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

type InitKind string

const (
	InitNone    InitKind = "none"
	InitFactory InitKind = "factory"
	InitEIP7702 InitKind = "eip7702"
)

type Request struct {
	Sender                        common.Address
	Nonce                         *big.Int
	InitCode                      []byte
	InitKind                      InitKind
	Factory                       *common.Address
	FactoryData                   []byte
	CallData                      []byte
	CallGasLimit                  *big.Int
	VerificationGasLimit          *big.Int
	PreVerificationGas            *big.Int
	MaxFeePerGas                  *big.Int
	MaxPriorityFeePerGas          *big.Int
	Paymaster                     *common.Address
	PaymasterVerificationGasLimit *big.Int
	PaymasterPostOpGasLimit       *big.Int
	PaymasterAndData              []byte
	PaymasterData                 []byte
	PaymasterSignature            []byte
	Signature                     []byte
	AccountGasLimits              []byte
	GasFees                       []byte
	Aggregator                    *common.Address
	AggregatedSignature           []byte
}

type EventKind string

const (
	EventAccountDeployed    EventKind = "account_deployed"
	EventIgnoredInitCode    EventKind = "ignored_init_code"
	EventEIP7702Initialized EventKind = "eip7702_initialized"
	EventExecutionRevert    EventKind = "execution_revert"
	EventPostOpRevert       EventKind = "post_op_revert"
	EventPrefundTooLow      EventKind = "prefund_too_low"
)

type ProtocolEvent struct {
	Kind           EventKind
	LogIndex       uint64
	Sender         common.Address
	Nonce          *big.Int
	RelatedAddress *common.Address
	Paymaster      *common.Address
	RawData        []byte
	Reason         string
	PanicCode      *big.Int
}

type Operation struct {
	Hash             common.Hash
	EntryPoint       common.Address
	Version          Version
	BlockNumber      uint64
	BlockHash        common.Hash
	TransactionHash  common.Hash
	TransactionIndex uint64
	OperationIndex   uint64
	EventLogIndex    uint64
	Bundler          common.Address
	Beneficiary      common.Address
	Request          Request
	Success          bool
	ActualGasCost    *big.Int
	ActualGasUsed    *big.Int
	Events           []ProtocolEvent
}
