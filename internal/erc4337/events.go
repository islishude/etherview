package erc4337

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"unicode/utf8"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

const entryPointEventABI = `[
  {"type":"event","name":"UserOperationEvent","anonymous":false,"inputs":[
    {"name":"userOpHash","type":"bytes32","indexed":true},
    {"name":"sender","type":"address","indexed":true},
    {"name":"paymaster","type":"address","indexed":true},
    {"name":"nonce","type":"uint256","indexed":false},
    {"name":"success","type":"bool","indexed":false},
    {"name":"actualGasCost","type":"uint256","indexed":false},
    {"name":"actualGasUsed","type":"uint256","indexed":false}
  ]},
  {"type":"event","name":"AccountDeployed","anonymous":false,"inputs":[
    {"name":"userOpHash","type":"bytes32","indexed":true},
    {"name":"sender","type":"address","indexed":true},
    {"name":"factory","type":"address","indexed":false},
    {"name":"paymaster","type":"address","indexed":false}
  ]},
  {"type":"event","name":"UserOperationRevertReason","anonymous":false,"inputs":[
    {"name":"userOpHash","type":"bytes32","indexed":true},
    {"name":"sender","type":"address","indexed":true},
    {"name":"nonce","type":"uint256","indexed":false},
    {"name":"revertReason","type":"bytes","indexed":false}
  ]},
  {"type":"event","name":"PostOpRevertReason","anonymous":false,"inputs":[
    {"name":"userOpHash","type":"bytes32","indexed":true},
    {"name":"sender","type":"address","indexed":true},
    {"name":"nonce","type":"uint256","indexed":false},
    {"name":"revertReason","type":"bytes","indexed":false}
  ]},
  {"type":"event","name":"UserOperationPrefundTooLow","anonymous":false,"inputs":[
    {"name":"userOpHash","type":"bytes32","indexed":true},
    {"name":"sender","type":"address","indexed":true},
    {"name":"nonce","type":"uint256","indexed":false}
  ]},
  {"type":"event","name":"IgnoredInitCode","anonymous":false,"inputs":[
    {"name":"userOpHash","type":"bytes32","indexed":true},
    {"name":"sender","type":"address","indexed":true},
    {"name":"unusedFactory","type":"address","indexed":false}
  ]},
  {"type":"event","name":"EIP7702AccountInitialized","anonymous":false,"inputs":[
    {"name":"userOpHash","type":"bytes32","indexed":true},
    {"name":"sender","type":"address","indexed":true},
    {"name":"delegate","type":"address","indexed":true}
  ]},
  {"type":"event","name":"SignatureAggregatorChanged","anonymous":false,"inputs":[
    {"name":"aggregator","type":"address","indexed":true}
  ]},
  {"type":"event","name":"BeforeExecution","anonymous":false,"inputs":[]}
]`

var protocolEventABI = mustABI(entryPointEventABI)

type operationOutcome struct {
	Hash          common.Hash
	Sender        common.Address
	Paymaster     common.Address
	Nonce         *big.Int
	Success       bool
	ActualGasCost *big.Int
	ActualGasUsed *big.Int
	LogIndex      uint64
}

type associatedEvent struct {
	Hash  common.Hash
	Event ProtocolEvent
}

func eventName(log *types.Log) (string, bool) {
	if log == nil || len(log.Topics) == 0 {
		return "", false
	}
	for name, event := range protocolEventABI.Events {
		if event.ID == log.Topics[0] {
			return name, true
		}
	}
	return "", false
}

