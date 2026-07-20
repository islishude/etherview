// Package config loads and validates Etherview's runtime configuration.
package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const envPrefix = "ETHERVIEW_"

// Config is the complete runtime configuration. A deployment serves exactly
// one chain, although chain_id remains present in persistent identities.
type Config struct {
	Server       ServerConfig       `yaml:"server"`
	Chain        ChainConfig        `yaml:"chain"`
	Database     DatabaseConfig     `yaml:"database"`
	RPC          RPCConfig          `yaml:"rpc"`
	Runtime      RuntimeConfig      `yaml:"runtime"`
	Mempool      MempoolConfig      `yaml:"mempool"`
	Maintenance  MaintenanceConfig  `yaml:"maintenance"`
	Metadata     MetadataConfig     `yaml:"metadata"`
	Features     FeatureConfig      `yaml:"features"`
	Security     SecurityConfig     `yaml:"security"`
	Verification VerificationConfig `yaml:"verification"`
	Adapters     AdapterConfig      `yaml:"adapters"`
}

type ServerConfig struct {
	Address         string        `yaml:"address"`
	MetricsAddress  string        `yaml:"metrics_address"`
	PublicURL       string        `yaml:"public_url"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	ReadTimeout     time.Duration `yaml:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout"`
}

type ChainConfig struct {
	ID             uint64 `yaml:"id"`
	GenesisHash    string `yaml:"genesis_hash"`
	StartBlock     uint64 `yaml:"start_block"`
	Name           string `yaml:"name"`
	NativeSymbol   string `yaml:"native_symbol"`
	NativeName     string `yaml:"native_name"`
	NativeDecimals uint8  `yaml:"native_decimals"`
	MaxReorgDepth  uint64 `yaml:"max_reorg_depth"`
}

type DatabaseConfig struct {
	URL              string        `yaml:"url"`
	MaxConnections   int32         `yaml:"max_connections"`
	MinConnections   int32         `yaml:"min_connections"`
	ConnectTimeout   time.Duration `yaml:"connect_timeout"`
	StatementTimeout time.Duration `yaml:"statement_timeout"`
}

type RPCConfig struct {
	RequestTimeout time.Duration `yaml:"request_timeout"`
	BatchSize      int           `yaml:"batch_size"`
	Endpoints      []RPCEndpoint `yaml:"endpoints"`
}

type RPCEndpoint struct {
	Name        string   `yaml:"name"`
	URL         string   `yaml:"url"`
	Purposes    []string `yaml:"purposes"`
	MaxRequests int      `yaml:"max_requests_per_second"`
}

type RuntimeConfig struct {
	Roles           []string      `yaml:"roles"`
	PollInterval    time.Duration `yaml:"poll_interval"`
	WorkerCount     int           `yaml:"worker_count"`
	BackfillWorkers int           `yaml:"backfill_workers"`
	LeaseDuration   time.Duration `yaml:"lease_duration"`
}

// MempoolConfig bounds the optional authoritative pending-block poller. The
// retained PostgreSQL snapshots, rather than an in-process cache, are served by
// API-only processes.
type MempoolConfig struct {
	PollInterval     time.Duration `yaml:"poll_interval"`
	Retention        time.Duration `yaml:"retention"`
	MaxTransactions  int           `yaml:"max_transactions"`
	MaxResponseBytes int           `yaml:"max_response_bytes"`
}

// MaintenanceConfig bounds optional PostgreSQL housekeeping. Cleanup improves
// storage behavior only; it is never a correctness prerequisite for indexing.
type MaintenanceConfig struct {
	Interval                   time.Duration `yaml:"interval"`
	SearchRetentionGenerations int64         `yaml:"search_retention_generations"`
	AdapterDeleteBatch         int           `yaml:"adapter_delete_batch"`
}

