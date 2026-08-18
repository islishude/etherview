// Package config loads and validates Etherview's runtime configuration.
package config

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/apiops"
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

// BillingConfig describes the local x402 policy. FingerprintPepper and
// FacilitatorHeaders are Secret-only values and are never accepted from YAML.
type BillingConfig struct {
	FacilitatorURL              string                        `yaml:"facilitator_url"`
	FacilitatorAllowedCIDRs     []string                      `yaml:"facilitator_allowed_cidrs"`
	FacilitatorTimeout          time.Duration                 `yaml:"facilitator_timeout"`
	FacilitatorMaxResponseBytes int64                         `yaml:"facilitator_max_response_bytes"`
	Network                     string                        `yaml:"network"`
	Asset                       string                        `yaml:"asset"`
	AssetDecimals               uint8                         `yaml:"asset_decimals"`
	AssetEIP712Name             string                        `yaml:"asset_eip712_name"`
	AssetEIP712Version          string                        `yaml:"asset_eip712_version"`
	Recipient                   string                        `yaml:"recipient"`
	RequirementMaxTimeout       time.Duration                 `yaml:"requirement_max_timeout"`
	ReservationTTL              time.Duration                 `yaml:"reservation_ttl"`
	MaxPaymentHeaderBytes       int                           `yaml:"max_payment_header_bytes"`
	MaxBufferedResponseBytes    int64                         `yaml:"max_buffered_response_bytes"`
	MaxCapturedHeaderBytes      int                           `yaml:"max_captured_header_bytes"`
	CoarseIPRate                int                           `yaml:"coarse_ip_rate"`
	CoarseIPBurst               int                           `yaml:"coarse_ip_burst"`
	Routes                      map[string]BillingRouteConfig `yaml:"routes"`
	FingerprintPepper           string                        `yaml:"-"`
	FacilitatorHeaders          map[string]string             `yaml:"-"`
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
			MaxInputBytes:  5 << 20,
			MaxOutputBytes: 64 << 20,
			WorkerCount:    1,
			Timeout:        2 * time.Minute,
			CacheDirectory: "/var/lib/etherview/compilers/cache",
			ExecutorPath:   defaultVerificationExecutorPath,
			GeasPath:       defaultVerificationGeasPath,
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
			MaxPaymentHeaderBytes:       16 << 10,
			MaxBufferedResponseBytes:    8 << 20,
			MaxCapturedHeaderBytes:      64 << 10,
			CoarseIPRate:                100,
			CoarseIPBurst:               200,
			Routes:                      map[string]BillingRouteConfig{},
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
	if err := validateObservability(c.Observability); err != nil {
		errs = append(errs, err)
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
	if err := validateENSConfig(c.ENS, c.Features.ENS); err != nil {
		errs = append(errs, err)
	}
	if _, err := NormalizeRoles(c.Runtime.Roles); err != nil {
		errs = append(errs, err)
	}
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
	if c.Verification.MaxInputBytes <= 0 || c.Verification.MaxInputBytes > 64<<20 {
		errs = append(errs, errors.New("verification.max_input_bytes must be between 1 and 67108864"))
	}
	if c.Verification.MaxOutputBytes <= 0 || c.Verification.MaxOutputBytes > 256<<20 {
		errs = append(errs, errors.New("verification.max_output_bytes must be between 1 and 268435456"))
	}
	if c.Verification.WorkerCount <= 0 || c.Verification.WorkerCount > 64 {
		errs = append(errs, errors.New("verification.worker_count must be between 1 and 64"))
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
	if err := validateUserAuthConfig(c); err != nil {
		errs = append(errs, err)
	}
	if err := validateBillingConfig(c); err != nil {
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
	if c.Database.ReadURL != "" {
		if u, parseErr := url.Parse(c.Database.ReadURL); parseErr != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
			errs = append(errs, errors.New("database.read_url must be an absolute postgres URL"))
		}
	}
	needsRPC := false
	needsVerificationWorker := false
	needsVerificationReadAuth := false
	needsAPI := false
	metadataOnly := len(normalized) == 1 && normalized[0] == "metadata" && c.Features.NFTMetadata
	for _, role := range normalized {
		if role == "sync" || role == "enrich" || role == "trace" || role == "maintenance" {
			needsRPC = true
		}
		if role == "api" && c.Features.Verification {
			needsVerificationWorker = true
		}
		if role == "api" && c.Features.Verification {
			needsVerificationReadAuth = true
		}
		if role == "api" {
			needsAPI = true
		}
	}
	if needsRPC && len(c.RPC.Endpoints) == 0 {
		errs = append(errs, errors.New("at least one rpc endpoint is required for selected roles"))
	}
	if needsVerificationWorker {
		if strings.TrimSpace(c.Verification.CacheDirectory) == "" {
			errs = append(errs, errors.New("verification.cache_directory is required by the api verification worker"))
		} else if !filepath.IsAbs(c.Verification.CacheDirectory) ||
			filepath.Clean(c.Verification.CacheDirectory) != c.Verification.CacheDirectory {
			errs = append(errs, errors.New("verification.cache_directory must be an absolute clean path"))
		} else if !safeNodePermissionPath(c.Verification.CacheDirectory) {
			errs = append(errs, errors.New("verification.cache_directory contains characters unsafe for Node permissions"))
		}
		for _, runtimePath := range []struct {
			name  string
			value string
		}{
			{name: "executor_path", value: c.Verification.ExecutorPath},
			{name: "geas_path", value: c.Verification.GeasPath},
		} {
			if strings.TrimSpace(runtimePath.value) == "" {
				errs = append(errs, fmt.Errorf(
					"verification.%s is required by the api verification worker",
					runtimePath.name,
				))
			} else if !filepath.IsAbs(runtimePath.value) ||
				filepath.Clean(runtimePath.value) != runtimePath.value {
				errs = append(errs, fmt.Errorf(
					"verification.%s must be an absolute clean path",
					runtimePath.name,
				))
			}
		}
	}
	if c.Verification.UnsafeAllowPrivateDownloadNetworks && !needsVerificationWorker {
		errs = append(errs, errors.New(
			"verification.unsafe_allow_private_download_networks requires an api verification worker",
		))
	}
	if c.Metadata.UnsafeAllowPrivateNetworks && !metadataOnly {
		errs = append(errs, errors.New(
			"metadata.unsafe_allow_private_networks requires a metadata-only NFT metadata worker",
		))
	}
	if needsVerificationReadAuth && len(c.Security.APIKeyPepper) < 32 {
		errs = append(errs, errors.New("API role verification reads require API key authentication"))
	}
	if needsAPI && c.Features.UserAuth && len(c.UserAuth.SessionPepper) < 32 {
		errs = append(errs, errors.New("API role user authentication requires a session pepper of at least 32 bytes"))
	}
	if needsAPI && c.Features.X402Billing {
		if len(c.Billing.FingerprintPepper) < 32 {
			errs = append(errs, errors.New("API role x402 billing requires a fingerprint pepper of at least 32 bytes"))
		}
		if len(c.Billing.FacilitatorAllowedCIDRs) == 0 {
			errs = append(errs, errors.New("API role x402 billing requires facilitator_allowed_cidrs"))
		}
	}
	if needsAPI && c.Features.ENS {
		if len(c.ENS.OfficialRPCEndpoints) == 0 && (c.Chain.ID != 1 || len(c.RPC.Endpoints) == 0) {
			errs = append(errs, errors.New(
				"API role ENS requires ETHERVIEW_ENS_RPC_URLS or an explored Ethereum Mainnet RPC endpoint",
			))
		}
		if c.ENS.Custom.UniversalResolver != "" && len(c.RPC.Endpoints) == 0 {
			errs = append(errs, errors.New("API role custom ENS requires a current-chain RPC endpoint"))
		}
	}
	return errors.Join(errs...)
}

func validateENSConfig(cfg ENSConfig, enabled bool) error {
	var errs []error
	if cfg.ResolutionFreshness < time.Minute || cfg.ResolutionFreshness > 24*time.Hour {
		errs = append(errs, errors.New("ens.resolution_freshness must be between 1m and 24h"))
	}
	if cfg.SnapshotTTL < cfg.ResolutionFreshness || cfg.SnapshotTTL > 24*time.Hour {
		errs = append(errs, errors.New("ens.snapshot_ttl must be at least resolution_freshness and at most 24h"))
	}
	if cfg.FailureTTL < time.Second || cfg.FailureTTL > time.Hour {
		errs = append(errs, errors.New("ens.failure_ttl must be between 1s and 1h"))
	}
	if cfg.RequestTimeout < 100*time.Millisecond || cfg.RequestTimeout > 30*time.Second {
		errs = append(errs, errors.New("ens.request_timeout must be between 100ms and 30s"))
	}
	if cfg.MaxResponseBytes <= 0 || cfg.MaxResponseBytes > 8<<20 {
		errs = append(errs, errors.New("ens.max_response_bytes must be between 1 and 8388608"))
	}
	if cfg.MaxCCIPDepth <= 0 || cfg.MaxCCIPDepth > 8 {
		errs = append(errs, errors.New("ens.max_ccip_depth must be between 1 and 8"))
	}
	if cfg.MaxBatchAddresses <= 0 || cfg.MaxBatchAddresses > 100 {
		errs = append(errs, errors.New("ens.max_batch_addresses must be between 1 and 100"))
	}
	if cfg.MaxConcurrency <= 0 || cfg.MaxConcurrency > 16 {
		errs = append(errs, errors.New("ens.max_concurrency must be between 1 and 16"))
	}
	if enabled && len(cfg.OfficialGateways) == 0 {
		errs = append(errs, errors.New("ens.official_gateways is required when ENS is enabled"))
	}
	if len(cfg.OfficialGateways) > 4 || len(cfg.Custom.Gateways) > 4 {
		errs = append(errs, errors.New("ENS gateway lists must contain at most 4 URLs"))
	}
	for field, gateways := range map[string][]string{
		"ens.official_gateways": cfg.OfficialGateways,
		"ens.custom.gateways":   cfg.Custom.Gateways,
	} {
		seen := make(map[string]struct{}, len(gateways))
		for index, raw := range gateways {
			parsed, err := url.Parse(raw)
			if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil ||
				parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
				len(raw) > 2048 || strings.HasSuffix(parsed.Host, ":") ||
				(parsed.Port() != "" && parsed.Port() != "443") || unsafeURLPath(parsed.EscapedPath()) {
				errs = append(errs, fmt.Errorf("%s[%d] must be a bounded absolute HTTPS URL without credentials, query, fragment, or path traversal", field, index))
				continue
			}
			canonical := strings.ToLower(parsed.Scheme + "://" + parsed.Host + parsed.EscapedPath())
			if _, exists := seen[canonical]; exists {
				errs = append(errs, fmt.Errorf("%s contains a duplicate URL", field))
			}
			seen[canonical] = struct{}{}
		}
	}
	registrySet := cfg.Custom.Registry != ""
	resolverSet := cfg.Custom.UniversalResolver != ""
	if registrySet != resolverSet {
		errs = append(errs, errors.New("ens.custom.registry and ens.custom.universal_resolver must be configured together"))
	}
	for field, value := range map[string]string{
		"ens.custom.registry":           cfg.Custom.Registry,
		"ens.custom.universal_resolver": cfg.Custom.UniversalResolver,
	} {
		if value != "" && (!validFixedHex(value, 20) || strings.EqualFold(value, "0x"+strings.Repeat("0", 40))) {
			errs = append(errs, fmt.Errorf("%s must be a non-zero 20-byte address", field))
		}
	}
	if cfg.Custom.CoinType != "" {
		coinType, ok := new(big.Int).SetString(cfg.Custom.CoinType, 10)
		if !resolverSet || !ok || coinType.Sign() <= 0 || coinType.BitLen() > 256 || coinType.String() != cfg.Custom.CoinType {
			errs = append(errs, errors.New("ens.custom.coin_type must be a canonical positive uint256 and requires a custom resolver"))
		}
	}
	seenEndpoints := make(map[string]struct{}, len(cfg.OfficialRPCEndpoints))
	for index, endpoint := range cfg.OfficialRPCEndpoints {
		if err := endpoint.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("ENS RPC endpoint %d: %w", index, err))
		}
		if _, exists := seenEndpoints[endpoint.Name]; exists && endpoint.Name != "" {
			errs = append(errs, fmt.Errorf("ENS RPC endpoint name %q is duplicated", endpoint.Name))
		}
		seenEndpoints[endpoint.Name] = struct{}{}
	}
	return errors.Join(errs...)
}