func decodeOutcome(log *types.Log) (operationOutcome, error) {
	if err := validateEventEnvelope(log, "UserOperationEvent", 4); err != nil {
		return operationOutcome{}, err
	}
	values, err := unpackEventData("UserOperationEvent", log.Data)
	if err != nil || len(values) != 4 {
		return operationOutcome{}, fmt.Errorf("decode UserOperationEvent data: %w", err)
	}
	nonce, nonceOK := values[0].(*big.Int)
	success, successOK := values[1].(bool)
	cost, costOK := values[2].(*big.Int)
	used, usedOK := values[3].(*big.Int)
	if !nonceOK || !successOK || !costOK || !usedOK {
		return operationOutcome{}, errors.New("UserOperationEvent has invalid ABI value types")
	}
	return operationOutcome{
		Hash: log.Topics[1], Sender: topicAddress(log.Topics[2]), Paymaster: topicAddress(log.Topics[3]),
		Nonce: cloneBig(nonce), Success: success, ActualGasCost: cloneBig(cost), ActualGasUsed: cloneBig(used),
		LogIndex: uint64(log.Index),
	}, nil
}

func decodeAssociatedEvent(name string, log *types.Log) (associatedEvent, error) {
	switch name {
	case "AccountDeployed":
		if err := validateEventEnvelope(log, name, 3); err != nil {
			return associatedEvent{}, err
		}
		values, err := unpackEventData(name, log.Data)
		if err != nil || len(values) != 2 {
			return associatedEvent{}, fmt.Errorf("decode %s data: %w", name, err)
		}
		factory, factoryOK := values[0].(common.Address)
		paymaster, paymasterOK := values[1].(common.Address)
		if !factoryOK || !paymasterOK {
			return associatedEvent{}, errors.New("AccountDeployed has invalid ABI value types")
		}
		return associatedEvent{
			Hash: log.Topics[1], Event: ProtocolEvent{
				Kind: EventAccountDeployed, LogIndex: uint64(log.Index), Sender: topicAddress(log.Topics[2]),
				RelatedAddress: addressPointer(factory), Paymaster: addressPointer(paymaster),
			},
		}, nil
	case "UserOperationRevertReason", "PostOpRevertReason":
		if err := validateEventEnvelope(log, name, 3); err != nil {
			return associatedEvent{}, err
		}
		values, err := unpackEventData(name, log.Data)
		if err != nil || len(values) != 2 {
			return associatedEvent{}, fmt.Errorf("decode %s data: %w", name, err)
		}
		nonce, nonceOK := values[0].(*big.Int)
		raw, rawOK := values[1].([]byte)
		if !nonceOK || !rawOK {
			return associatedEvent{}, fmt.Errorf("%s has invalid ABI value types", name)
		}
		kind := EventExecutionRevert
		if name == "PostOpRevertReason" {
			kind = EventPostOpRevert
		}
		reason, panicCode := decodeStandardRevert(raw)
		return associatedEvent{
			Hash: log.Topics[1], Event: ProtocolEvent{
				Kind: kind, LogIndex: uint64(log.Index), Sender: topicAddress(log.Topics[2]),
				Nonce: cloneBig(nonce), RawData: bytes.Clone(raw), Reason: reason, PanicCode: panicCode,
			},
		}, nil
	case "UserOperationPrefundTooLow":
		if err := validateEventEnvelope(log, name, 3); err != nil {
			return associatedEvent{}, err
		}
		values, err := unpackEventData(name, log.Data)
		if err != nil || len(values) != 1 {
			return associatedEvent{}, fmt.Errorf("decode %s data: %w", name, err)
		}
		nonce, ok := values[0].(*big.Int)
		if !ok {
			return associatedEvent{}, errors.New("UserOperationPrefundTooLow has invalid nonce type")
		}
		return associatedEvent{
			Hash: log.Topics[1], Event: ProtocolEvent{
				Kind: EventPrefundTooLow, LogIndex: uint64(log.Index), Sender: topicAddress(log.Topics[2]), Nonce: cloneBig(nonce),
			},
		}, nil
	case "IgnoredInitCode":
		if err := validateEventEnvelope(log, name, 3); err != nil {
			return associatedEvent{}, err
		}
		values, err := unpackEventData(name, log.Data)
		if err != nil || len(values) != 1 {
			return associatedEvent{}, fmt.Errorf("decode %s data: %w", name, err)
		}
		factory, ok := values[0].(common.Address)
		if !ok {
			return associatedEvent{}, errors.New("IgnoredInitCode has invalid factory type")
		}
		return associatedEvent{
			Hash: log.Topics[1], Event: ProtocolEvent{
				Kind: EventIgnoredInitCode, LogIndex: uint64(log.Index), Sender: topicAddress(log.Topics[2]), RelatedAddress: addressPointer(factory),
			},
		}, nil
	case "EIP7702AccountInitialized":
		if err := validateEventEnvelope(log, name, 4); err != nil {
			return associatedEvent{}, err
		}
		if len(log.Data) != 0 {
			return associatedEvent{}, errors.New("EIP7702AccountInitialized has non-empty data")
		}
		delegate := topicAddress(log.Topics[3])
		return associatedEvent{
			Hash: log.Topics[1], Event: ProtocolEvent{
				Kind: EventEIP7702Initialized, LogIndex: uint64(log.Index), Sender: topicAddress(log.Topics[2]), RelatedAddress: addressPointer(delegate),
			},
		}, nil
	default:
		return associatedEvent{}, fmt.Errorf("unsupported associated EntryPoint event %q", name)
	}
}

