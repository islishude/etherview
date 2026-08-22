package config

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

func applyRoleEnvironment(cfg *Config, lookup func(string) (string, bool), forcedRoles []string) (bool, error) {
	for _, removed := range []string{
		"COMPILER_SANDBOX",
		"VERIFICATION_RUNNER_ENDPOINT",
		"VERIFICATION_RUNNER_IMAGE",
		"VERIFICATION_VYPER_CATALOG_URL",
	} {
		if _, exists := lookup(envPrefix + removed); exists {
			return false, fmt.Errorf("%s%s is no longer supported", envPrefix, removed)
		}
	}
	if err := setBool(lookup, "FEATURE_USER_AUTH", &cfg.Features.UserAuth); err != nil {
		return false, err
	}
	if err := setBool(lookup, "FEATURE_USER_API_KEYS", &cfg.Features.UserAPIKeys); err != nil {
		return false, err
	}
	if err := setBool(lookup, "FEATURE_X402_BILLING", &cfg.Features.X402Billing); err != nil {
		return false, err
	}
	if forcedRoles != nil {
		cfg.Runtime.Roles = slices.Clone(forcedRoles)
	} else if value, ok := lookup(envPrefix + "ROLES"); ok {
		cfg.Runtime.Roles = strings.Split(value, ",")
	}
	roles, err := NormalizeRoles(cfg.Runtime.Roles)
	if err != nil {
		return false, err
	}
	apiRole := slices.Contains(roles, "api")
	return apiRole, nil
}

func applySecretEnvironment(cfg *Config, lookup func(string) (string, bool), readFile func(string) ([]byte, error), apiRole bool) error {
	secret, err := lookupValueOrFile("DATABASE_URL", lookup, readFile)
	if err != nil {
		return err
	}
	if secret != "" {
		cfg.Database.URL = secret
	}
	secretRead, err := lookupValueOrFile("DATABASE_READ_URL", lookup, readFile)
	if err != nil {
		return err
	}
	if secretRead != "" {
		cfg.Database.ReadURL = secretRead
	}
	setString(lookup, "SERVER_ADDRESS", &cfg.Server.Address)
	setString(lookup, "SERVER_METRICS_ADDRESS", &cfg.Server.MetricsAddress)
	setString(lookup, "SERVER_PUBLIC_URL", &cfg.Server.PublicURL)
	setString(lookup, "SERVER_TLS_CERT_FILE", &cfg.Server.TLSCertFile)
	setString(lookup, "SERVER_TLS_KEY_FILE", &cfg.Server.TLSKeyFile)
	setString(lookup, "CHAIN_GENESIS_HASH", &cfg.Chain.GenesisHash)
	setString(lookup, "CHAIN_GENESIS_FILE", &cfg.Chain.GenesisFile)
	setString(lookup, "CHAIN_GENESIS_URL", &cfg.Chain.GenesisURL)
	setString(lookup, "CHAIN_GENESIS_SHA256", &cfg.Chain.GenesisSHA256)
	setString(lookup, "CHAIN_NAME", &cfg.Chain.Name)
	setString(lookup, "CHAIN_NATIVE_SYMBOL", &cfg.Chain.NativeSymbol)
	setString(lookup, "CHAIN_NATIVE_NAME", &cfg.Chain.NativeName)
	if value, ok := lookup(envPrefix + "WALLET_ADD_CHAIN_RPC_URLS"); ok {
		cfg.Wallet.AddChain.RPCURLs = splitCSV(value)
	}
	if value, ok := lookup(envPrefix + "WALLET_ADD_CHAIN_BLOCK_EXPLORER_URLS"); ok {
		cfg.Wallet.AddChain.BlockExplorerURLs = splitCSV(value)
	}
	if value, ok := lookup(envPrefix + "WALLET_ADD_CHAIN_ICON_URLS"); ok {
		cfg.Wallet.AddChain.IconURLs = splitCSV(value)
	}
	pepper, err := lookupValueOrFile("API_KEY_PEPPER", lookup, readFile)
	if err != nil {
		return err
	}
	if pepper != "" {
		cfg.Security.APIKeyPepper = pepper
	}
	if apiRole && cfg.Features.UserAuth {
		sessionPepper, err := lookupValueOrFile("SESSION_PEPPER", lookup, readFile)
		if err != nil {
			return err
		}
		if sessionPepper != "" {
			cfg.UserAuth.SessionPepper = sessionPepper
		}
	}
	if apiRole && cfg.Features.X402Billing {
		fingerprintPepper, err := lookupValueOrFile("X402_FINGERPRINT_PEPPER", lookup, readFile)
		if err != nil {
			return err
		}
		if fingerprintPepper != "" {
			cfg.Billing.FingerprintPepper = fingerprintPepper
		}
		headersJSON, err := lookupValueOrFile("X402_FACILITATOR_HEADERS", lookup, readFile)
		if err != nil {
			return err
		}
		if headersJSON != "" {
			headers, err := parseSecretHeaders(headersJSON)
			if err != nil {
				return err
			}
			cfg.Billing.FacilitatorHeaders = headers
		}
	}
	return nil
}

