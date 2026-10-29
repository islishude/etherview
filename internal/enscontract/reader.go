package enscontract

import (
	"context"
	"errors"
	"github.com/ethereum/go-ethereum/common"
	"time"
)

var ErrInvalidName = errors.New("invalid ENS name")
var ErrSnapshotInvalid = errors.New("invalid ENS name snapshot")

type ForwardResolution struct {
	ObservationID int64 // Immutable effective publication, not a request-time observation.
	Outcome       State
	Name          string
	Address       common.Address
	Source        Namespace
	ValidUntil    time.Time
}
type PrimaryResolution struct {
	ObservationID   int64
	SnapshotVersion int64
	Outcome         State
	Name            string
	Address         common.Address
	Source          Namespace
	Code            string
	ValidUntil      time.Time
}
type NameReader interface {
	ResolveForward(context.Context, string) (ForwardResolution, error)
	ResolveCurrentPrimary(context.Context, common.Address) (PrimaryResolution, error)
	ResolveAddressBatch(context.Context, []common.Address, string) ([]PrimaryResolution, string, error)
}
type UnavailableError struct {
	Code  string
	State string
}

func (e *UnavailableError) Error() string { return "ENS resolution unavailable" }
func (e *UnavailableError) CapabilityDetails() (string, string, string) {
	if e == nil {
		return "name", "failed", "resolver_failure"
	}
	state := e.State
	if state == "" {
		state = "failed"
		if e.Code == "rpc_unavailable" || e.Code == "ccip_unavailable" {
			state = "unavailable"
		}
	}
	return "name", state, e.Code
}

type SourceStatus struct {
	Source                     Namespace
	State                      string
	Protocols                  []string
	Target, Scanned, Published *string
	Pending                    string
	LastSuccess, ValidUntil    time.Time
	Code                       string
}
type StatusReader interface {
	NameStatus(context.Context) ([]SourceStatus, error)
}
