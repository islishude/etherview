package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
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
			cfg.Observability.OTLPTraceEndpoint = "https://otel.example:4318"
			return cfg
		}(), wake: true},
		{name: "optional NATS wake", cfg: func() config.Config {
			cfg := config.Default()
			cfg.Adapters.NATSURL = "nats://127.0.0.1:4222"
			cfg.Features.Trace = true
			return cfg
		}()},
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
		{name: "api", role: components.RoleAPI, want: []string{"00-operations-http", "02-durable-metrics", "08-runtime-event-relay", "20-public-api"}},
		{name: "api NATS", role: components.RoleAPI, setup: func(cfg *config.Config) {
			cfg.Adapters.NATSURL = "nats://127.0.0.1:4222"
		}, want: []string{"00-operations-http", "02-durable-metrics", "04-optional-nats-wake", "08-runtime-event-relay", "20-public-api"}},
		{name: "api with OTLP", role: components.RoleAPI, setup: func(cfg *config.Config) {
			cfg.Observability.OTLPTraceEndpoint = "https://otel.example:4318"
		}, want: []string{"00-operations-http", "02-durable-metrics", "03-opentelemetry-traces", "08-runtime-event-relay", "20-public-api"}},
		{name: "sync", role: components.RoleSync, want: []string{"00-operations-http", "02-durable-metrics", "10-core-sync"}},
		{name: "sync optional", role: components.RoleSync, wake: true, setup: func(cfg *config.Config) {
			cfg.Features.Mempool = true
		}, want: []string{"00-operations-http", "02-durable-metrics", "05-new-head-wake", "10-core-sync", "15-pending-mempool"}},
		{name: "enrich", role: components.RoleEnrich, want: []string{
			"00-operations-http", "02-durable-metrics", "30-enrichment-outbox",
			"35-core-enrichment-01", "35-core-enrichment-02",
			"35-core-enrichment-03", "35-core-enrichment-04",
		}},
		{name: "trace disabled", role: components.RoleTrace, want: []string{"00-operations-http", "02-durable-metrics", "50-role-trace"}},
		{name: "trace enabled", role: components.RoleTrace, setup: func(cfg *config.Config) {
			cfg.Features.Trace = true
		}, want: []string{
			"00-operations-http", "02-durable-metrics",
			"37-trace-enrichment-01", "37-trace-enrichment-02",
			"37-trace-enrichment-03", "37-trace-enrichment-04",
		}},
		{name: "verify disabled", role: components.RoleVerify, want: []string{"00-operations-http", "02-durable-metrics", "50-role-verify"}},
		{name: "verify enabled", role: components.RoleVerify, setup: func(cfg *config.Config) {
			cfg.Features.Verification = true
		}, want: []string{
			"00-operations-http", "02-durable-metrics",
			"40-contract-verification-01", "40-contract-verification-02",
			"40-contract-verification-03", "40-contract-verification-04",
		}},
		{name: "metadata disabled", role: components.RoleMetadata, want: []string{"00-operations-http", "02-durable-metrics", "50-role-metadata"}},
		{name: "metadata enabled", role: components.RoleMetadata, setup: func(cfg *config.Config) {
			cfg.Features.NFTMetadata = true
		}, want: []string{
			"00-operations-http", "02-durable-metrics", "42-nft-metadata-discovery",
			"45-nft-metadata-01", "45-nft-metadata-02",
			"45-nft-metadata-03", "45-nft-metadata-04",
		}},
		{name: "maintenance", role: components.RoleMaintenance, want: []string{
			"00-operations-http", "02-durable-metrics",
			"45-maintenance-01", "45-maintenance-02",
			"45-maintenance-03", "45-maintenance-04",
			"46-search-catalog-maintenance",
		}},
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

