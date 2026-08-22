package config

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

func validateServerConfig(c Config) error {
	var errs []error
	if c.Server.Address == "" {
		errs = append(errs, errors.New("server.address is required"))
	}
	if c.Server.MetricsAddress == "" {
		errs = append(errs, errors.New("server.metrics_address is required"))
	}
	if c.Server.Address != "" && c.Server.Address == c.Server.MetricsAddress {
		errs = append(errs, errors.New("server.address and server.metrics_address must use different listeners"))
	}
	if c.Server.ShutdownTimeout <= 0 || c.Server.ReadTimeout <= 0 || c.Server.WriteTimeout <= 0 {
		errs = append(errs, errors.New("server timeouts must be positive"))
	}
	if c.Server.PublicURL != "" {
		if err := validatePublicOrigin(c.Server.PublicURL); err != nil {
			errs = append(errs, err)
		}
	}
	certificateConfigured := c.Server.TLSCertFile != ""
	keyConfigured := c.Server.TLSKeyFile != ""
	if certificateConfigured != keyConfigured {
		errs = append(errs, errors.New("server.tls_cert_file and server.tls_key_file must be configured together"))
	}
	if certificateConfigured && !filepath.IsAbs(c.Server.TLSCertFile) {
		errs = append(errs, errors.New("server.tls_cert_file must be an absolute path"))
	}
	if keyConfigured && !filepath.IsAbs(c.Server.TLSKeyFile) {
		errs = append(errs, errors.New("server.tls_key_file must be an absolute path"))
	}
	if certificateConfigured && keyConfigured && c.Server.PublicURL != "" {
		publicURL, parseErr := url.Parse(c.Server.PublicURL)
		if parseErr == nil && publicURL.Scheme != "https" {
			errs = append(errs, errors.New("server.public_url must use HTTPS when server TLS is enabled"))
		}
	}
	return errors.Join(errs...)
}

func validateChainWalletConfig(c Config) error {
	var errs []error
	if c.Chain.ID == 0 {
		errs = append(errs, errors.New("chain.id must be greater than zero"))
	}
	if c.Chain.GenesisHash != "" && !validFixedHex(c.Chain.GenesisHash, 32) {
		errs = append(errs, errors.New("chain.genesis_hash must be a 32-byte 0x-prefixed hash"))
	}
	if c.Chain.GenesisFile != "" && c.Chain.GenesisURL != "" {
		errs = append(errs, errors.New("chain.genesis_file and chain.genesis_url are mutually exclusive"))
	}
	if c.Chain.GenesisFile != "" || c.Chain.GenesisURL != "" {
		if c.Chain.StartBlock != 0 {
			errs = append(errs, errors.New("chain genesis source requires chain.start_block=0"))
		}
	}
	if c.Chain.GenesisFile != "" {
		if !filepath.IsAbs(c.Chain.GenesisFile) {
			errs = append(errs, errors.New("chain.genesis_file must be an absolute path"))
		}
	}
	if c.Chain.GenesisURL != "" {
		parsed, err := url.Parse(c.Chain.GenesisURL)
		if c.Chain.GenesisURL != strings.TrimSpace(c.Chain.GenesisURL) || err != nil ||
			parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil ||
			parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery ||
			parsed.Fragment != "" || strings.Contains(c.Chain.GenesisURL, "#") ||
			len(c.Chain.GenesisURL) > 4096 || strings.HasSuffix(parsed.Host, ":") ||
			(parsed.Port() != "" && parsed.Port() != "443") ||
			unsafeURLPath(parsed.EscapedPath()) {
			errs = append(errs, errors.New("chain.genesis_url must be an absolute HTTPS URL using the default port or 443 without credentials, query, fragment, or path traversal"))
		}
	}
	if c.Chain.GenesisSHA256 != "" {
		digest, err := decodeNonzeroSHA256(c.Chain.GenesisSHA256)
		if c.Chain.GenesisURL == "" || err != nil ||
			c.Chain.GenesisSHA256 != strings.ToLower(c.Chain.GenesisSHA256) || allZero(digest) {
			errs = append(errs, errors.New("chain.genesis_sha256 must be a non-zero lowercase SHA-256 digest and requires chain.genesis_url"))
		}
	}
	if c.Chain.GenesisFetchTimeout < time.Second || c.Chain.GenesisFetchTimeout > 5*time.Minute {
		errs = append(errs, errors.New("chain.genesis_fetch_timeout must be between 1s and 5m"))
	}
	if c.Chain.NativeSymbol == "" || c.Chain.NativeName == "" {
		errs = append(errs, errors.New("chain native currency name and symbol are required"))
	}
	if len(c.Wallet.AddChain.RPCURLs) > 0 {
		if len(c.Chain.NativeSymbol) < 2 || len(c.Chain.NativeSymbol) > 6 {
			errs = append(errs, errors.New("wallet.add_chain requires chain.native_symbol to contain 2 to 6 characters"))
		}
	} else if len(c.Wallet.AddChain.BlockExplorerURLs) > 0 || len(c.Wallet.AddChain.IconURLs) > 0 {
		errs = append(errs, errors.New("wallet.add_chain metadata requires at least one rpc_url"))
	}
	for name, values := range map[string][]string{
		"wallet.add_chain.rpc_urls":            c.Wallet.AddChain.RPCURLs,
		"wallet.add_chain.block_explorer_urls": c.Wallet.AddChain.BlockExplorerURLs,
		"wallet.add_chain.icon_urls":           c.Wallet.AddChain.IconURLs,
	} {
		allowLocalHTTP := name == "wallet.add_chain.rpc_urls"
		if len(values) > 5 {
			errs = append(errs, fmt.Errorf("%s must contain at most 5 URLs", name))
			continue
		}
		for index, value := range values {
			if err := validateWalletURL(value, allowLocalHTTP); err != nil {
				errs = append(errs, fmt.Errorf("%s[%d]: %w", name, index, err))
			}
		}
	}
	if c.Chain.MaxReorgDepth == 0 {
		errs = append(errs, errors.New("chain.max_reorg_depth must be greater than zero"))
	}
	return errors.Join(errs...)
}

