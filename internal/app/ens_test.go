package app

import (
	"context"
	"encoding/json"
	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/islishude/etherview/internal/enssync"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/islishude/etherview/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestENSReaderConstructionDoesNotAccessUpstream(t *testing.T) {
	var calls atomic.Int64
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "forbidden", 500) }))
	defer forbidden.Close()
	cfg := config.Default()
	cfg.Features.ENS = true
	cfg.ENS.PolicyDigest = "0x" + strings.Repeat("1", 64)
	cfg.ENS.OfficialRPCEndpoints = []config.RPCEndpoint{{Name: "never-called", URL: forbidden.URL}}
	cfg.ENS.OfficialGateways = []string{forbidden.URL}
	reader, e := newENSReader(&pgxpool.Pool{}, cfg)
	if e != nil || reader == nil {
		t.Fatal(e)
	}
	if calls.Load() != 0 {
		t.Fatal("API constructed a network client")
	}
}

func TestENSComponentManifestMatchesAllAndSplit(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ENS = true
	cfg.ENS.MaxConcurrency = 2
	all := productionComponentKeys(cfg, []components.Role{components.RoleAPI, components.RoleSync, components.RoleEnrich, components.RoleTrace, components.RoleMetadata, components.RoleENS, components.RoleMaintenance}, false)
	split := productionComponentKeys(cfg, []components.Role{components.RoleENS}, false)
	for _, key := range []string{"38-ens-collection", "39-ens-resolution-01", "39-ens-resolution-02"} {
		if !slices.Contains(all, key) || !slices.Contains(split, key) {
			t.Fatalf("missing %s all=%v split=%v", key, all, split)
		}
		if slices.Contains(productionComponentKeys(cfg, []components.Role{components.RoleAPI}, false), key) {
			t.Fatal("API owns ENS worker")
		}
	}
	cfg.Features.ENS = false
	if keys := productionComponentKeys(cfg, []components.Role{components.RoleENS}, false); !slices.Contains(keys, "50-role-ens") || slices.Contains(keys, "38-ens-collection") {
		t.Fatal(keys)
	}
}

func TestENSAssemblyDoesNotProbeAndCanceledServicesDrain(t *testing.T) {
	var calls atomic.Int64
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "offline", http.StatusServiceUnavailable)
	}))
	defer forbidden.Close()
	cfg := config.Default()
	cfg.Features.ENS = true
	cfg.ENS.Mode = "custom_only"
	cfg.ENS.MaxConcurrency = 2
	cfg.ENS.PolicyDigest = "0x" + strings.Repeat("1", 64)
	cfg.ENS.Custom.Registry = common.HexToAddress("0x1").Hex()
	cfg.ENS.Custom.UniversalResolver = common.HexToAddress("0x2").Hex()
	cfg.ENS.CustomManifest = filepath.Join(t.TempDir(), "custom.json")
	cfg.RPC.Endpoints = []config.RPCEndpoint{{Name: "offline", URL: forbidden.URL, Purposes: []string{"state", "history"}, MaxRequests: 10}}
	genesis := common.HexToHash("0x1")
	m := enssync.Manifest{Registry: common.HexToAddress(cfg.ENS.Custom.Registry), UniversalResolver: common.HexToAddress(cfg.ENS.Custom.UniversalResolver), Namespace: enscontract.Custom, TargetChainID: "1", SourceChainID: "1", Genesis: genesis, StartHash: genesis, Policy: common.HexToHash(cfg.ENS.PolicyDigest), CoinType: "60", ParentName: "test", Deployments: []enssync.Deployment{{Address: common.HexToAddress("0x1"), Profile: "registry", Hash: genesis, CodeHash: genesis}, {Address: common.HexToAddress("0x2"), Profile: "universal", Hash: genesis, CodeHash: genesis}}}
	raw, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(cfg.ENS.CustomManifest, raw, 0600); e != nil {
		t.Fatal(e)
	}
	registry := components.NewRegistry()
	assembly := runtimeAssembly{ctx: t.Context(), cfg: cfg, roleSet: map[components.Role]bool{components.RoleENS: true}, componentRegistry: registry, db: &pgxpool.Pool{}}
	if e = assembly.registerENSComponents(); e != nil {
		t.Fatal(e)
	}
	services, e := registry.Build([]components.Role{components.RoleENS})
	if e != nil || len(services) != 3 {
		t.Fatal(services, e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, service := range services {
		if e = service.Run(ctx); e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("startup or cancellation probed an ENS source")
	}
}
