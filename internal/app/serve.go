package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/islishude/etherview/internal/accelerator"
	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/events"
	"github.com/islishude/etherview/internal/httpapi"
	"github.com/islishude/etherview/internal/indexer"
	"github.com/islishude/etherview/internal/mempool"
	"github.com/islishude/etherview/internal/observability"
	"github.com/islishude/etherview/internal/stagecontract"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/syncer"
	"github.com/islishude/etherview/internal/verify"
)

func (b *Backend) Serve(ctx context.Context, cfg config.Config, roleNames []string) error {
	roles, roleSet, err := componentRoles(roleNames)
	if err != nil {
		return err
	}
	db, err := openDatabase(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer db.Close() //nolint:errcheck
	if err := store.CheckSchema(ctx, db); err != nil {
		return err
	}
	readDB := db
	if readCfg, useDedicatedReadPool := readDatabaseConfigForRoles(cfg.Database, roleSet); useDedicatedReadPool {
		dbForRead, err := openReadDatabase(ctx, readCfg)
		if err != nil {
			return err
		}
		readDB = dbForRead
		defer dbForRead.Close() //nolint:errcheck
		if err := checkReadDatabaseSchema(ctx, readDB); err != nil {
			return err
		}
	}
	logger := b.logger().With(
		"roles", strings.Join(roleNames, ","), "chain_id", cfg.Chain.ID,
		"environment", cfg.Observability.Environment,
	)
	registry := observability.NewRegistry(b.Version, strings.Join(roleNames, ","))
	if err := registry.RegisterDatabasePool("writer", db); err != nil {
		return err
	}
	if readDB != db {
		if err := registry.RegisterDatabasePool("reader", readDB); err != nil {
			return err
		}
	}
	businessObserver := observability.NewBusinessObserver(registry, logger)
	tracker := &syncer.Tracker{}
	// Operational backlog snapshots stay writer-backed so every role exports
	// the same authoritative model even when an API reader is replaying behind.
	metricSource, err := observability.NewPostgresMetricSource(db, cfg.Chain.ID)
	if err != nil {
		return err
	}
	metricCollector, err := observability.NewDurableCollector(metricSource, registry, observability.DurableCollectorOptions{
		Interval: cfg.Observability.MetricsRefreshInterval, Logger: logger,
	})
	if err != nil {
		return err
	}
	var telemetry *observability.Telemetry
	if cfg.Observability.OTLPTraceEndpoint != "" {
		telemetry, err = observability.NewTelemetry(ctx, observability.TelemetryOptions{
			Endpoint: cfg.Observability.OTLPTraceEndpoint, Insecure: cfg.Observability.OTLPTraceInsecure,
			SampleRatio: cfg.Observability.TraceSampleRatio, ExportTimeout: cfg.Observability.TraceExportTimeout,
			Service: "etherview", Version: b.Version, Environment: cfg.Observability.Environment,
			Role: strings.Join(roleNames, ","), Logger: logger,
		})
		if err != nil {
			return err
		}
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Observability.TraceExportTimeout)
			defer cancel()
			telemetry.Shutdown(shutdownCtx)
		}()
	}

	var (
		rpcBuild         *RPCBuild
		databaseIdentity store.ChainIdentity
	)
	if needsRPCForServe(roleSet, cfg) ||
		(roleSet[components.RoleAPI] && len(cfg.RPC.Endpoints) > 0) {
		built, err := buildRPC(ctx, cfg, logger, businessObserver)
		if err != nil {
			return err
		}
		rpcBuild = &built
		if err := store.BindChainIdentity(ctx, db, built.Identity.ChainID, built.Identity.GenesisHash); err != nil {
			return err
		}
		databaseIdentity = store.ChainIdentity{
			ChainID: built.Identity.ChainID, GenesisHash: built.Identity.GenesisHash,
		}
	} else {
		if cfg.Chain.GenesisHash != "" {
			configuredGenesis, parseErr := ethrpc.ParseHash(cfg.Chain.GenesisHash)
			if parseErr != nil {
				return fmt.Errorf("parse configured genesis hash: %w", parseErr)
			}
			// API-only processes must be able to participate in a fresh
			// split deployment without racing the first RPC-backed role. Every
			// role receives the same server configuration; BindChainIdentity
			// serializes the exact pair and rejects any discovered mismatch.
			if err := store.BindChainIdentity(
				ctx,
				db,
				strconv.FormatUint(cfg.Chain.ID, 10),
				configuredGenesis,
			); err != nil {
				return err
			}
		}
		identity, err := store.ReadChainIdentity(ctx, db, strconv.FormatUint(cfg.Chain.ID, 10))
		if err != nil {
			return err
		}
		if cfg.Chain.GenesisHash != "" && !strings.EqualFold(cfg.Chain.GenesisHash, identity.GenesisHash.String()) {
			return fmt.Errorf("configured genesis %s does not match database genesis %s", cfg.Chain.GenesisHash, identity.GenesisHash)
		}
		databaseIdentity = identity
	}
	if readDB != db {
		if err := validateReadDatabaseIdentity(ctx, readDB, databaseIdentity); err != nil {
			return err
		}
	}

	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		return err
	}
	pendingRepository, err := mempool.NewPostgres(db, mempool.PostgresOptions{
		ChainID: cfg.Chain.ID, Enabled: cfg.Features.Mempool,
	})
	if err != nil {
		return err
	}
	chainID := strconv.FormatUint(cfg.Chain.ID, 10)
	runtimeEvents, err := events.NewPostgresStore(db, chainID, events.PostgresOptions{
		ReplayLimit: events.DefaultReplayLimit,
	})
	if err != nil {
		return err
	}
	var redisAccelerator *accelerator.RedisAccelerator
	if roleSet[components.RoleAPI] && cfg.Adapters.RedisURL != "" {
		redisAccelerator, err = accelerator.NewRedisAccelerator(cfg.Adapters.RedisURL, accelerator.RedisOptions{
			Namespace: cfg.Adapters.Namespace, ChainID: cfg.Chain.ID,
			OperationTimeout: cfg.Adapters.OperationTimeout, CacheTTL: cfg.Adapters.RedisCacheTTL,
			Logger: logger,
		})
		if err != nil {
			return err
		}
		defer func() { _ = redisAccelerator.Close() }()
		redisAccelerator.FenceCache(ctx)
	}
	var brokerInvalidators []events.CacheInvalidator
	if redisAccelerator != nil {
		brokerInvalidators = append(brokerInvalidators, redisAccelerator)
	}
	broker, err := events.NewDurableBroker(events.DefaultReplayLimit, runtimeEvents, brokerInvalidators...)
	if err != nil {
		return err
	}
	eventWake := make(chan struct{}, 1)
	var natsWake *accelerator.NATSWake
	var outboxWake, enrichJobWake, traceJobWake <-chan struct{}
	if cfg.Adapters.NATSURL != "" && rolesUseNATSWake(roles, cfg) {
		natsWake, err = accelerator.NewNATSWake(cfg.Adapters.NATSURL, accelerator.NATSWakeOptions{
			Namespace: cfg.Adapters.Namespace, ChainID: cfg.Chain.ID,
			ConnectTimeout: cfg.Adapters.ConnectTimeout, Logger: logger,
		})
		if err != nil {
			return err
		}
		if roleSet[components.RoleAPI] {
			if err := natsWake.SubscribeInto(accelerator.WakeRuntime, eventWake); err != nil {
				return err
			}
		}
		if roleSet[components.RoleEnrich] {
			if outboxWake, err = natsWake.Subscribe(accelerator.WakeOutbox); err != nil {
				return err
			}
			if enrichJobWake, err = natsWake.Subscribe(accelerator.WakeJobs); err != nil {
				return err
			}
		}
		if roleSet[components.RoleTrace] && cfg.Features.Trace {
			if traceJobWake, err = natsWake.Subscribe(accelerator.WakeJobs); err != nil {
				return err
			}
		}
	}
	signalEvents := func() {
		select {
		case eventWake <- struct{}{}:
		default:
		}
		if natsWake != nil {
			natsWake.Signal(accelerator.WakeRuntime)
			natsWake.Signal(accelerator.WakeOutbox)
		}
	}
	var coreRPCSource *syncer.RPCSource
	var coreCanonicalizer *indexer.Canonicalizer
	if roleSet[components.RoleSync] || roleSet[components.RoleMaintenance] {
		if rpcBuild == nil {
			return errors.New("sync or maintenance role requires an RPC pool")
		}
		if len(rpcBuild.Pool.Names(ethrpc.PurposeHistory)) == 0 {
			return errors.New("sync or maintenance role requires an HTTP history RPC endpoint")
		}
		fetcher := ethrpc.Fetcher{ReceiptStrategy: ethrpc.ReceiptStrategyAuto, ReceiptBatchSize: cfg.RPC.BatchSize}
		bundleSource := &indexer.PoolBundleSource{Pool: rpcBuild.Pool, Fetcher: fetcher, Purpose: ethrpc.PurposeHistory}
		headBundleSource := &indexer.PoolBundleSource{Pool: rpcBuild.Pool, Fetcher: fetcher, Purpose: ethrpc.PurposeHead}
		coreCanonicalizer = &indexer.Canonicalizer{
			ChainID: chainID, StartBlock: cfg.Chain.StartBlock,
			MaxReorgDepth: cfg.Chain.MaxReorgDepth, Repository: repository,
			Source: bundleSource, HeadSource: headBundleSource,
		}
		coreRPCSource = &syncer.RPCSource{Pool: rpcBuild.Pool, Fetcher: fetcher}
	}
	var verificationRepository *verify.PostgresRepository
	var verificationService *verify.Service
	var compilerCatalog *verify.CompilerCatalog
	if cfg.Features.Verification && roleSet[components.RoleAPI] {
		compilerCatalog, err = verify.NewCompilerCatalog(db, verify.CompilerCatalogOptions{
			Sources: map[verify.Language]string{
				verify.LanguageSolidity: cfg.Verification.CatalogURLs["solidity"],
			},
			Platform:       verify.CompilerPlatformEmscriptenWASM32,
			AllowedOrigins: cfg.Verification.AllowedDownloadOrigins,
			Timeout:        cfg.Verification.Timeout, Freshness: cfg.Verification.CatalogMaxStaleness,
			UnsafeAllowPrivateNetworks: cfg.Verification.UnsafeAllowPrivateDownloadNetworks,
		})
		if err != nil {
			return err
		}
		verificationRepository, err = verify.NewPostgresRepository(db, verify.RepositoryOptions{
			MaxRequestBytes: cfg.Verification.MaxInputBytes,
			MaxResultBytes:  cfg.Verification.MaxOutputBytes,
		})
		if err != nil {
			return err
		}
		verificationService, err = verify.NewService(
			verificationRepository,
			cfg.Verification.MaxInputBytes,
		)
		if err != nil {
			return err
		}
	}
	publicVerification := publicVerificationService(cfg, verificationService)
	verificationReader, verificationSubmitter, compatibilityVerification :=
		verificationCapabilityInterfaces(verificationService, publicVerification)
	sourcify, err := sourcifyClient(cfg)
	if err != nil {
		return err
	}
	lifecycle := components.NewLifecycle()
	componentRegistry := components.NewRegistry()
	var databaseHealth databasePinger = db
	if readDB != db {
		databaseHealth = databasePingerGroup{db, readDB}
	}
	assembly := runtimeAssembly{
		backend: b, ctx: ctx, cfg: cfg, roles: roles, roleSet: roleSet,
		db: db, readDB: readDB, logger: logger, registry: registry,
		businessObserver: businessObserver, tracker: tracker,
		metricCollector: metricCollector, telemetry: telemetry, rpcBuild: rpcBuild,
		repository: repository, pendingRepository: pendingRepository,
		runtimeEvents: runtimeEvents, redisAccelerator: redisAccelerator,
		broker: broker, eventWake: eventWake, natsWake: natsWake,
		outboxWake: outboxWake, enrichJobWake: enrichJobWake, traceJobWake: traceJobWake,
		signalEvents: signalEvents, coreRPCSource: coreRPCSource,
		coreCanonicalizer:      coreCanonicalizer,
		verificationRepository: verificationRepository, compilerCatalog: compilerCatalog,
		verificationReader: verificationReader, verificationSubmitter: verificationSubmitter,
		compatibilityVerification: compatibilityVerification, sourcify: sourcify,
		lifecycle: lifecycle, componentRegistry: componentRegistry,
		databaseHealth: databaseHealth, chainID: chainID,
	}
	if err := assembly.registerComponents(); err != nil {
		return err
	}

	services, err := componentRegistry.Build(roles)
	if err != nil {
		return err
	}
	registeredKeys, err := componentRegistry.Keys(roles)
	if err != nil {
		return err
	}
	wakeEnabled := rpcBuild != nil && len(rpcBuild.WakeURLs) > 0
	if err := validateProductionComponentGraph(cfg, roles, wakeEnabled, registeredKeys); err != nil {
		return err
	}
	logger.InfoContext(ctx, "starting Etherview components",
		"event", "runtime_starting", "components", serviceNames(services),
		"component_count", len(services),
	)
	return components.RunWithOptions(ctx, services, components.RunOptions{
		Lifecycle: lifecycle, ShutdownTimeout: cfg.Server.ShutdownTimeout, Logger: logger,
	})
}