// MetadataConfig bounds hostile external NFT metadata retrieval. The IPFS
// gateway is optional; without it, ipfs:// resources become explicitly
// unavailable while direct HTTPS metadata continues to work.
type MetadataConfig struct {
	FetchTimeout     time.Duration `yaml:"fetch_timeout"`
	MaxDocumentBytes int           `yaml:"max_document_bytes"`
	MaxRedirects     int           `yaml:"max_redirects"`
	IPFSGateway      string        `yaml:"ipfs_gateway"`
}

type FeatureConfig struct {
	Trace           bool `yaml:"trace"`
	Mempool         bool `yaml:"mempool"`
	HistoricalState bool `yaml:"historical_state"`
	Verification    bool `yaml:"verification"`
	NFTMetadata     bool `yaml:"nft_metadata"`
	Pricing         bool `yaml:"pricing"`
}

type SecurityConfig struct {
	PublicVerification bool     `yaml:"public_verification"`
	CompilerSandbox    string   `yaml:"compiler_sandbox"`
	APIKeyPepper       string   `yaml:"api_key_pepper"`
	AnonymousRate      int      `yaml:"anonymous_rate"`
	AnonymousBurst     int      `yaml:"anonymous_burst"`
	AllowedOrigins     []string `yaml:"allowed_origins"`
	TrustedProxies     []string `yaml:"trusted_proxies"`
}

// VerificationConfig describes reproducible compiler execution. Public
// verification only accepts digest-pinned container images. Process compiler
// artifacts are reserved for explicitly private deployments and must use an
// HTTPS URL plus a SHA-256 allowlist entry.
type VerificationConfig struct {
	MaxInputBytes    int                                    `yaml:"max_input_bytes"`
	MaxOutputBytes   int                                    `yaml:"max_output_bytes"`
	Timeout          time.Duration                          `yaml:"timeout"`
	CacheDirectory   string                                 `yaml:"cache_directory"`
	ContainerRuntime string                                 `yaml:"container_runtime"`
	ContainerMemory  string                                 `yaml:"container_memory"`
	ContainerCPUs    string                                 `yaml:"container_cpus"`
	ContainerPIDs    int                                    `yaml:"container_pids"`
	Artifacts        map[string]map[string]CompilerArtifact `yaml:"artifacts"`
	Images           map[string]map[string]string           `yaml:"images"`
}

type CompilerArtifact struct {
	URL      string `yaml:"url"`
	SHA256   string `yaml:"sha256"`
	MaxBytes int64  `yaml:"max_bytes"`
}

// AdapterConfig contains optional accelerators. No correctness path may require
// any of these values; an empty AdapterConfig is the normal monolith setup.
type AdapterConfig struct {
	Namespace        string        `yaml:"namespace"`
	NATSURL          string        `yaml:"nats_url"`
	RedisURL         string        `yaml:"redis_url"`
	ConnectTimeout   time.Duration `yaml:"connect_timeout"`
	OperationTimeout time.Duration `yaml:"operation_timeout"`
	RedisCacheTTL    time.Duration `yaml:"redis_cache_ttl"`
	S3Endpoint       string        `yaml:"s3_endpoint"`
	S3Bucket         string        `yaml:"s3_bucket"`
	S3Prefix         string        `yaml:"s3_prefix"`
	S3Region         string        `yaml:"s3_region"`
	S3AccessKey      string        `yaml:"s3_access_key"`
	S3SecretKey      string        `yaml:"s3_secret_key"`
	S3SessionToken   string        `yaml:"s3_session_token"`
	S3PathStyle      bool          `yaml:"s3_path_style"`
	S3MaxObjectBytes int64         `yaml:"s3_max_object_bytes"`
	PriceBaseURL     string        `yaml:"price_base_url"`
	NameBaseURL      string        `yaml:"name_base_url"`
	FetchTimeout     time.Duration `yaml:"fetch_timeout"`
	MaxResponseBytes int           `yaml:"max_response_bytes"`
	MaxRedirects     int           `yaml:"max_redirects"`
	PriceFreshness   time.Duration `yaml:"price_freshness"`
	NameFreshness    time.Duration `yaml:"name_freshness"`
	FailureTTL       time.Duration `yaml:"failure_ttl"`
}

