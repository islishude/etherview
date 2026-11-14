package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/defi"
	"github.com/islishude/etherview/internal/deficontract"
)

func TestCustomChainDeFiManifestReaders(t *testing.T) {
	hash := common.HexToHash("0x1234")
	m := defi.Manifest{Version: 1, Revision: 7, ChainID: "77777", Genesis: hash, Deployments: []defi.Deployment{{ID: "custom-v2", Protocol: deficontract.UniswapV2, StartHash: hash, Contracts: []defi.Contract{{Role: "factory", Address: common.HexToAddress("0x9876"), CodeHash: hash}}}}}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "custom.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Chain.ID = 77777
	cfg.Chain.GenesisHash = hash.Hex()
	cfg.DeFi = config.DeFiConfig{Enabled: true, Manifest: path}
	worker, err := loadDeFiManifest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := newDeFiReader(nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if reader.Digest != worker.Digest() || len(reader.Sources) != 1 || reader.Sources[0].ID != worker.Digest()+":custom-v2" {
		t.Fatal("API/enrich source identity differs")
	}
	cfg.Chain.ID++
	if _, err = loadDeFiManifest(cfg); err == nil {
		t.Fatal("accepted wrong chain")
	}
	cfg.Chain.ID = 77777
	cfg.Chain.GenesisHash = common.HexToHash("0x9999").Hex()
	if _, err = newDeFiReader(nil, cfg); err == nil {
		t.Fatal("accepted wrong genesis")
	}
}

func TestDeFiRoleParityAndDisabledReader(t *testing.T) {
	cfg := config.Default()
	cfg.DeFi.Enabled = true
	all := productionComponentKeys(cfg, []components.Role{components.RoleAPI, components.RoleEnrich}, false)
	split := productionComponentKeys(cfg, []components.Role{components.RoleEnrich}, false)
	api := productionComponentKeys(cfg, []components.Role{components.RoleAPI}, false)
	if !slices.Contains(all, "40-defi-index") || !slices.Contains(split, "40-defi-index") || slices.Contains(api, "40-defi-index") {
		t.Fatal("DeFi worker role parity")
	}
	cfg.DeFi.Enabled = false
	r, e := newDeFiReader(nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	page, e := r.Positions(t.Context(), deficontract.Request{Owner: "0x1111111111111111111111111111111111111111"})
	if e != nil || len(page.Deployments) != 6 {
		t.Fatal(page, e)
	}
}
