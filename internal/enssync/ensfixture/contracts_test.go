package ensfixture

import (
	"github.com/ethereum/go-ethereum/common"
	"math/big"
	"testing"
)

func TestPinnedENSV2Contracts(t *testing.T) {
	f := New(t)
	f.Call(f.Registry, "register", "alice", f.Owner, f.Child, f.Resolver, big.NewInt(0), uint64(1_900_000_000))
	f.Call(f.ChildContract(), "register", "wallet", f.Owner, common.Address{}, f.Resolver, big.NewInt(0), uint64(1_900_000_000))
	f.Call(f.ResolverContract(), "setAddress", []byte("\x06wallet\x05alice\x03eth\x00"), big.NewInt(60), f.Owner.Bytes())
	if len(f.Logs) < 15 {
		t.Fatal("missing real lifecycle logs")
	}
}

func TestRealUniversalResolverForwardAndReverse(t *testing.T) {
	f := New(t)
	_, universal := f.Universal(f.Owner)
	f.Call(f.Registry, "register", "alice", f.Owner, f.Child, f.Resolver, big.NewInt(0), uint64(1_900_000_000))
	f.Call(f.ChildContract(), "register", "wallet", f.Owner, common.Address{}, f.Resolver, big.NewInt(0), uint64(1_900_000_000))
	f.Call(f.ResolverContract(), "setAddress", []byte("\x06wallet\x05alice\x03eth\x00"), big.NewInt(60), f.Owner.Bytes())
	result := f.Call(universal, "reverse", f.Owner.Bytes(), big.NewInt(60))
	decoded, e := universal.ABI.Methods["reverse"].Outputs.Unpack(result)
	if e != nil || decoded[0] != "wallet.alice.eth" {
		t.Fatal(decoded, e)
	}
}