func TestProductionWorkerCountControlsDurableRoleGraphs(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Runtime.WorkerCount = 2
	cfg.Features.Trace = true
	cfg.Features.Verification = true
	cfg.Features.NFTMetadata = true

	tests := []struct {
		role components.Role
		want []string
	}{
		{role: components.RoleEnrich, want: []string{
			"00-operations-http", "02-durable-metrics", "30-enrichment-outbox",
			"35-core-enrichment-01", "35-core-enrichment-02",
		}},
		{role: components.RoleTrace, want: []string{
			"00-operations-http", "02-durable-metrics",
			"37-trace-enrichment-01", "37-trace-enrichment-02",
		}},
		{role: components.RoleVerify, want: []string{
			"00-operations-http", "02-durable-metrics",
			"40-contract-verification-01", "40-contract-verification-02",
		}},
		{role: components.RoleMetadata, want: []string{
			"00-operations-http", "02-durable-metrics", "42-nft-metadata-discovery",
			"45-nft-metadata-01", "45-nft-metadata-02",
		}},
		{role: components.RoleMaintenance, want: []string{
			"00-operations-http", "02-durable-metrics",
			"45-maintenance-01", "45-maintenance-02",
			"46-search-catalog-maintenance",
		}},
	}
	for _, test := range tests {
		if got := productionComponentKeys(cfg, []components.Role{test.role}, false); !slices.Equal(got, test.want) {
			t.Errorf("role=%s keys=%v want=%v", test.role, got, test.want)
		}
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

type appVerificationTargetResolver struct{}

func (appVerificationTargetResolver) ResolveVerificationTarget(
	context.Context,
	string,
) (verify.VerificationTarget, error) {
	return verify.VerificationTarget{}, nil
}

func TestDisabledVerificationAndSourcifyRemainAbsentCapabilities(t *testing.T) {
	t.Parallel()
	var disabledVerification *verify.Service
	reader, submitter, compatibility := verificationCapabilityInterfaces(
		disabledVerification,
		disabledVerification,
	)
	sourcify := sourcifyCapabilityInterface(nil)
	if reader != nil || submitter != nil || compatibility != nil || sourcify != nil {
		t.Fatalf(
			"typed nil leaked across capability interfaces: reader=%T submitter=%T compatibility=%T sourcify=%T",
			reader,
			submitter,
			compatibility,
			sourcify,
		)
	}
	readOnlyVerification := &verify.Service{}
	reader, submitter, compatibility = verificationCapabilityInterfaces(readOnlyVerification, nil)
	if reader == nil || submitter != nil || compatibility != nil {
		t.Fatalf(
			"read-only verification capability mismatch: reader=%T submitter=%T compatibility=%T",
			reader,
			submitter,
			compatibility,
		)
	}

	handler, err := httpapi.New(httpapi.Options{
		Config:                config.Default(),
		Reader:                &appStatusReader{},
		VerificationReader:    reader,
		VerificationSubmitter: submitter,
		VerificationTargets:   appVerificationTargetResolver{},
		Sourcify:              sourcify,
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))
	if recorder.Code != http.StatusOK ||
		!strings.Contains(recorder.Body.String(), `"verification":false`) ||
		!strings.Contains(recorder.Body.String(), `"sourcify":false`) {
		t.Fatalf("config status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSourcifyClientHonorsFeatureSwitchAndBounds(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	client, err := sourcifyClient(cfg)
	if err != nil || client != nil {
		t.Fatalf("disabled Sourcify client=%p error=%v", client, err)
	}
	cfg.Features.Sourcify = true
	client, err = sourcifyClient(cfg)
	if err != nil || client == nil {
		t.Fatalf("enabled Sourcify client=%p error=%v", client, err)
	}
	cfg.Sourcify.MaxResponseBytes = 64<<20 + 1
	if _, err := sourcifyClient(cfg); err == nil || !strings.Contains(err.Error(), "response limit") {
		t.Fatalf("unexpected propagated Sourcify bound error: %v", err)
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

func TestRegisterMaintenanceWorkersUsesUniqueRealDurableWorkers(t *testing.T) {
	t.Parallel()
	registry := components.NewRegistry()
	if err := registerMaintenanceWorkers(
		registry,
		&appMaintenanceRepository{},
		&appRangeExecutor{},
		3,
		maintenance.WorkerOptions{},
	); err != nil {
		t.Fatal(err)
	}
	services, err := registry.Build([]components.Role{components.RoleMaintenance})
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 3 {
		t.Fatalf("services=%d", len(services))
	}
	for index, service := range services {
		if _, ok := service.(*maintenance.Worker); !ok {
			t.Fatalf("maintenance service type=%T", service)
		}
		wantName := indexedWorkerName("maintenance-worker", index)
		if service.Name() != wantName {
			t.Fatalf("maintenance service name=%q, want %q", service.Name(), wantName)
		}
	}
}

func TestRuntimeWorkerIDPreservesUniqueIndexedSuffix(t *testing.T) {
	t.Parallel()
	host := strings.Repeat("h", 256)
	first := runtimeWorkerIDForHost(host, 1234, indexedWorkerName("enrich", 0))
	second := runtimeWorkerIDForHost(host, 1234, indexedWorkerName("enrich", 1))
	if len(first) != 128 || len(second) != 128 {
		t.Fatalf("worker ID lengths=%d,%d", len(first), len(second))
	}
	if first == second || !strings.HasSuffix(first, "-1234-enrich-01") ||
		!strings.HasSuffix(second, "-1234-enrich-02") {
		t.Fatalf("worker IDs lost unique suffix: %q %q", first, second)
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
