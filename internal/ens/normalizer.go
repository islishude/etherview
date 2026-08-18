// Package ens implements bounded, snapshot-pinned ENS resolution.
package ens

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"unicode/utf8"

	"github.com/adraffy/go-ens-normalize/ensip15"
	"github.com/ethereum/go-ethereum/common"
	goens "github.com/wealdtech/go-ens/v3"
)

var ErrInvalidName = errors.New("invalid ENS name")

// Normalizer keeps resolution code injectable in tests while delegating the
// actual rules to the user-selected Go ENSIP-15 library.
type Normalizer struct{}

func NewNormalizer() (*Normalizer, error) { return &Normalizer{}, nil }

func (*Normalizer) Normalize(ctx context.Context, input string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	input = strings.TrimSpace(input)
	if input == "" || len(input) > 1024 || !utf8.ValidString(input) ||
		strings.ContainsAny(input, "\x00\r\n\t /\\") {
		return "", ErrInvalidName
	}
	normalized, err := ensip15.Shared().Normalize(input)
	if err != nil || !validNormalizedName(normalized) {
		return "", ErrInvalidName
	}
	return normalized, nil
}

func validNormalizedName(name string) bool {
	if name == "" || len(name) > 255 || !utf8.ValidString(name) {
		return false
	}
	wireLength := 1
	for label := range strings.SplitSeq(name, ".") {
		if label == "" || len(label) > 255 {
			return false
		}
		wireLength += 1 + len(label)
		if wireLength > 255 {
			return false
		}
	}
	return true
}

func DNSWireFormat(normalizedName string) ([]byte, error) {
	if !validNormalizedName(normalizedName) {
		return nil, ErrInvalidName
	}
	wire := goens.DNSWireFormat(normalizedName)
	if len(wire) == 0 || len(wire) > 255 || wire[len(wire)-1] != 0 {
		return nil, ErrInvalidName
	}
	return append([]byte(nil), wire...), nil
}

func Namehash(normalizedName string) (common.Hash, error) {
	if !validNormalizedName(normalizedName) {
		return common.Hash{}, ErrInvalidName
	}
	hash, err := goens.NameHash(normalizedName)
	if err != nil {
		return common.Hash{}, ErrInvalidName
	}
	return common.BytesToHash(hash[:]), nil
}

// EVMCoinType applies ENSIP-11. Ethereum Mainnet retains SLIP-44 coin type 60;
// other representable EVM chain IDs set the hardened bit.
func EVMCoinType(chainID uint64) (*big.Int, bool) {
	if chainID == 1 {
		return big.NewInt(60), true
	}
	coinType := new(big.Int).SetUint64(chainID)
	coinType.Or(coinType, new(big.Int).SetUint64(1<<31))
	return coinType, true
}
