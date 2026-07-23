package enrich

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/islishude/etherview/internal/ethrpc"
)

// exactStateRPCError classifies failures from a request that was deliberately
// pinned to an EIP-1898 block-hash selector. Capability gaps are terminal for
// that immutable block. Other failures keep their cause for retry decisions,
// but expose only a stable message so hostile RPC text cannot reach logs.
func exactStateRPCError(ctx context.Context, method string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if ethrpc.IsMethodNotFound(err) {
		return Unavailable(fmt.Errorf("%s with an EIP-1898 block hash is unavailable", method))
	}
	if rpcError, ok := errors.AsType[*ethrpc.RPCError](err); ok {
		message := strings.ToLower(rpcError.Message)
		if rpcError.Code == -32602 || strings.Contains(message, "eip-1898") ||
			strings.Contains(message, "block hash") || strings.Contains(message, "missing trie") ||
			strings.Contains(message, "historical state") || strings.Contains(message, "pruned") ||
			strings.Contains(message, "header not found") || strings.Contains(message, "state is not available") {
			return Unavailable(fmt.Errorf("%s cannot serve the exact block-hash state", method))
		}
	}
	return exactStateRetryableError{method: method, cause: err}
}

type exactStateRetryableError struct {
	method string
	cause  error
}

func (err exactStateRetryableError) Error() string {
	return fmt.Sprintf("exact-state RPC %s failed", err.method)
}

func (err exactStateRetryableError) Unwrap() error { return err.cause }
