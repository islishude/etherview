package state

import (
	"errors"
	"math/big"
	"strconv"

	"github.com/islishude/etherview/internal/catalog"
	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/islishude/etherview/internal/ethrpc"
)

const maxBalanceCandidates = 256

var maximumUint256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

// TokenReconciler turns bounded event-derived ERC-20 and NFT candidates into
// exact observations at one canonical block hash. PostgreSQL retains immutable
// observations; only cache misses require an endpoint-pinned RPC batch.
type TokenReconciler struct {
	db        dbaccess.Database
	pool      *ethrpc.Pool
	canonical CanonicalSource
}

var (
	_ catalog.NFTStateReconciler   = (*TokenReconciler)(nil)
	_ catalog.ERC20StateReconciler = (*TokenReconciler)(nil)
)

func NewTokenReconciler(db dbaccess.Database, pool *ethrpc.Pool, canonical CanonicalSource) (*TokenReconciler, error) {
	if db == nil {
		return nil, errors.New("token reconciler requires PostgreSQL")
	}
	if pool == nil {
		return nil, errors.New("token reconciler requires a state RPC pool")
	}
	if canonical == nil {
		return nil, errors.New("token reconciler requires a canonical source")
	}
	return &TokenReconciler{db: db, pool: pool, canonical: canonical}, nil
}

func validateTokenSnapshot(snapshot catalog.Snapshot) (CanonicalRef, string, error) {
	chainID, err := parseUint256(snapshot.ChainID)
	if err != nil || chainID.Sign() == 0 {
		return CanonicalRef{}, "", errors.New("invalid token state chain ID")
	}
	blockNumber, err := strconv.ParseUint(snapshot.BlockNumber, 10, 64)
	if err != nil || strconv.FormatUint(blockNumber, 10) != snapshot.BlockNumber {
		return CanonicalRef{}, "", errors.New("invalid token state block number")
	}
	blockHash, err := ethrpc.ParseHash(snapshot.BlockHash)
	if err != nil {
		return CanonicalRef{}, "", errors.New("invalid token state block hash")
	}
	return CanonicalRef{Number: blockNumber, Hash: blockHash}, chainID.String(), nil
}

func parseUint256(value string) (*big.Int, error) {
	if len(value) > 78 {
		return nil, errors.New("uint256 is out of range")
	}
	if value == "" || len(value) > 1 && value[0] == '0' {
		return nil, errors.New("uint256 is not canonical decimal")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return nil, errors.New("uint256 contains a non-decimal digit")
		}
	}
	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok || parsed.Sign() < 0 || parsed.Cmp(maximumUint256) > 0 {
		return nil, errors.New("uint256 is out of range")
	}
	return parsed, nil
}
