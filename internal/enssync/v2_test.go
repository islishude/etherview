package enssync

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestV2RejectsUnboundIdentitiesAndHostileLabels(t *testing.T) {
	address := common.HexToAddress("0x1234")
	hash := crypto.Keccak256Hash([]byte("fixture"))
	m := Manifest{Deployments: []Deployment{{Address: address, Profile: "v2_registry"}}, Roots: []RegistryRoot{{address, "eth"}}, Implementations: []Implementation{{Address: common.HexToAddress("0x5678"), CodeHash: hash, ProxyCodeHash: hash, Profile: "v2_registry"}}}
	if e := validateV2Manifest(m); e != nil {
		t.Fatal(e)
	}
	m.Implementations[0].Address = address
	if e := validateV2Manifest(m); !errors.Is(e, ErrInvalidInput) {
		t.Fatal("implementation enrolled as instance", e)
	}
	m.Implementations = nil
	m.Roots = append(m.Roots, m.Roots[0])
	if e := validateV2Manifest(m); !errors.Is(e, ErrInvalidInput) {
		t.Fatal("duplicate roots", e)
	}
	p, log := eventLog(t, "v2_registry", "LabelRegistered", big.NewInt(1), hash, "alice", address, uint64(1900000000), address)
	c := &Collector{profiles: map[common.Address]contractProfile{address: p}}
	revision := int64(1)
	if e := c.projectV2(t.Context(), nil, 1, Deployment{Address: address, Profile: "v2_registry"}, log, &revision); !errors.Is(e, ErrInvalidEvent) {
		t.Fatal("claimed hash mismatch", e)
	}
	budget := 512
	if paths, e := c.registryPaths(t.Context(), nil, 1, address, 1800000000, []common.Address{address}, &budget); e != nil || len(paths) != 0 {
		t.Fatal("cycle", paths, e)
	}
	budget = 0
	if _, e := c.registryPaths(t.Context(), nil, 1, address, 1800000000, nil, &budget); !errors.Is(e, ErrCollectionLimit) {
		t.Fatal("unbounded graph", e)
	}
}