func validateDatabaseRPCConfig(c Config) error {
	var errs []error
	if c.Database.MaxConnections <= 0 || c.Database.MinConnections < 0 || c.Database.MinConnections > c.Database.MaxConnections {
		errs = append(errs, errors.New("database connection bounds are invalid"))
	}
	if c.Database.ReadMaxConnections < 0 || c.Database.ReadMinConnections < 0 {
		errs = append(errs, errors.New("database read connection bounds must be non-negative; zero inherits writer bounds"))
	} else if c.Database.ReadURL != "" || c.Database.ReadMaxConnections != 0 || c.Database.ReadMinConnections != 0 {
		readMax := c.Database.ReadMaxConnections
		readMin := c.Database.ReadMinConnections
		if readMax == 0 {
			readMax = c.Database.MaxConnections
		}
		if readMin == 0 {
			readMin = c.Database.MinConnections
		}
		if readMax <= 0 || readMin < 0 || readMin > readMax {
			errs = append(errs, errors.New("database read connection bounds are invalid"))
		}
	}
	if c.Database.ConnectTimeout <= 0 || c.Database.StatementTimeout <= 0 {
		errs = append(errs, errors.New("database timeouts must be positive"))
	}
	if c.RPC.RequestTimeout <= 0 || c.RPC.BatchSize <= 0 {
		errs = append(errs, errors.New("rpc timeout and batch_size must be positive"))
	}
	seenEndpoint := make(map[string]struct{}, len(c.RPC.Endpoints))
	for i, endpoint := range c.RPC.Endpoints {
		if err := endpoint.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("rpc.endpoints[%d]: %w", i, err))
		}
		if _, ok := seenEndpoint[endpoint.Name]; ok && endpoint.Name != "" {
			errs = append(errs, fmt.Errorf("rpc endpoint name %q is duplicated", endpoint.Name))
		}
		seenEndpoint[endpoint.Name] = struct{}{}
	}
	return errors.Join(errs...)
}

