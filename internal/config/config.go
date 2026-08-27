// Package config loads and validates Etherview's runtime configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	envPrefix = "ETHERVIEW_"

	maximumRuntimeWorkerCount         = 64
	maximumRuntimeBackfillWorkers     = 64
	maximumRuntimeBackfillBatchBlocks = 256

	defaultVerificationExecutorPath = "/opt/etherview/solcjs/etherview-solcjs"
	defaultVerificationGeasPath     = "/usr/local/bin/etherview-geas-compiler"
)

// Config is the complete runtime configuration. A deployment serves exactly
// one chain, although chain_id remains present in persistent identities.
type Config struct {
	Server        ServerConfig        `yaml:"server"`
	Chain         ChainConfig         `yaml:"chain"`
	Wallet        WalletConfig        `yaml:"wallet"`
	Database      DatabaseConfig      `yaml:"database"`
	RPC           RPCConfig           `yaml:"rpc"`
	Runtime       RuntimeConfig       `yaml:"runtime"`
	Mempool       MempoolConfig       `yaml:"mempool"`
	Maintenance   MaintenanceConfig   `yaml:"maintenance"`
	Observability ObservabilityConfig `yaml:"observability"`
	Metadata      MetadataConfig      `yaml:"metadata"`
	Features      FeatureConfig       `yaml:"features"`
	Security      SecurityConfig      `yaml:"security"`
	Verification  VerificationConfig  `yaml:"verification"`
	Sourcify      SourcifyConfig      `yaml:"sourcify"`
	UserAuth      UserAuthConfig      `yaml:"user_auth"`
	Billing       BillingConfig       `yaml:"billing"`
	ENS           ENSConfig           `yaml:"ens"`
	Adapters      AdapterConfig       `yaml:"adapters"`
}

type ServerConfig struct {
	Address         string        `yaml:"address"`
	MetricsAddress  string        `yaml:"metrics_address"`
	PublicURL       string        `yaml:"public_url"`
	TLSCertFile     string        `yaml:"tls_cert_file"`
	TLSKeyFile      string        `yaml:"tls_key_file"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	ReadTimeout     time.Duration `yaml:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout"`
}

type ChainConfig struct {
	ID                  uint64        `yaml:"id"`
	GenesisHash         string        `yaml:"genesis_hash"`
	GenesisFile         string        `yaml:"genesis_file"`
	GenesisURL          string        `yaml:"genesis_url"`
	GenesisSHA256       string        `yaml:"genesis_sha256"`
	GenesisFetchTimeout time.Duration `yaml:"genesis_fetch_timeout"`
	StartBlock          uint64        `yaml:"start_block"`
	Name                string        `yaml:"name"`
	NativeSymbol        string        `yaml:"native_symbol"`
	NativeName          string        `yaml:"native_name"`
	NativeDecimals      uint8         `yaml:"native_decimals"`
	MaxReorgDepth       uint64        `yaml:"max_reorg_depth"`
}

type WalletConfig struct {
	AddChain WalletAddChainConfig `yaml:"add_chain"`
}

// WalletAddChainConfig contains only explicitly public URLs that may be sent
// to an injected wallet. Server RPC endpoints are intentionally separate and
// are never used as a fallback for this browser-facing configuration.
type WalletAddChainConfig struct {
	RPCURLs           []string `yaml:"rpc_urls"`
	BlockExplorerURLs []string `yaml:"block_explorer_urls"`
	IconURLs          []string `yaml:"icon_urls"`
}

type DatabaseConfig struct {
	URL                string        `yaml:"url"`
	ReadURL            string        `yaml:"read_url"`
	MaxConnections     int32         `yaml:"max_connections"`
	MinConnections     int32         `yaml:"min_connections"`
	ReadMaxConnections int32         `yaml:"read_max_connections"`
	ReadMinConnections int32         `yaml:"read_min_connections"`
	ConnectTimeout     time.Duration `yaml:"connect_timeout"`
	StatementTimeout   time.Duration `yaml:"statement_timeout"`
}

type RPCConfig struct {
	RequestTimeout time.Duration `yaml:"request_timeout"`
	BatchSize      int           `yaml:"batch_size"`
	Endpoints      []RPCEndpoint `yaml:"endpoints"`
}

type RPCEndpoint struct {
	Name        string   `json:"name" yaml:"name"`
	URL         string   `json:"url" yaml:"url"`
	Purposes    []string `json:"purposes" yaml:"purposes"`
	MaxRequests int      `json:"max_requests_per_second" yaml:"max_requests_per_second"`
}