func enrichmentDispatchStages(trace, userOperations bool) []stagecontract.ID {
	stages := []stagecontract.ID{
		stagecontract.Proxy, stagecontract.ABI, stagecontract.Token, stagecontract.Holder, stagecontract.Stats,
	}
	if userOperations {
		stages = append(stages, stagecontract.UserOperation)
	}
	if trace {
		stages = append(stages, stagecontract.Trace, stagecontract.StateDiff)
	}
	return stages
}

func (b *Backend) protectPublicAPI(db *sql.DB, cfg config.Config, observer auth.RateObserver, limiter auth.Limiter, next http.Handler) (http.Handler, error) {
	if limiter == nil {
		limiter = auth.NewMemoryLimiter(nil)
	}
	trustedProxies, err := auth.NewTrustedProxySet(cfg.Security.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("configure trusted proxies: %w", err)
	}
	protected := next
	protected = auth.RateMiddleware{
		Limiter: limiter,
		Anonymous: auth.Limit{
			Rate: cfg.Security.AnonymousRate, Burst: cfg.Security.AnonymousBurst,
		},
		Observer: observer, TrustedProxies: trustedProxies,
	}.Wrap(protected)
	if cfg.Security.APIKeyPepper != "" {
		repository, err := auth.NewPostgresRepository(db)
		if err != nil {
			return nil, err
		}
		manager := auth.Manager{
			Repository: repository, Pepper: []byte(cfg.Security.APIKeyPepper),
			MaxCompatibilityFormBodyBytes: int64(cfg.Verification.MaxInputBytes) + 1<<20,
		}
		protected = manager.Middleware(false, protected)
	}
	return httpapi.NFTMediaSecurityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/v2/api" {
			protected.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})), nil
}

func serviceNames(services []components.Service) []string {
	names := make([]string, len(services))
	for index, service := range services {
		names[index] = service.Name()
	}
	return names
}
