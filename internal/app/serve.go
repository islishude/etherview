package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/islishude/etherview/internal/adapters"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/etherscan"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/events"
	"github.com/islishude/etherview/internal/httpapi"
	"github.com/islishude/etherview/internal/indexer"
	"github.com/islishude/etherview/internal/maintenance"
	"github.com/islishude/etherview/internal/mempool"
	"github.com/islishude/etherview/internal/metadata"
	"github.com/islishude/etherview/internal/observability"
	"github.com/islishude/etherview/internal/query"
	"github.com/islishude/etherview/internal/state"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/syncer"
	"github.com/islishude/etherview/internal/verify"
	"github.com/islishude/etherview/internal/webui"
)

func (b *Backend) Serve(ctx context.Context, cfg config.Config, roleNames []string) error {
	db, err := openDatabase(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := store.CheckSchema(ctx, db); err != nil {
		return err
	}
	roles, roleSet, err := componentRoles(roleNames)
	if err != nil {
		return err
	}
	logger := b.logger().With("roles", strings.Join(roleNames, ","), "chain_id", cfg.Chain.ID)
	registry := observability.NewRegistry(b.Version, strings.Join(roleNames, ","))
	tracker := &syncer.Tracker{}

	var rpcBuild *RPCBuild
	if needsRPC(roleSet) || (roleSet[components.RoleAPI] && len(cfg.RPC.Endpoints) > 0) {
		built, err := buildRPC(ctx, cfg, logger)
		if err != nil {
			return err
		}
		rpcBuild = &built
		if err := store.BindChainIdentity(ctx, db, built.Identity.ChainID, built.Identity.GenesisHash); err != nil {
			return err
		}
	} else {
		identity, err := store.ReadChainIdentity(ctx, db, strconv.FormatUint(cfg.Chain.ID, 10))
		if err != nil {
			return err
		}
		if cfg.Chain.GenesisHash != "" && !strings.EqualFold(cfg.Chain.GenesisHash, identity.GenesisHash.String()) {
			return fmt.Errorf("configured genesis %s does not match database genesis %s", cfg.Chain.GenesisHash, identity.GenesisHash)
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
	broker, err := events.NewDurableBroker(events.DefaultReplayLimit, runtimeEvents)
	if err != nil {
		return err
	}
	eventWake := make(chan struct{}, 1)
	signalEvents := func() {
		select {
		case eventWake <- struct{}{}:
		default:
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
	if cfg.Features.Verification {
		verificationRepository, err = verify.NewPostgresRepository(db, verify.RepositoryOptions{
			MaxRequestBytes: cfg.Verification.MaxInputBytes,
			MaxResultBytes:  cfg.Verification.MaxOutputBytes,
		})
		if err != nil {
			return err
		}
		verificationService, err = verify.NewService(verificationRepository, cfg.Verification.MaxInputBytes)
		if err != nil {
			return err
		}
	}
	publicVerification := publicVerificationService(cfg, verificationService)
	lifecycle := components.NewLifecycle()
	componentRegistry := components.NewRegistry()
	for _, role := range roles {
		role := role
		if err := componentRegistry.Register(role, "00-operations-http", func() (components.Service, error) {
			return &operationalService{
				address: cfg.Server.MetricsAddress, shutdownTimeout: cfg.Server.ShutdownTimeout,
				db: db, registry: registry, lifecycle: lifecycle,
			}, nil
		}); err != nil {
			return err
		}
	}
	if roleSet[components.RoleAPI] {
		relay, err := events.NewRelay(runtimeEvents, broker, events.RelayOptions{
			PollInterval: cfg.Runtime.PollInterval, Wake: eventWake, Logger: logger,
		})
		if err != nil {
			return err
		}
		if err := componentRegistry.Register(components.RoleAPI, "08-runtime-event-relay", func() (components.Service, error) {
			return relay, nil
		}); err != nil {
			return err
		}
	}

	if roleSet[components.RoleSync] {
		if len(rpcBuild.Pool.Names(ethrpc.PurposeHead)) == 0 {
			return errors.New("sync role requires HTTP RPC endpoints for both head and history purposes")
		}
		head, err := coreRPCSource.Head(ctx)
		if err != nil {
			return fmt.Errorf("read initial RPC head: %w", err)
		}
		if cfg.Chain.StartBlock <= head {
			if head == math.MaxUint64 {
				return errors.New("RPC head exceeds partition provisioning range")
			}
			if err := repository.EnsureBlockPartitions(ctx, cfg.Chain.StartBlock, head+1); err != nil {
				return fmt.Errorf("provision block partitions through RPC head: %w", err)
			}
		}
		service := &syncer.Service{
			ChainID: chainID, StartBlock: cfg.Chain.StartBlock,
			PollInterval: cfg.Runtime.PollInterval, Workers: cfg.Runtime.BackfillWorkers,
			WorkerID: runtimeWorkerID("core-backfill"), LeaseDuration: cfg.Runtime.LeaseDuration,
			Source: coreRPCSource, Repository: repository, Canonicalizer: coreCanonicalizer,
			Status: runtimeEvents, EventWake: signalEvents,
			Tracker: tracker, Observer: registry, Logger: logger,
		}
		if len(rpcBuild.WakeURLs) > 0 {
			headWake, err := syncer.NewHeadWake(rpcBuild.WakeURLs, syncer.HeadWakeOptions{Logger: logger})
			if err != nil {
				return err
			}
			service.Wake = headWake.Signal()
			if err := componentRegistry.Register(components.RoleSync, "05-new-head-wake", func() (components.Service, error) {
				return headWake, nil
			}); err != nil {
				return err
			}
		}
		if err := componentRegistry.Register(components.RoleSync, "10-core-sync", func() (components.Service, error) {
			return service, nil
		}); err != nil {
			return err
		}
		if cfg.Features.Mempool {
			poller, err := mempool.NewPoller(mempool.PoolSource{Pool: rpcBuild.Pool}, pendingRepository, mempool.PollerOptions{
				ChainID: cfg.Chain.ID, PollInterval: cfg.Mempool.PollInterval,
				Retention: cfg.Mempool.Retention, MaxTransactions: cfg.Mempool.MaxTransactions,
				MaxResponseBytes: cfg.Mempool.MaxResponseBytes, Logger: logger,
			})
			if err != nil {
				return err
			}
			if err := componentRegistry.Register(components.RoleSync, "15-pending-mempool", func() (components.Service, error) {
				return poller, nil
			}); err != nil {
				return err
			}
		}
	}

	if roleSet[components.RoleAPI] {
		var (
			canonicalState state.CanonicalSource
			nftState       catalog.NFTStateReconciler
			nameResolver   query.NameResolver
			priceProvider  etherscan.PriceProvider
		)
		if cfg.Adapters.NameBaseURL != "" || cfg.Features.Pricing {
			adapterClient, clientErr := metadata.New(metadata.Policy{
				Timeout: cfg.Adapters.FetchTimeout, MaxBytes: int64(cfg.Adapters.MaxResponseBytes),
				MaxRedirects: cfg.Adapters.MaxRedirects, UserAgent: "etherview-adapters/1",
			}, nil)
			if clientErr != nil {
				return fmt.Errorf("configure external adapters: %w", clientErr)
			}
			if cfg.Adapters.NameBaseURL != "" {
				nameResolver, err = adapters.NewPostgresNameService(db, cfg.Chain.ID, adapterClient, adapters.NameOptions{
					BaseURL: cfg.Adapters.NameBaseURL, Freshness: cfg.Adapters.NameFreshness,
					FailureTTL: cfg.Adapters.FailureTTL,
				})
				if err != nil {
					return fmt.Errorf("configure name adapter: %w", err)
				}
			}
			if cfg.Features.Pricing {
				priceService, priceErr := adapters.NewPostgresPriceService(db, cfg.Chain.ID, adapterClient, adapters.PriceOptions{
					BaseURL: cfg.Adapters.PriceBaseURL, Freshness: cfg.Adapters.PriceFreshness,
					FailureTTL: cfg.Adapters.FailureTTL,
				})
				if priceErr != nil {
					return fmt.Errorf("configure price adapter: %w", priceErr)
				}
				priceProvider = func(callbackCtx context.Context) (etherscan.NativePrice, error) {
					price, quoteErr := priceService.NativePrice(callbackCtx)
					return etherscan.NativePrice{USD: price.USD, BTC: price.BTC, ObservedAt: price.ObservedAt}, quoteErr
				}
			}
		}
		if rpcBuild != nil && len(rpcBuild.Pool.Names(ethrpc.PurposeState)) > 0 {
			canonicalState = state.PostgresCanonicalSource{DB: db, ChainID: chainID}
			nftState, err = state.NewNFTReconciler(db, rpcBuild.Pool, canonicalState)
			if err != nil {
				return err
			}
		}
		catalogReader, err := catalog.NewPostgres(db, catalog.Options{NFTState: nftState})
		if err != nil {
			return err
		}
		completeness := configuredCompleteness(cfg)
		if cfg.Features.Trace && (rpcBuild == nil || !traceRPCAvailable(rpcBuild.Pool)) {
			completeness.Trace = gen.StageStateUnavailable
		}
		reader, err := query.NewPostgresReader(db, query.Options{
			ChainID: cfg.Chain.ID, StartBlock: cfg.Chain.StartBlock,
			RuntimeStatus: func(callbackCtx context.Context) (query.RuntimeStatus, bool, error) {
				status, exists, err := runtimeEvents.Status(callbackCtx)
				return query.RuntimeStatus{
					Latest: status.Latest, Indexed: status.Indexed, HighestCovered: status.HighestCovered,
					LatestKnown: status.LatestKnown, IndexedKnown: status.IndexedKnown,
					HighestCoveredKnown: status.HighestCoveredKnown,
					BackfillComplete:    status.BackfillComplete, Ready: status.Ready,
				}, exists, err
			},
			OptionalStages: completeness, NameResolver: nameResolver,
		})
		if err != nil {
			return err
		}
		var publicReader httpapi.Reader = reader
		var compatibilityState etherscan.StateProvider
		if canonicalState != nil {
			stateReader := &state.Reader{
				Base: reader, Pool: rpcBuild.Pool, Completeness: completeness,
				Canonical: canonicalState,
			}
			publicReader = stateReader
			compatibilityState = stateReader
		}
		compatibilityBackend, err := etherscan.NewPostgresBackend(db, etherscan.PostgresOptions{
			ChainID: cfg.Chain.ID, State: compatibilityState, Price: priceProvider,
			Verification: publicVerification, VerificationMaxInputBytes: cfg.Verification.MaxInputBytes,
		})
		if err != nil {
			return err
		}
		compatibility := etherscan.Handler{
			ChainID: cfg.Chain.ID, Backend: compatibilityBackend,
			MaxBody: int64(cfg.Verification.MaxInputBytes) + 1<<20,
		}
		var (
			mediaSource metadata.NFTImageSource
			mediaProxy  *metadata.MediaProxy
		)
		if cfg.Features.NFTMetadata {
			mediaSource, err = metadata.NewPostgresImageSource(db, chainID)
			if err != nil {
				return err
			}
			mediaClient, err := newMetadataClient(cfg)
			if err != nil {
				return fmt.Errorf("configure NFT media proxy: %w", err)
			}
			mediaProxy, err = metadata.NewMediaProxy(mediaClient)
			if err != nil {
				return err
			}
		}
		handler, err := httpapi.New(httpapi.Options{
			Config: cfg, Reader: publicReader, Catalog: catalogReader, Web: webui.NewHandler(),
			Etherscan: compatibility, Events: broker, Mempool: pendingRepository, Verification: publicVerification,
			NFTMediaSource: mediaSource, NFTMediaProxy: mediaProxy,
			MaxVerificationBody: int64(cfg.Verification.MaxInputBytes) + 1<<20,
			Metrics:             registry.Handler(), Logger: logger, RuntimeReady: lifecycle.Ready,
		})
		if err != nil {
			return err
		}
		publicHandler, err := b.protectPublicAPI(db, cfg, registry, handler)
		if err != nil {
			return err
		}
		publicHandler = observability.HTTPMiddleware(publicHandler, observability.HTTPOptions{Registry: registry, Logger: logger})
		apiService := httpapi.NewService(cfg, publicHandler)
		if err := componentRegistry.Register(components.RoleAPI, "20-public-api", func() (components.Service, error) {
			return apiService, nil
		}); err != nil {
			return err
		}
	}

	if roleSet[components.RoleVerify] && cfg.Features.Verification {
		compiler, err := verificationCompiler(cfg)
		if err != nil {
			return err
		}
		if validator, ok := compiler.(verify.RuntimeValidator); ok {
			if err := validator.ValidateRuntime(ctx); err != nil {
				return fmt.Errorf("verification compiler sandbox is not ready: %w", err)
			}
		}
		worker, err := verify.NewWorker(verificationRepository, compiler, verify.WorkerOptions{
			WorkerID: verificationWorkerID(), LeaseDuration: cfg.Runtime.LeaseDuration,
			PollInterval: cfg.Runtime.PollInterval, MaxOutputBytes: cfg.Verification.MaxOutputBytes,
			Public: cfg.Security.PublicVerification,
		})
		if err != nil {
			return err
		}
		if err := componentRegistry.Register(components.RoleVerify, "40-contract-verification", func() (components.Service, error) {
			return worker, nil
		}); err != nil {
			return err
		}
	}

	if roleSet[components.RoleEnrich] {
		if rpcBuild == nil || len(rpcBuild.Pool.Names(ethrpc.PurposeState)) == 0 {
			return errors.New("enrich role requires an HTTP state RPC endpoint for block-pinned token detection")
		}
		tokenDetector, err := enrich.NewPoolTokenDetector(rpcBuild.Pool, enrich.TokenProbeLimits{})
		if err != nil {
			return err
		}
		queue, err := enrich.NewPostgresJobQueue(db)
		if err != nil {
			return err
		}
		stages := enrichmentDispatchStages(cfg.Features.Trace)
		dispatcher, err := enrich.NewOutboxDispatcher(db, queue, enrich.OutboxDispatcherOptions{
			PollInterval: cfg.Runtime.PollInterval,
			Stages:       stages,
		})
		if err != nil {
			return err
		}
		if err := componentRegistry.Register(components.RoleEnrich, "30-enrichment-outbox", func() (components.Service, error) {
			return dispatcher, nil
		}); err != nil {
			return err
		}
		tokenProcessor, err := enrich.NewPostgresTokenProcessorWithDetector(db, tokenDetector)
		if err != nil {
			return err
		}
		proxyProcessor, err := enrich.NewPostgresProxyProcessor(db, rpcBuild.Pool, enrich.ProxyLimits{})
		if err != nil {
			return err
		}
		abiProcessor, err := enrich.NewPostgresABIProcessorWithProxyDependency(db)
		if err != nil {
			return err
		}
		statsProcessor, err := enrich.NewPostgresStatsProcessor(db)
		if err != nil {
			return err
		}
		worker, err := enrich.NewWorker(queue, []enrich.Processor{proxyProcessor, abiProcessor, tokenProcessor, statsProcessor}, enrich.WorkerOptions{
			ID: runtimeWorkerID("enrich"), LeaseDuration: cfg.Runtime.LeaseDuration,
			PollInterval: cfg.Runtime.PollInterval,
		})
		if err != nil {
			return err
		}
		if err := componentRegistry.Register(components.RoleEnrich, "35-core-enrichment", func() (components.Service, error) {
			return worker, nil
		}); err != nil {
			return err
		}
	}

	if roleSet[components.RoleTrace] && cfg.Features.Trace {
		if rpcBuild == nil || !traceRPCAvailable(rpcBuild.Pool) {
			return errors.New("trace role is enabled but no configured trace RPC reports debug or trace-module capability")
		}
		queue, err := enrich.NewPostgresJobQueue(db)
		if err != nil {
			return err
		}
		processor, err := enrich.NewTraceRPCProcessor(db, rpcBuild.Pool, enrich.TraceLimits{})
		if err != nil {
			return err
		}
		worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
			ID: runtimeWorkerID("trace"), LeaseDuration: cfg.Runtime.LeaseDuration,
			PollInterval: cfg.Runtime.PollInterval,
		})
		if err != nil {
			return err
		}
		if err := componentRegistry.Register(components.RoleTrace, "37-trace-enrichment", func() (components.Service, error) {
			return worker, nil
		}); err != nil {
			return err
		}
	}

	if roleSet[components.RoleMetadata] && cfg.Features.NFTMetadata {
		if err := registerMetadataWorker(componentRegistry, db, cfg); err != nil {
			return err
		}
	}

	if roleSet[components.RoleMaintenance] {
		requestRepository, err := maintenance.NewPostgresRepository(db)
		if err != nil {
			return err
		}
		queue, err := enrich.NewPostgresJobQueue(db)
		if err != nil {
			return err
		}
		executor, err := maintenance.NewExecutor(chainID, coreRPCSource, coreCanonicalizer, repository, queue)
		if err != nil {
			return err
		}
		if err := registerMaintenanceWorker(componentRegistry, requestRepository, executor, maintenance.WorkerOptions{
			ServiceName: "maintenance-worker", WorkerID: runtimeWorkerID("maintenance"),
			PollInterval: cfg.Runtime.PollInterval,
		}); err != nil {
			return err
		}
		catalogCleaner, err := maintenance.NewPostgresCatalogCleaner(db)
		if err != nil {
			return err
		}
		if err := registerCatalogHousekeeper(componentRegistry, catalogCleaner, logger, maintenance.CatalogHousekeeperOptions{
			ChainID: cfg.Chain.ID, Interval: cfg.Maintenance.Interval,
			RetentionGenerations: cfg.Maintenance.SearchRetentionGenerations,
			AdapterDeleteBatch:   cfg.Maintenance.AdapterDeleteBatch,
		}); err != nil {
			return err
		}
	}

	for _, role := range []components.Role{
		components.RoleEnrich, components.RoleTrace, components.RoleVerify,
		components.RoleMetadata,
	} {
		if !roleSet[role] {
			continue
		}
		if role == components.RoleEnrich || role == components.RoleTrace && cfg.Features.Trace ||
			role == components.RoleVerify && cfg.Features.Verification || role == components.RoleMetadata && cfg.Features.NFTMetadata {
			continue
		}
		role := role
		key := "50-role-" + string(role)
		if err := componentRegistry.Register(role, key, func() (components.Service, error) {
			return &databaseRoleService{name: string(role) + "-worker", db: db, interval: cfg.Runtime.PollInterval}, nil
		}); err != nil {
			return err
		}
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
	logger.InfoContext(ctx, "starting Etherview components", "components", serviceNames(services))
	return components.RunWithOptions(ctx, services, components.RunOptions{
		Lifecycle: lifecycle, ShutdownTimeout: cfg.Server.ShutdownTimeout,
	})
}

func enrichmentDispatchStages(trace bool) []enrich.StageID {
	stages := []enrich.StageID{enrich.ProxyStage, enrich.ABIStage, enrich.TokenStage, enrich.StatsStage}
	if trace {
		stages = append(stages, enrich.TraceStage)
	}
	return stages
}

func (b *Backend) protectPublicAPI(db *sql.DB, cfg config.Config, observer auth.RateObserver, next http.Handler) (http.Handler, error) {
	limiter := auth.NewMemoryLimiter(nil)
	protected := auth.RateMiddleware{
		Limiter:   limiter,
		Anonymous: auth.Limit{Rate: cfg.Security.AnonymousRate, Burst: cfg.Security.AnonymousBurst},
		Observer:  observer,
	}.Wrap(next)
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/v2/api" {
			protected.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	}), nil
}

func componentRoles(names []string) ([]components.Role, map[components.Role]bool, error) {
	normalized, err := config.NormalizeRoles(names)
	if err != nil {
		return nil, nil, err
	}
	roles := make([]components.Role, 0, len(normalized))
	set := make(map[components.Role]bool, len(normalized))
	for _, name := range normalized {
		role := components.Role(name)
		roles = append(roles, role)
		set[role] = true
	}
	return roles, set, nil
}

func needsRPC(roles map[components.Role]bool) bool {
	return roles[components.RoleSync] || roles[components.RoleEnrich] || roles[components.RoleTrace] || roles[components.RoleMaintenance]
}

// productionComponentKeys is the durable role/feature graph contract used by
// both monolith and split processes. Serve compares it with the components it
// actually registered, so a new runtime component cannot silently diverge
// from the parity tests below.
func productionComponentKeys(cfg config.Config, roles []components.Role, wakeEnabled bool) []string {
	set := make(map[string]struct{})
	add := func(key string) { set[key] = struct{}{} }
	for _, role := range roles {
		add("00-operations-http")
		switch role {
		case components.RoleAPI:
			add("08-runtime-event-relay")
			add("20-public-api")
		case components.RoleSync:
			if wakeEnabled {
				add("05-new-head-wake")
			}
			add("10-core-sync")
			if cfg.Features.Mempool {
				add("15-pending-mempool")
			}
		case components.RoleEnrich:
			add("30-enrichment-outbox")
			add("35-core-enrichment")
		case components.RoleTrace:
			if cfg.Features.Trace {
				add("37-trace-enrichment")
			} else {
				add("50-role-trace")
			}
		case components.RoleVerify:
			if cfg.Features.Verification {
				add("40-contract-verification")
			} else {
				add("50-role-verify")
			}
		case components.RoleMetadata:
			if cfg.Features.NFTMetadata {
				add("45-nft-metadata")
			} else {
				add("50-role-metadata")
			}
		case components.RoleMaintenance:
			add("45-maintenance")
			add("46-search-catalog-maintenance")
		}
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func validateProductionComponentGraph(cfg config.Config, roles []components.Role, wakeEnabled bool, registeredKeys []string) error {
	expectedKeys := productionComponentKeys(cfg, roles, wakeEnabled)
	if !slices.Equal(registeredKeys, expectedKeys) {
		return fmt.Errorf("production component graph mismatch: registered=%v expected=%v", registeredKeys, expectedKeys)
	}
	return nil
}

func configuredCompleteness(cfg config.Config) gen.Completeness {
	stage := func(enabled bool) gen.StageState {
		if enabled {
			return gen.StageStatePending
		}
		return gen.StageStateUnavailable
	}
	return gen.Completeness{
		Core: gen.StageStateComplete, Trace: stage(cfg.Features.Trace),
		Metadata: stage(cfg.Features.NFTMetadata), State: stage(cfg.Features.HistoricalState),
	}
}

func traceRPCAvailable(pool *ethrpc.Pool) bool {
	if pool == nil {
		return false
	}
	for range pool.Names(ethrpc.PurposeTrace) {
		endpoint, err := pool.Acquire(ethrpc.PurposeTrace)
		if err != nil {
			return false
		}
		if endpoint.Capabilities.Status(ethrpc.CapabilityDebugTrace) != ethrpc.AvailabilityUnavailable ||
			endpoint.Capabilities.Status(ethrpc.CapabilityParityTrace) != ethrpc.AvailabilityUnavailable {
			return true
		}
	}
	return false
}

func serviceNames(services []components.Service) []string {
	names := make([]string, len(services))
	for index, service := range services {
		names[index] = service.Name()
	}
	return names
}