type RuntimeConfig struct {
	Roles               []string      `yaml:"roles"`
	PollInterval        time.Duration `yaml:"poll_interval"`
	WorkerCount         int           `yaml:"worker_count"`
	BackfillWorkers     int           `yaml:"backfill_workers"`
	BackfillBatchBlocks int           `yaml:"backfill_batch_blocks"`
	LeaseDuration       time.Duration `yaml:"lease_duration"`
}

// MempoolConfig bounds the optional authoritative txpool-backed pending-transaction poller. The
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

// ObservabilityConfig controls process-local telemetry. OTLP tracing is
// disabled when OTLPTraceEndpoint is empty, so the normal PostgreSQL-only
// deployment starts no exporter goroutines and makes no collector calls.
type ObservabilityConfig struct {
	Environment             string        `yaml:"environment"`
	LogLevel                string        `yaml:"log_level"`
	LogFormat               string        `yaml:"log_format"`
	OTLPTraceEndpoint       string        `yaml:"otlp_trace_endpoint"`
	OTLPTraceInsecure       bool          `yaml:"otlp_trace_insecure"`
	TraceSampleRatio        float64       `yaml:"trace_sample_ratio"`
	TraceExportTimeout      time.Duration `yaml:"trace_export_timeout"`
	MetricsRefreshInterval  time.Duration `yaml:"metrics_refresh_interval"`
	SyncProgressLogInterval time.Duration `yaml:"sync_progress_log_interval"`
}

// MetadataConfig bounds hostile external NFT metadata retrieval. The IPFS
// gateway is optional; without it, ipfs:// resources become explicitly
// unavailable while direct HTTPS metadata continues to work.
type MetadataConfig struct {
	FetchTimeout               time.Duration `yaml:"fetch_timeout"`
	MaxDocumentBytes           int           `yaml:"max_document_bytes"`
	MaxRedirects               int           `yaml:"max_redirects"`
	IPFSGateway                string        `yaml:"ipfs_gateway"`
	UnsafeAllowPrivateNetworks bool          `yaml:"unsafe_allow_private_networks"`
}

type FeatureConfig struct {
	Trace                  bool `yaml:"trace"`
	Mempool                bool `yaml:"mempool"`
	HistoricalState        bool `yaml:"historical_state"`
	Verification           bool `yaml:"verification"`
	Sourcify               bool `yaml:"sourcify"`
	NFTMetadata            bool `yaml:"nft_metadata"`
	Pricing                bool `yaml:"pricing"`
	ENS                    bool `yaml:"ens"`
	UserAuth               bool `yaml:"user_auth"`
	UserAPIKeys            bool `yaml:"user_api_keys"`
	X402Billing            bool `yaml:"x402_billing"`
	APIBilling             bool `yaml:"api_billing"`
	X402Topups             bool `yaml:"x402_topups"`
	ProxyDetectionV2       bool `yaml:"proxy_detection_v2"`
	SafeProxyDetection     bool `yaml:"safe_proxy_detection"`
	DiamondProxyDetection  bool `yaml:"diamond_proxy_detection"`
	ProxyDetectionV2Public bool `yaml:"proxy_detection_v2_public"`
}

// ENSConfig bounds the optional current-name capability. OfficialRPCEndpoints
// is secret-only and is populated from ETHERVIEW_ENS_RPC_URLS(_FILE), never
// from ConfigMap-backed YAML.
type ENSConfig struct {
	OfficialRPCEndpoints []RPCEndpoint   `yaml:"-"`
	OfficialGateways     []string        `yaml:"official_gateways"`
	Custom               ENSCustomConfig `yaml:"custom"`
	ResolutionFreshness  time.Duration   `yaml:"resolution_freshness"`
	SnapshotTTL          time.Duration   `yaml:"snapshot_ttl"`
	FailureTTL           time.Duration   `yaml:"failure_ttl"`
	RequestTimeout       time.Duration   `yaml:"request_timeout"`
	MaxResponseBytes     int64           `yaml:"max_response_bytes"`
	MaxCCIPDepth         int             `yaml:"max_ccip_depth"`
	MaxBatchAddresses    int             `yaml:"max_batch_addresses"`
	MaxConcurrency       int             `yaml:"max_concurrency"`
}

type ENSCustomConfig struct {
	Registry          string   `yaml:"registry"`
	UniversalResolver string   `yaml:"universal_resolver"`
	CoinType          string   `yaml:"coin_type"`
	Gateways          []string `yaml:"gateways"`
}

