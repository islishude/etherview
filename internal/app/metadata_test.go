package app

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/metadata"
)

type metadataTestCaller struct{}

func (metadataTestCaller) Call(context.Context, string, []any, any) error {
	return errors.New("unused metadata test RPC")
}

func TestRegisterMetadataWorkersUseUniqueDurableSafeWorkers(t *testing.T) {
	t.Parallel()
	registry := components.NewRegistry()
	cfg := config.Default()
	cfg.Runtime.WorkerCount = 3
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "state", Client: metadataTestCaller{},
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := registerMetadataWorkers(registry, &sql.DB{}, pool, cfg); err != nil {
		t.Fatal(err)
	}
	services, err := registry.Build([]components.Role{components.RoleMetadata})
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 4 {
		t.Fatalf("metadata services = %d, want 4", len(services))
	}
	if _, ok := services[0].(*metadata.SourceDiscoverer); !ok {
		t.Fatalf("metadata service type = %T", services[0])
	}
	if services[0].Name() != "metadata-source-discovery" {
		t.Fatalf("metadata discovery name = %q", services[0].Name())
	}
	for index, service := range services[1:] {
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
