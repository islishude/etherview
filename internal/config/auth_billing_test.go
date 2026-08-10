package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/apiops"
)

func TestAuthAndBillingDefaultsAreDisabledAndBounded(t *testing.T) {
	t.Parallel()
	cfg := Default()
	if cfg.Features.UserAuth || cfg.Features.UserAPIKeys || cfg.Features.X402Billing {
		t.Fatalf("auth and billing must default off: %#v", cfg.Features)
	}
	if cfg.UserAuth.ChallengeTTL.String() != "5m0s" ||
		cfg.UserAuth.SessionTTL.String() != "168h0m0s" ||
		cfg.Billing.FacilitatorTimeout.String() != "10s" ||
		cfg.Billing.RequirementMaxTimeout.String() != "1m0s" ||
		cfg.Billing.ReservationTTL.String() != "2m0s" ||
		cfg.UserAuth.APIKeyRate != 20 || cfg.UserAuth.APIKeyBurst != 40 ||
		cfg.UserAuth.MaxActiveAPIKeys != 5 || len(cfg.Billing.Routes) != 0 {
		t.Fatalf("unexpected auth or billing defaults: auth=%#v billing=%#v", cfg.UserAuth, cfg.Billing)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestUserAPIKeysRequireAuthAndAPIKeyPepper(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Features.UserAPIKeys = true
	if err := cfg.Validate(); err == nil ||
		!strings.Contains(err.Error(), "requires features.user_auth") ||
		!strings.Contains(err.Error(), "requires API key authentication") {
		t.Fatalf("missing dependencies error = %v", err)
	}
	cfg.Features.UserAuth = true
	cfg.Server.PublicURL = "https://explorer.example"
	cfg.Security.APIKeyPepper = strings.Repeat("k", 32)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestUserAPIKeyPolicyEnvironmentOverrides(t *testing.T) {
	t.Parallel()
	cfg := Default()
	overrides := map[string]string{
		"ETHERVIEW_FEATURE_USER_API_KEYS":         "true",
		"ETHERVIEW_USER_AUTH_API_KEY_RATE":        "25",
		"ETHERVIEW_USER_AUTH_API_KEY_BURST":       "50",
		"ETHERVIEW_USER_AUTH_MAX_ACTIVE_API_KEYS": "7",
	}
	if err := applyEnvironment(&cfg, func(key string) (string, bool) {
		value, ok := overrides[key]
		return value, ok
	}, nil); err != nil {
		t.Fatal(err)
	}
	if !cfg.Features.UserAPIKeys || cfg.UserAuth.APIKeyRate != 25 ||
		cfg.UserAuth.APIKeyBurst != 50 || cfg.UserAuth.MaxActiveAPIKeys != 7 {
		t.Fatalf("user API key environment overrides = features:%#v auth:%#v", cfg.Features, cfg.UserAuth)
	}
}

func TestFeatureOffDoesNotReadAuthOrBillingSecretFiles(t *testing.T) {
	for _, name := range []string{
		"ETHERVIEW_FEATURE_USER_AUTH",
		"ETHERVIEW_FEATURE_X402_BILLING",
		"ETHERVIEW_SESSION_PEPPER",
		"ETHERVIEW_X402_FINGERPRINT_PEPPER",
		"ETHERVIEW_X402_FACILITATOR_HEADERS",
	} {
		unsetHostEnvironment(t, name)
	}
	t.Setenv("ETHERVIEW_SESSION_PEPPER_FILE", "/does/not/exist/session")
	t.Setenv("ETHERVIEW_X402_FINGERPRINT_PEPPER_FILE", "/does/not/exist/fingerprint")
	t.Setenv("ETHERVIEW_X402_FACILITATOR_HEADERS_FILE", "/does/not/exist/headers")
	if _, err := Load(""); err != nil {
		t.Fatalf("disabled features read a Secret file: %v", err)
	}
}

func TestSessionPepperFileIsReadOnlyForTheFinalAPIRole(t *testing.T) {
	unsetHostEnvironment(t, "ETHERVIEW_SESSION_PEPPER")
	t.Setenv("ETHERVIEW_FEATURE_USER_AUTH", "true")
	t.Setenv("ETHERVIEW_SERVER_PUBLIC_URL", "https://explorer.example")
	t.Setenv("ETHERVIEW_SESSION_PEPPER_FILE", "/does/not/exist/session")

	t.Setenv("ETHERVIEW_ROLES", "sync")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("sync role read the session Secret: %v", err)
	}
	if strings.Join(cfg.Runtime.Roles, ",") != "sync" {
		t.Fatalf("runtime roles = %v", cfg.Runtime.Roles)
	}

	t.Setenv("ETHERVIEW_ROLES", "all")
	if _, err := Load(""); err == nil ||
		!strings.Contains(err.Error(), "SESSION_PEPPER_FILE") {
		t.Fatalf("all role did not read the session Secret: %v", err)
	}

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
runtime:
  roles: [all]
features:
  user_auth: true
server:
  public_url: https://explorer.example
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadForRoles(path, []string{"sync"})
	if err != nil {
		t.Fatalf("CLI sync override read the YAML/env API Secret: %v", err)
	}
	if strings.Join(cfg.Runtime.Roles, ",") != "sync" {
		t.Fatalf("forced runtime roles = %v", cfg.Runtime.Roles)
	}
	cfg, err = LoadForRoles(path, []string{"maintenance"})
	if err != nil {
		t.Fatalf("maintenance override read the session Secret: %v", err)
	}
	if strings.Join(cfg.Runtime.Roles, ",") != "maintenance" {
		t.Fatalf("forced runtime roles = %v", cfg.Runtime.Roles)
	}
	if _, err := LoadForRoles(path, []string{"api"}); err == nil ||
		!strings.Contains(err.Error(), "SESSION_PEPPER_FILE") {
		t.Fatalf("CLI API override did not read the session Secret: %v", err)
	}
}

func TestBillingSecretFilesAreReadOnlyForTheFinalAPIRole(t *testing.T) {
	unsetHostEnvironment(t, "ETHERVIEW_X402_FINGERPRINT_PEPPER")
	unsetHostEnvironment(t, "ETHERVIEW_X402_FACILITATOR_HEADERS")
	t.Setenv(
		"ETHERVIEW_X402_FINGERPRINT_PEPPER_FILE",
		"/does/not/exist/fingerprint",
	)
	t.Setenv(
		"ETHERVIEW_X402_FACILITATOR_HEADERS_FILE",
		"/does/not/exist/headers",
	)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
runtime:
  roles: [all]
features:
  x402_billing: true
server:
  public_url: https://explorer.example
billing:
  facilitator_url: https://facilitator.example
  facilitator_allowed_cidrs: [203.0.113.0/24]
  network: eip155:84532
  asset: "0x1111111111111111111111111111111111111111"
  asset_decimals: 6
  asset_eip712_name: USDC
  asset_eip712_version: "2"
  recipient: "0x2222222222222222222222222222222222222222"
  routes: {}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadForRoles(path, []string{"sync"})
	if err != nil {
		t.Fatalf("sync override read billing Secrets: %v", err)
	}
	if strings.Join(cfg.Runtime.Roles, ",") != "sync" {
		t.Fatalf("forced runtime roles = %v", cfg.Runtime.Roles)
	}
	cfg, err = LoadForRoles(path, []string{"maintenance"})
	if err != nil {
		t.Fatalf("maintenance override read billing Secrets: %v", err)
	}
	if strings.Join(cfg.Runtime.Roles, ",") != "maintenance" {
		t.Fatalf("forced runtime roles = %v", cfg.Runtime.Roles)
	}
	if _, err := LoadForRoles(path, []string{"api"}); err == nil ||
		!strings.Contains(err.Error(), "FINGERPRINT_PEPPER_FILE") {
		t.Fatalf("API override did not read fingerprint Secret: %v", err)
	}

	fingerprintPath := filepath.Join(t.TempDir(), "fingerprint")
	if err := os.WriteFile(
		fingerprintPath,
		[]byte(strings.Repeat("f", 32)),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ETHERVIEW_X402_FINGERPRINT_PEPPER_FILE", fingerprintPath)
	if _, err := LoadForRoles(path, []string{"api"}); err == nil ||
		!strings.Contains(err.Error(), "FACILITATOR_HEADERS_FILE") {
		t.Fatalf("API override did not read facilitator header Secret: %v", err)
	}

	headersPath := filepath.Join(t.TempDir(), "headers.json")
	if err := os.WriteFile(headersPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ETHERVIEW_X402_FACILITATOR_HEADERS_FILE", headersPath)
	cfg, err = LoadForRoles(path, []string{"api"})
	if err != nil {
		t.Fatalf("API override rejected valid billing Secrets: %v", err)
	}
	if err := cfg.ValidateForRoles([]string{"api"}); err == nil ||
		!strings.Contains(err.Error(), "database.url") {
		t.Fatalf("API runnable validation did not continue after Secret loading: %v", err)
	}
}

func TestAuthAndBillingSecretsAreRejectedFromYAML(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		input string
		field string
	}{
		{
			name: "session pepper",
			input: `user_auth:
  session_pepper: SHOULD-NOT-ENTER-ERRORS
`,
			field: "session_pepper",
		},
		{
			name: "fingerprint pepper",
			input: `billing:
  fingerprint_pepper: SHOULD-NOT-ENTER-ERRORS
`,
			field: "fingerprint_pepper",
		},
		{
			name: "facilitator headers",
			input: `billing:
  facilitator_headers:
    Authorization: SHOULD-NOT-ENTER-ERRORS
`,
			field: "facilitator_headers",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(test.input), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("secret YAML field was not rejected: %v", err)
			}
			if strings.Contains(err.Error(), "SHOULD-NOT-ENTER-ERRORS") {
				t.Fatalf("secret YAML value entered the error: %v", err)
			}
		})
	}
}

func TestUserAuthRequiresCanonicalOriginAndAPIRoleSecret(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Features.UserAuth = true
	cfg.Server.PublicURL = "https://explorer.example"
	cfg.Database.URL = "postgres://database.example/etherview"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateForRoles([]string{"api"}); err == nil ||
		!strings.Contains(err.Error(), "session pepper") {
		t.Fatalf("missing API session pepper error = %v", err)
	}
	if err := cfg.ValidateForRoles([]string{"metadata"}); err != nil {
		t.Fatalf("non-API role requires session Secret: %v", err)
	}
	cfg.UserAuth.SessionPepper = strings.Repeat("s", 32)
	if err := cfg.ValidateForRoles([]string{"api"}); err != nil {
		t.Fatal(err)
	}

	for _, publicURL := range []string{
		"http://explorer.example",
		"https://user:secret@explorer.example",
		"https://explorer.example/path",
		"https://explorer.example?secret=yes",
		"https://example.com.",
		"https://example.com:",
		"https://example.com:0",
		"https://example.com:65536",
		"https://:",
	} {
		cfg.Server.PublicURL = publicURL
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "server.public_url") {
			t.Fatalf("invalid public URL %q passed: %v", publicURL, err)
		}
	}
	cfg.Server.PublicURL = "http://127.0.0.1:8080"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("loopback development origin rejected: %v", err)
	}
	cfg.Server.PublicURL = "https://explorer.example:65535"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("highest valid public origin port rejected: %v", err)
	}
}

