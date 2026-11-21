package ethrpc

import (
	"errors"
	"strings"

	"github.com/ethereum/go-ethereum/rpc"
)

func IsExecutionReverted(err error) bool {
	var rpcError rpc.Error
	if !errors.As(err, &rpcError) {
		return false
	}
	message := strings.ToLower(rpcError.Error())
	return rpcError.ErrorCode() == 3 || strings.Contains(message, "execution reverted") || strings.Contains(message, "revert")
}

func IsExactStateUnavailable(err error) bool {
	if IsMethodNotFound(err) {
		return true
	}
	var rpcError rpc.Error
	if !errors.As(err, &rpcError) {
		return false
	}
	message := strings.ToLower(rpcError.Error())
	return rpcError.ErrorCode() == -32602 || strings.Contains(message, "eip-1898") ||
		strings.Contains(message, "block hash") || strings.Contains(message, "missing trie") ||
		strings.Contains(message, "historical state") || strings.Contains(message, "pruned") ||
		strings.Contains(message, "header not found") || strings.Contains(message, "state is not available")
}