func validateRuntimeConfig(c Config) error {
	var errs []error
	if c.Runtime.PollInterval <= 0 || c.Runtime.LeaseDuration <= 0 {
		errs = append(errs, errors.New("runtime poll_interval and lease_duration must be positive"))
	}
	if c.Runtime.WorkerCount <= 0 || c.Runtime.WorkerCount > maximumRuntimeWorkerCount {
		errs = append(errs, fmt.Errorf("runtime.worker_count must be between 1 and %d", maximumRuntimeWorkerCount))
	}
	if c.Runtime.BackfillWorkers <= 0 || c.Runtime.BackfillWorkers > maximumRuntimeBackfillWorkers {
		errs = append(errs, fmt.Errorf("runtime.backfill_workers must be between 1 and %d", maximumRuntimeBackfillWorkers))
	}
	if c.Runtime.BackfillBatchBlocks <= 0 || c.Runtime.BackfillBatchBlocks > maximumRuntimeBackfillBatchBlocks {
		errs = append(errs, fmt.Errorf("runtime.backfill_batch_blocks must be between 1 and %d", maximumRuntimeBackfillBatchBlocks))
	}
	return errors.Join(errs...)
}

func validateMempoolMaintenanceConfig(c Config) error {
	var errs []error
	if c.Mempool.PollInterval < 250*time.Millisecond || c.Mempool.PollInterval > time.Minute {
		errs = append(errs, errors.New("mempool.poll_interval must be between 250ms and 1m"))
	}
	if c.Mempool.Retention <= c.Mempool.PollInterval || c.Mempool.Retention > 24*time.Hour {
		errs = append(errs, errors.New("mempool.retention must exceed poll_interval and be at most 24h"))
	}
	if c.Mempool.MaxTransactions <= 0 || c.Mempool.MaxTransactions > 200_000 {
		errs = append(errs, errors.New("mempool.max_transactions must be between 1 and 200000"))
	}
	if c.Mempool.MaxResponseBytes <= 0 || c.Mempool.MaxResponseBytes > 32<<20 {
		errs = append(errs, errors.New("mempool.max_response_bytes must be between 1 and 33554432"))
	}
	if c.Maintenance.Interval < time.Second || c.Maintenance.Interval > 24*time.Hour {
		errs = append(errs, errors.New("maintenance.interval must be between 1s and 24h"))
	}
	if c.Maintenance.SearchRetentionGenerations < 1000 || c.Maintenance.SearchRetentionGenerations > 10_000_000 {
		errs = append(errs, errors.New("maintenance.search_retention_generations must be between 1000 and 10000000"))
	}
	if c.Maintenance.AdapterDeleteBatch <= 0 || c.Maintenance.AdapterDeleteBatch > 10_000 {
		errs = append(errs, errors.New("maintenance.adapter_delete_batch must be between 1 and 10000"))
	}
	return errors.Join(errs...)
}

func validateMetadataConfig(c Config) error {
	var errs []error
	if c.Metadata.FetchTimeout < 100*time.Millisecond || c.Metadata.FetchTimeout > time.Minute {
		errs = append(errs, errors.New("metadata.fetch_timeout must be between 100ms and 1m"))
	}
	if c.Metadata.MaxDocumentBytes <= 0 || c.Metadata.MaxDocumentBytes > 2<<20 {
		errs = append(errs, errors.New("metadata.max_document_bytes must be between 1 and 2097152"))
	}
	if c.Metadata.MaxRedirects <= 0 || c.Metadata.MaxRedirects > 10 {
		errs = append(errs, errors.New("metadata.max_redirects must be between 1 and 10"))
	}
	if c.Metadata.IPFSGateway != "" {
		gateway, err := url.Parse(c.Metadata.IPFSGateway)
		if err != nil || gateway.Scheme != "https" || gateway.Host == "" || gateway.User != nil || gateway.Fragment != "" {
			errs = append(errs, errors.New("metadata.ipfs_gateway must be an absolute HTTPS URL without credentials or fragment"))
		}
	}
	return errors.Join(errs...)
}

