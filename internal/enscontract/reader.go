package enscontract

import (
	"context"
	"errors"
	"github.com/ethereum/go-ethereum/common"
	"time"
)

var ErrInvalidName = errors.New("invalid ENS name")
var ErrSnapshotInvalid = errors.New("invalid ENS name snapshot")

type PrimaryResolution struct {
	ObservationID   int64
	SnapshotVersion int64
	Outcome         State
	Name            string
	Address         common.Address

	Code        string
	BlockNumber string
	BlockHash   common.Hash
	ValidUntil  time.Time
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
	State                      string
	Protocols                  []string
	Target, Scanned, Published *string
	Coverage                   string
	CoverageStart              *string
	Pending                    string
	LastSuccess, ValidUntil    time.Time
	Code                       string
}
type StatusReader interface {
	NameStatus(context.Context) ([]SourceStatus, error)
}
