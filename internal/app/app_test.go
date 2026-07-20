package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/maintenance"
	"github.com/islishude/etherview/internal/verify"
)

type appPinger struct{ err error }

func (p appPinger) PingContext(context.Context) error { return p.err }

type appBlockingService struct{ name string }

func (s appBlockingService) Name() string { return s.name }
func (s appBlockingService) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestRPCPurposesExpandsAll(t *testing.T) {
	t.Parallel()
	purposes := rpcPurposes([]string{"all"})
	for _, purpose := range []ethrpc.Purpose{
		ethrpc.PurposeHead, ethrpc.PurposeHistory, ethrpc.PurposeState,
		ethrpc.PurposeTrace, ethrpc.PurposeMempool,
	} {
		if !purposes[purpose] {
			t.Fatalf("purpose %q was not expanded", purpose)
		}
	}
}

func TestEnrichRoleRequiresRPC(t *testing.T) {
	t.Parallel()
	if !needsRPC(map[components.Role]bool{components.RoleEnrich: true}) {
		t.Fatal("enrich role did not require RPC for block-pinned token detection")
	}
	if needsRPC(map[components.Role]bool{components.RoleAPI: true}) {
		t.Fatal("API-only role unexpectedly requires RPC")
	}
}

func TestEnrichmentDispatcherAlwaysSchedulesABIStage(t *testing.T) {
	t.Parallel()
	if got, want := enrichmentDispatchStages(false), []enrich.StageID{enrich.ProxyStage, enrich.ABIStage, enrich.TokenStage, enrich.StatsStage}; !slices.Equal(got, want) {
		t.Fatalf("core enrichment stages=%v want=%v", got, want)
	}
	if got := enrichmentDispatchStages(true); !slices.Contains(got, enrich.ProxyStage) || !slices.Contains(got, enrich.ABIStage) || !slices.Contains(got, enrich.TraceStage) {
		t.Fatalf("trace-enabled enrichment stages=%v", got)
	}
}

func TestProductionMonolithGraphEqualsUnionOfSplitRoleGraphs(t *testing.T) {
	t.Parallel()
	allRoles, _, err := componentRoles([]string{"all"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		cfg  config.Config
		wake bool
	}{
		{name: "optional workers disabled", cfg: config.Default()},
		{name: "all optional workers enabled with websocket wake", cfg: func() config.Config {
			cfg := config.Default()
			cfg.Features.Mempool = true
			cfg.Features.Trace = true
			cfg.Features.Verification = true
			cfg.Features.NFTMetadata = true
			return cfg
		}(), wake: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			monolith := productionComponentKeys(test.cfg, allRoles, test.wake)
			union := make(map[string]struct{})
			for _, role := range allRoles {
				for _, key := range productionComponentKeys(test.cfg, []components.Role{role}, test.wake) {
					union[key] = struct{}{}
				}
			}
			split := make([]string, 0, len(union))
			for key := range union {
				split = append(split, key)
			}
			slices.Sort(split)
			if !slices.Equal(monolith, split) {
				t.Fatalf("monolith=%v split union=%v", monolith, split)
			}
		})
	}
}