func TestX402ConfigurationUsesClosedEligibleCatalog(t *testing.T) {
	t.Parallel()
	cfg := validX402Config()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateForRoles([]string{"api"}); err != nil {
		t.Fatal(err)
	}
	cfg.Billing.Routes["getStatus"] = BillingRouteConfig{
		Access: "x402", AmountAtomic: "1",
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "not eligible") {
		t.Fatalf("ineligible billing route passed: %v", err)
	}
	delete(cfg.Billing.Routes, "getStatus")
	cfg.Billing.Routes["getVerifiedContract"] = BillingRouteConfig{
		Access: "x402", AmountAtomic: "1",
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "not eligible") {
		t.Fatalf("free verified-artifact read remained billable: %v", err)
	}
	delete(cfg.Billing.Routes, "getVerifiedContract")
	cfg.Billing.Routes["listBlocks"] = BillingRouteConfig{
		Access: "api_key_or_x402", AmountAtomic: "01",
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "canonical positive integer") {
		t.Fatalf("non-canonical amount passed: %v", err)
	}
	cfg.Billing.Routes["listBlocks"] = BillingRouteConfig{
		Access:       "x402",
		AmountAtomic: "115792089237316195423570985008687907853269984665640564039457584007913129639935",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("maximum uint256 amount rejected: %v", err)
	}
	cfg.Billing.Routes["listBlocks"] = BillingRouteConfig{
		Access:       "x402",
		AmountAtomic: "115792089237316195423570985008687907853269984665640564039457584007913129639936",
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "fit in uint256") {
		t.Fatalf("uint256 overflow amount passed: %v", err)
	}
}

