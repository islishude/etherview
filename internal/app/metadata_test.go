package app

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"

	"github.com/islishude/etherview/internal/metadata"
)

func TestRegisterMetadataWorkersUseUniqueDurableSafeWorkers(t *testing.T) {
	t.Parallel()
	registry := components.NewRegistry()
	cfg := config.Default()
	cfg.Runtime.WorkerCount = 3
	if err := registerMetadataWorkers(registry, &pgxpool.Pool{}, cfg, nil); err != nil {
		t.Fatal(err)
	}
	services, err := registry.Build([]components.Role{components.RoleMetadata})
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 4 {
		t.Fatalf("metadata services = %d, want 4", len(services))
	}
	if _, ok := services[0].(*metadata.UpdateDiscoverer); !ok {
		t.Fatalf("metadata update service type = %T", services[0])
	}
	if services[0].Name() != "metadata-update-discovery" {
		t.Fatalf("metadata update discovery name = %q", services[0].Name())
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

func TestMetadataWorkersConsumeDurableSourceObservations(t *testing.T) {
	t.Parallel()
	roles := map[components.Role]bool{components.RoleMetadata: true}
	if needsRPC(roles) {
		t.Fatal("metadata workers unexpectedly require a state RPC")
	}

}

func TestImageNavigationAllowlistDoesNotAuthorizeNetworkFetches(t *testing.T) {
	for _, roles := range [][]string{{"all"}, {"api"}, {"metadata"}} {
		t.Run(roles[0], func(t *testing.T) {
			cfg := config.Default()
			cfg.Runtime.Roles = roles
			cfg.Metadata.ImageNavigationAllowedOrigins = []string{"https://127.0.0.1"}
			client, err := newMetadataClient(cfg)
			if err != nil {
				t.Fatal(err)
			}
			for _, kind := range []metadata.Kind{metadata.KindJSON, metadata.KindImage} {
				_, err := client.Fetch(t.Context(), "https://127.0.0.1/image.png", kind)
				var failure *metadata.FetchError
				if !errors.As(err, &failure) || failure.Kind != metadata.FailureUnsafeURL {
					t.Fatalf("fetch kind %v: %v", kind, err)
				}
			}
		})
	}
}
