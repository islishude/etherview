// Package enscontract owns background ENS read identities and states shared by
// storage, future workers and public readers. It has no RPC or database client.
package enscontract

import (
	"context"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

type Namespace string

const (
	Official Namespace = "ens"
	Custom   Namespace = "custom_ens"
)

type Direction string

const (
	Forward Direction = "forward"
	Primary Direction = "primary"
)

type State string

const (
	Resolved    State = "resolved"
	NotFound    State = "not_found"
	Unavailable State = "unavailable"
)

type Code string

const (
	NotIndexed        Code = "not_indexed"
	SyncPending       Code = "sync_pending"
	ResolutionPending Code = "resolution_pending"
	Stale             Code = "stale"
	UnsupportedSource Code = "unsupported_source"
)

// Lookup explicitly binds the explored chain and policy as well as the source.
// CoinType is canonical unsigned decimal, preserving the full uint256 domain.
// The lookup key is already ENSIP-15 normalized or a lowercase hex address.
type Lookup struct {
	SourceID int64
	// Zero selects the latest publication; a positive value reads exactly that retained version.
	PublicationID int64
	TargetChainID string
	PolicyDigest  common.Hash
	Direction     Direction
	Key           string
	CoinType      string
}

// Result exposes a source publication, not official/custom fallback selection.
// A missing candidate is unavailable/not_indexed, never a definitive negative.
// Publications are retained evidence; ValidUntil independently limits display.
type Result struct {
	State           State
	Code            Code
	Source          Namespace
	PublicationID   int64
	Generation      int64
	Name            string
	Address         common.Address
	Resolver        common.Address
	ReverseResolver common.Address
	BlockHash       common.Hash
	ValidUntil      time.Time
}

type Reader interface {
	Lookup(context.Context, Lookup) (Result, error)
}

// EffectiveLookup reads a policy-selected immutable publication. Snapshot -1
// selects current state; zero is an empty, already-issued catalog snapshot.
type EffectiveLookup struct {
	TargetChainID string
	PolicyDigest  common.Hash
	Direction     Direction
	Key           string
	Snapshot      int64
}
type EffectiveResult struct {
	Result
	Version int64
}
type CatalogReader interface {
	LookupEffective(context.Context, EffectiveLookup) (EffectiveResult, error)
	Snapshot(context.Context, string, common.Hash) (int64, error)
}