// Default returns safe development defaults. A database URL and a real RPC
// endpoint are still required before the corresponding roles can run.
func Default() Config {
	return Config{
		Server: ServerConfig{
			Address:         "127.0.0.1:8080",
			MetricsAddress:  "127.0.0.1:9090",
			ShutdownTimeout: 20 * time.Second,
			ReadTimeout:     15 * time.Second,
			WriteTimeout:    30 * time.Second,
		},
		Chain: ChainConfig{
			ID:             1,
			Name:           "Ethereum",
			NativeSymbol:   "ETH",
			NativeName:     "Ether",
			NativeDecimals: 18,
			MaxReorgDepth:  128,
		},
		Database: DatabaseConfig{
			MaxConnections:   20,
			MinConnections:   2,
			ConnectTimeout:   10 * time.Second,
			StatementTimeout: 30 * time.Second,
		},
		RPC: RPCConfig{
			RequestTimeout: 20 * time.Second,
			BatchSize:      100,
		},
		Runtime: RuntimeConfig{
			Roles:           []string{"all"},
			PollInterval:    2 * time.Second,
			WorkerCount:     4,
			BackfillWorkers: 4,
			LeaseDuration:   30 * time.Second,
		},
		Mempool: MempoolConfig{
			PollInterval:     3 * time.Second,
			Retention:        10 * time.Minute,
			MaxTransactions:  50_000,
			MaxResponseBytes: 16 << 20,
		},
		Maintenance: MaintenanceConfig{
			Interval: 15 * time.Minute, SearchRetentionGenerations: 100_000,
			AdapterDeleteBatch: 1_000,
		},
		Metadata: MetadataConfig{
			FetchTimeout:     10 * time.Second,
			MaxDocumentBytes: 2 << 20,
			MaxRedirects:     3,
		},
		Adapters: AdapterConfig{
			Namespace: "etherview", ConnectTimeout: 2 * time.Second,
			OperationTimeout: 500 * time.Millisecond, RedisCacheTTL: 30 * time.Second,
			S3Prefix: "etherview", S3MaxObjectBytes: 16 << 20,
			FetchTimeout: 5 * time.Second, MaxResponseBytes: 1 << 20, MaxRedirects: 2,
			PriceFreshness: 5 * time.Minute, NameFreshness: 24 * time.Hour, FailureTTL: 30 * time.Second,
		},
		Security: SecurityConfig{
			CompilerSandbox: "disabled",
			AnonymousRate:   5,
			AnonymousBurst:  20,
		},
		Verification: VerificationConfig{
			MaxInputBytes:    5 << 20,
			MaxOutputBytes:   64 << 20,
			Timeout:          2 * time.Minute,
			CacheDirectory:   "/var/lib/etherview/compilers",
			ContainerRuntime: "docker",
			ContainerMemory:  "512m",
			ContainerCPUs:    "1",
			ContainerPIDs:    64,
		},
	}
}

// Load reads an optional YAML file, overlays supported ETHERVIEW_ environment
// variables, and validates the resulting configuration.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("decode config: %w", err)
		}
	}
	if err := applyEnvironment(&cfg, os.LookupEnv, os.ReadFile); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks structural and security-sensitive invariants without making
