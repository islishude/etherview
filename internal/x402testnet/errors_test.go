package x402testnet

import (
	"errors"
	"testing"
)

func TestErrorCodeNeverExposesNestedInput(t *testing.T) {
	t.Parallel()
	if got := ErrorCode(errors.New("postgres://user:secret@example.invalid/db")); got != CodeFailed {
		t.Fatalf("ErrorCode(hostile) = %q", got)
	}
	if got := ErrorCode(boundaryError("payment_requirement_mismatch")); got != "payment_requirement_mismatch" {
		t.Fatalf("ErrorCode(boundary) = %q", got)
	}
	if got := (&BoundaryError{Code: "secret value"}).Error(); got != CodeFailed {
		t.Fatalf("BoundaryError invalid code = %q", got)
	}
}
