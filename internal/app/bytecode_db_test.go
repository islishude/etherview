package app

import (
	"slices"
	"testing"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
)

func TestBytecodeDBComponentManifestIsAPIOwned(t *testing.T) {
	cfg := config.Default()
	cfg.Features.Verification = true
	key := "43-bytecode-db-01"
	if slices.Contains(productionComponentKeys(cfg, []components.Role{components.RoleAPI}, false), key) {
		t.Fatal("discovery enabled by default")
	}
	cfg.BytecodeDB.Enabled = true
	for _, role := range []components.Role{components.RoleAPI, components.RoleSync, components.RoleEnrich, components.RoleTrace, components.RoleMetadata, components.RoleMaintenance} {
		keys := productionComponentKeys(cfg, []components.Role{role}, false)
		if slices.Contains(keys, key) != (role == components.RoleAPI) {
			t.Fatalf("unexpected owner: %s", role)
		}
		if role == components.RoleAPI && !slices.Contains(keys, "43-bytecode-db-02") {
			t.Fatal("worker count omitted")
		}
	}
}
