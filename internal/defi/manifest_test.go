package defi

import (
	"encoding/json"
	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/deficontract"
	"strings"
	"testing"
)

func validManifest() Manifest {
	h := common.HexToHash("0x1")
	return Manifest{Version: 1, ChainID: "1", Genesis: h, Deployments: []Deployment{{ID: "uniswap", Protocol: deficontract.UniswapV2, StartHash: h, Contracts: []Contract{{Role: "factory", Address: common.HexToAddress("0x1"), CodeHash: h}}}}}
}
func TestManifestStrictIdentity(t *testing.T) {
	m := validManifest()
	raw, _ := json.Marshal(m)
	if _, e := ReadManifest(strings.NewReader(string(raw))); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{string(raw) + "{}", strings.Replace(string(raw), `"version":1`, `"version":1,"version":1`, 1), strings.Replace(string(raw), `"version":1`, `"version":1,"unknown":true`, 1), strings.Replace(string(raw), `"chain_id":"1"`, `"chain_id":"01"`, 1)} {
		if _, e := ReadManifest(strings.NewReader(bad)); e == nil {
			t.Fatal("accepted malformed manifest")
		}
	}
	m.Deployments = append(m.Deployments, m.Deployments[0])
	if m.Validate() == nil {
		t.Fatal("duplicate source accepted")
	}
	m = validManifest()
	m.Deployments[0].Contracts[0].Implementation = common.HexToAddress("0x2")
	if m.Validate() == nil {
		t.Fatal("unbound proxy accepted")
	}
}