func validateFeatureSecurityConfig(c Config) error {
	var errs []error
	if c.Security.PublicVerification && !c.Features.Verification {
		errs = append(errs, errors.New("public verification requires features.verification"))
	}
	if c.Features.ProxyDetectionV2Public && !c.Features.ProxyDetectionV2 {
		errs = append(errs, errors.New("features.proxy_detection_v2_public requires features.proxy_detection_v2"))
	}
	if c.Features.SafeProxyDetection && !c.Features.ProxyDetectionV2 {
		errs = append(errs, errors.New("features.safe_proxy_detection requires features.proxy_detection_v2"))
	}
	if c.Features.DiamondProxyDetection && !c.Features.ProxyDetectionV2 {
		errs = append(errs, errors.New("features.diamond_proxy_detection requires features.proxy_detection_v2"))
	}
	if c.Security.PublicVerification && len(c.Security.APIKeyPepper) < 32 {
		errs = append(errs, errors.New("public verification requires API key authentication"))
	}
	if c.Security.APIKeyPepper != "" && len(c.Security.APIKeyPepper) < 32 {
		errs = append(errs, errors.New("security.api_key_pepper must contain at least 32 bytes"))
	}
	if c.Security.AnonymousRate <= 0 || c.Security.AnonymousBurst < c.Security.AnonymousRate {
		errs = append(errs, errors.New("security anonymous rate must be positive and burst must be at least rate"))
	}
	for _, origin := range c.Security.AllowedOrigins {
		if origin == "*" {
			errs = append(errs, errors.New("security.allowed_origins cannot contain wildcard"))
			continue
		}
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" {
			errs = append(errs, fmt.Errorf("invalid allowed origin %q", origin))
		}
	}
	for index, trustedProxy := range c.Security.TrustedProxies {
		if !validCanonicalTrustedProxy(trustedProxy) {
			errs = append(errs, fmt.Errorf(
				"security.trusted_proxies[%d] must be a canonical IP address or masked CIDR prefix",
				index,
			))
		}
	}
	return errors.Join(errs...)
}

