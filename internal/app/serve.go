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

	"github.com/islishude/etherview/internal/accelerator"
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
	genesisstate "github.com/islishude/etherview/internal/genesis"
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
	"github.com/islishude/etherview/internal/userauth"
	"github.com/islishude/etherview/internal/verify"
	webui "github.com/islishude/etherview/web"
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
		built, err := buildRPC(ctx, cfg, logger, registry)
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
			// API/verify-only processes must be able to participate in a fresh
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
	if cfg.Features.Verification {
		compilerCatalog, err = verify.NewCompilerCatalog(db, verify.CompilerCatalogOptions{
			Sources: map[verify.Language]string{
				verify.LanguageSolidity: cfg.Verification.CatalogURLs["solidity"],
				verify.LanguageVyper:    cfg.Verification.CatalogURLs["vyper"],
			},
			AllowedOrigins: cfg.Verification.AllowedDownloadOrigins,
			Timeout:        cfg.Verification.Timeout, Freshness: cfg.Verification.CatalogMaxStaleness,
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
			verify.ServiceOptions{
				RequiresHardIsolation: cfg.Security.PublicVerification,
				Catalog:               compilerCatalog, RunnerImage: cfg.Verification.RunnerImage,
			},
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
	for _, role := range roles {
		if err := componentRegistry.Register(role, "00-operations-http", func() (components.Service, error) {
			return &operationalService{
				address: cfg.Server.MetricsAddress, shutdownTimeout: cfg.Server.ShutdownTimeout,
				db: databaseHealth, registry: registry, lifecycle: lifecycle, logger: logger, telemetry: telemetry,
			}, nil
		}); err != nil {
			return err
		}
		if err := componentRegistry.Register(role, "02-durable-metrics", func() (components.Service, error) {
			return metricCollector, nil
		}); err != nil {
			return err
		}
		if telemetry != nil {
			if err := componentRegistry.Register(role, "03-opentelemetry-traces", func() (components.Service, error) {
				return telemetry, nil
			}); err != nil {
				return err
			}
		}
	}
	if natsWake != nil {
		for _, role := range roles {
			if !roleUsesNATSWake(role, cfg) {
				continue
			}
			role := role
			if err := componentRegistry.Register(role, "04-optional-nats-wake", func() (components.Service, error) {
				return natsWake, nil
			}); err != nil {
				return err
			}
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
			BackfillBatchBlocks:     uint64(cfg.Runtime.BackfillBatchBlocks),
			SyncProgressLogInterval: cfg.Observability.SyncProgressLogInterval,
			WorkerID:                runtimeWorkerID("core-backfill"),
			LeaseDuration:           cfg.Runtime.LeaseDuration,
			Source:                  coreRPCSource, Repository: repository, Canonicalizer: coreCanonicalizer,
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
		genesisQueue, err := enrich.NewPostgresJobQueue(db)
		if err != nil {
			return err
		}
		genesisImporter, err := genesisstate.NewImporter(
			db, cfg.Chain, genesisQueue, cfg.Runtime.PollInterval,
		)
		if err != nil {
			return err
		}
		if err := componentRegistry.Register(components.RoleSync, "12-genesis-state", func() (components.Service, error) {
			return genesisImporter, nil
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
			// Canonical state is a correctness fence around external RPC reads
			// and exact observation writes. It must not inherit replica lag.
			canonicalState = state.PostgresCanonicalSource{DB: db, ChainID: chainID}
			nftState, err = state.NewNFTReconciler(db, rpcBuild.Pool, canonicalState)
			if err != nil {
				return err
			}
		}
		var traceCache accelerator.BlobStore
		if cfg.Adapters.S3Endpoint != "" {
			traceCache, err = accelerator.NewS3BlobStore(cfg.Adapters.S3Endpoint, accelerator.S3Options{
				Bucket: cfg.Adapters.S3Bucket, Prefix: cfg.Adapters.S3Prefix, Region: cfg.Adapters.S3Region,
				AccessKey: cfg.Adapters.S3AccessKey, SecretKey: cfg.Adapters.S3SecretKey,
				SessionToken: cfg.Adapters.S3SessionToken, PathStyle: cfg.Adapters.S3PathStyle,
				OperationTimeout: cfg.Adapters.OperationTimeout, MaxObjectBytes: cfg.Adapters.S3MaxObjectBytes,
			})
			if err != nil {
				return err
			}
		}
		catalogReader, err := catalog.NewPostgres(readDB, catalog.Options{NFTState: nftState, TraceCache: traceCache, Logger: logger})
		if err != nil {
			return err
		}
		completeness := configuredCompleteness(cfg)
		if cfg.Features.Trace && (rpcBuild == nil || !traceRPCAvailable(rpcBuild.Pool)) {
			completeness.Trace = gen.StageStateUnavailable
		}
		queryOptions := query.Options{
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
		}
		reader, err := query.NewPostgresReader(readDB, queryOptions)
		if err != nil {
			return err
		}
		homeReader, err := query.NewPostgresReader(db, queryOptions)
		if err != nil {
			return err
		}
		homeFeed, err := httpapi.NewHomeFeed(homeReader, broker, httpapi.HomeFeedOptions{
			ChainID: cfg.Chain.ID,
			Logger:  logger,
		})
		if err != nil {
			return err
		}
		if err := componentRegistry.Register(
			components.RoleAPI,
			"09-home-snapshot-feed",
			func() (components.Service, error) { return homeFeed, nil },
		); err != nil {
			return err
		}
		var baseReader httpapi.Reader = reader
		if readDB != db && nameResolver != nil {
			writerSearchReader, err := query.NewPostgresReader(db, queryOptions)
			if err != nil {
				return err
			}
			baseReader = searchRoutingReader{Reader: reader, search: writerSearchReader}
		}
		publicReader := baseReader
		var compatibilityState etherscan.StateProvider
		if canonicalState != nil {
			stateReader := &state.Reader{
				Base: baseReader, Pool: rpcBuild.Pool, Completeness: completeness,
				Canonical: canonicalState,
			}
			publicReader = stateReader
			compatibilityState = stateReader
		}
		if redisAccelerator != nil {
			publicReader = redisStatusReader{Reader: publicReader, cache: redisAccelerator, chainID: cfg.Chain.ID}
		}
		compatibilityOptions := etherscan.PostgresOptions{
			ChainID: cfg.Chain.ID, State: compatibilityState, Price: priceProvider,
			Verification: compatibilityVerification, VerificationMaxInputBytes: cfg.Verification.MaxInputBytes,
		}
		readCompatibilityBackend, err := etherscan.NewPostgresBackend(readDB, compatibilityOptions)
		if err != nil {
			return err
		}
		var (
			compatibilityBackend etherscan.Backend                  = readCompatibilityBackend
			verificationTargets  httpapi.VerificationTargetResolver = readCompatibilityBackend
		)
		if readDB != db {
			authoritativeBackend, err := etherscan.NewPostgresBackend(db, compatibilityOptions)
			if err != nil {
				return err
			}
			compatibilityBackend = replicaAwareEtherscanBackend{
				reader: readCompatibilityBackend, authoritative: authoritativeBackend,
			}
			verificationTargets = authoritativeBackend
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
		var (
			userAuthenticator  httpapi.UserAuthenticator
			userAdministration httpapi.UserAdministration
			userRepository     *userauth.PostgresRepository
		)
		if cfg.Features.UserAuth {
			userRepository, err = userauth.NewPostgresRepository(db, cfg.Chain.ID)
			if err != nil {
				return err
			}
			userService, err := userauth.NewService(userRepository, userauth.Options{
				ChainID: cfg.Chain.ID, PublicURL: cfg.Server.PublicURL,
				ChallengeTTL:  cfg.UserAuth.ChallengeTTL,
				SessionTTL:    cfg.UserAuth.SessionTTL,
				TouchInterval: cfg.UserAuth.LastUsedInterval,
				Pepper:        []byte(cfg.UserAuth.SessionPepper),
			})
			if err != nil {
				return err
			}
			userAuthenticator = httpapi.UserAuthAdapter{Service: userService}
			userAdministration = userRepository
		}
		billingDispatcher, billingReader, err := newBillingServices(
			cfg, db, userRepository, registry, logger,
		)
		if err != nil {
			return err
		}
		limiter := auth.Limiter(auth.NewMemoryLimiter(nil))
		if redisAccelerator != nil {
			limiter = redisAccelerator.Limiter(limiter)
		}
		trustedProxies, err := auth.NewTrustedProxySet(cfg.Security.TrustedProxies)
		if err != nil {
			return fmt.Errorf("configure trusted proxies: %w", err)
		}
		var quota func(http.Handler) http.Handler
		if cfg.Features.X402Billing {
			quota = auth.RateMiddleware{
				Limiter: limiter,
				Anonymous: auth.Limit{
					Rate: cfg.Security.AnonymousRate, Burst: cfg.Security.AnonymousBurst,
				},
				Observer: registry, TrustedProxies: trustedProxies,
			}.Wrap
		}
		handler, err := httpapi.New(httpapi.Options{
			Config: cfg, Reader: publicReader, AddressActivities: reader,
			Genesis: reader, Catalog: catalogReader, Web: webui.NewHandler(),
			Etherscan: compatibility, Events: broker, HomeSnapshots: homeFeed,
			Mempool:            pendingRepository,
			VerificationReader: verificationReader, VerificationSubmitter: verificationSubmitter,
			CompilerCatalog:     compilerCatalog,
			VerificationTargets: verificationTargets,
			NFTMediaSource:      mediaSource, NFTMediaProxy: mediaProxy,
			UserAuth: userAuthenticator, UserAdministration: userAdministration,
			Billing: billingDispatcher, BillingReader: billingReader, Quota: quota,
			MaxVerificationBody: int64(cfg.Verification.MaxInputBytes) + 1<<20,
			Metrics:             registry.Handler(), Logger: logger, RuntimeReady: lifecycle.Ready,
		})
		if err != nil {
			return err
		}
		outerLimiter := limiter
		if cfg.Features.X402Billing {
			outerLimiter = auth.NewMemoryLimiter(nil)
			if redisAccelerator != nil {
				outerLimiter = redisAccelerator.Limiter(outerLimiter)
			}
		}
		publicHandler, err := b.protectPublicAPI(db, cfg, registry, outerLimiter, handler)
		if err != nil {
			return err
		}
		publicHandler = observability.HTTPMiddleware(publicHandler, observability.HTTPOptions{
			Registry: registry, Logger: logger, Telemetry: telemetry,
			Route: handler.RoutePattern, PanicResponse: httpapi.WriteRecoveredPanicResponse,
		})
		apiService := httpapi.NewService(cfg, publicHandler, logger)
		if err := componentRegistry.Register(components.RoleAPI, "20-public-api", func() (components.Service, error) {
			return apiService, nil
		}); err != nil {
			return err
		}
	}

	if roleSet[components.RoleVerify] && cfg.Features.Verification {
		compiler, err := verificationCompiler(cfg, compilerCatalog)
		if err != nil {
			return err
		}
		if validator, ok := compiler.(verify.RuntimeValidator); ok {
			if err := validator.ValidateRuntime(ctx); err != nil {
				return fmt.Errorf("verification compiler sandbox is not ready: %w", err)
			}
		}
		for _, language := range []verify.Language{verify.LanguageSolidity, verify.LanguageVyper} {
			if _, refreshErr := compilerCatalog.Refresh(ctx, language); refreshErr != nil {
				if _, retainedErr := compilerCatalog.Versions(ctx, language); retainedErr != nil {
					return fmt.Errorf("refresh %s compiler catalog: %w", language, refreshErr)
				}
			}
		}
		catalogRefresher, err := verify.NewCatalogRefresher(
			compilerCatalog, cfg.Verification.CatalogRefreshInterval,
		)
		if err != nil {
			return err
		}
		if err := componentRegistry.Register(
			components.RoleVerify,
			"35-compiler-catalog-refresher",
			func() (components.Service, error) { return catalogRefresher, nil },
		); err != nil {
			return err
		}
		if err := registerWorkerPool(
			componentRegistry,
			components.RoleVerify,
			"40-contract-verification",
			"contract-verification-worker",
			cfg.Runtime.WorkerCount,
			func(index int, serviceName string) (components.Service, error) {
				return verify.NewWorker(verificationRepository, compiler, verify.WorkerOptions{
					ServiceName: serviceName, WorkerID: verificationWorkerID(index),
					LeaseDuration: cfg.Runtime.LeaseDuration,
					PollInterval:  cfg.Runtime.PollInterval, MaxOutputBytes: cfg.Verification.MaxOutputBytes,
					Public: cfg.Security.PublicVerification, Observer: businessObserver, Sourcify: sourcify,
				})
			},
		); err != nil {
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
			Wake:         outboxWake,
			Published: func() {
				if natsWake != nil {
					natsWake.Signal(accelerator.WakeJobs)
				}
			},
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
		processors := []enrich.Processor{proxyProcessor, abiProcessor, tokenProcessor, statsProcessor}
		if err := registerWorkerPool(
			componentRegistry,
			components.RoleEnrich,
			"35-core-enrichment",
			"enrichment-worker",
			cfg.Runtime.WorkerCount,
			func(index int, _ string) (components.Service, error) {
				return enrich.NewWorker(queue, processors, enrich.WorkerOptions{
					ID:            runtimeWorkerID(indexedWorkerName("enrich", index)),
					LeaseDuration: cfg.Runtime.LeaseDuration,
					PollInterval:  cfg.Runtime.PollInterval, Wake: enrichJobWake,
					Observer: businessObserver,
				})
			},
		); err != nil {
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
		traceProcessor, err := enrich.NewTraceRPCProcessor(db, rpcBuild.Pool, enrich.TraceLimits{})
		if err != nil {
			return err
		}
		stateDiffProcessor, err := enrich.NewStateDiffRPCProcessor(db, rpcBuild.Pool, enrich.StateDiffLimits{})
		if err != nil {
			return err
		}
		if err := registerWorkerPool(
			componentRegistry,
			components.RoleTrace,
			"37-trace-enrichment",
			"trace-enrichment-worker",
			cfg.Runtime.WorkerCount,
			func(index int, _ string) (components.Service, error) {
				return enrich.NewWorker(queue, []enrich.Processor{traceProcessor, stateDiffProcessor}, enrich.WorkerOptions{
					ID:            runtimeWorkerID(indexedWorkerName("trace", index)),
					LeaseDuration: cfg.Runtime.LeaseDuration,
					PollInterval:  cfg.Runtime.PollInterval, Wake: traceJobWake,
					Observer: businessObserver,
				})
			},
		); err != nil {
			return err
		}
	}

	if roleSet[components.RoleMetadata] && cfg.Features.NFTMetadata {
		if rpcBuild == nil || len(rpcBuild.Pool.Names(ethrpc.PurposeState)) == 0 {
			return errors.New("metadata role requires an HTTP state RPC endpoint for block-pinned source discovery")
		}
		if err := registerMetadataWorkers(componentRegistry, db, rpcBuild.Pool, cfg, businessObserver); err != nil {
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
		if err := registerMaintenanceWorkers(componentRegistry, requestRepository, executor, cfg.Runtime.WorkerCount, maintenance.WorkerOptions{
			PollInterval: cfg.Runtime.PollInterval, Observer: businessObserver,
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
		// Authentication and billing housekeeping deliberately receives only
		// the writer pool. The maintenance role does not load the API-only
		// session, fingerprint, or facilitator-header Secrets.
		if err := registerAuthBillingHousekeepers(
			componentRegistry, db, cfg, logger,
		); err != nil {
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
		stages = append(stages, enrich.TraceStage, enrich.StateDiffStage)
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
	if !cfg.Features.X402Billing {
		protected = auth.RateMiddleware{
			Limiter: limiter,
			Anonymous: auth.Limit{
				Rate: cfg.Security.AnonymousRate, Burst: cfg.Security.AnonymousBurst,
			},
			Observer: observer, TrustedProxies: trustedProxies,
		}.Wrap(protected)
	}
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
	if cfg.Features.X402Billing {
		// The coarse per-peer bucket deliberately precedes API-key parsing. The
		// handler's inner quota gate preserves the original anonymous/API-key
		// policy after billing selects a paid versus bypass route.
		protected = auth.RateMiddleware{
			Limiter: limiter,
			Anonymous: auth.Limit{
				Rate: cfg.Billing.CoarseIPRate, Burst: cfg.Billing.CoarseIPBurst,
			},
			Observer: observer, TrustedProxies: trustedProxies,
		}.Wrap(protected)
	}
	return httpapi.NFTMediaSecurityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/v2/api" {
			protected.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})), nil
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

func needsRPCForServe(roles map[components.Role]bool, cfg config.Config) bool {
	return needsRPC(roles) || roles[components.RoleMetadata] && cfg.Features.NFTMetadata
}

func roleUsesNATSWake(role components.Role, cfg config.Config) bool {
	return role == components.RoleAPI || role == components.RoleSync || role == components.RoleEnrich ||
		role == components.RoleTrace && cfg.Features.Trace
}

func rolesUseNATSWake(roles []components.Role, cfg config.Config) bool {
	for _, role := range roles {
		if roleUsesNATSWake(role, cfg) {
			return true
		}
	}
	return false
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
		add("02-durable-metrics")
		if cfg.Observability.OTLPTraceEndpoint != "" {
			add("03-opentelemetry-traces")
		}
		if cfg.Adapters.NATSURL != "" && roleUsesNATSWake(role, cfg) {
			add("04-optional-nats-wake")
		}
		switch role {
		case components.RoleAPI:
			add("08-runtime-event-relay")
			add("09-home-snapshot-feed")
			add("20-public-api")
		case components.RoleSync:
			if wakeEnabled {
				add("05-new-head-wake")
			}
			add("10-core-sync")
			add("12-genesis-state")
			if cfg.Features.Mempool {
				add("15-pending-mempool")
			}
		case components.RoleEnrich:
			add("30-enrichment-outbox")
			addWorkerComponentKeys(add, "35-core-enrichment", cfg.Runtime.WorkerCount)
		case components.RoleTrace:
			if cfg.Features.Trace {
				addWorkerComponentKeys(add, "37-trace-enrichment", cfg.Runtime.WorkerCount)
			} else {
				add("50-role-trace")
			}
		case components.RoleVerify:
			if cfg.Features.Verification {
				addWorkerComponentKeys(add, "40-contract-verification", cfg.Runtime.WorkerCount)
			} else {
				add("50-role-verify")
			}
		case components.RoleMetadata:
			if cfg.Features.NFTMetadata {
				add("42-nft-metadata-discovery")
				addWorkerComponentKeys(add, "45-nft-metadata", cfg.Runtime.WorkerCount)
			} else {
				add("50-role-metadata")
			}
		case components.RoleMaintenance:
			addWorkerComponentKeys(add, "45-maintenance", cfg.Runtime.WorkerCount)
			add("46-search-catalog-maintenance")
			if cfg.Features.UserAuth {
				add("47-user-auth-cleanup")
			}
			if cfg.Features.X402Billing {
				add("48-x402-billing-expiry")
			}
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