func TestProductionRoleGraphIsFeatureAwareAndExact(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		role  components.Role
		setup func(*config.Config)
		wake  bool
		want  []string
	}{
		{name: "api", role: components.RoleAPI, want: []string{"00-operations-http", "08-runtime-event-relay", "20-public-api"}},
		{name: "sync", role: components.RoleSync, want: []string{"00-operations-http", "10-core-sync"}},
		{name: "sync optional", role: components.RoleSync, wake: true, setup: func(cfg *config.Config) {
			cfg.Features.Mempool = true
		}, want: []string{"00-operations-http", "05-new-head-wake", "10-core-sync", "15-pending-mempool"}},
		{name: "enrich", role: components.RoleEnrich, want: []string{"00-operations-http", "30-enrichment-outbox", "35-core-enrichment"}},
		{name: "trace disabled", role: components.RoleTrace, want: []string{"00-operations-http", "50-role-trace"}},
		{name: "trace enabled", role: components.RoleTrace, setup: func(cfg *config.Config) {
			cfg.Features.Trace = true
		}, want: []string{"00-operations-http", "37-trace-enrichment"}},
		{name: "verify disabled", role: components.RoleVerify, want: []string{"00-operations-http", "50-role-verify"}},
		{name: "verify enabled", role: components.RoleVerify, setup: func(cfg *config.Config) {
			cfg.Features.Verification = true
		}, want: []string{"00-operations-http", "40-contract-verification"}},
		{name: "metadata disabled", role: components.RoleMetadata, want: []string{"00-operations-http", "50-role-metadata"}},
		{name: "metadata enabled", role: components.RoleMetadata, setup: func(cfg *config.Config) {
			cfg.Features.NFTMetadata = true
		}, want: []string{"00-operations-http", "45-nft-metadata"}},
		{name: "maintenance", role: components.RoleMaintenance, want: []string{"00-operations-http", "45-maintenance", "46-search-catalog-maintenance"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Default()
			if test.setup != nil {
				test.setup(&cfg)
			}
			got := productionComponentKeys(cfg, []components.Role{test.role}, test.wake)
			if !slices.Equal(got, test.want) {
				t.Fatalf("role=%s keys=%v want=%v", test.role, got, test.want)
			}
		})
	}
}

func TestProductionAssemblyGuardFailsClosedOnGraphDrift(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	roles := []components.Role{components.RoleAPI, components.RoleSync}
	exact := productionComponentKeys(cfg, roles, false)
	if err := validateProductionComponentGraph(cfg, roles, false, exact); err != nil {
		t.Fatalf("exact production graph: %v", err)
	}
	if err := validateProductionComponentGraph(cfg, roles, false, exact[1:]); err == nil {
		t.Fatal("missing production registration was accepted")
	}
	unexpected := append(slices.Clone(exact), "99-unowned-component")
	if err := validateProductionComponentGraph(cfg, roles, false, unexpected); err == nil {
		t.Fatal("unexpected production registration was accepted")
	}
}

func TestOperationalReadinessTracksSharedLifecycleAndDatabase(t *testing.T) {
	t.Parallel()
	lifecycle := components.NewLifecycle()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- components.RunWithOptions(ctx, []components.Service{
			appBlockingService{name: "worker"},
		}, components.RunOptions{Lifecycle: lifecycle, ShutdownTimeout: time.Second})
	}()
	waitAppLifecycleReady(t, lifecycle)

	service := &operationalService{db: appPinger{}, lifecycle: lifecycle}
	request := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		service.handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
		return recorder
	}
	if recorder := request(); recorder.Code != http.StatusOK {
		t.Fatalf("ready status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	service.db = appPinger{err: errors.New("database unavailable")}
	if recorder := request(); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unready database status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	service.db = appPinger{}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("graceful shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("supervisor did not stop")
	}
	if recorder := request(); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("stopped status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func waitAppLifecycleReady(t *testing.T, lifecycle *components.Lifecycle) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("lifecycle did not become ready")
		case <-ticker.C:
			if lifecycle.Ready() {
				return
			}
		}
	}
}

func TestDatabaseErrorsRedactSecrets(t *testing.T) {
	t.Parallel()
	raw := "postgres://alice:very-secret@example.invalid/etherview"
	got := redactDatabaseError(errors.New("could not connect using "+raw+" password very-secret"), raw)
	if got == "" || containsAny(got, raw, "very-secret") {
		t.Fatalf("error was not redacted: %q", got)
	}
}

func TestPublicVerificationServiceHonorsSecuritySwitch(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	service := &verify.Service{}
	if got := publicVerificationService(cfg, service); got != nil {
		t.Fatalf("disabled public verification returned %p", got)
	}
	cfg.Security.PublicVerification = true
	if got := publicVerificationService(cfg, service); got != service {
		t.Fatalf("enabled public verification returned %p, want %p", got, service)
	}
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if candidate != "" && len(value) >= len(candidate) {
			for index := 0; index+len(candidate) <= len(value); index++ {
				if value[index:index+len(candidate)] == candidate {
					return true
				}
			}
		}
	}
	return false
}

type appMaintenanceRepository struct{}