func applyStringEnvironment(cfg *Config, lookup func(string) (string, bool), readFile func(string) ([]byte, error), apiRole bool) error {
	setString(lookup, "COMPILER_CACHE_DIRECTORY", &cfg.Verification.CacheDirectory)
	setString(lookup, "VERIFICATION_EXECUTOR_PATH", &cfg.Verification.ExecutorPath)
	setString(lookup, "VERIFICATION_GEAS_PATH", &cfg.Verification.GeasPath)
	if value, ok := lookup(envPrefix + "VERIFICATION_SOLIDITY_CATALOG_URL"); ok {
		if value = strings.TrimSpace(value); value != "" {
			cfg.Verification.CatalogURLs["solidity"] = value
		}
	}
	if value, ok := lookup(envPrefix + "VERIFICATION_ALLOWED_DOWNLOAD_ORIGINS"); ok {
		cfg.Verification.AllowedDownloadOrigins = splitCSV(value)
	}
	setString(lookup, "SOURCIFY_BASE_URL", &cfg.Sourcify.BaseURL)
	setString(lookup, "OBSERVABILITY_ENVIRONMENT", &cfg.Observability.Environment)
	setExactString(lookup, "LOG_LEVEL", &cfg.Observability.LogLevel)
	setExactString(lookup, "LOG_FORMAT", &cfg.Observability.LogFormat)
	setString(lookup, "OTLP_TRACE_ENDPOINT", &cfg.Observability.OTLPTraceEndpoint)
	setString(lookup, "X402_FACILITATOR_URL", &cfg.Billing.FacilitatorURL)
	setString(lookup, "X402_NETWORK", &cfg.Billing.Network)
	setString(lookup, "X402_ASSET", &cfg.Billing.Asset)
	setString(lookup, "X402_ASSET_EIP712_NAME", &cfg.Billing.AssetEIP712Name)
	setString(lookup, "X402_ASSET_EIP712_VERSION", &cfg.Billing.AssetEIP712Version)
	setString(lookup, "X402_RECIPIENT", &cfg.Billing.Recipient)
	for name, target := range map[string]*string{
		"NATS_URL":    &cfg.Adapters.NATSURL,
		"REDIS_URL":   &cfg.Adapters.RedisURL,
		"S3_ENDPOINT": &cfg.Adapters.S3Endpoint,
	} {
		value, err := lookupValueOrFile(name, lookup, readFile)
		if err != nil {
			return err
		}
		if value != "" {
			*target = value
		}
	}
	if apiRole && cfg.Adapters.S3Endpoint != "" {
		for name, target := range map[string]*string{
			"S3_ACCESS_KEY":    &cfg.Adapters.S3AccessKey,
			"S3_SECRET_KEY":    &cfg.Adapters.S3SecretKey,
			"S3_SESSION_TOKEN": &cfg.Adapters.S3SessionToken,
		} {
			value, err := lookupValueOrFile(name, lookup, readFile)
			if err != nil {
				return err
			}
			if value != "" {
				*target = value
			}
		}
	} else {
		cfg.Adapters.S3AccessKey = ""
		cfg.Adapters.S3SecretKey = ""
		cfg.Adapters.S3SessionToken = ""
	}
	setString(lookup, "ADAPTER_NAMESPACE", &cfg.Adapters.Namespace)
	setString(lookup, "S3_BUCKET", &cfg.Adapters.S3Bucket)
	setString(lookup, "S3_PREFIX", &cfg.Adapters.S3Prefix)
	setString(lookup, "S3_REGION", &cfg.Adapters.S3Region)
	setString(lookup, "PRICE_BASE_URL", &cfg.Adapters.PriceBaseURL)
	setString(lookup, "ENS_CUSTOM_REGISTRY", &cfg.ENS.Custom.Registry)
	setString(lookup, "ENS_CUSTOM_UNIVERSAL_RESOLVER", &cfg.ENS.Custom.UniversalResolver)
	setString(lookup, "ENS_CUSTOM_COIN_TYPE", &cfg.ENS.Custom.CoinType)
	setString(lookup, "METADATA_IPFS_GATEWAY", &cfg.Metadata.IPFSGateway)
	if value, ok := lookup(envPrefix + "ENS_OFFICIAL_GATEWAYS"); ok {
		cfg.ENS.OfficialGateways = splitCSV(value)
	}
	if value, ok := lookup(envPrefix + "ENS_CUSTOM_GATEWAYS"); ok {
		cfg.ENS.Custom.Gateways = splitCSV(value)
	}
	return nil
}

