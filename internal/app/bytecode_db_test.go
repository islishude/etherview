package app

import (
	"slices"
	"testing"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
)

func TestBytecodeDBComponentManifestIsVerificationOwned(t *testing.T) {
	cfg := config.Default()
	cfg.Features.Verification = true
	key := "43-bytecode-db-01"
	if slices.Contains(productionComponentKeys(cfg, []components.Role{components.RoleVerification}, false), key) {
		t.Fatal("discovery enabled by default")
	}
	cfg.BytecodeDB.Enabled = true
	for _, role := range []components.Role{components.RoleAPI, components.RoleSync, components.RoleEnrich, components.RoleTrace, components.RoleMetadata, components.RoleMaintenance, components.RoleVerification} {
		keys := productionComponentKeys(cfg, []components.Role{role}, false)
		if slices.Contains(keys, key) != (role == components.RoleVerification) {
			t.Fatalf("unexpected owner: %s", role)
		}
		if role == components.RoleVerification && !slices.Contains(keys, "43-bytecode-db-02") {
			t.Fatal("worker count omitted")
		}
	}
}
