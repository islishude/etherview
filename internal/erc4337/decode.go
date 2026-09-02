package erc4337

import (
	"errors"
	"fmt"
	"math/big"
	"slices"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func DecodeBlock(registry Registry, chainID *big.Int, block *types.Block, receipts types.Receipts) ([]Operation, error) {
	if chainID == nil || chainID.Sign() <= 0 || block == nil || block.Number() == nil || !block.Number().IsUint64() {
		return nil, errors.New("decode UserOperations using an invalid block identity")
	}
	transactions := block.Transactions()
	if len(transactions) != len(receipts) {
		return nil, errors.New("UserOperation receipt count does not match the block")
	}
	var operations []Operation
	for index, transaction := range transactions {
		if transaction == nil || receipts[index] == nil {
			return nil, errors.New("UserOperation block contains a nil transaction or receipt")
		}
		target := transaction.To()
		if target == nil {
			continue
		}
		entryPoint, configured := registry.Match(*target, block.NumberU64())
		if !configured {
			continue
		}
		decoded, err := decodeTransaction(
			entryPoint, chainID, block, uint64(index), transaction, receipts[index],
		)
		if errors.Is(err, ErrNotBundleMethod) {
			if containsOutcomeEvent(receipts[index].Logs, entryPoint.Address) {
				return nil, fmt.Errorf("configured EntryPoint emitted UserOperationEvent from an unsupported top-level method")
			}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("decode EntryPoint transaction %s: %w", transaction.Hash(), err)
		}
		operations = append(operations, decoded...)
	}
	return operations, nil
}

func decodeTransaction(
	entryPoint EntryPoint,
	chainID *big.Int,
	block *types.Block,
	transactionIndex uint64,
	transaction *types.Transaction,
	receipt *types.Receipt,
) ([]Operation, error) {
	if receipt.Status == types.ReceiptStatusFailed {
		return nil, nil
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return nil, errors.New("EntryPoint receipt has an invalid status")
	}
	requests, beneficiary, err := decodeHandleCalldata(entryPoint.Version, transaction.Data())
	if err != nil {
		return nil, err
	}
	outcomes := make([]operationOutcome, 0, len(requests))
	associated := make(map[common.Hash][]ProtocolEvent)
	for _, log := range receipt.Logs {
		if log == nil || log.Address != entryPoint.Address {
			continue
		}
		name, known := eventName(log)
		if !known {
			continue
		}
		switch name {
		case "UserOperationEvent":
			outcome, decodeErr := decodeOutcome(log)
			if decodeErr != nil {
				return nil, decodeErr
			}
			outcomes = append(outcomes, outcome)
		case "AccountDeployed", "UserOperationRevertReason", "PostOpRevertReason",
			"UserOperationPrefundTooLow", "IgnoredInitCode", "EIP7702AccountInitialized":
			event, decodeErr := decodeAssociatedEvent(name, log)
			if decodeErr != nil {
				return nil, decodeErr
			}
			associated[event.Hash] = append(associated[event.Hash], event.Event)
		}
	}
	if len(outcomes) != len(requests) {
		return nil, fmt.Errorf("UserOperationEvent count %d does not match calldata operation count %d", len(outcomes), len(requests))
	}
	bundler, err := types.Sender(types.LatestSignerForChainID(chainID), transaction)
	if err != nil {
		return nil, fmt.Errorf("recover EntryPoint transaction sender: %w", err)
	}
	operations := make([]Operation, len(requests))
	seenHashes := make(map[common.Hash]struct{}, len(requests))
	for index := range requests {
		request, outcome := requests[index], outcomes[index]
		if index > 0 && outcomes[index-1].LogIndex >= outcome.LogIndex {
			return nil, errors.New("UserOperationEvent order does not match calldata")
		}
		if request.Sender != outcome.Sender || request.Nonce.Cmp(outcome.Nonce) != 0 ||
			paymasterAddress(request.Paymaster) != outcome.Paymaster {
			return nil, fmt.Errorf("UserOperationEvent %d does not match its calldata identity", index)
		}
		if _, duplicate := seenHashes[outcome.Hash]; duplicate {
			return nil, errors.New("bundle contains a duplicate userOpHash")
		}
		seenHashes[outcome.Hash] = struct{}{}
		events := associated[outcome.Hash]
		slices.SortFunc(events, func(left, right ProtocolEvent) int {
			if left.LogIndex < right.LogIndex {
				return -1
			}
			if left.LogIndex > right.LogIndex {
				return 1
			}
			return 0
		})
		if err := validateAssociatedEvents(entryPoint.Version, request, outcome, events); err != nil {
			return nil, fmt.Errorf("validate UserOperation %d events: %w", index, err)
		}
		operations[index] = Operation{
			Hash: outcome.Hash, EntryPoint: entryPoint.Address, Version: entryPoint.Version,
			BlockNumber: block.NumberU64(), BlockHash: block.Hash(),
			TransactionHash: transaction.Hash(), TransactionIndex: transactionIndex,
			OperationIndex: uint64(index), EventLogIndex: outcome.LogIndex,
			Bundler: bundler, Beneficiary: beneficiary,
			Request: request, Success: outcome.Success,
			ActualGasCost: cloneBig(outcome.ActualGasCost), ActualGasUsed: cloneBig(outcome.ActualGasUsed),
			Events: events,
		}
	}
	for hash := range associated {
		if _, exists := seenHashes[hash]; !exists {
			return nil, errors.New("EntryPoint supplemental event has no matching UserOperationEvent")
		}
	}
	return operations, nil
}

func validateAssociatedEvents(version Version, request Request, outcome operationOutcome, events []ProtocolEvent) error {
	failure := false
	for _, event := range events {
		if event.LogIndex >= outcome.LogIndex {
			return errors.New("supplemental event does not precede UserOperationEvent")
		}
		if event.Sender != request.Sender {
			return errors.New("supplemental event sender does not match calldata")
		}
		if event.Nonce != nil && event.Nonce.Cmp(request.Nonce) != 0 {
			return errors.New("supplemental event nonce does not match calldata")
		}
		switch event.Kind {
		case EventAccountDeployed:
			if request.InitKind != InitFactory || request.Factory == nil || event.RelatedAddress == nil ||
				*request.Factory != *event.RelatedAddress || paymasterAddress(event.Paymaster) != outcome.Paymaster {
				return errors.New("AccountDeployed does not match factory or paymaster calldata")
			}
		case EventIgnoredInitCode:
			if version != Version09 || request.InitKind != InitFactory || request.Factory == nil ||
				event.RelatedAddress == nil || *request.Factory != *event.RelatedAddress {
				return errors.New("IgnoredInitCode does not match v0.9 factory calldata")
			}
		case EventEIP7702Initialized:
			if version != Version09 || request.InitKind != InitEIP7702 || event.RelatedAddress == nil {
				return errors.New("EIP7702AccountInitialized does not match v0.9 initCode")
			}
		case EventPostOpRevert, EventPrefundTooLow:
			if version == Version06 {
				return errors.New("supplemental event is not defined by EntryPoint v0.6")
			}
			failure = true
		case EventExecutionRevert:
			failure = true
		}
	}
	if outcome.Success && failure {
		return errors.New("successful UserOperation has a failure event")
	}
	return nil
}

func containsOutcomeEvent(logs []*types.Log, entryPoint common.Address) bool {
	event := protocolEventABI.Events["UserOperationEvent"]
	for _, log := range logs {
		if log != nil && log.Address == entryPoint && len(log.Topics) > 0 && log.Topics[0] == event.ID {
			return true
		}
	}
	return false
}

func paymasterAddress(value *common.Address) common.Address {
	if value == nil {
		return common.Address{}
	}
	return *value
}