func validateVerificationConfig(c Config) error {
	var errs []error
	if c.Verification.MaxInputBytes <= 0 || c.Verification.MaxInputBytes > 64<<20 {
		errs = append(errs, errors.New("verification.max_input_bytes must be between 1 and 67108864"))
	}
	if c.Verification.MaxOutputBytes <= 0 || c.Verification.MaxOutputBytes > 256<<20 {
		errs = append(errs, errors.New("verification.max_output_bytes must be between 1 and 268435456"))
	}
	if c.Verification.WorkerCount <= 0 || c.Verification.WorkerCount > 64 {
		errs = append(errs, errors.New("verification.worker_count must be between 1 and 64"))
	}
	if c.Verification.DerivedWorkerCount <= 0 || c.Verification.DerivedWorkerCount > 64 {
		errs = append(errs, errors.New("verification.derived_worker_count must be between 1 and 64"))
	}
	if c.Verification.DerivedMaxTracesPerScan <= 0 ||
		c.Verification.DerivedMaxTracesPerScan > 10_000 {
		errs = append(errs, errors.New("verification.derived_max_traces_per_scan must be between 1 and 10000"))
	}
	if c.Verification.DerivedBackfillEnabled && !c.Verification.DerivedEnabled {
		errs = append(errs, errors.New("verification.derived_backfill_enabled requires derived_enabled"))
	}
	if c.Verification.Timeout <= 0 || c.Verification.Timeout > 30*time.Minute {
		errs = append(errs, errors.New("verification.timeout must be between 1ns and 30m"))
	}
	if c.Verification.CatalogRefreshInterval <= 0 || c.Verification.CatalogRefreshInterval > 24*time.Hour {
		errs = append(errs, errors.New("verification.catalog_refresh_interval must be between 1ns and 24h"))
	}
	if c.Verification.CatalogMaxStaleness < c.Verification.CatalogRefreshInterval ||
		c.Verification.CatalogMaxStaleness > 7*24*time.Hour {
		errs = append(errs, errors.New("verification.catalog_max_staleness must be at least the refresh interval and at most 168h"))
	}
	if err := validateCompilerCatalogConfig(c.Verification); err != nil {
		errs = append(errs, err)
	}
	if c.Features.Sourcify && (!c.Features.Verification || !c.Security.PublicVerification) {
		errs = append(errs, errors.New("features.sourcify requires public verification"))
	}
	if err := validateSourcifyConfig(c.Sourcify); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func validateAdapterConfig(c Config) error {
	var errs []error
	if !validAdapterNamespace(c.Adapters.Namespace) {
		errs = append(errs, errors.New("adapters.namespace must contain 1 to 63 ASCII letters, digits, dots, underscores, or hyphens"))
	}
	if c.Adapters.ConnectTimeout < 10*time.Millisecond || c.Adapters.ConnectTimeout > 30*time.Second {
		errs = append(errs, errors.New("adapters.connect_timeout must be between 10ms and 30s"))
	}
	if c.Adapters.OperationTimeout < 10*time.Millisecond || c.Adapters.OperationTimeout > 30*time.Second {
		errs = append(errs, errors.New("adapters.operation_timeout must be between 10ms and 30s"))
	}
	if c.Adapters.RedisCacheTTL < time.Second || c.Adapters.RedisCacheTTL > time.Hour {
		errs = append(errs, errors.New("adapters.redis_cache_ttl must be between 1s and 1h"))
	}
	if c.Adapters.S3MaxObjectBytes < 1<<20 || c.Adapters.S3MaxObjectBytes > 64<<20 {
		errs = append(errs, errors.New("adapters.s3_max_object_bytes must be between 1048576 and 67108864"))
	}
	if raw := c.Adapters.NATSURL; raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || (u.Scheme != "nats" && u.Scheme != "tls" && u.Scheme != "ws" && u.Scheme != "wss") || u.Fragment != "" {
			errs = append(errs, errors.New("adapters.nats_url must use nats, tls, ws, or wss with an absolute host and no fragment"))
		}
	}
	if raw := c.Adapters.RedisURL; raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || (u.Scheme != "redis" && u.Scheme != "rediss") || u.Fragment != "" {
			errs = append(errs, errors.New("adapters.redis_url must use redis or rediss with an absolute host and no fragment"))
		}
	}
	if raw := c.Adapters.S3Endpoint; raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			errs = append(errs, errors.New("adapters.s3_endpoint must be an absolute HTTP(S) origin without credentials, path, query, or fragment"))
		}
	}
	for name, raw := range map[string]string{
		"adapters.price_base_url": c.Adapters.PriceBaseURL,
	} {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
			errs = append(errs, fmt.Errorf("%s must be an absolute HTTPS URL without credentials or fragments", name))
		}
	}
	if c.Features.Pricing && c.Adapters.PriceBaseURL == "" {
		errs = append(errs, errors.New("adapters.price_base_url is required when pricing is enabled"))
	}
	if c.Adapters.FetchTimeout <= 0 || c.Adapters.FetchTimeout > time.Minute {
		errs = append(errs, errors.New("adapters.fetch_timeout must be between 1ns and 1m"))
	}
	if c.Adapters.MaxResponseBytes <= 0 || c.Adapters.MaxResponseBytes > 8<<20 {
		errs = append(errs, errors.New("adapters.max_response_bytes must be between 1 and 8388608"))
	}
	if c.Adapters.MaxRedirects <= 0 || c.Adapters.MaxRedirects > 5 {
		errs = append(errs, errors.New("adapters.max_redirects must be between 1 and 5"))
	}
	if c.Adapters.PriceFreshness <= 0 || c.Adapters.PriceFreshness > 24*time.Hour {
		errs = append(errs, errors.New("adapters.price_freshness must be between 1ns and 24h"))
	}
	if c.Adapters.FailureTTL <= 0 || c.Adapters.FailureTTL > time.Hour {
		errs = append(errs, errors.New("adapters.failure_ttl must be between 1ns and 1h"))
	}
	if c.Adapters.S3Endpoint != "" && strings.TrimSpace(c.Adapters.S3Bucket) == "" {
		errs = append(errs, errors.New("adapters.s3_bucket is required when s3_endpoint is configured"))
	}
	if c.Adapters.S3Endpoint == "" && (c.Adapters.S3Bucket != "" || c.Adapters.S3AccessKey != "" || c.Adapters.S3SecretKey != "" || c.Adapters.S3SessionToken != "") {
		errs = append(errs, errors.New("adapters.s3_endpoint is required when S3 bucket or credentials are configured"))
	}
	if c.Adapters.S3Bucket != "" && !validS3Bucket(c.Adapters.S3Bucket) {
		errs = append(errs, errors.New("adapters.s3_bucket is not a valid DNS-style bucket name"))
	}
	if (c.Adapters.S3AccessKey == "") != (c.Adapters.S3SecretKey == "") {
		errs = append(errs, errors.New("adapters.s3_access_key and adapters.s3_secret_key must be configured together"))
	}
	if c.Adapters.S3SessionToken != "" && c.Adapters.S3AccessKey == "" {
		errs = append(errs, errors.New("adapters.s3_session_token requires static S3 credentials"))
	}
	return errors.Join(errs...)
}

func validateConfiguredRoles(roles []string) error {
	_, err := NormalizeRoles(roles)
	return err
}