func applyNumericEnvironment(cfg *Config, lookup func(string) (string, bool)) error {
	if err := setUint64(lookup, "CHAIN_ID", &cfg.Chain.ID); err != nil {
		return err
	}
	if err := setUint64(lookup, "CHAIN_START_BLOCK", &cfg.Chain.StartBlock); err != nil {
		return err
	}
	if err := setUint64(lookup, "CHAIN_MAX_REORG_DEPTH", &cfg.Chain.MaxReorgDepth); err != nil {
		return err
	}
	if err := setUint8(lookup, "CHAIN_NATIVE_DECIMALS", &cfg.Chain.NativeDecimals); err != nil {
		return err
	}
	if err := setUint8(lookup, "X402_ASSET_DECIMALS", &cfg.Billing.AssetDecimals); err != nil {
		return err
	}
	if err := setInt32(lookup, "DATABASE_MAX_CONNECTIONS", &cfg.Database.MaxConnections); err != nil {
		return err
	}
	if err := setInt32(lookup, "DATABASE_MIN_CONNECTIONS", &cfg.Database.MinConnections); err != nil {
		return err
	}
	if err := setInt32(lookup, "DATABASE_READ_MAX_CONNECTIONS", &cfg.Database.ReadMaxConnections); err != nil {
		return err
	}
	if err := setInt32(lookup, "DATABASE_READ_MIN_CONNECTIONS", &cfg.Database.ReadMinConnections); err != nil {
		return err
	}
	if err := setInt(lookup, "RPC_BATCH_SIZE", &cfg.RPC.BatchSize); err != nil {
		return err
	}
	if err := setInt(lookup, "WORKER_COUNT", &cfg.Runtime.WorkerCount); err != nil {
		return err
	}
	if err := setInt(lookup, "BACKFILL_WORKERS", &cfg.Runtime.BackfillWorkers); err != nil {
		return err
	}
	if err := setInt(lookup, "BACKFILL_BATCH_BLOCKS", &cfg.Runtime.BackfillBatchBlocks); err != nil {
		return err
	}
	if err := setInt(lookup, "MEMPOOL_MAX_TRANSACTIONS", &cfg.Mempool.MaxTransactions); err != nil {
		return err
	}
	if err := setInt(lookup, "MEMPOOL_MAX_RESPONSE_BYTES", &cfg.Mempool.MaxResponseBytes); err != nil {
		return err
	}
	if err := setInt64(lookup, "MAINTENANCE_SEARCH_RETENTION_GENERATIONS", &cfg.Maintenance.SearchRetentionGenerations); err != nil {
		return err
	}
	if err := setInt(lookup, "MAINTENANCE_ADAPTER_DELETE_BATCH", &cfg.Maintenance.AdapterDeleteBatch); err != nil {
		return err
	}
	if err := setInt(lookup, "METADATA_MAX_DOCUMENT_BYTES", &cfg.Metadata.MaxDocumentBytes); err != nil {
		return err
	}
	if err := setInt(lookup, "METADATA_MAX_REDIRECTS", &cfg.Metadata.MaxRedirects); err != nil {
		return err
	}
	if err := setInt(lookup, "ADAPTER_MAX_RESPONSE_BYTES", &cfg.Adapters.MaxResponseBytes); err != nil {
		return err
	}
	if err := setInt(lookup, "ADAPTER_MAX_REDIRECTS", &cfg.Adapters.MaxRedirects); err != nil {
		return err
	}
	if err := setInt64(lookup, "ENS_MAX_RESPONSE_BYTES", &cfg.ENS.MaxResponseBytes); err != nil {
		return err
	}
	if err := setInt(lookup, "ENS_MAX_CCIP_DEPTH", &cfg.ENS.MaxCCIPDepth); err != nil {
		return err
	}
	if err := setInt(lookup, "ENS_MAX_BATCH_ADDRESSES", &cfg.ENS.MaxBatchAddresses); err != nil {
		return err
	}
	if err := setInt(lookup, "ENS_MAX_CONCURRENCY", &cfg.ENS.MaxConcurrency); err != nil {
		return err
	}
	if err := setInt64(lookup, "S3_MAX_OBJECT_BYTES", &cfg.Adapters.S3MaxObjectBytes); err != nil {
		return err
	}
	if err := setInt(lookup, "ANONYMOUS_RATE", &cfg.Security.AnonymousRate); err != nil {
		return err
	}
	if err := setInt(lookup, "ANONYMOUS_BURST", &cfg.Security.AnonymousBurst); err != nil {
		return err
	}
	if err := setInt(lookup, "USER_AUTH_API_KEY_RATE", &cfg.UserAuth.APIKeyRate); err != nil {
		return err
	}
	if err := setInt(lookup, "USER_AUTH_API_KEY_BURST", &cfg.UserAuth.APIKeyBurst); err != nil {
		return err
	}
	if err := setInt(lookup, "USER_AUTH_MAX_ACTIVE_API_KEYS", &cfg.UserAuth.MaxActiveAPIKeys); err != nil {
		return err
	}
	if err := setInt(lookup, "VERIFICATION_MAX_INPUT_BYTES", &cfg.Verification.MaxInputBytes); err != nil {
		return err
	}
	if err := setInt(lookup, "VERIFICATION_MAX_OUTPUT_BYTES", &cfg.Verification.MaxOutputBytes); err != nil {
		return err
	}
	if err := setInt(lookup, "VERIFICATION_WORKER_COUNT", &cfg.Verification.WorkerCount); err != nil {
		return err
	}
	if err := setBool(lookup, "DERIVED_VERIFY_ENABLED", &cfg.Verification.DerivedEnabled); err != nil {
		return err
	}
	if err := setBool(lookup, "DERIVED_VERIFY_BACKFILL_ENABLED", &cfg.Verification.DerivedBackfillEnabled); err != nil {
		return err
	}
	if err := setBool(lookup, "DERIVED_VERIFY_FORWARD_ENABLED", &cfg.Verification.DerivedForwardEnabled); err != nil {
		return err
	}
	if err := setInt(lookup, "DERIVED_VERIFY_WORKER_COUNT", &cfg.Verification.DerivedWorkerCount); err != nil {
		return err
	}
	if err := setInt(lookup, "DERIVED_VERIFY_MAX_TRACES_PER_SCAN", &cfg.Verification.DerivedMaxTracesPerScan); err != nil {
		return err
	}
	if err := setDuration(lookup, "VERIFICATION_CATALOG_REFRESH_INTERVAL", &cfg.Verification.CatalogRefreshInterval); err != nil {
		return err
	}
	if err := setDuration(lookup, "VERIFICATION_CATALOG_MAX_STALENESS", &cfg.Verification.CatalogMaxStaleness); err != nil {
		return err
	}
	if err := setInt(lookup, "SOURCIFY_MAX_REQUEST_BYTES", &cfg.Sourcify.MaxRequestBytes); err != nil {
		return err
	}
	if err := setInt(lookup, "SOURCIFY_ATTEMPTS", &cfg.Sourcify.Attempts); err != nil {
		return err
	}
	if err := setInt(lookup, "SOURCIFY_MAX_POLLS", &cfg.Sourcify.MaxPolls); err != nil {
		return err
	}
	if err := setInt(lookup, "X402_MAX_PAYMENT_HEADER_BYTES", &cfg.Billing.MaxPaymentHeaderBytes); err != nil {
		return err
	}
	if err := setInt(lookup, "X402_MAX_CAPTURED_HEADER_BYTES", &cfg.Billing.MaxCapturedHeaderBytes); err != nil {
		return err
	}
	if err := setInt(lookup, "X402_COARSE_IP_RATE", &cfg.Billing.CoarseIPRate); err != nil {
		return err
	}
	if err := setInt(lookup, "X402_COARSE_IP_BURST", &cfg.Billing.CoarseIPBurst); err != nil {
		return err
	}
	if err := setInt64(lookup, "SOURCIFY_MAX_RESPONSE_BYTES", &cfg.Sourcify.MaxResponseBytes); err != nil {
		return err
	}
	if err := setInt64(lookup, "X402_FACILITATOR_MAX_RESPONSE_BYTES", &cfg.Billing.FacilitatorMaxResponseBytes); err != nil {
		return err
	}
	if err := setInt64(lookup, "X402_MAX_BUFFERED_RESPONSE_BYTES", &cfg.Billing.MaxBufferedResponseBytes); err != nil {
		return err
	}
	if err := setFloat64(lookup, "TRACE_SAMPLE_RATIO", &cfg.Observability.TraceSampleRatio); err != nil {
		return err
	}
	return nil
}

