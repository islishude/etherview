package billing

import (
	"errors"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

var (
	ErrInsufficientCredit = errors.New("billing account has insufficient credit")
	ErrTopupUnavailable   = errors.New("billing top-up is unavailable")
)

type Account struct {
	UserID            string
	ChainID           uint64
	Network           string
	Asset             common.Address
	TotalCreditAtomic string
	TotalDebitAtomic  string
	ReservedAtomic    string
	AvailableAtomic   string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type TopupIntentState string

const (
	TopupIntentOpen       TopupIntentState = "open"
	TopupIntentProcessing TopupIntentState = "processing"
	TopupIntentSettling   TopupIntentState = "settling"
	TopupIntentCredited   TopupIntentState = "credited"
	TopupIntentFailed     TopupIntentState = "failed"
	TopupIntentExpired    TopupIntentState = "expired"
)

func (state TopupIntentState) valid() bool {
	switch state {
	case TopupIntentOpen, TopupIntentProcessing, TopupIntentSettling,
		TopupIntentCredited, TopupIntentFailed, TopupIntentExpired:
		return true
	default:
		return false
	}
}

func (state TopupIntentState) Valid() bool { return state.valid() }

type TopupIntent struct {
	ID              string
	UserID          string
	ChainID         uint64
	Network         string
	Asset           common.Address
	AmountAtomic    string
	Recipient       common.Address
	Payer           common.Address
	State           TopupIntentState
	ActivePaymentID *string
	TransactionHash *common.Hash
	FailureCode     *string
	ExpiresAt       time.Time
	ProcessingAt    *time.Time
	SettlingAt      *time.Time
	CreditedAt      *time.Time
	FailedAt        *time.Time
	ExpiredAt       *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type UsageState string

const (
	UsageReserved  UsageState = "reserved"
	UsageCommitted UsageState = "committed"
	UsageReleased  UsageState = "released"
	UsageExpired   UsageState = "expired"
)

func (state UsageState) valid() bool {
	switch state {
	case UsageReserved, UsageCommitted, UsageReleased, UsageExpired:
		return true
	default:
		return false
	}
}

func (state UsageState) Valid() bool { return state.valid() }

type UsageCharge struct {
	ID                   string
	Owner                string
	UserID               string
	APIKeyPrefix         string
	ChainID              uint64
	Network              string
	Asset                common.Address
	Method               string
	Operation            string
	ResourceDigest       Digest
	AmountAtomic         string
	State                UsageState
	FailureCode          *string
	ResponseDigest       *Digest
	ResponseBytes        *int64
	ReservationExpiresAt time.Time
	CommittedAt          *time.Time
	ReleasedAt           *time.Time
	ExpiredAt            *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type UsageReservation struct {
	Charge UsageCharge
	Owner  string
}

type CreateTopupIntentInput struct {
	UserID       string
	Payer        common.Address
	AmountAtomic string
	ObservedAt   time.Time
}

type ReserveUsageInput struct {
	UserID       string
	APIKeyPrefix string
	Method       string
	Operation    string
	Resource     Digest
	AmountAtomic string
	ObservedAt   time.Time
}

type CommitUsageInput struct {
	ChargeID      string
	Owner         string
	Response      Digest
	ResponseBytes int64
	ObservedAt    time.Time
}

type AdjustmentInput struct {
	UserID       string
	Direction    string
	AmountAtomic string
	Reason       string
	ObservedAt   time.Time
}

type AccountPageAfter struct {
	UpdatedAt time.Time
	UserID    string
}

type AccountSummary struct {
	AccountCount      string
	TotalCreditAtomic string
	TotalDebitAtomic  string
	ReservedAtomic    string
	AvailableAtomic   string
}

type PrepaidObserver interface {
	ObserveBillingTopup(method, result string)
	ObserveBillingUsage(operation, result string)
}

type ExpiryResult struct {
	UsageReservations uint64
	TopupPayments     uint64
	TopupIntents      uint64
}
