// Package publicquery owns transport-independent public read contracts.
package publicquery

import (
	"context"
	"errors"
	"regexp"

	"github.com/islishude/etherview/internal/api/gen"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrUnavailable   = errors.New("capability unavailable")
	ErrNotReady      = errors.New("not ready")
	ErrInvalidCursor = errors.New("invalid or stale cursor")
	ErrInvalidInput  = errors.New("invalid input")
)

var capabilityIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)

// CapabilityUnavailableError carries only controlled machine identifiers safe
// for a transport adapter to expose.
type CapabilityUnavailableError struct {
	Capability string
	State      string
	Code       string
}

func (*CapabilityUnavailableError) Error() string { return ErrUnavailable.Error() }
func (*CapabilityUnavailableError) Unwrap() error { return ErrUnavailable }

func NewCapabilityUnavailableError(capability, state, code string) error {
	errorValue := &CapabilityUnavailableError{Capability: capability, State: state, Code: code}
	if !errorValue.Valid() {
		return ErrUnavailable
	}
	return errorValue
}

func (err *CapabilityUnavailableError) Valid() bool {
	return err != nil && capabilityIdentifierPattern.MatchString(err.Capability) &&
		(err.State == "unavailable" || err.State == "failed") &&
		capabilityIdentifierPattern.MatchString(err.Code)
}

type StatusSnapshot struct {
	LatestBlock         uint64
	IndexedBlock        uint64
	HighestCoveredBlock uint64
	HighestCoveredKnown bool
	BackfillComplete    bool
	SafeBlock           *uint64
	FinalizedBlock      *uint64
	CoverageStart       uint64
	CoverageEnd         uint64
	CoreReady           bool
	Completeness        gen.Completeness
}

// Reader is the core public-query boundary. Implementations return stable,
// validated API models and preserve exact canonical/hash lookup semantics.
type Reader interface {
	Status(context.Context) (StatusSnapshot, error)
	Blocks(context.Context, string, int) ([]gen.Block, string, error)
	Block(context.Context, string) (gen.Block, error)
	BlockTransactions(context.Context, string, string, int) ([]gen.Transaction, string, error)
	Transactions(context.Context, string, int) ([]gen.Transaction, string, error)
	Transaction(context.Context, string) (gen.Transaction, error)
	Address(context.Context, string) (gen.AddressSummary, error)
	Search(context.Context, string, string, int) ([]gen.SearchResult, string, error)
}

type HomeSnapshotState struct {
	EventID      uint64
	Status       StatusSnapshot
	Blocks       []gen.Block
	Transactions []gen.Transaction
}

type HomeSnapshotReader interface {
	HomeSnapshot(context.Context) (HomeSnapshotState, error)
}
