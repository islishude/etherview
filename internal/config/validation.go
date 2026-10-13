package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/etherscanops"
)

// Validate checks structural and security-sensitive invariants without making
// network connections. Each subsystem validator is pure and returns every
// error in deterministic configuration order.
func (c Config) Validate() error {
	return errors.Join(
		validateServerConfig(c),
		validateChainWalletConfig(c),
		validateDatabaseRPCConfig(c),
		validateRuntimeConfig(c),
		validateMempoolMaintenanceConfig(c),
		validateObservability(c.Observability),
		validateMetadataConfig(c),
		validateENSConfig(c.ENS, c.Features.ENS),
		validateERC4337Config(c),
		validateConfiguredRoles(c.Runtime.Roles),
		validateFeatureSecurityConfig(c),
		validateVerificationConfig(c),
		validateUserAuthConfig(c),
		validateBillingConfig(c),
		validateAdapterConfig(c),
	)
}

func validateERC4337Config(cfg Config) error {
	const maximumEntryPoints = 16
	var errs []error
	entries := cfg.ERC4337.EntryPoints
	if cfg.Features.UserOperations && len(entries) == 0 {
		errs = append(errs, errors.New("features.user_operations requires at least one erc4337.entry_points entry"))
	}
	if !cfg.Features.UserOperations && len(entries) > 0 {
		errs = append(errs, errors.New("erc4337.entry_points requires features.user_operations"))
	}
	if len(entries) > maximumEntryPoints {
		errs = append(errs, fmt.Errorf("erc4337.entry_points must contain at most %d entries", maximumEntryPoints))
	}
	for index, entry := range entries {
		if !validFixedHex(entry.Address, 20) || strings.EqualFold(entry.Address, "0x"+strings.Repeat("0", 40)) {
			errs = append(errs, fmt.Errorf("erc4337.entry_points[%d].address must be a non-zero 20-byte 0x-prefixed address", index))
		}
		switch entry.Version {
		case "0.6", "0.7", "0.8", "0.9":
		default:
			errs = append(errs, fmt.Errorf("erc4337.entry_points[%d].version must be one of 0.6, 0.7, 0.8, or 0.9", index))
		}
		if entry.ToBlock != nil && *entry.ToBlock < entry.FromBlock {
			errs = append(errs, fmt.Errorf("erc4337.entry_points[%d].to_block must be at least from_block", index))
		}
	}
	for left := range entries {
		for right := left + 1; right < len(entries); right++ {
			if !strings.EqualFold(entries[left].Address, entries[right].Address) {
				continue
			}
			leftEnd, rightEnd := uint64(math.MaxUint64), uint64(math.MaxUint64)
			if entries[left].ToBlock != nil {
				leftEnd = *entries[left].ToBlock
			}
			if entries[right].ToBlock != nil {
				rightEnd = *entries[right].ToBlock
			}
			if entries[left].FromBlock <= rightEnd && entries[right].FromBlock <= leftEnd {
				errs = append(errs, fmt.Errorf(
					"erc4337.entry_points[%d] and erc4337.entry_points[%d] overlap for the same address",
					left, right,
				))
			}
		}
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
	if needsAPI && c.Features.UserAPIKeys && len(c.Security.APIKeyPepper) < 32 {
		errs = append(errs, errors.New("API role user API keys require API key authentication"))
	}
	if needsAPI && c.Features.UserAuth && len(c.UserAuth.SessionPepper) < 32 {
		errs = append(errs, errors.New("API role user authentication requires a session pepper of at least 32 bytes"))
	}
	if needsAPI && c.Features.X402Topups {
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
	}
	if cfg.Features.APIBilling && (!cfg.Features.UserAuth || !cfg.Features.UserAPIKeys) {
		errs = append(errs, errors.New("features.api_billing requires user_auth and user_api_keys"))
	}
	if cfg.Features.X402Topups && !cfg.Features.APIBilling {
		errs = append(errs, errors.New("features.x402_topups requires features.api_billing"))
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
	if billing.TopupIntentTTL < time.Minute || billing.TopupIntentTTL > time.Hour {
		errs = append(errs, errors.New("billing.topup_intent_ttl must be between 1m and 1h"))
	}
	if billing.UsageReservationTTL < 10*time.Second || billing.UsageReservationTTL > 10*time.Minute {
		errs = append(errs, errors.New("billing.usage_reservation_ttl must be between 10s and 10m"))
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
	if len(billing.Operations) > 0 && !cfg.Features.APIBilling {
		errs = append(errs, errors.New("billing.operations requires features.api_billing"))
	}
	for operationID, operationConfig := range billing.Operations {
		operation, ok := etherscanops.Lookup(operationID)
		if !ok || !operation.BillingEligible {
			errs = append(errs, fmt.Errorf("billing operation %q is not eligible", operationID))
		}
		if !validBillingAtomic(operationConfig.AmountAtomic) {
			errs = append(errs, fmt.Errorf("billing operation %q amount_atomic must fit in uint256", operationID))
		}
	}
	if !cfg.Features.APIBilling {
		if cfg.Features.X402Topups {
			errs = append(errs, errors.New("features.x402_topups requires features.api_billing"))
		}
		if billing.FacilitatorUnsafeAllowHTTP {
			errs = append(errs, errors.New("billing.facilitator_unsafe_allow_http requires features.x402_topups"))
		}
		return errors.Join(errs...)
	}
	if !caip2EVMNetworkPattern.MatchString(billing.Network) {
		errs = append(errs, errors.New("billing.network must be a canonical eip155 CAIP-2 network"))
	}
	if !validFixedHex(billing.Asset, 20) {
		errs = append(errs, errors.New("billing.asset must be a 20-byte 0x-prefixed address"))
	}
	if !cfg.Features.X402Topups {
		if len(billing.AssetTransferMethods) != 0 || billing.MinimumTopupAmountAtomic != "" ||
			billing.MaximumTopupAmountAtomic != "" {
			errs = append(errs, errors.New("billing top-up policy requires features.x402_topups"))
		}
		return errors.Join(errs...)
	}
	if cfg.Server.PublicURL == "" {
		errs = append(errs, errors.New("features.x402_topups requires server.public_url"))
	}
	if err := validateFacilitatorOrigin(billing.FacilitatorURL, billing.FacilitatorUnsafeAllowHTTP); err != nil {
		errs = append(errs, err)
	}
	if !validFixedHex(billing.Recipient, 20) {
		errs = append(errs, errors.New("billing.recipient must be a 20-byte 0x-prefixed address"))
	}
	methods := make(map[string]bool, len(billing.AssetTransferMethods))
	for _, method := range billing.AssetTransferMethods {
		if method != "eip3009" && method != "permit2" || methods[method] {
			errs = append(errs, errors.New("billing.asset_transfer_methods must contain unique eip3009 or permit2 values"))
			break
		}
		methods[method] = true
	}
	if len(methods) == 0 {
		errs = append(errs, errors.New("billing.asset_transfer_methods is required for x402 top-ups"))
	}
	if methods["eip3009"] {
		if value := billing.AssetEIP712Name; value == "" || value != strings.TrimSpace(value) || len(value) > 128 {
			errs = append(errs, errors.New("billing.asset_eip712_name must contain between 1 and 128 trimmed bytes for eip3009"))
		}
		if value := billing.AssetEIP712Version; value == "" || value != strings.TrimSpace(value) || len(value) > 32 {
			errs = append(errs, errors.New("billing.asset_eip712_version must contain between 1 and 32 trimmed bytes for eip3009"))
		}
	}
	minimum, minimumOK := billingAtomic(billing.MinimumTopupAmountAtomic)
	maximum, maximumOK := billingAtomic(billing.MaximumTopupAmountAtomic)
	if !minimumOK || !maximumOK || minimum.Cmp(maximum) > 0 {
		errs = append(errs, errors.New("billing top-up amount bounds must be canonical positive uint256 values with minimum <= maximum"))
	}
	return errors.Join(errs...)
}

func validateFacilitatorOrigin(raw string, unsafeAllowHTTP bool) error {
	if !unsafeAllowHTTP {
		return validateHTTPSOrigin("billing.facilitator_url", raw)
	}
	parsed, err := url.Parse(raw)
	if raw == "" || raw != strings.TrimSpace(raw) || err != nil || parsed == nil ||
		parsed.Scheme != "http" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery ||
		parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("billing.facilitator_url must be a root HTTP origin when facilitator_unsafe_allow_http is enabled")
	}
	return nil
}

func validBillingAtomic(value string) bool {
	_, ok := billingAtomic(value)
	return ok
}

func billingAtomic(value string) (*big.Int, bool) {
	if !decimalAtomicPattern.MatchString(value) || len(value) > 78 {
		return nil, false
	}
	amount, ok := new(big.Int).SetString(value, 10)
	return amount, ok && amount.Cmp(maximumBillingAtomic) <= 0 && amount.String() == value
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
