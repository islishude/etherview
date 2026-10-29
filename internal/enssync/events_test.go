package enssync

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func eventLog(t *testing.T, profile, name string, values ...any) (contractProfile, types.Log) {
	t.Helper()
	p, e := loadProfile(profile)
	if e != nil {
		t.Fatal(e)
	}
	event := p.abi.Events[name]
	log := types.Log{Topics: []common.Hash{event.ID}}
	plain := []any{}
	if len(values) != len(event.Inputs) {
		t.Fatalf("%s inputs %d != %d", name, len(values), len(event.Inputs))
	}
	for i, arg := range event.Inputs {
		if arg.Indexed {
			b, e := (event.Inputs[i : i+1]).Pack(values[i])
			if e != nil {
				t.Fatal(e)
			}
			log.Topics = append(log.Topics, common.BytesToHash(b))
		} else {
			plain = append(plain, values[i])
		}
	}
	log.Data, e = event.Inputs.NonIndexed().Pack(plain...)
	if e != nil {
		t.Fatal(e)
	}
	return p, log
}
func TestCollectorEventProfiles(t *testing.T) {
	m := Manifest{ParentName: "eth", CoinType: "60"}
	owner := common.HexToAddress("0x1234")
	label := crypto.Keccak256Hash([]byte("alice"))
	node := nameHash("alice.eth")
	t.Run("controller plaintext is checked against claimed label", func(t *testing.T) {
		p, log := eventLog(t, "controller_v3", "NameRegistered", "alice", label, owner, big.NewInt(1), big.NewInt(0), big.NewInt(1234))
		got, e := decodeEvent(m, Deployment{Profile: "controller_v3"}, p, log)
		if e != nil {
			t.Fatal(e)
		}
		if len(got.Candidates) != 2 || got.Candidates[1].Key != "alice.eth" {
			t.Fatalf("candidates %+v", got.Candidates)
		}
		log.Topics[1] = common.Hash{}
		if _, e = decodeEvent(m, Deployment{Profile: "controller_v3"}, p, log); !errors.Is(e, ErrInvalidEvent) {
			t.Fatal(e)
		}
	})
	t.Run("registrar hash does not fabricate a name", func(t *testing.T) {
		p, log := eventLog(t, "registrar", "NameRegistered", new(big.Int).SetBytes(label[:]), owner, big.NewInt(1234))
		got, e := decodeEvent(m, Deployment{Profile: "registrar"}, p, log)
		if e != nil {
			t.Fatal(e)
		}
		if len(got.Candidates) != 1 || got.Candidates[0].Direction != "primary" || len(got.Projections) != 1 {
			t.Fatalf("hash-only discovery %+v", got)
		}
	})
	t.Run("resolver deletion and version remain distinct facts", func(t *testing.T) {
		p, log := eventLog(t, "resolver", "AddressChanged", node, big.NewInt(60), []byte{})
		got, e := decodeEvent(m, Deployment{Profile: "resolver"}, p, log)
		if e != nil || len(got.Candidates) != 0 || got.Projections[0].Fields["deleted"] != true {
			t.Fatalf("%+v %v", got, e)
		}
		p, log = eventLog(t, "resolver", "VersionChanged", node, uint64(4))
		got, e = decodeEvent(m, Deployment{Profile: "resolver"}, p, log)
		if e != nil || got.Projections[0].Fields["version"] != "4" {
			t.Fatalf("%+v %v", got, e)
		}
	})
	t.Run("noncanonical ABI and recognized upgrades fail closed", func(t *testing.T) {
		p, log := eventLog(t, "registry", "Transfer", node, owner)
		log.Data = append(log.Data, 0)
		if _, e := decodeEvent(m, Deployment{Profile: "registry"}, p, log); !errors.Is(e, ErrInvalidEvent) {
			t.Fatal(e)
		}
		log = types.Log{Topics: []common.Hash{crypto.Keccak256Hash([]byte("Upgraded(address)"))}}
		if _, e := decodeEvent(m, Deployment{Profile: "registry"}, p, log); !errors.Is(e, ErrUnsupportedDeployment) {
			t.Fatal(e)
		}
	})
	t.Run("wrapper verifies DNS wire preimage", func(t *testing.T) {
		p, log := eventLog(t, "wrapper", "NameWrapped", node, []byte("\x05alice\x03eth\x00"), owner, uint32(0), uint64(1234))
		got, e := decodeEvent(m, Deployment{Profile: "wrapper"}, p, log)
		if e != nil || len(got.Candidates) != 1 || got.Candidates[0].Key != "alice.eth" {
			t.Fatalf("%+v %v", got, e)
		}
		log.Topics[1] = common.Hash{}
		if _, e = decodeEvent(m, Deployment{Profile: "wrapper"}, p, log); !errors.Is(e, ErrInvalidEvent) {
			t.Fatal(e)
		}
	})
	for _, profile := range []string{"registry", "registrar", "controller_legacy", "controller_v3", "controller", "resolver", "reverse", "default_reverse", "wrapper"} {
		if _, e := loadProfile(profile); e != nil {
			t.Fatal(profile, e)
		}
	}
}
func TestCollectorInputBounds(t *testing.T) {
	for _, name := range []string{"", "Alice.eth", "a..eth", "a.eth\x00", "a\xff.eth", "a.eth/", "a eth"} {
		if _, ok := normalizedName(name); ok {
			t.Fatalf("accepted %q", name)
		}
	}
	for _, wire := range [][]byte{{0}, {1, 'a'}, {0xc0, 0}, {1, 'a', 0, 0}} {
		if _, e := decodeDNSName(wire); e == nil {
			t.Fatalf("accepted %x", wire)
		}
	}
	if _, e := loadProfile("../registry"); !errors.Is(e, ErrUnsupportedDeployment) {
		t.Fatal(e)
	}
	if _, e := NewCollector(nil, Manifest{}, nil, CollectorOptions{}); !errors.Is(e, ErrInvalidInput) {
		t.Fatal(e)
	}
}