func validateCompilerCatalogConfig(cfg VerificationConfig) error {
	if len(cfg.CatalogURLs) != 1 || cfg.CatalogURLs["solidity"] == "" {
		return errors.New("verification.catalog_urls must define only solidity")
	}
	allowed := make(map[string]struct{}, len(cfg.AllowedDownloadOrigins))
	for _, raw := range cfg.AllowedDownloadOrigins {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
			parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("verification.allowed_download_origins must contain absolute HTTPS origins")
		}
		allowed[strings.ToLower(parsed.Scheme+"://"+parsed.Host)] = struct{}{}
	}
	if len(allowed) == 0 {
		return errors.New("verification.allowed_download_origins is required")
	}
	for language, raw := range cfg.CatalogURLs {
		if language != "solidity" {
			return errors.New("verification.catalog_urls must define only solidity")
		}
		if raw == "auto" {
			continue
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
			parsed.Fragment != "" {
			return errors.New("verification.catalog_urls must contain absolute HTTPS URLs")
		}
		if _, ok := allowed[strings.ToLower(parsed.Scheme+"://"+parsed.Host)]; !ok {
			return errors.New("verification catalog URL origin is not allowlisted")
		}
	}
	return nil
}

func safeNodePermissionPath(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f || character == '*' || character == ',' {
			return false
		}
	}
	return true
}

