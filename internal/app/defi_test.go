package app

import (
	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/deficontract"
	"slices"
	"testing"
)

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
