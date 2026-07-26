// Package x402testnet implements the explicit, one-shot Base Sepolia
// conformance gate. It is not part of the normal test or serving runtime.
package x402testnet

import (
	"errors"
	"regexp"
)

const CodeFailed = "x402_testnet_failed"

var errorCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)

// BoundaryError deliberately carries only a stable code. Live conformance
// failures can contain a private key, RPC or database credential, payment
// authorization, facilitator response, or resource URL in nested errors, so
// those errors must never reach command output.
type BoundaryError struct {
	Code string
}

func (err *BoundaryError) Error() string {
	if err == nil || !errorCodePattern.MatchString(err.Code) {
		return CodeFailed
	}
	return err.Code
}

func boundaryError(code string) error {
	if !errorCodePattern.MatchString(code) {
		code = CodeFailed
	}
	return &BoundaryError{Code: code}
}

// ErrorCode maps every failure to a bounded non-secret command boundary.
func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var boundary *BoundaryError
	if errors.As(err, &boundary) {
		return boundary.Error()
	}
	return CodeFailed
}