type SecurityConfig struct {
	PublicVerification bool     `yaml:"public_verification"`
	APIKeyPepper       string   `yaml:"api_key_pepper"`
	AnonymousRate      int      `yaml:"anonymous_rate"`
	AnonymousBurst     int      `yaml:"anonymous_burst"`
	AllowedOrigins     []string `yaml:"allowed_origins"`
	TrustedProxies     []string `yaml:"trusted_proxies"`
}

// VerificationConfig describes reproducible, checksum-bound solc-js
// compilation owned by API-capable processes.
type VerificationConfig struct {
	MaxInputBytes          int               `yaml:"max_input_bytes"`
	MaxOutputBytes         int               `yaml:"max_output_bytes"`
	WorkerCount            int               `yaml:"worker_count"`
	Timeout                time.Duration     `yaml:"timeout"`
	CacheDirectory         string            `yaml:"cache_directory"`
	ExecutorPath           string            `yaml:"executor_path"`
	GeasPath               string            `yaml:"geas_path"`
	CatalogURLs            map[string]string `yaml:"catalog_urls"`
	AllowedDownloadOrigins []string          `yaml:"allowed_download_origins"`
	CatalogRefreshInterval time.Duration     `yaml:"catalog_refresh_interval"`
	CatalogMaxStaleness    time.Duration     `yaml:"catalog_max_staleness"`
	// UnsafeAllowPrivateDownloadNetworks exists only for explicit local
	// Preview/E2E environments whose DNS proxy uses synthetic private
	// addresses. HTTPS origin allowlisting, TLS, size, and SHA-256 checks remain
	// mandatory.
	UnsafeAllowPrivateDownloadNetworks bool `yaml:"unsafe_allow_private_download_networks"`
	DerivedEnabled                     bool `yaml:"derived_enabled"`
	DerivedBackfillEnabled             bool `yaml:"derived_backfill_enabled"`
	DerivedForwardEnabled              bool `yaml:"derived_forward_enabled"`
	DerivedWorkerCount                 int  `yaml:"derived_worker_count"`
	DerivedMaxTracesPerScan            int  `yaml:"derived_max_traces_per_scan"`
}

// SourcifyConfig bounds the optional external Sourcify v2 interoperability
// adapter. API/all workers own explicit requests and bounded upstream polling.
type SourcifyConfig struct {
	BaseURL          string        `yaml:"base_url"`
	Timeout          time.Duration `yaml:"timeout"`
	MaxRequestBytes  int           `yaml:"max_request_bytes"`
	MaxResponseBytes int64         `yaml:"max_response_bytes"`
	Attempts         int           `yaml:"attempts"`
	PollInterval     time.Duration `yaml:"poll_interval"`
	MaxPolls         int           `yaml:"max_polls"`
}

// UserAuthConfig bounds server-created EIP-4361 challenges and absolute,
// non-sliding Cookie sessions. SessionPepper is populated only from the
// API-role Secret environment and is intentionally not a YAML field.
type UserAuthConfig struct {
	ChallengeTTL      time.Duration `yaml:"challenge_ttl"`
	SessionTTL        time.Duration `yaml:"session_ttl"`
	LastUsedInterval  time.Duration `yaml:"last_used_interval"`
	MaxMessageBytes   int           `yaml:"max_message_bytes"`
	MaxSignatureBytes int           `yaml:"max_signature_bytes"`
	APIKeyRate        int           `yaml:"api_key_rate"`
	APIKeyBurst       int           `yaml:"api_key_burst"`
	MaxActiveAPIKeys  int           `yaml:"max_active_api_keys"`
	SessionPepper     string        `yaml:"-"`
}

type BillingRouteConfig struct {
	Access       string `yaml:"access"`
	AmountAtomic string `yaml:"amount_atomic"`
}

type BillingOperationConfig struct {
	AmountAtomic string `yaml:"amount_atomic"`
}

