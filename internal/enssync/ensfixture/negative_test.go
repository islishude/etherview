package ensfixture

import (
	"github.com/ethereum/go-ethereum/common"
	"math/big"
	"testing"
)

func TestRealUniversalResolverEmptyReverse(t *testing.T) {
	f := New(t)
	_, u := f.Universal(f.Owner)
	out := f.Call(u, "reverse", common.HexToAddress("0x1234").Bytes(), big.NewInt(60))
	decoded, e := u.ABI.Methods["reverse"].Outputs.Unpack(out)
	if e != nil || decoded[0] != "" {
		t.Fatal(decoded, e)
	}
}
