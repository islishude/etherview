// Package maintenance executes operator-approved repair and reindex ranges.
// PostgreSQL is the only coordination dependency; optional brokers may wake a
// worker in the future but cannot own request or lease state.
package maintenance

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrLeaseLost      = errors.New("maintenance request lease is no longer owned")
	ErrFinalizedRange = errors.New("maintenance range intersects finalized history")
	ErrInvalidRequest = errors.New("invalid maintenance request")
)

type Operation string

const (
	OperationRepair  Operation = "repair"
	OperationReindex Operation = "reindex"
)

func (operation Operation) Validate() error {
	if operation != OperationRepair && operation != OperationReindex {
		return fmt.Errorf("%w: unsupported operation %q", ErrInvalidRequest, operation)
	}
	return nil
}

// ValidateOperationStage is the shared admission rule for CLI persistence and
// worker execution. Keeping it in the maintenance domain prevents durable
// requests that can only fail after being claimed.
func ValidateOperationStage(operation Operation, stage string) error {
	if err := operation.Validate(); err != nil {
		return err
	}
	switch operation {
	case OperationRepair:
		if stage != "core" {
			return fmt.Errorf("%w: repair only supports stage core, got %q", ErrInvalidRequest, stage)
		}
	case OperationReindex:
		switch stage {
		case "token", "stats", "trace":
			return nil
		default:
			return fmt.Errorf(
				"%w: reindex only supports stage token, stats, or trace, got %q",
				ErrInvalidRequest, stage,
			)
		}
	}
	return nil
}

// Request is the immutable scheduling boundary passed to a range executor.
// Block numbers are uint64 because the current indexer and operator CLI use
// that domain even though PostgreSQL stores them in NUMERIC(78,0).
type Request struct {
	ID             int64
	ChainID        string
	Operation      Operation
	Stage          string
	FromBlock      uint64
	ToBlock        uint64
	AllowFinalized bool
	Reason         string
}

func (request Request) Validate() error {
	if request.ID <= 0 {
		return fmt.Errorf("%w: request ID must be a positive BIGINT", ErrInvalidRequest)
	}
	if err := validateCanonicalDecimal(request.ChainID, 78); err != nil {
		return fmt.Errorf("%w: chain ID: %v", ErrInvalidRequest, err)
	}
	if request.ChainID == "0" {
		return fmt.Errorf("%w: chain ID must be positive", ErrInvalidRequest)
	}
	if err := request.Operation.Validate(); err != nil {
		return err
	}
	if request.Stage == "" || len(request.Stage) > 64 {
		return fmt.Errorf("%w: stage must contain 1 to 64 bytes", ErrInvalidRequest)
	}
	for _, character := range request.Stage {
		if character < 'a' || character > 'z' {
			if character < '0' || character > '9' {
				if character != '-' && character != '_' {
					return fmt.Errorf("%w: stage %q contains an unsupported character", ErrInvalidRequest, request.Stage)
				}
			}
		}
	}
	if err := ValidateOperationStage(request.Operation, request.Stage); err != nil {
		return err
	}
	if request.ToBlock < request.FromBlock {
		return fmt.Errorf("%w: range end is below its start", ErrInvalidRequest)
	}
	if strings.TrimSpace(request.Reason) == "" || len(request.Reason) > 1024 {
		return fmt.Errorf("%w: reason must contain 1 to 1024 bytes", ErrInvalidRequest)
	}
	return nil
}

// RangeExecutor deliberately separates repair and reindex. Repair may fill or
// reconcile missing core facts, while reindex may rebuild a named derived
// stage; the worker must never silently substitute one operation for the other.
// Implementations must be idempotent because a process can die after applying
// a range but before persisting the terminal request status.
type RangeExecutor interface {
	Repair(context.Context, Request) error
	Reindex(context.Context, Request) error
}

// Lease is opaque outside this package except for the claimed request. A valid
// PostgreSQL lease also owns a dedicated session-level advisory lock.
type Lease struct {
	Request Request
	session *leaseSession
}

// Repository is the durable worker boundary. GuardFinalized is repeated just
// before execution so a finality advance after Claim cannot be ignored.
type Repository interface {
	Claim(context.Context, string) (Lease, bool, error)
	GuardFinalized(context.Context, Lease) error
	Complete(context.Context, Lease) error
	Fail(context.Context, Lease, error) error
	Release(context.Context, Lease) error
}

func validateCanonicalDecimal(value string, maximumDigits int) error {
	if value == "" || len(value) > maximumDigits {
		return errors.New("must be a canonical non-negative decimal integer")
	}
	if len(value) > 1 && value[0] == '0' {
		return errors.New("must not contain a leading zero")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return errors.New("must contain only decimal digits")
		}
	}
	return nil
}