func validatePublicOrigin(raw string) error {
	parsed, err := url.Parse(raw)
	if raw == "" || raw != strings.TrimSpace(raw) || err != nil || parsed == nil ||
		parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return errors.New("server.public_url must be an absolute HTTP(S) origin without credentials, query, or fragment")
	}
	host := parsed.Hostname()
	if strings.HasSuffix(host, ".") || strings.Contains(host, "%") ||
		strings.HasSuffix(parsed.Host, ":") {
		return errors.New("server.public_url host is invalid")
	}
	for index := range len(host) {
		if host[index] > 0x7f {
			return errors.New("server.public_url host must be ASCII")
		}
	}
	if port := parsed.Port(); port != "" {
		number, parseErr := strconv.ParseUint(port, 10, 16)
		if parseErr != nil || number == 0 {
			return errors.New("server.public_url port must be between 1 and 65535")
		}
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !isLoopbackHostname(host) {
			return errors.New("server.public_url may use HTTP only for a loopback development origin")
		}
	default:
		return errors.New("server.public_url must use HTTP or HTTPS")
	}
	return nil
}

func validateWalletURL(raw string, allowLocalHTTP bool) error {
	parsed, err := url.Parse(raw)
	if raw == "" || len(raw) > 2048 || raw != strings.TrimSpace(raw) || err != nil || parsed == nil ||
		(parsed.Scheme != "https" &&
			(!allowLocalHTTP || parsed.Scheme != "http" || parsed.Hostname() != "localhost")) ||
		parsed.Hostname() == "" || parsed.User != nil ||
		parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		strings.HasSuffix(parsed.Host, ":") {
		if allowLocalHTTP {
			return errors.New("must be an absolute HTTPS URL or HTTP localhost URL without credentials, query, or fragment")
		}
		return errors.New("must be an absolute HTTPS URL without credentials, query, or fragment")
	}
	host := parsed.Hostname()
	if strings.HasSuffix(host, ".") || strings.Contains(host, "%") {
		return errors.New("host is invalid")
	}
	for index := range len(host) {
		if host[index] > 0x7f {
			return errors.New("host must be ASCII")
		}
	}
	if port := parsed.Port(); port != "" {
		number, parseErr := strconv.ParseUint(port, 10, 16)
		if parseErr != nil || number == 0 {
			return errors.New("port must be between 1 and 65535")
		}
	}
	return nil
}