func (*appMaintenanceRepository) Claim(context.Context, string) (maintenance.Lease, bool, error) {
	return maintenance.Lease{}, false, nil
}
func (*appMaintenanceRepository) GuardFinalized(context.Context, maintenance.Lease) error {
	return nil
}
func (*appMaintenanceRepository) Complete(context.Context, maintenance.Lease) error { return nil }
func (*appMaintenanceRepository) Fail(context.Context, maintenance.Lease, error) error {
	return nil
}
func (*appMaintenanceRepository) Release(context.Context, maintenance.Lease) error { return nil }

type appRangeExecutor struct{}

func (*appRangeExecutor) Repair(context.Context, maintenance.Request) error  { return nil }
func (*appRangeExecutor) Reindex(context.Context, maintenance.Request) error { return nil }

func TestRegisterMaintenanceWorkerUsesRealDurableWorker(t *testing.T) {
	t.Parallel()
	registry := components.NewRegistry()
	if err := registerMaintenanceWorker(
		registry,
		&appMaintenanceRepository{},
		&appRangeExecutor{},
		maintenance.WorkerOptions{WorkerID: "app-test"},
	); err != nil {
		t.Fatal(err)
	}
	services, err := registry.Build([]components.Role{components.RoleMaintenance})
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("services=%d", len(services))
	}
	if _, ok := services[0].(*maintenance.Worker); !ok {
		t.Fatalf("maintenance service type=%T", services[0])
	}
	if services[0].Name() != "maintenance-worker" {
		t.Fatalf("maintenance service name=%q", services[0].Name())
	}
}

type appCatalogCleaner struct {
	calls chan struct{}
	err   error
}

func (cleaner *appCatalogCleaner) Sweep(context.Context, uint64, int64, int, time.Time) (maintenance.CatalogCleanupResult, error) {
	select {
	case cleaner.calls <- struct{}{}:
	default:
	}
	return maintenance.CatalogCleanupResult{}, cleaner.err
}

func TestCatalogHousekeeperRegistrationReadinessAndShutdown(t *testing.T) {
	t.Parallel()
	cleaner := &appCatalogCleaner{calls: make(chan struct{}, 1), err: errors.New("database secret")}
	registry := components.NewRegistry()
	if err := registerCatalogHousekeeper(registry, cleaner, nil, maintenance.CatalogHousekeeperOptions{
		ChainID: 1, Interval: 50 * time.Millisecond, RetryInterval: time.Millisecond,
		RetentionGenerations: 1000, AdapterDeleteBatch: 1,
	}); err != nil {
		t.Fatal(err)
	}
	services, err := registry.Build([]components.Role{components.RoleMaintenance})
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("services=%d", len(services))
	}
	if _, ok := services[0].(*maintenance.CatalogHousekeeper); !ok {
		t.Fatalf("catalog housekeeper service type=%T", services[0])
	}
	if services[0].Name() != "search-catalog-maintenance" {
		t.Fatalf("catalog housekeeper service name=%q", services[0].Name())
	}

	lifecycle := components.NewLifecycle()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- components.RunWithOptions(ctx, services, components.RunOptions{
			Lifecycle: lifecycle, ShutdownTimeout: time.Second,
		})
	}()
	waitAppLifecycleReady(t, lifecycle)
	select {
	case <-cleaner.calls:
	case <-time.After(time.Second):
		t.Fatal("catalog housekeeper did not attempt an immediate sweep")
	}
	if !lifecycle.Ready() {
		t.Fatal("retryable catalog cleanup failure withdrew readiness")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("graceful catalog housekeeper shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("catalog housekeeper did not stop with the supervisor")
	}
	if lifecycle.Ready() {
		t.Fatal("catalog housekeeper lifecycle remained ready after shutdown")
	}
}

func TestValidateMaintenanceOperationStage(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		operation string
		stage     string
		valid     bool
	}{
		{"repair", "core", true},
		{"repair", "token", false},
		{"reindex", "token", true},
		{"reindex", "stats", true},
		{"reindex", "trace", true},
		{"reindex", "core", false},
		{"reindex", "", false},
		{"unknown", "core", false},
	} {
		err := validateMaintenanceOperationStage(test.operation, test.stage)
		if (err == nil) != test.valid {
			t.Fatalf("operation=%q stage=%q valid=%v error=%v", test.operation, test.stage, test.valid, err)
		}
	}
}
