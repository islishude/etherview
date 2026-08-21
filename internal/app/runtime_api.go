package app

import (
	"context"
	"fmt"
	"net/http"

	"github.com/islishude/etherview/internal/accelerator"
	"github.com/islishude/etherview/internal/adapters"
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
		if cfg.Features.Pricing {
			adapterClient, clientErr := metadata.New(metadata.Policy{
				Timeout: cfg.Adapters.FetchTimeout, MaxBytes: int64(cfg.Adapters.MaxResponseBytes),
				MaxRedirects: cfg.Adapters.MaxRedirects, UserAgent: "etherview-adapters/1",
			}, nil)
			if clientErr != nil {
				return fmt.Errorf("configure external adapters: %w", clientErr)
			}
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
		if rpcBuild != nil && len(rpcBuild.Pool.Names(ethrpc.PurposeState)) > 0 {
			stateReconciler, stateErr := state.NewNFTReconciler(db, rpcBuild.Pool, canonicalState)
			if stateErr != nil {
				return stateErr
			}
			nftState = stateReconciler
			erc20State = stateReconciler
		}
		var traceCache accelerator.BlobStore
		if cfg.Adapters.S3Endpoint != "" {
			traceCache, err = accelerator.NewS3BlobStore(ctx, cfg.Adapters.S3Endpoint, accelerator.S3Options{
				Bucket: cfg.Adapters.S3Bucket, Prefix: cfg.Adapters.S3Prefix, Region: cfg.Adapters.S3Region,
				AccessKey: cfg.Adapters.S3AccessKey, SecretKey: cfg.Adapters.S3SecretKey,
				SessionToken: cfg.Adapters.S3SessionToken, PathStyle: cfg.Adapters.S3PathStyle,
				OperationTimeout: cfg.Adapters.OperationTimeout, MaxObjectBytes: cfg.Adapters.S3MaxObjectBytes,
			})
			if err != nil {
				return err
			}
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
		var compatibilityState etherscan.StateProvider
		if canonicalState != nil {
			stateReader := &state.Reader{
				Base: baseReader, Pool: rpcBuild.Pool, Completeness: completeness,
				Canonical: canonicalState, Origin: reader, DelegationHistory: writerReader,
			}
			publicReader = stateReader
			compatibilityState = stateReader
		}
		if redisAccelerator != nil {
			publicReader = redisStatusReader{Reader: publicReader, cache: redisAccelerator, chainID: cfg.Chain.ID}
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
		compatibility := etherscan.Handler{
			ChainID: cfg.Chain.ID, Backend: compatibilityBackend,
			MaxBody: int64(cfg.Verification.MaxInputBytes) + 1<<20,
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
			Config: cfg, Reader: publicReader, TransactionReader: transactionReader, AddressActivities: reader,
			AddressNames: addressNames,
			Genesis:      reader, Catalog: catalogReader, Web: webui.NewHandler(),
			Analytics: analyticsReader,
			ProxyReader: newProxyReaderAdapter(
				writerReader, cfg.Chain.ID, cfg.Features.ProxyDetectionV2Public,
			),
			Etherscan: compatibility, Events: broker, HomeSnapshots: homeFeed,
			Mempool:            pendingRepository,
			VerificationReader: verificationReader, VerificationSubmitter: verificationSubmitter,
			CompilerCatalog:     compilerCatalog,
			VerificationTargets: verificationTargets,
			NFTMetadataReader:   metadataReader,
			NFTMediaSource:      mediaSource, NFTMediaProxy: mediaProxy,
			UserAuth: userAuthenticator, UserAdministration: userAdministration,
			UserAPIKeys: userAPIKeys,
			Billing:     billingDispatcher, BillingReader: billingReader, Quota: quota,
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