func isLoopbackHostname(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.IsLoopback()
}

func validateUserAuthConfig(cfg Config) error {
	var errs []error
	if cfg.UserAuth.ChallengeTTL < time.Minute || cfg.UserAuth.ChallengeTTL > 30*time.Minute {
		errs = append(errs, errors.New("user_auth.challenge_ttl must be between 1m and 30m"))
	}
	if cfg.UserAuth.SessionTTL < time.Hour || cfg.UserAuth.SessionTTL > 31*24*time.Hour {
		errs = append(errs, errors.New("user_auth.session_ttl must be between 1h and 744h"))
	}
	if cfg.UserAuth.LastUsedInterval < time.Minute || cfg.UserAuth.LastUsedInterval > time.Hour {
		errs = append(errs, errors.New("user_auth.last_used_interval must be between 1m and 1h"))
	}
	if cfg.UserAuth.MaxMessageBytes < 512 || cfg.UserAuth.MaxMessageBytes > 16<<10 {
		errs = append(errs, errors.New("user_auth.max_message_bytes must be between 512 and 16384"))
	}
	if cfg.UserAuth.MaxSignatureBytes != 65 {
		errs = append(errs, errors.New("user_auth.max_signature_bytes must be exactly 65"))
	}
	if cfg.UserAuth.APIKeyRate < 1 || cfg.UserAuth.APIKeyRate > 10_000 {
		errs = append(errs, errors.New("user_auth.api_key_rate must be between 1 and 10000"))
	}
	if cfg.UserAuth.APIKeyBurst < cfg.UserAuth.APIKeyRate || cfg.UserAuth.APIKeyBurst > 100_000 {
		errs = append(errs, errors.New("user_auth.api_key_burst must be at least api_key_rate and at most 100000"))
	}
	if cfg.UserAuth.MaxActiveAPIKeys < 1 || cfg.UserAuth.MaxActiveAPIKeys > 100 {
		errs = append(errs, errors.New("user_auth.max_active_api_keys must be between 1 and 100"))
	}
	if cfg.UserAuth.SessionPepper != "" && len(cfg.UserAuth.SessionPepper) < 32 {
		errs = append(errs, errors.New("user_auth session pepper must contain at least 32 bytes"))
	}
	if cfg.Features.UserAuth {
		if cfg.Server.PublicURL == "" {
			errs = append(errs, errors.New("features.user_auth requires server.public_url"))
		}
		if cfg.Chain.ID > uint64(math.MaxInt) {
			errs = append(errs, errors.New("features.user_auth requires chain.id to fit in int"))
		}
	}
	if cfg.Features.UserAPIKeys {
		if !cfg.Features.UserAuth {
			errs = append(errs, errors.New("features.user_api_keys requires features.user_auth"))
		}
		if len(cfg.Security.APIKeyPepper) < 32 {
			errs = append(errs, errors.New("features.user_api_keys requires API key authentication"))
		}
	}
	return errors.Join(errs...)
}

