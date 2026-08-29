package app

import (
	"context"
	"fmt"

	"github.com/islishude/etherview/internal/analytics"
	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/contractartifact"
	"github.com/islishude/etherview/internal/etherscan"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
	"github.com/islishude/etherview/internal/metadata"
	"github.com/islishude/etherview/internal/observability"
	"github.com/islishude/etherview/internal/query"
	"github.com/islishude/etherview/internal/state"
	"github.com/islishude/etherview/internal/userauth"
	webui "github.com/islishude/etherview/web"
)

func (assembly runtimeAssembly) registerAPIComponents() error {
	b := assembly.backend
	ctx := assembly.ctx
	cfg := assembly.cfg
	roleSet := assembly.roleSet
	db := assembly.db
	readDB := assembly.readDB
	logger := assembly.logger
	registry := assembly.registry
	businessObserver := assembly.businessObserver
	telemetry := assembly.telemetry
	rpcBuild := assembly.rpcBuild
	pendingRepository := assembly.pendingRepository
	runtimeEvents := assembly.runtimeEvents
	redisAccelerator := assembly.redisAccelerator
	broker := assembly.broker
	compilerCatalog := assembly.compilerCatalog
	verificationReader := assembly.verificationReader
	verificationSubmitter := assembly.verificationSubmitter
	compatibilityVerification := assembly.compatibilityVerification
	lifecycle := assembly.lifecycle
	componentRegistry := assembly.componentRegistry
	chainID := assembly.chainID
	if roleSet[components.RoleAPI] {
		var (
			canonicalState state.CanonicalSource
			nftState       catalog.NFTStateReconciler
			erc20State     catalog.ERC20StateReconciler
			nameResolver   query.NameResolver
			addressNames   httpapi.AddressNameReader
			priceProvider  etherscan.PriceProvider
		)
		if rpcBuild != nil && len(rpcBuild.Pool.Names(ethrpc.PurposeState)) > 0 {
			// Canonical state is a correctness fence around external RPC reads
			// and exact observation writes. It must not inherit replica lag.
			canonicalState = state.PostgresCanonicalSource{DB: db, ChainID: chainID}
		}
		ensService, err := newENSService(ctx, db, cfg, rpcBuild, canonicalState, businessObserver, logger)
		if err != nil {
			return err
		}
		if ensService != nil {
			nameResolver = ensService
			addressNames = ensService
		}
		priceProvider, err = newAPIPriceProvider(db, cfg)
		if err != nil {
			return err
		}
		if rpcBuild != nil && len(rpcBuild.Pool.Names(ethrpc.PurposeState)) > 0 {
			stateReconciler, stateErr := state.NewNFTReconciler(db, rpcBuild.Pool, canonicalState)
			if stateErr != nil {
				return stateErr
			}
			nftState = stateReconciler
			erc20State = stateReconciler
		}
		traceCache, err := newAPITraceCache(ctx, cfg)
		if err != nil {
			return err
		}
		catalogReader, err := catalog.NewPostgres(readDB, catalog.Options{
			NFTState: nftState, ERC20State: erc20State,
			TraceCache: traceCache, Logger: logger,
		})
		if err != nil {
			return err
		}
		analyticsReader, err := analytics.NewReader(readDB)
		if err != nil {
			return err
		}
		completeness := configuredCompleteness(cfg, rpcBuild)
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
		writerReader, err := query.NewPostgresReader(db, queryOptions)
		if err != nil {
			return err
		}
		homeFeed, err := httpapi.NewHomeFeed(writerReader, broker, httpapi.HomeFeedOptions{
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
		var (
			compatibilityState etherscan.StateProvider
			delegationBindings httpapi.DelegationBindingReader
		)
		if canonicalState != nil {
			stateReader := &state.Reader{
				Base: baseReader, Pool: rpcBuild.Pool, Completeness: completeness,
				Canonical: canonicalState, Origin: reader, DelegationHistory: writerReader,
			}
			publicReader = stateReader
			compatibilityState = stateReader
			delegationBindings = stateReader
		}
		readinessStatus := publicReader.Status
		if redisAccelerator != nil {
			cachedReader := redisStatusReader{Reader: publicReader, cache: redisAccelerator, chainID: cfg.Chain.ID}
			publicReader = cachedReader
			readinessStatus = cachedReader.ReadinessStatus
		}
		// Included identity must win over a node-local replacement observation,
		// so the unified detail lookup cannot inherit replica lag when enabled.
		transactionReader := selectTransactionDetailReader(cfg.Features.Mempool, publicReader, writerReader)
		artifactResolver, err := contractartifact.NewResolver(db)
		if err != nil {
			return err
		}
		compatibilityOptions := etherscan.PostgresOptions{
			ChainID: cfg.Chain.ID, State: compatibilityState, Price: priceProvider,
			ERC20State: erc20State, NFTState: nftState,
			Verification: compatibilityVerification, Artifacts: artifactResolver,
			VerificationMaxInputBytes: cfg.Verification.MaxInputBytes,
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
		var (
			metadataReader metadata.NFTMetadataReader
			mediaSource    metadata.NFTImageSource
			mediaProxy     *metadata.MediaProxy
		)
		if cfg.Features.NFTMetadata {
			metadataReader, err = metadata.NewPostgresMetadataReader(readDB, chainID, cfg.Metadata.IPFSGateway)
			if err != nil {
				return err
			}
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
			userAPIKeys        httpapi.UserAPIKeyAdministration
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
		if cfg.Features.UserAPIKeys {
			apiKeyRepository, err := auth.NewPostgresRepository(db)
			if err != nil {
				return err
			}
			userAPIKeys, err = auth.NewUserService(auth.Manager{
				Repository: apiKeyRepository,
				Pepper:     []byte(cfg.Security.APIKeyPepper),
			}, auth.UserKeyPolicy{
				Rate: cfg.UserAuth.APIKeyRate, Burst: cfg.UserAuth.APIKeyBurst,
				MaximumActive: cfg.UserAuth.MaxActiveAPIKeys,
			})
			if err != nil {
				return err
			}
		}
		billingDispatcher, billingReader, err := newBillingServices(
			cfg, db, userRepository, registry, logger,
		)
		if err != nil {
			return err
		}
		prepaidLedger, usageDispatcher, err := newPrepaidServices(cfg, db, logger, registry)
		if err != nil {
			return err
		}
		topupDispatcher, err := newTopupDispatcher(cfg, billingReader, prepaidLedger, logger, registry)
		if err != nil {
			return err
		}
		limiter := auth.Limiter(auth.NewMemoryLimiter(nil))
		if redisAccelerator != nil {
			limiter = redisAccelerator.Limiter(limiter)
		}
		compatibility := &etherscan.Handler{
			ChainID: cfg.Chain.ID, Backend: compatibilityBackend,
			MaxBody:      int64(cfg.Verification.MaxInputBytes) + 1<<20,
			PublicOrigin: cfg.Server.PublicURL, Usage: usageDispatcher,
		}
		proxyReader := newProxyReaderAdapter(
			writerReader, cfg.Chain.ID, cfg.Features.ProxyDetectionV2Public,
		)
		webHandler := webui.NewHandler()
		handler, err := httpapi.New(httpapi.Options{
			Config: cfg, Reader: publicReader, TransactionReader: transactionReader, AddressActivities: reader,
			AddressEnrichment: catalogReader, AddressNames: addressNames,
			DelegationBindings: delegationBindings, DelegationHistory: catalogReader,
			Genesis: reader, Catalog: catalogReader, Web: webHandler,
			WebRoutePattern: webHandler.RoutePattern,
			Analytics:       analyticsReader,
			ProxyReader:     proxyReader,
			Etherscan:       compatibility, Events: broker, HomeSnapshots: homeFeed,
			Mempool:            pendingRepository,
			VerificationReader: verificationReader, VerificationSubmitter: verificationSubmitter,
			CompilerCatalog:     compilerCatalog,
			VerificationTargets: verificationTargets,
			NFTMetadataReader:   metadataReader,
			NFTMediaSource:      mediaSource, NFTMediaProxy: mediaProxy,
			UserAuth: userAuthenticator, UserAdministration: userAdministration,
			UserAPIKeys: userAPIKeys,
			Billing:     billingDispatcher, BillingReader: billingReader,
			PrepaidBilling:      prepaidLedger,
			TopupBilling:        topupDispatcher,
			MaxVerificationBody: int64(cfg.Verification.MaxInputBytes) + 1<<20,
			Metrics:             registry.Handler(), Logger: logger, RuntimeReady: lifecycle.Ready,
			ReadinessStatus: readinessStatus,
			Requirements: httpapi.CapabilityRequirements{
				Native: true, Catalog: true, Analytics: true,
				Compatibility: true, Events: true, HomeSnapshots: true,
				Metadata: true, Proxy: true, Verification: true, Web: true,
			},
		})
		if err != nil {
			return err
		}
		publicHandler, err := b.protectPublicAPI(db, cfg, registry, limiter, handler)
		if err != nil {
			return err
		}
		publicHandler = observability.HTTPMiddleware(publicHandler, observability.HTTPOptions{
			Registry: registry, Logger: logger, Component: "http-api", Telemetry: telemetry,
			Route: handler.RoutePattern, PanicResponse: httpapi.WriteRecoveredPanicResponse,
		})
		apiService := httpapi.NewService(cfg, publicHandler, logger)
		if err := componentRegistry.Register(components.RoleAPI, "20-public-api", func() (components.Service, error) {
			return apiService, nil
		}); err != nil {
			return err
		}
	}
	return nil
}
