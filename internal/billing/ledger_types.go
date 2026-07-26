// Package billing implements the writer-authoritative x402 reservation,
// settlement, and audit state machine. Protocol parsing and facilitator
// transport live in the separate x402wire package.
package billing

import (
	"errors"
	"time"
)

type State string

const (
	// SettlementCrashReconcileDelay is the minimum age before an unmarked
	// settling row may be treated as a crashed runtime and reconciled by an
	// operator. Durable observability uses this same threshold.
	SettlementCrashReconcileDelay = 2 * time.Minute

	StateReserved State = "reserved"
	StateVerified State = "verified"
	StateSettling State = "settling"
	StateSettled  State = "settled"
	StateFailed   State = "failed"
	StateExpired  State = "expired"
)

func (state State) valid() bool {
	switch state {
	case StateReserved, StateVerified, StateSettling, StateSettled,
		StateFailed, StateExpired:
		return true
	default:
		return false
	}
}

type Actor string

const (
	ActorRuntime  Actor = "runtime"
	ActorOperator Actor = "operator"
)

var (
	ErrInvalidInput  = errors.New("billing input is invalid")
	ErrNotFound      = errors.New("billing payment was not found")
	ErrStateConflict = errors.New("billing payment state or reservation fence changed")
	ErrIntegrity     = errors.New("billing payment durable state is invalid")
)

type Digest [32]byte
type Address [20]byte
type TransactionHash [32]byte

type Payment struct {
	ID                   string
	ChainID              uint64
	Operation            string
	Method               string
	Network              string
	Asset                Address
	AmountAtomic         string
	Recipient            Address
	Payer                *Address
	UserID               *string
	APIKeyPrefix         *string
	TransactionHash      *TransactionHash
	State                State
	FailureCode          *string
	ReservationExpiresAt time.Time
	HandlerStartedAt     *time.Time
	VerifiedAt           *time.Time
	SettlingAt           *time.Time
	SettledAt            *time.Time
	FailedAt             *time.Time
	ExpiredAt            *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time

	fingerprint       Digest
	resourceDigest    Digest
	requirementDigest Digest
	facilitatorDigest Digest
}

// Reservation carries the server-only fence only for the request that created
// the durable row. A duplicate fingerprint never receives the existing owner.
type Reservation struct {
	Payment Payment
	Owned   bool
	Owner   string
}

type ReserveInput struct {
	Fingerprint       Digest
	Operation         string
	ResourceDigest    Digest
	RequirementDigest Digest
	Network           string
	Asset             Address
	AmountAtomic      string
	Recipient         Address
	APIKeyPrefix      *string
	FacilitatorDigest Digest
	ObservedAt        time.Time
}

type VerifiedInput struct {
	PaymentID    string
	Owner        string
	Payer        Address
	UserID       *string
	APIKeyPrefix *string
	ObservedAt   time.Time
}

type PageAfter struct {
	CreatedAt time.Time
	ID        string
}

type AdminFilter struct {
	State     *State
	Operation *string
	Network   *string
	Asset     *Address
	FromTime  *time.Time
	ToTime    *time.Time
}

type SummaryRow struct {
	State        State
	Operation    string
	Network      string
	Asset        Address
	PaymentCount string
	AmountAtomic string
}

// PaymentEvent is the public, non-secret audit projection for one durable
// payment transition. It deliberately omits reservation owners and all
// authorization/resource/facilitator digests.
type PaymentEvent struct {
	ID              int64
	PaymentID       string
	FromState       *State
	ToState         State
	Code            string
	Actor           Actor
	TransactionHash *TransactionHash
	OccurredAt      time.Time
}

// Inspection is a transactionally consistent payment and append-only event
// snapshot for operator inspection.
type Inspection struct {
	Payment Payment
	Events  []PaymentEvent
}