var (
	caip2EVMNetworkPattern = regexp.MustCompile(`^eip155:[1-9][0-9]*$`)
	decimalAtomicPattern   = regexp.MustCompile(`^[1-9][0-9]*$`)
	httpHeaderNamePattern  = regexp.MustCompile(`^[!#$%&'*+\-.^_` + "`" + `|~0-9A-Za-z]+$`)
	maximumBillingAtomic   = new(big.Int).Sub(
		new(big.Int).Lsh(big.NewInt(1), 256),
		big.NewInt(1),
	)
)

func validateBillingConfig(cfg Config) error {
	var errs []error
	billing := cfg.Billing
	if billing.FacilitatorTimeout < 100*time.Millisecond || billing.FacilitatorTimeout > time.Minute {
		errs = append(errs, errors.New("billing.facilitator_timeout must be between 100ms and 1m"))
	}
	if billing.FacilitatorMaxResponseBytes < 1 || billing.FacilitatorMaxResponseBytes > 1<<20 {
		errs = append(errs, errors.New("billing.facilitator_max_response_bytes must be between 1 and 1048576"))
	}
	if billing.RequirementMaxTimeout < time.Second || billing.RequirementMaxTimeout > 5*time.Minute ||
		billing.RequirementMaxTimeout%time.Second != 0 {
		errs = append(errs, errors.New("billing.requirement_max_timeout must be a whole number of seconds between 1s and 5m"))
	}
	if billing.ReservationTTL < 10*time.Second || billing.ReservationTTL > 10*time.Minute {
		errs = append(errs, errors.New("billing.reservation_ttl must be between 10s and 10m"))
	}
	if billing.MaxPaymentHeaderBytes < 1024 || billing.MaxPaymentHeaderBytes > 16<<10 {
		errs = append(errs, errors.New("billing.max_payment_header_bytes must be between 1024 and 16384"))
	}
	if billing.MaxBufferedResponseBytes < 1 || billing.MaxBufferedResponseBytes > 8<<20 {
		errs = append(errs, errors.New("billing.max_buffered_response_bytes must be between 1 and 8388608"))
	}
	if billing.MaxCapturedHeaderBytes < 1024 || billing.MaxCapturedHeaderBytes > 64<<10 {
		errs = append(errs, errors.New("billing.max_captured_header_bytes must be between 1024 and 65536"))
	}
	if billing.CoarseIPRate <= 0 || billing.CoarseIPBurst < billing.CoarseIPRate {
		errs = append(errs, errors.New("billing coarse IP rate must be positive and burst must be at least rate"))
	}
	if billing.FingerprintPepper != "" && len(billing.FingerprintPepper) < 32 {
		errs = append(errs, errors.New("billing fingerprint pepper must contain at least 32 bytes"))
	}
	for name, value := range billing.FacilitatorHeaders {
		canonical := http.CanonicalHeaderKey(name)
		if !httpHeaderNamePattern.MatchString(name) || canonical != name || value == "" ||
			strings.ContainsAny(value, "\r\n") || forbiddenFacilitatorHeader(canonical) {
			errs = append(errs, errors.New("billing facilitator headers contain an invalid or forbidden entry"))
			break
		}
	}
	for index, raw := range billing.FacilitatorAllowedCIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || prefix.Masked() != prefix || prefix.String() != raw {
			errs = append(errs, fmt.Errorf("billing.facilitator_allowed_cidrs[%d] must be a canonical masked CIDR prefix", index))
		}
	}
	if len(billing.Routes) > 0 && !cfg.Features.X402Billing {
		errs = append(errs, errors.New("billing.routes requires features.x402_billing"))
	}
	for operationID, route := range billing.Routes {
		operation, ok := apiops.Lookup(operationID)
		if !ok || !operation.BillingEligible {
			errs = append(errs, fmt.Errorf("billing route operation %q is not eligible", operationID))
		}
		if route.Access != "x402" && route.Access != "api_key_or_x402" {
			errs = append(errs, fmt.Errorf("billing route %q access must be x402 or api_key_or_x402", operationID))
		}
		if !decimalAtomicPattern.MatchString(route.AmountAtomic) {
			errs = append(errs, fmt.Errorf("billing route %q amount_atomic must be a canonical positive integer", operationID))
		} else if len(route.AmountAtomic) > 78 {
			errs = append(errs, fmt.Errorf("billing route %q amount_atomic must fit in uint256", operationID))
		} else if amount, ok := new(big.Int).SetString(route.AmountAtomic, 10); !ok ||
			amount.Cmp(maximumBillingAtomic) > 0 {
			errs = append(errs, fmt.Errorf("billing route %q amount_atomic must fit in uint256", operationID))
		}
	}
	if !cfg.Features.X402Billing {
		return errors.Join(errs...)
	}
	if cfg.Server.PublicURL == "" {
		errs = append(errs, errors.New("features.x402_billing requires server.public_url"))
	}
	if err := validateHTTPSOrigin("billing.facilitator_url", billing.FacilitatorURL); err != nil {
		errs = append(errs, err)
	}
	if !caip2EVMNetworkPattern.MatchString(billing.Network) {
		errs = append(errs, errors.New("billing.network must be a canonical eip155 CAIP-2 network"))
	}
	if !validFixedHex(billing.Asset, 20) {
		errs = append(errs, errors.New("billing.asset must be a 20-byte 0x-prefixed address"))
	}
	if !validFixedHex(billing.Recipient, 20) {
		errs = append(errs, errors.New("billing.recipient must be a 20-byte 0x-prefixed address"))
	}
	if value := billing.AssetEIP712Name; value == "" || value != strings.TrimSpace(value) || len(value) > 128 {
		errs = append(errs, errors.New("billing.asset_eip712_name must contain between 1 and 128 trimmed bytes"))
	}
	if value := billing.AssetEIP712Version; value == "" || value != strings.TrimSpace(value) || len(value) > 32 {
		errs = append(errs, errors.New("billing.asset_eip712_version must contain between 1 and 32 trimmed bytes"))
	}
	return errors.Join(errs...)
}