func applyDurationEnvironment(cfg *Config, lookup func(string) (string, bool)) error {
	for name, target := range map[string]*time.Duration{
		"SERVER_SHUTDOWN_TIMEOUT":      &cfg.Server.ShutdownTimeout,
		"SERVER_READ_TIMEOUT":          &cfg.Server.ReadTimeout,
		"SERVER_WRITE_TIMEOUT":         &cfg.Server.WriteTimeout,
		"DATABASE_CONNECT_TIMEOUT":     &cfg.Database.ConnectTimeout,
		"DATABASE_STATEMENT_TIMEOUT":   &cfg.Database.StatementTimeout,
		"RPC_REQUEST_TIMEOUT":          &cfg.RPC.RequestTimeout,
		"CHAIN_GENESIS_FETCH_TIMEOUT":  &cfg.Chain.GenesisFetchTimeout,
		"POLL_INTERVAL":                &cfg.Runtime.PollInterval,
		"LEASE_DURATION":               &cfg.Runtime.LeaseDuration,
		"MEMPOOL_POLL_INTERVAL":        &cfg.Mempool.PollInterval,
		"MEMPOOL_RETENTION":            &cfg.Mempool.Retention,
		"MAINTENANCE_INTERVAL":         &cfg.Maintenance.Interval,
		"METADATA_FETCH_TIMEOUT":       &cfg.Metadata.FetchTimeout,
		"ADAPTER_FETCH_TIMEOUT":        &cfg.Adapters.FetchTimeout,
		"ADAPTER_CONNECT_TIMEOUT":      &cfg.Adapters.ConnectTimeout,
		"ADAPTER_OPERATION_TIMEOUT":    &cfg.Adapters.OperationTimeout,
		"REDIS_CACHE_TTL":              &cfg.Adapters.RedisCacheTTL,
		"ADAPTER_PRICE_FRESHNESS":      &cfg.Adapters.PriceFreshness,
		"ADAPTER_FAILURE_TTL":          &cfg.Adapters.FailureTTL,
		"ENS_RESOLUTION_FRESHNESS":     &cfg.ENS.ResolutionFreshness,
		"ENS_SNAPSHOT_TTL":             &cfg.ENS.SnapshotTTL,
		"ENS_FAILURE_TTL":              &cfg.ENS.FailureTTL,
		"ENS_REQUEST_TIMEOUT":          &cfg.ENS.RequestTimeout,
		"VERIFICATION_TIMEOUT":         &cfg.Verification.Timeout,
		"SOURCIFY_TIMEOUT":             &cfg.Sourcify.Timeout,
		"SOURCIFY_POLL_INTERVAL":       &cfg.Sourcify.PollInterval,
		"TRACE_EXPORT_TIMEOUT":         &cfg.Observability.TraceExportTimeout,
		"METRICS_REFRESH_INTERVAL":     &cfg.Observability.MetricsRefreshInterval,
		"SYNC_PROGRESS_LOG_INTERVAL":   &cfg.Observability.SyncProgressLogInterval,
		"USER_AUTH_CHALLENGE_TTL":      &cfg.UserAuth.ChallengeTTL,
		"USER_AUTH_SESSION_TTL":        &cfg.UserAuth.SessionTTL,
		"USER_AUTH_LAST_USED_INTERVAL": &cfg.UserAuth.LastUsedInterval,
		"X402_FACILITATOR_TIMEOUT":     &cfg.Billing.FacilitatorTimeout,
		"X402_REQUIREMENT_MAX_TIMEOUT": &cfg.Billing.RequirementMaxTimeout,
		"X402_RESERVATION_TTL":         &cfg.Billing.ReservationTTL,
	} {
		if err := setDuration(lookup, name, target); err != nil {
			return err
		}
	}
	return nil
}

