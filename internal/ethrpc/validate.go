package ethrpc

import (
	"errors"
	"fmt"
	"strings"
)

type ValidationError struct {
	Path    string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

func ValidateBundle(bundle Bundle) error {
	block := &bundle.Block
	if block.Number == nil {
		return validation("block.number", "must not be null")
	}
	blockNumber, err := block.Number.Uint64()
	if err != nil {
		return validation("block.number", err.Error())
	}
	if block.Hash == nil {
		return validation("block.hash", "must not be null")
	}
	if _, err := ParseHash(block.Hash.String()); err != nil {
		return validation("block.hash", err.Error())
	}
	if _, err := ParseHash(block.ParentHash.String()); err != nil {
		return validation("block.parentHash", err.Error())
	}
	if len(block.Transactions) != len(bundle.Receipts) {
		return validation("receipts", fmt.Sprintf("count %d does not match transaction count %d", len(bundle.Receipts), len(block.Transactions)))
	}
	seenTransactions := make(map[string]struct{}, len(block.Transactions))
	nextLogIndex := uint64(0)
	for index, reference := range block.Transactions {
		path := fmt.Sprintf("block.transactions[%d]", index)
		if reference.Transaction == nil {
			return validation(path, "is a hash reference; full transaction object required")
		}
		transaction := reference.Transaction
		if _, err := ParseHash(transaction.Hash.String()); err != nil {
			return validation(path+".hash", err.Error())
		}
		hashKey := strings.ToLower(string(transaction.Hash))
		if _, duplicate := seenTransactions[hashKey]; duplicate {
			return validation(path+".hash", "duplicates another transaction in the block")
		}
		seenTransactions[hashKey] = struct{}{}
		if transaction.BlockHash == nil || !transaction.BlockHash.Equal(*block.Hash) {
			return validation(path+".blockHash", "does not match block hash")
		}
		if transaction.BlockNumber == nil {
			return validation(path+".blockNumber", "must not be null")
		}
		transactionBlockNumber, err := transaction.BlockNumber.Uint64()
		if err != nil || transactionBlockNumber != blockNumber {
			return validation(path+".blockNumber", "does not match block number")
		}
		if transaction.TransactionIndex == nil {
			return validation(path+".transactionIndex", "must not be null")
		}
		transactionIndex, err := transaction.TransactionIndex.Uint64()
		if err != nil || transactionIndex != uint64(index) {
			return validation(path+".transactionIndex", fmt.Sprintf("must equal %d", index))
		}

		receipt := &bundle.Receipts[index]
		receiptPath := fmt.Sprintf("receipts[%d]", index)
		if !receipt.TransactionHash.Equal(transaction.Hash) {
			return validation(receiptPath+".transactionHash", "does not match transaction hash")
		}
		receiptIndex, err := receipt.TransactionIndex.Uint64()
		if err != nil || receiptIndex != uint64(index) {
			return validation(receiptPath+".transactionIndex", fmt.Sprintf("must equal %d", index))
		}
		if !receipt.BlockHash.Equal(*block.Hash) {
			return validation(receiptPath+".blockHash", "does not match block hash")
		}
		receiptBlockNumber, err := receipt.BlockNumber.Uint64()
		if err != nil || receiptBlockNumber != blockNumber {
			return validation(receiptPath+".blockNumber", "does not match block number")
		}
		if receipt.Status == nil && receipt.Root == nil {
			return validation(receiptPath, "must contain either status or pre-Byzantium root")
		}
		for logPosition := range receipt.Logs {
			log := &receipt.Logs[logPosition]
			logPath := fmt.Sprintf("%s.logs[%d]", receiptPath, logPosition)
			if log.Removed {
				return validation(logPath+".removed", "must be false for a block receipt")
			}
			if log.BlockHash == nil || !log.BlockHash.Equal(*block.Hash) {
				return validation(logPath+".blockHash", "does not match block hash")
			}
			if log.BlockNumber == nil {
				return validation(logPath+".blockNumber", "must not be null")
			}
			logBlockNumber, err := log.BlockNumber.Uint64()
			if err != nil || logBlockNumber != blockNumber {
				return validation(logPath+".blockNumber", "does not match block number")
			}
			if log.TransactionHash == nil || !log.TransactionHash.Equal(transaction.Hash) {
				return validation(logPath+".transactionHash", "does not match transaction hash")
			}
			if log.TransactionIndex == nil {
				return validation(logPath+".transactionIndex", "must not be null")
			}
			logTransactionIndex, err := log.TransactionIndex.Uint64()
			if err != nil || logTransactionIndex != uint64(index) {
				return validation(logPath+".transactionIndex", "does not match transaction index")
			}
			if log.LogIndex == nil {
				return validation(logPath+".logIndex", "must not be null")
			}
			logIndex, err := log.LogIndex.Uint64()
			if err != nil || logIndex != nextLogIndex {
				return validation(logPath+".logIndex", fmt.Sprintf("must equal %d", nextLogIndex))
			}
			nextLogIndex++
		}
	}
	if err := validateWithdrawals(block.Withdrawals); err != nil {
		return err
	}
	return nil
}

func ValidateParent(child, parent Bundle) error {
	childNumber, err := child.Number()
	if err != nil {
		return err
	}
	parentNumber, err := parent.Number()
	if err != nil {
		return err
	}
	parentHash, err := parent.BlockHash()
	if err != nil {
		return err
	}
	if childNumber != parentNumber+1 {
		return validation("block.number", fmt.Sprintf("child %d does not immediately follow parent %d", childNumber, parentNumber))
	}
	if !child.Block.ParentHash.Equal(parentHash) {
		return validation("block.parentHash", "does not match supplied parent")
	}
	return nil
}

func validateWithdrawals(withdrawals []Withdrawal) error {
	var previous uint64
	for index := range withdrawals {
		value, err := withdrawals[index].Index.Uint64()
		if err != nil {
			return validation(fmt.Sprintf("block.withdrawals[%d].index", index), err.Error())
		}
		if index > 0 && value != previous+1 {
			return validation(fmt.Sprintf("block.withdrawals[%d].index", index), "must immediately follow the previous withdrawal index")
		}
		previous = value
	}
	return nil
}

func validation(path, message string) error {
	return &ValidationError{Path: path, Message: message}
}

func IsValidationError(err error) bool {
	var target *ValidationError
	return errors.As(err, &target)
}