func validateHTTPSOrigin(name, raw string) error {
	parsed, err := url.Parse(raw)
	if raw == "" || raw != strings.TrimSpace(raw) || err != nil || parsed == nil ||
		parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery ||
		parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("%s must be an absolute HTTPS origin without credentials, path, query, or fragment", name)
	}
	return nil
}

func forbiddenFacilitatorHeader(name string) bool {
	switch name {
	case "Host", "Connection", "Content-Length", "Transfer-Encoding",
		"Proxy-Authorization", "Proxy-Authenticate", "Trailer", "Upgrade":
		return true
	default:
		return strings.HasPrefix(strings.ToLower(name), "payment-")
	}
}

func decodeNonzeroSHA256(value string) ([]byte, error) {
	if len(value) != 64 {
		return nil, errors.New("invalid SHA-256")
	}
	return hex.DecodeString(value)
}

func allZero(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}

func validateSourcifyConfig(cfg SourcifyConfig) error {
	var errs []error
	raw := cfg.BaseURL
	parsed, err := url.Parse(raw)
	if raw == "" || raw != strings.TrimSpace(raw) || err != nil || parsed.Scheme != "https" ||
		parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" ||
		parsed.ForceQuery || parsed.Fragment != "" || len(parsed.String()) > 4096 || unsafeURLPath(parsed.EscapedPath()) {
		errs = append(errs, errors.New("sourcify.base_url must be an absolute HTTPS URL without credentials, query, fragment, or path traversal"))
	}
	if cfg.Timeout < 100*time.Millisecond || cfg.Timeout > 2*time.Minute {
		errs = append(errs, errors.New("sourcify.timeout must be between 100ms and 2m"))
	}
	if cfg.MaxRequestBytes < 1 || cfg.MaxRequestBytes > 64<<20 {
		errs = append(errs, errors.New("sourcify.max_request_bytes must be between 1 and 67108864"))
	}
	if cfg.MaxResponseBytes < 1 || cfg.MaxResponseBytes > 64<<20 {
		errs = append(errs, errors.New("sourcify.max_response_bytes must be between 1 and 67108864"))
	}
	if cfg.Attempts < 1 || cfg.Attempts > 10 {
		errs = append(errs, errors.New("sourcify.attempts must be between 1 and 10"))
	}
	if cfg.PollInterval < 10*time.Millisecond || cfg.PollInterval > time.Minute {
		errs = append(errs, errors.New("sourcify.poll_interval must be between 10ms and 1m"))
	}
	if cfg.MaxPolls < 1 || cfg.MaxPolls > 1000 {
		errs = append(errs, errors.New("sourcify.max_polls must be between 1 and 1000"))
	}
	return errors.Join(errs...)
}