// BillingConfig describes the local x402 policy. FingerprintPepper and
// FacilitatorHeaders are Secret-only values and are never accepted from YAML.
type BillingConfig struct {
	FacilitatorURL              string                            `yaml:"facilitator_url"`
	FacilitatorAllowedCIDRs     []string                          `yaml:"facilitator_allowed_cidrs"`
	FacilitatorTimeout          time.Duration                     `yaml:"facilitator_timeout"`
	FacilitatorMaxResponseBytes int64                             `yaml:"facilitator_max_response_bytes"`
	FacilitatorUnsafeAllowHTTP  bool                              `yaml:"facilitator_unsafe_allow_http"`
	Network                     string                            `yaml:"network"`
	Asset                       string                            `yaml:"asset"`
	AssetDecimals               uint8                             `yaml:"asset_decimals"`
	AssetEIP712Name             string                            `yaml:"asset_eip712_name"`
	AssetEIP712Version          string                            `yaml:"asset_eip712_version"`
	Recipient                   string                            `yaml:"recipient"`
	RequirementMaxTimeout       time.Duration                     `yaml:"requirement_max_timeout"`
	ReservationTTL              time.Duration                     `yaml:"reservation_ttl"`
	TopupIntentTTL              time.Duration                     `yaml:"topup_intent_ttl"`
	UsageReservationTTL         time.Duration                     `yaml:"usage_reservation_ttl"`
	MinimumTopupAmountAtomic    string                            `yaml:"minimum_topup_amount_atomic"`
	MaximumTopupAmountAtomic    string                            `yaml:"maximum_topup_amount_atomic"`
	AssetTransferMethods        []string                          `yaml:"asset_transfer_methods"`
	MaxPaymentHeaderBytes       int                               `yaml:"max_payment_header_bytes"`
	MaxBufferedResponseBytes    int64                             `yaml:"max_buffered_response_bytes"`
	MaxCapturedHeaderBytes      int                               `yaml:"max_captured_header_bytes"`
	CoarseIPRate                int                               `yaml:"coarse_ip_rate"`
	CoarseIPBurst               int                               `yaml:"coarse_ip_burst"`
	Routes                      map[string]BillingRouteConfig     `yaml:"routes"`
	Operations                  map[string]BillingOperationConfig `yaml:"operations"`
	FingerprintPepper           string                            `yaml:"-"`
	FacilitatorHeaders          map[string]string                 `yaml:"-"`
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
	FetchTimeout     time.Duration `yaml:"fetch_timeout"`
	MaxResponseBytes int           `yaml:"max_response_bytes"`
	MaxRedirects     int           `yaml:"max_redirects"`
	PriceFreshness   time.Duration `yaml:"price_freshness"`
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
			ID:                  1,
			GenesisFetchTimeout: time.Minute,
			Name:                "Ethereum",
			NativeSymbol:        "ETH",
			NativeName:          "Ether",
			NativeDecimals:      18,
			MaxReorgDepth:       128,
		},
		Database: DatabaseConfig{
			MaxConnections:     20,
			MinConnections:     2,
			ReadMaxConnections: 0,
			ReadMinConnections: 0,
			ConnectTimeout:     10 * time.Second,
			StatementTimeout:   30 * time.Second,
		},
		RPC: RPCConfig{
			RequestTimeout: 20 * time.Second,
			BatchSize:      100,
		},
		Runtime: RuntimeConfig{
			Roles:               []string{"all"},
			PollInterval:        2 * time.Second,
			WorkerCount:         4,
			BackfillWorkers:     4,
			BackfillBatchBlocks: maximumRuntimeBackfillBatchBlocks,
			LeaseDuration:       30 * time.Second,
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
		Observability: ObservabilityConfig{
			Environment: "production", LogLevel: "info", LogFormat: "json",
			TraceSampleRatio:        0.1,
			TraceExportTimeout:      5 * time.Second,
			MetricsRefreshInterval:  15 * time.Second,
			SyncProgressLogInterval: 30 * time.Second,
		},
		Metadata: MetadataConfig{
			FetchTimeout:     10 * time.Second,
			MaxDocumentBytes: 2 << 20,
			MaxRedirects:     3,
		},
		ENS: ENSConfig{
			OfficialGateways:    []string{"https://ccip-v3.ens.xyz"},
			ResolutionFreshness: 15 * time.Minute,
			SnapshotTTL:         30 * time.Minute,
			FailureTTL:          30 * time.Second,
			RequestTimeout:      8 * time.Second,
			MaxResponseBytes:    1 << 20,
			MaxCCIPDepth:        4,
			MaxBatchAddresses:   100,
			MaxConcurrency:      8,
		},
		Adapters: AdapterConfig{
			Namespace: "etherview", ConnectTimeout: 2 * time.Second,
			OperationTimeout: 500 * time.Millisecond, RedisCacheTTL: 30 * time.Second,
			S3Prefix: "etherview", S3MaxObjectBytes: 16 << 20,
			FetchTimeout: 5 * time.Second, MaxResponseBytes: 1 << 20, MaxRedirects: 2,
			PriceFreshness: 5 * time.Minute, FailureTTL: 30 * time.Second,
		},
		Security: SecurityConfig{
			AnonymousRate:  5,
			AnonymousBurst: 20,
		},
		Verification: VerificationConfig{
			MaxInputBytes:           5 << 20,
			MaxOutputBytes:          64 << 20,
			WorkerCount:             1,
			DerivedWorkerCount:      1,
			DerivedMaxTracesPerScan: 100,
			Timeout:                 2 * time.Minute,
			CacheDirectory:          "/var/lib/etherview/compilers/cache",
			ExecutorPath:            defaultVerificationExecutorPath,
			GeasPath:                defaultVerificationGeasPath,
			CatalogURLs: map[string]string{
				"solidity": "auto",
			},
			AllowedDownloadOrigins: []string{
				"https://binaries.soliditylang.org",
			},
			CatalogRefreshInterval: time.Hour,
			CatalogMaxStaleness:    24 * time.Hour,
		},
		Sourcify: SourcifyConfig{
			BaseURL:          "https://sourcify.dev/server",
			Timeout:          20 * time.Second,
			MaxRequestBytes:  5 << 20,
			MaxResponseBytes: 32 << 20,
			Attempts:         3,
			PollInterval:     time.Second,
			MaxPolls:         120,
		},
		UserAuth: UserAuthConfig{
			ChallengeTTL:      5 * time.Minute,
			SessionTTL:        7 * 24 * time.Hour,
			LastUsedInterval:  5 * time.Minute,
			MaxMessageBytes:   4096,
			MaxSignatureBytes: 65,
			APIKeyRate:        20,
			APIKeyBurst:       40,
			MaxActiveAPIKeys:  5,
		},
		Billing: BillingConfig{
			FacilitatorTimeout:          10 * time.Second,
			FacilitatorMaxResponseBytes: 1 << 20,
			RequirementMaxTimeout:       60 * time.Second,
			ReservationTTL:              2 * time.Minute,
			TopupIntentTTL:              10 * time.Minute,
			UsageReservationTTL:         2 * time.Minute,
			MaxPaymentHeaderBytes:       16 << 10,
			MaxBufferedResponseBytes:    8 << 20,
			MaxCapturedHeaderBytes:      64 << 10,
			CoarseIPRate:                100,
			CoarseIPBurst:               200,
			Routes:                      map[string]BillingRouteConfig{},
			Operations:                  map[string]BillingOperationConfig{},
		},
	}
}