// network connections.
func (c Config) Validate() error {
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
		u, err := url.Parse(c.Server.PublicURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			errs = append(errs, errors.New("server.public_url must be an absolute URL"))
		}
	}
	if c.Chain.ID == 0 {
		errs = append(errs, errors.New("chain.id must be greater than zero"))
	}
	if c.Chain.GenesisHash != "" && !validFixedHex(c.Chain.GenesisHash, 32) {
		errs = append(errs, errors.New("chain.genesis_hash must be a 32-byte 0x-prefixed hash"))
	}
	if c.Chain.NativeSymbol == "" || c.Chain.NativeName == "" {
		errs = append(errs, errors.New("chain native currency name and symbol are required"))
	}
	if c.Chain.MaxReorgDepth == 0 {
		errs = append(errs, errors.New("chain.max_reorg_depth must be greater than zero"))
	}
	if c.Database.MaxConnections <= 0 || c.Database.MinConnections < 0 || c.Database.MinConnections > c.Database.MaxConnections {
		errs = append(errs, errors.New("database connection bounds are invalid"))
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
	if c.Runtime.PollInterval <= 0 || c.Runtime.WorkerCount <= 0 || c.Runtime.BackfillWorkers <= 0 || c.Runtime.LeaseDuration <= 0 {
		errs = append(errs, errors.New("runtime poll_interval, worker_count, backfill_workers, and lease_duration must be positive"))
	}
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
	if _, err := NormalizeRoles(c.Runtime.Roles); err != nil {
		errs = append(errs, err)
	}
	if c.Security.CompilerSandbox != "disabled" && c.Security.CompilerSandbox != "process" && c.Security.CompilerSandbox != "container" {
		errs = append(errs, errors.New("security.compiler_sandbox must be disabled, process, or container"))
	}
	if c.Security.PublicVerification && c.Security.CompilerSandbox != "container" {
		errs = append(errs, errors.New("public verification requires a container compiler sandbox"))
	}
	if c.Security.PublicVerification && !c.Features.Verification {
		errs = append(errs, errors.New("public verification requires features.verification"))
	}
	if c.Security.CompilerSandbox == "container" && c.Verification.ContainerRuntime != "docker" && c.Verification.ContainerRuntime != "podman" {
		errs = append(errs, errors.New("verification.container_runtime must be docker or podman in container sandbox mode"))
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
	if c.Verification.MaxInputBytes <= 0 || c.Verification.MaxInputBytes > 64<<20 {
		errs = append(errs, errors.New("verification.max_input_bytes must be between 1 and 67108864"))
	}
	if c.Verification.MaxOutputBytes <= 0 || c.Verification.MaxOutputBytes > 256<<20 {
		errs = append(errs, errors.New("verification.max_output_bytes must be between 1 and 268435456"))
	}
	if c.Verification.Timeout <= 0 || c.Verification.Timeout > 30*time.Minute {
		errs = append(errs, errors.New("verification.timeout must be between 1ns and 30m"))
	}
	if c.Verification.ContainerPIDs <= 0 || c.Verification.ContainerPIDs > 4096 {
		errs = append(errs, errors.New("verification.container_pids must be between 1 and 4096"))
	}
	if err := validateCompilerAllowlist(c.Verification); err != nil {
		errs = append(errs, err)
	}
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
		"adapters.name_base_url":  c.Adapters.NameBaseURL,
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
	if c.Adapters.NameFreshness <= 0 || c.Adapters.NameFreshness > 30*24*time.Hour {
		errs = append(errs, errors.New("adapters.name_freshness must be between 1ns and 720h"))
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

// ValidateForRoles applies dependencies that are specific to runnable roles.
// Load deliberately does not require them so doctor and config tooling can
// report all missing values in one pass.
func (c Config) ValidateForRoles(roles []string) error {
	normalized, err := NormalizeRoles(roles)
	if err != nil {
		return err
	}
	var errs []error
	if strings.TrimSpace(c.Database.URL) == "" {
		errs = append(errs, errors.New("database.url is required for runnable roles"))
	} else if u, parseErr := url.Parse(c.Database.URL); parseErr != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		errs = append(errs, errors.New("database.url must be an absolute postgres URL"))
	}
	needsRPC := false
	needsVerificationWorker := false
	for _, role := range normalized {
		if role == "sync" || role == "enrich" || role == "trace" || role == "maintenance" {
			needsRPC = true
		}
		if role == "verify" && c.Features.Verification {
			needsVerificationWorker = true
		}
	}
	if needsRPC && len(c.RPC.Endpoints) == 0 {
		errs = append(errs, errors.New("at least one rpc endpoint is required for selected roles"))
	}
	if needsVerificationWorker {
		switch c.Security.CompilerSandbox {
		case "container":
			if len(c.Verification.Images) == 0 {
				errs = append(errs, errors.New("verification.images is required by the verify role in container sandbox mode"))
			}
		case "process":
			if c.Security.PublicVerification {
				errs = append(errs, errors.New("process compiler sandbox cannot serve public verification"))
			}
			if strings.TrimSpace(c.Verification.CacheDirectory) == "" || len(c.Verification.Artifacts) == 0 {
				errs = append(errs, errors.New("verification.cache_directory and artifacts are required by the verify role in process sandbox mode"))
			}
		default:
			errs = append(errs, errors.New("verification feature requires a configured compiler sandbox for the verify role"))
		}
	}
	return errors.Join(errs...)
}

func validateCompilerAllowlist(cfg VerificationConfig) error {
	var errs []error
	for language, versions := range cfg.Artifacts {
		if language != "solidity" && language != "vyper" {
			errs = append(errs, fmt.Errorf("verification artifact language %q is unsupported", language))
		}
		for version, artifact := range versions {
			if strings.TrimSpace(version) == "" {
				errs = append(errs, errors.New("verification artifact version is empty"))
			}
			u, err := url.Parse(artifact.URL)
			if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
				errs = append(errs, fmt.Errorf("verification artifact %s/%s must use an absolute HTTPS URL", language, version))
			}
			if len(artifact.SHA256) != 64 {
				errs = append(errs, fmt.Errorf("verification artifact %s/%s has an invalid SHA-256", language, version))
			} else if _, err := hex.DecodeString(artifact.SHA256); err != nil {
				errs = append(errs, fmt.Errorf("verification artifact %s/%s has an invalid SHA-256", language, version))
			}
			if artifact.MaxBytes < 0 {
				errs = append(errs, fmt.Errorf("verification artifact %s/%s max_bytes cannot be negative", language, version))
			}
		}
	}
	for language, versions := range cfg.Images {
		if language != "solidity" && language != "vyper" {
			errs = append(errs, fmt.Errorf("verification image language %q is unsupported", language))
		}
		for version, image := range versions {
			parts := strings.Split(image, "@sha256:")
			if strings.TrimSpace(version) == "" || len(parts) != 2 || parts[0] == "" || len(parts[1]) != 64 {
				errs = append(errs, fmt.Errorf("verification image %s/%s must be pinned by SHA-256 digest", language, version))
				continue
			}
			if _, err := hex.DecodeString(parts[1]); err != nil {
				errs = append(errs, fmt.Errorf("verification image %s/%s has an invalid digest", language, version))
			}
		}
	}
	return errors.Join(errs...)
}

func (e RPCEndpoint) Validate() error {
	var errs []error
	if strings.TrimSpace(e.Name) == "" {
		errs = append(errs, errors.New("name is required"))
	}
	u, err := url.Parse(e.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "ws" && u.Scheme != "wss") {
		errs = append(errs, errors.New("url must use http, https, ws, or wss"))
	}
	if len(e.Purposes) == 0 {
		errs = append(errs, errors.New("at least one purpose is required"))
	}
	allowed := map[string]bool{"all": true, "head": true, "history": true, "state": true, "trace": true, "mempool": true}
	for _, purpose := range e.Purposes {
		if !allowed[purpose] {
			errs = append(errs, fmt.Errorf("unsupported purpose %q", purpose))
		}
	}
	if e.MaxRequests < 0 {
		errs = append(errs, errors.New("max_requests_per_second cannot be negative"))
	}
	return errors.Join(errs...)
}

var allowedRoles = []string{"api", "sync", "enrich", "trace", "verify", "metadata", "maintenance"}

// NormalizeRoles validates roles, expands all, removes duplicates, and returns
// roles in stable architectural order.
func NormalizeRoles(input []string) ([]string, error) {
	if len(input) == 0 {
		return nil, errors.New("runtime.roles cannot be empty")
	}
	wanted := make(map[string]bool, len(input))
	for _, raw := range input {
		for _, role := range strings.Split(raw, ",") {
			role = strings.ToLower(strings.TrimSpace(role))
			if role == "" {
				continue
			}
			if role == "all" {
				for _, item := range allowedRoles {
					wanted[item] = true
				}
				continue
			}
			known := false
			for _, item := range allowedRoles {
				if role == item {
					known = true
					break
				}
			}
			if !known {
				return nil, fmt.Errorf("unsupported runtime role %q", role)
			}
			wanted[role] = true
		}
	}
	if len(wanted) == 0 {
		return nil, errors.New("runtime.roles cannot be empty")
	}
	out := make([]string, 0, len(wanted))
	for _, role := range allowedRoles {
		if wanted[role] {
			out = append(out, role)
		}
	}
	return out, nil
}

func applyEnvironment(cfg *Config, lookup func(string) (string, bool), readFile func(string) ([]byte, error)) error {
	secret, err := lookupValueOrFile("DATABASE_URL", lookup, readFile)
	if err != nil {
		return err
	}
	if secret != "" {
		cfg.Database.URL = secret
	}
	setString(lookup, "SERVER_ADDRESS", &cfg.Server.Address)
	setString(lookup, "SERVER_METRICS_ADDRESS", &cfg.Server.MetricsAddress)
	setString(lookup, "SERVER_PUBLIC_URL", &cfg.Server.PublicURL)
	setString(lookup, "CHAIN_GENESIS_HASH", &cfg.Chain.GenesisHash)
	setString(lookup, "CHAIN_NAME", &cfg.Chain.Name)
	setString(lookup, "CHAIN_NATIVE_SYMBOL", &cfg.Chain.NativeSymbol)
	setString(lookup, "CHAIN_NATIVE_NAME", &cfg.Chain.NativeName)
	pepper, err := lookupValueOrFile("API_KEY_PEPPER", lookup, readFile)
	if err != nil {
		return err
	}
	if pepper != "" {
		cfg.Security.APIKeyPepper = pepper
	}
	setString(lookup, "COMPILER_SANDBOX", &cfg.Security.CompilerSandbox)
	setString(lookup, "COMPILER_CACHE_DIRECTORY", &cfg.Verification.CacheDirectory)
	setString(lookup, "COMPILER_CONTAINER_RUNTIME", &cfg.Verification.ContainerRuntime)
	for name, target := range map[string]*string{
		"NATS_URL":         &cfg.Adapters.NATSURL,
		"REDIS_URL":        &cfg.Adapters.RedisURL,
		"S3_ENDPOINT":      &cfg.Adapters.S3Endpoint,
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
	setString(lookup, "ADAPTER_NAMESPACE", &cfg.Adapters.Namespace)
	setString(lookup, "S3_BUCKET", &cfg.Adapters.S3Bucket)
	setString(lookup, "S3_PREFIX", &cfg.Adapters.S3Prefix)
	setString(lookup, "S3_REGION", &cfg.Adapters.S3Region)
	setString(lookup, "PRICE_BASE_URL", &cfg.Adapters.PriceBaseURL)
	setString(lookup, "NAME_BASE_URL", &cfg.Adapters.NameBaseURL)
	setString(lookup, "METADATA_IPFS_GATEWAY", &cfg.Metadata.IPFSGateway)
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
	if err := setInt32(lookup, "DATABASE_MAX_CONNECTIONS", &cfg.Database.MaxConnections); err != nil {
		return err
	}
	if err := setInt32(lookup, "DATABASE_MIN_CONNECTIONS", &cfg.Database.MinConnections); err != nil {
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
	if err := setInt64(lookup, "S3_MAX_OBJECT_BYTES", &cfg.Adapters.S3MaxObjectBytes); err != nil {
		return err
	}
	if err := setInt(lookup, "ANONYMOUS_RATE", &cfg.Security.AnonymousRate); err != nil {
		return err
	}
	if err := setInt(lookup, "ANONYMOUS_BURST", &cfg.Security.AnonymousBurst); err != nil {
		return err
	}
	if err := setInt(lookup, "VERIFICATION_MAX_INPUT_BYTES", &cfg.Verification.MaxInputBytes); err != nil {
		return err
	}
	if err := setInt(lookup, "VERIFICATION_MAX_OUTPUT_BYTES", &cfg.Verification.MaxOutputBytes); err != nil {
		return err
	}
	for name, target := range map[string]*time.Duration{
		"SERVER_SHUTDOWN_TIMEOUT":    &cfg.Server.ShutdownTimeout,
		"SERVER_READ_TIMEOUT":        &cfg.Server.ReadTimeout,
		"SERVER_WRITE_TIMEOUT":       &cfg.Server.WriteTimeout,
		"DATABASE_CONNECT_TIMEOUT":   &cfg.Database.ConnectTimeout,
		"DATABASE_STATEMENT_TIMEOUT": &cfg.Database.StatementTimeout,
		"RPC_REQUEST_TIMEOUT":        &cfg.RPC.RequestTimeout,
		"POLL_INTERVAL":              &cfg.Runtime.PollInterval,
		"LEASE_DURATION":             &cfg.Runtime.LeaseDuration,
		"MEMPOOL_POLL_INTERVAL":      &cfg.Mempool.PollInterval,
		"MEMPOOL_RETENTION":          &cfg.Mempool.Retention,
		"MAINTENANCE_INTERVAL":       &cfg.Maintenance.Interval,
		"METADATA_FETCH_TIMEOUT":     &cfg.Metadata.FetchTimeout,
		"ADAPTER_FETCH_TIMEOUT":      &cfg.Adapters.FetchTimeout,
		"ADAPTER_CONNECT_TIMEOUT":    &cfg.Adapters.ConnectTimeout,
		"ADAPTER_OPERATION_TIMEOUT":  &cfg.Adapters.OperationTimeout,
		"REDIS_CACHE_TTL":            &cfg.Adapters.RedisCacheTTL,
		"ADAPTER_PRICE_FRESHNESS":    &cfg.Adapters.PriceFreshness,
		"ADAPTER_NAME_FRESHNESS":     &cfg.Adapters.NameFreshness,
		"ADAPTER_FAILURE_TTL":        &cfg.Adapters.FailureTTL,
		"VERIFICATION_TIMEOUT":       &cfg.Verification.Timeout,
	} {
		if err := setDuration(lookup, name, target); err != nil {
			return err
		}
	}
	for name, target := range map[string]*bool{
		"FEATURE_TRACE":            &cfg.Features.Trace,
		"FEATURE_MEMPOOL":          &cfg.Features.Mempool,
		"FEATURE_HISTORICAL_STATE": &cfg.Features.HistoricalState,
		"FEATURE_VERIFICATION":     &cfg.Features.Verification,
		"FEATURE_NFT_METADATA":     &cfg.Features.NFTMetadata,
		"FEATURE_PRICING":          &cfg.Features.Pricing,
		"PUBLIC_VERIFICATION":      &cfg.Security.PublicVerification,
		"S3_PATH_STYLE":            &cfg.Adapters.S3PathStyle,
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
	if value, ok := lookup(envPrefix + "ROLES"); ok {
		cfg.Runtime.Roles = strings.Split(value, ",")
	}
	rpcURLs, err := lookupValueOrFile("RPC_URLS", lookup, readFile)
	if err != nil {
		return err
	}
	if rpcURLs != "" {
		cfg.RPC.Endpoints = nil
		for i, raw := range strings.Split(rpcURLs, ",") {
			raw = strings.TrimSpace(raw)
			if raw != "" {
				cfg.RPC.Endpoints = append(cfg.RPC.Endpoints, RPCEndpoint{Name: fmt.Sprintf("env-%d", i+1), URL: raw, Purposes: []string{"all"}})
			}
		}
	}
	return nil
}

func lookupValueOrFile(name string, lookup func(string) (string, bool), readFile func(string) ([]byte, error)) (string, error) {
	value, valueSet := lookup(envPrefix + name)
	path, fileSet := lookup(envPrefix + name + "_FILE")
	if valueSet && fileSet {
		return "", fmt.Errorf("%s%s and %s%s_FILE are mutually exclusive", envPrefix, name, envPrefix, name)
	}
	if fileSet {
		data, err := readFile(path)
		if err != nil {
			return "", fmt.Errorf("read %s%s_FILE: %w", envPrefix, name, err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return strings.TrimSpace(value), nil
}

func setString(lookup func(string) (string, bool), name string, target *string) {
	if value, ok := lookup(envPrefix + name); ok {
		*target = strings.TrimSpace(value)
	}
}

func setUint64(lookup func(string) (string, bool), name string, target *uint64) error {
	if value, ok := lookup(envPrefix + name); ok {
		parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return fmt.Errorf("parse %s%s: %w", envPrefix, name, err)
		}
		*target = parsed
	}
	return nil
}

func setInt(lookup func(string) (string, bool), name string, target *int) error {
	if value, ok := lookup(envPrefix + name); ok {
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("parse %s%s: %w", envPrefix, name, err)
		}
		*target = parsed
	}
	return nil
}

func setInt64(lookup func(string) (string, bool), name string, target *int64) error {
	if value, ok := lookup(envPrefix + name); ok {
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return fmt.Errorf("parse %s%s: %w", envPrefix, name, err)
		}
		*target = parsed
	}
	return nil
}

func setInt32(lookup func(string) (string, bool), name string, target *int32) error {
	if value, ok := lookup(envPrefix + name); ok {
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 32)
		if err != nil {
			return fmt.Errorf("parse %s%s: %w", envPrefix, name, err)
		}
		*target = int32(parsed)
	}
	return nil
}

func setUint8(lookup func(string) (string, bool), name string, target *uint8) error {
	if value, ok := lookup(envPrefix + name); ok {
		parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 8)
		if err != nil {
			return fmt.Errorf("parse %s%s: %w", envPrefix, name, err)
		}
		*target = uint8(parsed)
	}
	return nil
}

func setDuration(lookup func(string) (string, bool), name string, target *time.Duration) error {
	if value, ok := lookup(envPrefix + name); ok {
		parsed, err := time.ParseDuration(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("parse %s%s: %w", envPrefix, name, err)
		}
		*target = parsed
	}
	return nil
}

func setBool(lookup func(string) (string, bool), name string, target *bool) error {
	if value, ok := lookup(envPrefix + name); ok {
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("parse %s%s: %w", envPrefix, name, err)
		}
		*target = parsed
	}
	return nil
}

func splitCSV(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func validAdapterNamespace(value string) bool {
	if len(value) < 1 || len(value) > 63 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validS3Bucket(value string) bool {
	if len(value) < 3 || len(value) > 63 || value[0] == '-' || value[0] == '.' ||
		value[len(value)-1] == '-' || value[len(value)-1] == '.' ||
		strings.Contains(value, "..") || strings.Contains(value, ".-") || strings.Contains(value, "-.") {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '.' || character == '-' {
			continue
		}
		return false
	}
	// DNS-looking IPv4 addresses are prohibited as S3 bucket names.
	return net.ParseIP(value) == nil
}

func validFixedHex(value string, byteLen int) bool {
	if len(value) != 2+byteLen*2 || !strings.HasPrefix(value, "0x") {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil
}
