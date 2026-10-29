package ensfixture

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestRealV1MigrationAndReverseMirror(t *testing.T) {
	f := New(t)
	m := f.Migration()
	for _, entry := range []struct {
		label           string
		wrapped, locked bool
	}{{"unwrapped", false, false}, {"unlocked", true, false}, {"locked", true, true}} {
		f.RegisterV1(m, entry.label, entry.wrapped, entry.locked)
		f.Call(m.Reverse, "setName", entry.label+".eth")
		result := f.Call(m.Universal, "reverse", f.Owner.Bytes(), big.NewInt(60))
		decoded, err := m.Universal.ABI.Methods["reverse"].Outputs.Unpack(result)
		if err != nil || decoded[0] != entry.label+".eth" {
			t.Fatalf("reserved mirror: %v %v", decoded, err)
		}
		f.MigrateV1(m, entry.label, entry.wrapped, entry.locked)
		id := new(big.Int).SetBytes(crypto.Keccak256([]byte(entry.label)))
		token := new(big.Int).SetBytes(f.Call(f.Registry, "getTokenId", id))
		owner := common.BytesToAddress(f.Call(f.Registry, "ownerOf", token))
		if owner != f.Owner {
			t.Fatalf("migration owner: %s", owner)
		}
		wire := append([]byte{byte(len(entry.label))}, []byte(entry.label)...)
		wire = append(wire, []byte("\x03eth\x00")...)
		f.Call(f.ResolverContract(), "setAddress", wire, big.NewInt(60), f.Owner.Bytes())
		result = f.Call(m.Universal, "reverse", f.Owner.Bytes(), big.NewInt(60))
		decoded, err = m.Universal.ABI.Methods["reverse"].Outputs.Unpack(result)
		if err != nil || decoded[0] != entry.label+".eth" {
			t.Fatalf("migrated mirror: %v %v", decoded, err)
		}
	}
}
