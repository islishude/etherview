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

func TestRegisterMetadataWorkerUsesDurableSafeWorker(t *testing.T) {
	t.Parallel()
	registry := components.NewRegistry()
	cfg := config.Default()
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "state", Client: metadataTestCaller{},
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := registerMetadataWorker(registry, &sql.DB{}, pool, cfg); err != nil {
		t.Fatal(err)
	}
	services, err := registry.Build([]components.Role{components.RoleMetadata})
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 2 {
		t.Fatalf("metadata services = %d, want 2", len(services))
	}
	if _, ok := services[0].(*metadata.SourceDiscoverer); !ok {
		t.Fatalf("metadata service type = %T", services[0])
	}
	if _, ok := services[1].(*metadata.Worker); !ok {
		t.Fatalf("metadata worker type = %T", services[1])
	}
	if services[0].Name() != "metadata-source-discovery" || services[1].Name() != "metadata-worker" {
		t.Fatalf("metadata service names = %q, %q", services[0].Name(), services[1].Name())
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