func applyBooleanEnvironment(cfg *Config, lookup func(string) (string, bool)) error {
	for name, target := range map[string]*bool{
		"FEATURE_TRACE":                                       &cfg.Features.Trace,
		"FEATURE_MEMPOOL":                                     &cfg.Features.Mempool,
		"FEATURE_HISTORICAL_STATE":                            &cfg.Features.HistoricalState,
		"FEATURE_VERIFICATION":                                &cfg.Features.Verification,
		"FEATURE_SOURCIFY":                                    &cfg.Features.Sourcify,
		"FEATURE_NFT_METADATA":                                &cfg.Features.NFTMetadata,
		"FEATURE_PRICING":                                     &cfg.Features.Pricing,
		"FEATURE_ENS":                                         &cfg.Features.ENS,
		"FEATURE_USER_AUTH":                                   &cfg.Features.UserAuth,
		"FEATURE_USER_API_KEYS":                               &cfg.Features.UserAPIKeys,
		"FEATURE_X402_BILLING":                                &cfg.Features.X402Billing,
		"FEATURE_PROXY_DETECTION_V2":                          &cfg.Features.ProxyDetectionV2,
		"FEATURE_SAFE_PROXY_DETECTION":                        &cfg.Features.SafeProxyDetection,
		"FEATURE_DIAMOND_PROXY_DETECTION":                     &cfg.Features.DiamondProxyDetection,
		"FEATURE_PROXY_DETECTION_V2_PUBLIC":                   &cfg.Features.ProxyDetectionV2Public,
		"PUBLIC_VERIFICATION":                                 &cfg.Security.PublicVerification,
		"METADATA_UNSAFE_ALLOW_PRIVATE_NETWORKS":              &cfg.Metadata.UnsafeAllowPrivateNetworks,
		"VERIFICATION_UNSAFE_ALLOW_PRIVATE_DOWNLOAD_NETWORKS": &cfg.Verification.UnsafeAllowPrivateDownloadNetworks,
		"S3_PATH_STYLE":                                       &cfg.Adapters.S3PathStyle,
		"OTLP_TRACE_INSECURE":                                 &cfg.Observability.OTLPTraceInsecure,
	} {
		if err := setBool(lookup, name, target); err != nil {
			return err
		}
	}
	if value, ok := lookup(envPrefix + "ALLOWED_ORIGINS"); ok {
		cfg.Security.AllowedOrigins = splitCSV(value)
	}
	if value, ok := lookup(envPrefix + "TRUSTED_PROXIES"); ok {
		cfg.Security.TrustedProxies = splitCSV(value)
	}
	if value, ok := lookup(envPrefix + "X402_FACILITATOR_ALLOWED_CIDRS"); ok {
		cfg.Billing.FacilitatorAllowedCIDRs = splitCSV(value)
	}
	return nil
}

func applyRPCEnvironment(cfg *Config, lookup func(string) (string, bool), readFile func(string) ([]byte, error), apiRole bool) error {
	rpcURLs, err := lookupValueOrFile("RPC_URLS", lookup, readFile)
	if err != nil {
		return err
	}
	if rpcURLs != "" {
		endpoints, err := parseEnvironmentRPCEndpoints(rpcURLs)
		if err != nil {
			return err
		}
		cfg.RPC.Endpoints = endpoints
	}
	if apiRole {
		ensRPCURLs, err := lookupValueOrFile("ENS_RPC_URLS", lookup, readFile)
		if err != nil {
			return err
		}
		if ensRPCURLs != "" {
			endpoints, err := parseEnvironmentRPCEndpointsNamed("ETHERVIEW_ENS_RPC_URLS", ensRPCURLs, []string{"state"})
			if err != nil {
				return err
			}
			cfg.ENS.OfficialRPCEndpoints = endpoints
		}
	} else {
		cfg.ENS.OfficialRPCEndpoints = nil
	}
	return nil
}
