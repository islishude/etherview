// Package mempool implements optional, authoritative pending-transaction
// snapshots. PostgreSQL snapshots are the public query source in both
// monolith and split-role deployments; RPC is used only by the sync role.
package mempool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type State string

const (
	StatePending     State = "pending"
	StateComplete    State = "complete"
	StateUnavailable State = "unavailable"
	StateFailed      State = "failed"
)

var (
	ErrInvalidCursor = errors.New("mempool cursor is invalid or expired")
	ErrCorruptData   = errors.New("mempool data is inconsistent")
)

type CapabilityError struct {
	State         State
	Code          string
	LastAttemptAt time.Time
}

func (err CapabilityError) Error() string {
	return fmt.Sprintf("mempool capability is %s", err.State)
}

type Transaction struct {
	Hash                 string
	From                 string
	To                   *string
	Nonce                string
	Value                string
	Gas                  string
	GasPrice             *string
	MaxFeePerGas         *string
	MaxPriorityFeePerGas *string
	Type                 *string
	Input                string
	Raw                  json.RawMessage
	FirstSeenAt          time.Time
	LastSeenAt           time.Time
	ExpiresAt            time.Time
	Endpoint             string
}

type Snapshot struct {
	Endpoint     string
	ObservedAt   time.Time
	ExpiresAt    time.Time
	Transactions []Transaction
}

type SnapshotInfo struct {
	ID               int64
	Endpoint         string
	ObservedAt       time.Time
	ExpiresAt        time.Time
	TransactionCount int
}

type Page struct {
	Items      []Transaction
	NextCursor string
	Snapshot   SnapshotInfo
}

type Failure struct {
	State      State
	Endpoint   string
	Code       string
	Message    string
	ObservedAt time.Time
}

type Store interface {
	StoreSnapshot(context.Context, Snapshot) (SnapshotInfo, error)
	StoreFailure(context.Context, Failure) error
}

type Reader interface {
	Pending(context.Context, string, int) (Page, error)
}