func validateObservability(cfg ObservabilityConfig) error {
	var errs []error
	if environment := strings.TrimSpace(cfg.Environment); environment == "" || environment != cfg.Environment || len(environment) > 64 {
		errs = append(errs, errors.New("observability.environment must contain between 1 and 64 trimmed bytes"))
	}
	if !slices.Contains([]string{"debug", "info", "warn", "error"}, cfg.LogLevel) {
		errs = append(errs, errors.New("observability.log_level must be debug, info, warn, or error"))
	}
	if cfg.LogFormat != "json" && cfg.LogFormat != "text" {
		errs = append(errs, errors.New("observability.log_format must be json or text"))
	}
	if math.IsNaN(cfg.TraceSampleRatio) || math.IsInf(cfg.TraceSampleRatio, 0) || cfg.TraceSampleRatio < 0 || cfg.TraceSampleRatio > 1 {
		errs = append(errs, errors.New("observability.trace_sample_ratio must be between 0 and 1"))
	}
	if cfg.TraceExportTimeout < 100*time.Millisecond || cfg.TraceExportTimeout > 30*time.Second {
		errs = append(errs, errors.New("observability.trace_export_timeout must be between 100ms and 30s"))
	}
	if cfg.MetricsRefreshInterval < time.Second || cfg.MetricsRefreshInterval > 5*time.Minute {
		errs = append(errs, errors.New("observability.metrics_refresh_interval must be between 1s and 5m"))
	}
	if cfg.SyncProgressLogInterval < time.Second || cfg.SyncProgressLogInterval > time.Hour {
		errs = append(errs, errors.New("observability.sync_progress_log_interval must be between 1s and 1h"))
	}
	if cfg.OTLPTraceEndpoint == "" {
		if cfg.OTLPTraceInsecure {
			errs = append(errs, errors.New("observability.otlp_trace_insecure requires otlp_trace_endpoint"))
		}
		return errors.Join(errs...)
	}
	parsed, err := url.Parse(cfg.OTLPTraceEndpoint)
	if err != nil || parsed == nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") || len(cfg.OTLPTraceEndpoint) > 4096 {
		errs = append(errs, errors.New("observability.otlp_trace_endpoint must be an absolute HTTP(S) origin without credentials, path, query, or fragment"))
		return errors.Join(errs...)
	}
	switch parsed.Scheme {
	case "https":
		if cfg.OTLPTraceInsecure {
			errs = append(errs, errors.New("observability.otlp_trace_insecure cannot be used with an HTTPS endpoint"))
		}
	case "http":
		if !cfg.OTLPTraceInsecure {
			errs = append(errs, errors.New("observability.otlp_trace_insecure must explicitly allow an HTTP endpoint"))
		}
	default:
		errs = append(errs, errors.New("observability.otlp_trace_endpoint must use HTTP or HTTPS"))
	}
	return errors.Join(errs...)
}

