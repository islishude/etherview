package app

import (
	"database/sql"
	"testing"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/metadata"
)

func TestRegisterMetadataWorkerUsesDurableSafeWorker(t *testing.T) {
	t.Parallel()
	registry := components.NewRegistry()
	cfg := config.Default()
	if err := registerMetadataWorker(registry, &sql.DB{}, cfg); err != nil {
		t.Fatal(err)
	}
	services, err := registry.Build([]components.Role{components.RoleMetadata})
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("metadata services = %d, want 1", len(services))
	}
	if _, ok := services[0].(*metadata.Worker); !ok {
		t.Fatalf("metadata service type = %T", services[0])
	}
	if services[0].Name() != "metadata-worker" {
		t.Fatalf("metadata service name = %q", services[0].Name())
	}
}

func TestMetadataRoleDoesNotMakeRPCACorrectnessDependency(t *testing.T) {
	t.Parallel()
	if needsRPC(map[components.Role]bool{components.RoleMetadata: true}) {
		t.Fatal("metadata-only role unexpectedly requires an execution RPC")
	}
}