func validateEventEnvelope(log *types.Log, name string, topics int) error {
	event, ok := protocolEventABI.Events[name]
	if !ok || log == nil || len(log.Topics) != topics || len(log.Topics) == 0 || log.Topics[0] != event.ID {
		return fmt.Errorf("%s log envelope is invalid", name)
	}
	topicIndex := 1
	for _, input := range event.Inputs {
		if !input.Indexed {
			continue
		}
		if input.Type.T == gethabi.AddressTy {
			for _, value := range log.Topics[topicIndex][:common.HashLength-common.AddressLength] {
				if value != 0 {
					return fmt.Errorf("%s indexed address is not canonically padded", name)
				}
			}
		}
		topicIndex++
	}
	return nil
}

func unpackEventData(name string, data []byte) ([]any, error) {
	event := protocolEventABI.Events[name]
	arguments := event.Inputs.NonIndexed()
	values, err := arguments.Unpack(data)
	if err != nil {
		return nil, err
	}
	repacked, err := arguments.Pack(values...)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(repacked, data) {
		return nil, errors.New("event data is not canonical ABI encoding")
	}
	return values, nil
}

func topicAddress(topic common.Hash) common.Address {
	return common.BytesToAddress(topic[common.HashLength-common.AddressLength:])
}

var (
	stringABIType, _ = gethabi.NewType("string", "", nil)
	uintABIType, _   = gethabi.NewType("uint256", "", nil)
	errorArguments   = gethabi.Arguments{{Type: stringABIType}}
	panicArguments   = gethabi.Arguments{{Type: uintABIType}}
	errorSelector    = []byte{0x08, 0xc3, 0x79, 0xa0}
	panicSelector    = []byte{0x4e, 0x48, 0x7b, 0x71}
)

func decodeStandardRevert(raw []byte) (string, *big.Int) {
	if len(raw) < 4 {
		return "", nil
	}
	switch {
	case bytes.Equal(raw[:4], errorSelector):
		values, err := errorArguments.Unpack(raw[4:])
		if err != nil || len(values) != 1 {
			return "", nil
		}
		reason, ok := values[0].(string)
		if !ok || len(reason) > 4096 || !utf8.ValidString(reason) {
			return "", nil
		}
		repacked, err := errorArguments.Pack(reason)
		if err != nil || !bytes.Equal(repacked, raw[4:]) {
			return "", nil
		}
		return reason, nil
	case bytes.Equal(raw[:4], panicSelector):
		values, err := panicArguments.Unpack(raw[4:])
		if err != nil || len(values) != 1 {
			return "", nil
		}
		code, ok := values[0].(*big.Int)
		if !ok {
			return "", nil
		}
		repacked, err := panicArguments.Pack(code)
		if err != nil || !bytes.Equal(repacked, raw[4:]) {
			return "", nil
		}
		return "", cloneBig(code)
	default:
		return "", nil
	}
}
