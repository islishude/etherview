package app

import (
	"database/sql"
	"testing"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/metadata"
)

func TestRegisterMetadataWorkersUseUniqueDurableSafeWorkers(t *testing.T) {
	t.Parallel()
	registry := components.NewRegistry()
	cfg := config.Default()
	cfg.Runtime.WorkerCount = 3
	server := rpc.NewServer()
	client := rpc.DialInProc(server)
	t.Cleanup(func() {
		client.Close()
		server.Stop()
	})
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "state", Client: client,
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := registerMetadataWorkers(registry, &sql.DB{}, pool, cfg, nil); err != nil {
		t.Fatal(err)
	}
	services, err := registry.Build([]components.Role{components.RoleMetadata})
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 5 {
		t.Fatalf("metadata services = %d, want 5", len(services))
	}
	if _, ok := services[0].(*metadata.UpdateDiscoverer); !ok {
		t.Fatalf("metadata update service type = %T", services[0])
	}
	if services[0].Name() != "metadata-update-discovery" {
		t.Fatalf("metadata update discovery name = %q", services[0].Name())
	}
	if _, ok := services[1].(*metadata.SourceDiscoverer); !ok {
		t.Fatalf("metadata source service type = %T", services[1])
	}
	if services[1].Name() != "metadata-source-discovery" {
		t.Fatalf("metadata source discovery name = %q", services[1].Name())
	}
	for index, service := range services[2:] {
		named, ok := service.(*namedWorkerService)
		if !ok {
			t.Fatalf("metadata worker wrapper type = %T", service)
		}
		if _, ok := named.worker.(*metadata.Worker); !ok {
			t.Fatalf("metadata worker type = %T", named.worker)
		}
		wantName := indexedWorkerName("metadata-worker", index)
		if service.Name() != wantName {
			t.Fatalf("metadata worker name = %q, want %q", service.Name(), wantName)
		}
	}
}

func TestMetadataRoleRequiresRPCOnlyForEnabledSourceDiscovery(t *testing.T) {
	t.Parallel()
	roles := map[components.Role]bool{components.RoleMetadata: true}
	cfg := config.Default()
	if needsRPCForServe(roles, cfg) {
		t.Fatal("disabled metadata role unexpectedly requires an execution RPC")
	}
	cfg.Features.NFTMetadata = true
	if !needsRPCForServe(roles, cfg) {
		t.Fatal("enabled metadata source discovery did not require a state RPC")
	}
}