func unsafeURLPath(value string) bool {
	for {
		normalized := strings.ReplaceAll(value, `\`, "/")
		for segment := range strings.SplitSeq(normalized, "/") {
			if segment == ".." {
				return true
			}
		}
		decoded, err := url.PathUnescape(value)
		if err != nil || strings.ContainsRune(decoded, '\x00') {
			return true
		}
		if decoded == value {
			return false
		}
		value = decoded
	}
}

func (e RPCEndpoint) Validate() error {
	var errs []error
	trimmedName := strings.TrimSpace(e.Name)
	if trimmedName == "" || trimmedName != e.Name || len(e.Name) > 128 {
		errs = append(errs, errors.New("name must contain between 1 and 128 trimmed bytes"))
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
	if e.MaxRequests < 0 || e.MaxRequests > 1_000_000_000 {
		errs = append(errs, errors.New("max_requests_per_second must be between 0 and 1000000000"))
	}
	return errors.Join(errs...)
}

var allowedRoles = []string{"api", "sync", "enrich", "trace", "metadata", "maintenance"}

// NormalizeRoles validates roles, expands all, removes duplicates, and returns
// roles in stable architectural order.
func NormalizeRoles(input []string) ([]string, error) {
	if len(input) == 0 {
		return nil, errors.New("runtime.roles cannot be empty")
	}
	wanted := make(map[string]bool, len(input))
	for _, raw := range input {
		for role := range strings.SplitSeq(raw, ",") {
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
			if role == "verify" {
				return nil, errors.New(`runtime role "verify" was removed; use the api role with its solc-js executor`)
			}
			known := slices.Contains(allowedRoles, role)
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
	return applyEnvironmentForRoles(cfg, lookup, readFile, nil)
}

func applyEnvironmentForRoles(
	cfg *Config,
	lookup func(string) (string, bool),
	readFile func(string) ([]byte, error),
	forcedRoles []string,
) error {
	for _, removed := range []string{
		"COMPILER_SANDBOX",
		"VERIFICATION_RUNNER_ENDPOINT",
		"VERIFICATION_RUNNER_IMAGE",
		"VERIFICATION_VYPER_CATALOG_URL",
	} {
		if _, exists := lookup(envPrefix + removed); exists {
			return fmt.Errorf("%s%s is no longer supported", envPrefix, removed)
		}
	}
	if err := setBool(lookup, "FEATURE_USER_AUTH", &cfg.Features.UserAuth); err != nil {
		return err
	}
	if err := setBool(lookup, "FEATURE_USER_API_KEYS", &cfg.Features.UserAPIKeys); err != nil {
		return err
	}
	if err := setBool(lookup, "FEATURE_X402_BILLING", &cfg.Features.X402Billing); err != nil {
		return err
	}
	if forcedRoles != nil {
		cfg.Runtime.Roles = slices.Clone(forcedRoles)
	} else if value, ok := lookup(envPrefix + "ROLES"); ok {
		cfg.Runtime.Roles = strings.Split(value, ",")
	}
	roles, err := NormalizeRoles(cfg.Runtime.Roles)
	if err != nil {
		return err
	}
	apiRole := slices.Contains(roles, "api")

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

func parseSecretHeaders(value string) (map[string]string, error) {
	if len(value) > 16<<10 {
		return nil, errors.New("ETHERVIEW_X402_FACILITATOR_HEADERS exceeds 16384 bytes")
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("ETHERVIEW_X402_FACILITATOR_HEADERS must be a JSON object of string values")
	}
	headers := make(map[string]string)
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok {
			return nil, errors.New("ETHERVIEW_X402_FACILITATOR_HEADERS must be a JSON object of string values")
		}
		if _, exists := headers[key]; exists {
			return nil, errors.New("ETHERVIEW_X402_FACILITATOR_HEADERS contains a duplicate header")
		}
		var headerValue string
		if err := decoder.Decode(&headerValue); err != nil {
			return nil, errors.New("ETHERVIEW_X402_FACILITATOR_HEADERS must be a JSON object of string values")
		}
		headers[key] = headerValue
		if len(headers) > 32 {
			return nil, errors.New("ETHERVIEW_X402_FACILITATOR_HEADERS contains too many headers")
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return nil, errors.New("ETHERVIEW_X402_FACILITATOR_HEADERS must be a JSON object of string values")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("ETHERVIEW_X402_FACILITATOR_HEADERS contains trailing JSON")
	}
	return headers, nil
}

// parseEnvironmentRPCEndpoints keeps the original comma-separated shorthand
// while allowing the same Secret value to carry purpose and per-process rate
// policy. Parse failures never include the raw value because RPC URLs may
// contain credentials.
func parseEnvironmentRPCEndpoints(value string) ([]RPCEndpoint, error) {
	return parseEnvironmentRPCEndpointsNamed("ETHERVIEW_RPC_URLS", value, []string{"all"})
}

func parseEnvironmentRPCEndpointsNamed(name, value string, defaultPurposes []string) ([]RPCEndpoint, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "[") {
		decoder := json.NewDecoder(strings.NewReader(value))
		decoder.DisallowUnknownFields()
		var endpoints []RPCEndpoint
		if err := decoder.Decode(&endpoints); err != nil {
			return nil, fmt.Errorf("%s contains invalid endpoint JSON", name)
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s contains invalid endpoint JSON", name)
		}
		if len(endpoints) == 0 {
			return nil, fmt.Errorf("%s endpoint JSON must not be empty", name)
		}
		return endpoints, nil
	}
	var endpoints []RPCEndpoint
	for raw := range strings.SplitSeq(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw != "" {
			endpoints = append(endpoints, RPCEndpoint{
				Name: fmt.Sprintf("env-%d", len(endpoints)+1), URL: raw, Purposes: slices.Clone(defaultPurposes),
			})
		}
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("%s must contain at least one endpoint", name)
	}
	return endpoints, nil
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

func setExactString(lookup func(string) (string, bool), name string, target *string) {
	if value, ok := lookup(envPrefix + name); ok {
		*target = value
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

func setFloat64(lookup func(string) (string, bool), name string, target *float64) error {
	if value, ok := lookup(envPrefix + name); ok {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
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
	for item := range strings.SplitSeq(value, ",") {
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

func validCanonicalTrustedProxy(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	if address, err := netip.ParseAddr(value); err == nil {
		return address.Zone() == "" && !address.Is4In6() && address.String() == value
	}
	prefix, err := netip.ParsePrefix(value)
	return err == nil && prefix.Addr().Zone() == "" && !prefix.Addr().Is4In6() &&
		prefix == prefix.Masked() && prefix.String() == value
}

func validFixedHex(value string, byteLen int) bool {
	if len(value) != 2+byteLen*2 || !strings.HasPrefix(value, "0x") {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil
}