func TestX402FacilitatorHeadersAreStrictAndRedacted(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "valid", value: `{"Authorization":"Bearer opaque"}`},
		{name: "duplicate", value: `{"Authorization":"one","Authorization":"two"}`, want: "duplicate"},
		{name: "non string", value: `{"Authorization":123}`, want: "JSON object"},
		{name: "trailing", value: `{"Authorization":"one"} {}`, want: "trailing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			headers, err := parseSecretHeaders(test.value)
			if test.want == "" {
				if err != nil || headers["Authorization"] != "Bearer opaque" {
					t.Fatalf("headers=%v error=%v", headers, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) ||
				strings.Contains(err.Error(), "one") || strings.Contains(err.Error(), "two") {
				t.Fatalf("error=%v", err)
			}
		})
	}

	cfg := validX402Config()
	cfg.Billing.FacilitatorHeaders = map[string]string{"Payment-Signature": "opaque"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("protocol header Secret passed: %v", err)
	}
}

func validX402Config() Config {
	cfg := Default()
	cfg.Features.X402Billing = true
	cfg.Server.PublicURL = "https://explorer.example"
	cfg.Database.URL = "postgres://database.example/etherview"
	cfg.Billing.FacilitatorURL = "https://facilitator.example"
	cfg.Billing.FacilitatorAllowedCIDRs = []string{"203.0.113.0/24"}
	cfg.Billing.Network = "eip155:84532"
	cfg.Billing.Asset = "0x1111111111111111111111111111111111111111"
	cfg.Billing.AssetDecimals = 6
	cfg.Billing.AssetEIP712Name = "USDC"
	cfg.Billing.AssetEIP712Version = "2"
	cfg.Billing.Recipient = "0x2222222222222222222222222222222222222222"
	cfg.Billing.FingerprintPepper = strings.Repeat("f", 32)
	cfg.Billing.FacilitatorHeaders = map[string]string{"Authorization": "Bearer opaque"}
	cfg.Billing.Routes = map[string]BillingRouteConfig{
		"listBlocks": {Access: "x402", AmountAtomic: "1000"},
	}
	return cfg
}

func TestHelmBillingRouteEnumMatchesEligibleCatalog(t *testing.T) {
	t.Parallel()
	encoded, err := os.ReadFile("../../deploy/helm/etherview/values.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	value := any(document)
	for _, key := range []string{
		"properties", "config", "properties", "billing", "properties", "routes", "propertyNames", "enum",
	} {
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("Helm billing route schema before %q is not an object", key)
		}
		value, ok = object[key]
		if !ok {
			t.Fatalf("Helm billing route schema lacks %q", key)
		}
	}
	rawOperations, ok := value.([]any)
	if !ok {
		t.Fatal("Helm billing route enum is not an array")
	}
	actual := make([]string, len(rawOperations))
	for index, operation := range rawOperations {
		actual[index], ok = operation.(string)
		if !ok {
			t.Fatalf("Helm billing route operation %d is not a string", index)
		}
	}
	expected := apiops.EligibleIDs()
	slices.Sort(actual)
	slices.Sort(expected)
	if !slices.Equal(actual, expected) {
		t.Fatalf("Helm billing routes=%v, want eligible catalog=%v", actual, expected)
	}
}