// Load reads an optional YAML file, overlays supported ETHERVIEW_ environment
// variables, and validates the resulting configuration.
func Load(path string) (Config, error) {
	return LoadWithOverrides(path, Overrides{})
}

// Overrides contains command-line values that must be applied after file and
// environment inputs but before validation.
type Overrides struct {
	Roles     []string
	LogLevel  *string
	LogFormat *string
}

// LoadWithOverrides loads configuration using explicit highest-precedence
// command-line values.
func LoadWithOverrides(path string, overrides Overrides) (Config, error) {
	var forcedRoles []string
	if overrides.Roles != nil {
		normalized, err := NormalizeRoles(overrides.Roles)
		if err != nil {
			return Config{}, err
		}
		forcedRoles = normalized
	}
	return load(path, forcedRoles, overrides)
}

// LoadForRoles loads configuration with an explicit final runtime-role
// selection. It exists for the serve --roles boundary so role-scoped Secret
// files are never opened according to a lower-precedence YAML or environment
// role value before the CLI override is applied.
func LoadForRoles(path string, roles []string) (Config, error) {
	return LoadWithOverrides(path, Overrides{Roles: roles})
}

func load(path string, forcedRoles []string, overrides Overrides) (Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("decode config: %w", err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			if err == nil {
				err = errors.New("multiple YAML documents are not allowed")
			}
			return Config{}, fmt.Errorf("decode config: %w", err)
		}
	}
	if err := applyEnvironmentForRoles(
		&cfg, os.LookupEnv, os.ReadFile, forcedRoles,
	); err != nil {
		return Config{}, err
	}
	if overrides.LogLevel != nil {
		cfg.Observability.LogLevel = *overrides.LogLevel
	}
	if overrides.LogFormat != nil {
		cfg.Observability.LogFormat = *overrides.LogFormat
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
