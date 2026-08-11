package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/config"
)

type fakeBackend struct {
	served        bool
	servedConfig  config.Config
	roles         []string
	doctorCalled  bool
	doctorConfig  config.Config
	doctorRoles   []string
	doctorErr     error
	doctorContext context.Context
	migrate       string
	migrateConfig config.Config
	repairKind    string
	repairConfig  config.Config
	repairArgs    []string
	adminResource string
	adminAction   string
	adminArgs     []string
	adminConfig   config.Config
}

func (f *fakeBackend) Serve(_ context.Context, cfg config.Config, roles []string) error {
	f.served = true
	f.servedConfig = cfg
	f.roles = roles
	return nil
}
func (f *fakeBackend) Doctor(ctx context.Context, cfg config.Config, roles []string) error {
	f.doctorCalled = true
	f.doctorConfig = cfg
	f.doctorRoles = append([]string(nil), roles...)
	f.doctorContext = ctx
	return f.doctorErr
}
func (f *fakeBackend) Migrate(_ context.Context, cfg config.Config, action string) error {
	f.migrate = action
	f.migrateConfig = cfg
	return nil
}
func (f *fakeBackend) Repair(_ context.Context, cfg config.Config, kind string, args []string) error {
	f.repairKind = kind
	f.repairConfig = cfg
	f.repairArgs = append([]string(nil), args...)
	return nil
}
func (f *fakeBackend) Admin(_ context.Context, cfg config.Config, resource, action string, args []string) error {
	f.adminResource = resource
	f.adminAction = action
	f.adminArgs = append([]string(nil), args...)
	f.adminConfig = cfg
	return nil
}

func TestVersionAndUnknownCommand(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	p := Program{Version: "v1.2.3", Stdout: &stdout, Stderr: &stderr}
	if code := p.Run(context.Background(), []string{"version"}); code != 0 || stdout.String() != "v1.2.3\n" {
		t.Fatalf("code=%d stdout=%q", code, stdout.String())
	}
	if code := p.Run(context.Background(), []string{"wat"}); code != 1 {
		t.Fatalf("backend validation should fail before unknown command, got %d", code)
	}
}

func TestServeNormalizesRoles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
database:
  url: postgres://localhost/etherview
rpc:
  endpoints:
    - name: primary
      url: http://localhost:8545
      purposes: [all]
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{}
	var stderr bytes.Buffer
	p := Program{Backend: backend, Stdout: &bytes.Buffer{}, Stderr: &stderr}
	code := p.Run(context.Background(), []string{"serve", "--config", path, "--roles", "trace,api,api"})
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !backend.served || strings.Join(backend.roles, ",") != "api,trace" {
		t.Fatalf("served=%v roles=%v", backend.served, backend.roles)
	}
}

func TestLoggingConfigurationPrecedenceAndCommandCoverage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
database:
  url: postgres://localhost/etherview
rpc:
  endpoints:
    - name: primary
      url: http://localhost:8545
      purposes: [all]
observability:
  log_level: warn
  log_format: json
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ETHERVIEW_LOG_LEVEL", "error")
	t.Setenv("ETHERVIEW_LOG_FORMAT", "text")

	tests := []struct {
		name string
		args []string
	}{
		{name: "serve", args: []string{"serve", "--config", path, "--log-level", "debug", "--log-format=json"}},
		{name: "doctor", args: []string{"doctor", "--config", path, "--log-level=debug", "--log-format", "json"}},
		{name: "migrate", args: []string{"migrate", "status", "--log-level", "debug", "--config", path, "--log-format", "json"}},
		{name: "repair", args: []string{"repair", "--from", "1", "--log-format=json", "--config", path, "--log-level=debug"}},
		{name: "reindex", args: []string{"reindex", "--stage", "trace", "--config", path, "--log-level", "debug", "--log-format", "json"}},
		{name: "admin", args: []string{"admin", "api-key", "list", "--log-level=debug", "--config", path, "--log-format=json"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &fakeBackend{}
			var configured []config.ObservabilityConfig
			var stderr bytes.Buffer
			program := Program{
				Backend: backend, Stdout: io.Discard, Stderr: &stderr,
				ConfigureLogging: func(cfg config.ObservabilityConfig) error {
					configured = append(configured, cfg)
					return nil
				},
			}
			if code := program.Run(context.Background(), test.args); code != 0 {
				t.Fatalf("code=%d stderr=%s", code, stderr.String())
			}
			if len(configured) != 1 || configured[0].LogLevel != "debug" ||
				configured[0].LogFormat != "json" {
				t.Fatalf("configured logging = %#v", configured)
			}
		})
	}
}

func TestLoggingCLIRejectsInvalidAndDuplicateValues(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "empty level", args: []string{"serve", "--log-level="}, want: "--log-level must be"},
		{name: "uppercase level", args: []string{"serve", "--log-level", "INFO"}, want: "--log-level must be"},
		{name: "numeric level", args: []string{"serve", "--log-level", "DEBUG-4"}, want: "--log-level must be"},
		{name: "spaced level", args: []string{"serve", "--log-level", " info"}, want: "--log-level must be"},
		{name: "empty format", args: []string{"doctor", "--log-format="}, want: "--log-format must be"},
		{name: "uppercase format", args: []string{"doctor", "--log-format", "JSON"}, want: "--log-format must be"},
		{name: "duplicate level", args: []string{"migrate", "status", "--log-level", "info", "--log-level=debug"}, want: "--log-level may only"},
		{name: "duplicate format", args: []string{"admin", "api-key", "list", "--log-format=json", "--log-format", "text"}, want: "--log-format may only"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			program := Program{
				Backend: &fakeBackend{}, Stdout: io.Discard, Stderr: &stderr,
			}
			if code := program.Run(context.Background(), test.args); code != 1 ||
				!strings.Contains(stderr.String(), test.want) {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
		})
	}
}

func TestServeRoleOverridePrecedesRoleScopedSecretLoading(t *testing.T) {
	unsetCLIEnvironment(t, "ETHERVIEW_SESSION_PEPPER")
	t.Setenv("ETHERVIEW_FEATURE_USER_AUTH", "true")
	t.Setenv("ETHERVIEW_SESSION_PEPPER_FILE", "/does/not/exist/session")
	t.Setenv("ETHERVIEW_ROLES", "all")

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
runtime:
  roles: [all]
features:
  user_auth: true
server:
  public_url: https://explorer.example
database:
  url: postgres://localhost/etherview
rpc:
  endpoints:
    - name: primary
      url: http://localhost:8545
      purposes: [all]
`), 0o600); err != nil {
		t.Fatal(err)
	}

	backend := &fakeBackend{}
	var stderr bytes.Buffer
	program := Program{Backend: backend, Stdout: &bytes.Buffer{}, Stderr: &stderr}
	if code := program.Run(
		context.Background(),
		[]string{"serve", "--config", path, "--roles", "sync"},
	); code != 0 {
		t.Fatalf("sync code=%d stderr=%s", code, stderr.String())
	}
	if !backend.served || strings.Join(backend.roles, ",") != "sync" {
		t.Fatalf("served=%v roles=%v", backend.served, backend.roles)
	}

	stderr.Reset()
	if code := program.Run(
		context.Background(),
		[]string{"serve", "--config", path, "--roles", "api"},
	); code != 1 || !strings.Contains(stderr.String(), "SESSION_PEPPER_FILE") {
		t.Fatalf("api code=%d stderr=%s", code, stderr.String())
	}
}

func TestDoctorRedactsURLs(t *testing.T) {
	t.Setenv("ETHERVIEW_DATABASE_URL", "postgres://user:secret@localhost/db")
	t.Setenv("ETHERVIEW_RPC_URLS", "https://user:secret@rpc.example")
	var stdout, stderr bytes.Buffer
	p := Program{Backend: &fakeBackend{}, Stdout: &stdout, Stderr: &stderr}
	if code := p.Run(context.Background(), []string{"doctor"}); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "secret") || !strings.Contains(stdout.String(), "env-1") {
		t.Fatalf("doctor output leaks or omits endpoint: %s", stdout.String())
	}
}

func unsetCLIEnvironment(t *testing.T, name string) {
	t.Helper()
	previous, present := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if present {
			_ = os.Setenv(name, previous)
			return
		}
		_ = os.Unsetenv(name)
	})
}

func TestDoctorReportsRunnableRoleValidationFailure(t *testing.T) {
	t.Setenv("ETHERVIEW_DATABASE_URL", "")
	t.Setenv("ETHERVIEW_RPC_URLS", "")
	t.Setenv("ETHERVIEW_ROLES", "all")
	var stdout, stderr bytes.Buffer
	backend := &fakeBackend{}
	program := Program{Backend: backend, Stdout: &stdout, Stderr: &stderr}
	if code := program.Run(context.Background(), []string{"doctor"}); code != 1 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report struct {
		Valid  bool     `json:"valid"`
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode doctor report: %v", err)
	}
	joined := strings.Join(report.Errors, "\n")
	if report.Valid || !strings.Contains(joined, "database.url is required") ||
		!strings.Contains(joined, "at least one rpc endpoint is required") {
		t.Fatalf("doctor report = %+v", report)
	}
	if !strings.Contains(stderr.String(), "database.url is required") ||
		!strings.Contains(stderr.String(), "at least one rpc endpoint is required") {
		t.Fatalf("doctor stderr = %q", stderr.String())
	}
	if backend.doctorCalled {
		t.Fatal("doctor backend ran before runnable-role validation succeeded")
	}
}

func TestDoctorIncludesBackendCapabilityFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
runtime:
  roles: [api]
database:
  url: postgres://localhost/etherview
`), 0o600); err != nil {
		t.Fatal(err)
	}
	type contextKey string
	const key contextKey = "doctor"
	ctx := context.WithValue(context.Background(), key, "present")
	backend := &fakeBackend{doctorErr: errors.New("x402_facilitator_unavailable")}
	var stdout, stderr bytes.Buffer
	program := Program{Backend: backend, Stdout: &stdout, Stderr: &stderr}
	if code := program.Run(ctx, []string{"doctor", "--config", path}); code != 1 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report struct {
		Valid  bool     `json:"valid"`
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode doctor report: %v", err)
	}
	if report.Valid || strings.Join(report.Errors, ",") != "x402_facilitator_unavailable" {
		t.Fatalf("doctor report = %+v", report)
	}
	if !backend.doctorCalled || strings.Join(backend.doctorRoles, ",") != "api" ||
		backend.doctorContext.Value(key) != "present" {
		t.Fatalf(
			"called=%v roles=%v context=%v",
			backend.doctorCalled,
			backend.doctorRoles,
			backend.doctorContext,
		)
	}
}

func TestExtractConfigFlagPreservesBackendFlags(t *testing.T) {
	t.Parallel()
	path, rest, err := extractConfigFlag("admin", []string{"--name", "reader", "--config=/tmp/config.yaml", "--rate", "10"})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/config.yaml" || strings.Join(rest, " ") != "--name reader --rate 10" {
		t.Fatalf("path=%q rest=%v", path, rest)
	}
	if _, _, err := extractConfigFlag("admin", []string{"--config", "a", "--config", "b"}); err == nil {
		t.Fatal("expected duplicate config error")
	}
}

func TestOperationalCommandsParseConfigAndForwardArguments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("database:\n  url: postgres://localhost/etherview\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		args   []string
		assert func(*testing.T, *fakeBackend)
	}{
		{
			name: "migrate up", args: []string{"migrate", "up", "--config", path},
			assert: func(t *testing.T, backend *fakeBackend) {
				if backend.migrate != "up" {
					t.Fatalf("migrate action=%q", backend.migrate)
				}
			},
		},
		{
			name: "migrate status", args: []string{"migrate", "status", "--config=" + path},
			assert: func(t *testing.T, backend *fakeBackend) {
				if backend.migrate != "status" {
					t.Fatalf("migrate action=%q", backend.migrate)
				}
			},
		},
		{
			name: "repair", args: []string{"repair", "--from", "10", "--config", path, "--to", "20", "--reason", "rpc replacement", "--stage", "core"},
			assert: func(t *testing.T, backend *fakeBackend) {
				if backend.repairKind != "repair" || strings.Join(backend.repairArgs, " ") != "--from 10 --to 20 --reason rpc replacement --stage core" {
					t.Fatalf("repair kind=%q args=%v", backend.repairKind, backend.repairArgs)
				}
			},
		},
		{
			name: "reindex", args: []string{"reindex", "--config=" + path, "--from", "10", "--to", "20", "--reason", "rebuild token", "--stage", "token"},
			assert: func(t *testing.T, backend *fakeBackend) {
				if backend.repairKind != "reindex" || strings.Join(backend.repairArgs, " ") != "--from 10 --to 20 --reason rebuild token --stage token" {
					t.Fatalf("reindex kind=%q args=%v", backend.repairKind, backend.repairArgs)
				}
			},
		},
		{
			name: "admin api key", args: []string{
				"admin", "api-key", "create", "--name", "reader", "--config", path,
				"--rate", "25", "--scope", "api:read", "--scope", "contract:verify",
			},
			assert: func(t *testing.T, backend *fakeBackend) {
				if backend.adminResource != "api-key" || backend.adminAction != "create" || strings.Join(backend.adminArgs, " ") != "--name reader --rate 25 --scope api:read --scope contract:verify" {
					t.Fatalf("admin resource=%q action=%q args=%v", backend.adminResource, backend.adminAction, backend.adminArgs)
				}
			},
		},
		{
			name: "admin api key rotate", args: []string{"admin", "api-key", "rotate", "abcdefghij", "--config", path},
			assert: func(t *testing.T, backend *fakeBackend) {
				if backend.adminResource != "api-key" || backend.adminAction != "rotate" || strings.Join(backend.adminArgs, " ") != "abcdefghij" {
					t.Fatalf("admin resource=%q action=%q args=%v", backend.adminResource, backend.adminAction, backend.adminArgs)
				}
			},
		},
		{
			name: "admin label", args: []string{"admin", "label", "set", "address", "0x0000000000000000000000000000000000000001", "treasury", "--config=" + path},
			assert: func(t *testing.T, backend *fakeBackend) {
				if backend.adminResource != "label" || backend.adminAction != "set" || strings.Join(backend.adminArgs, " ") != "address 0x0000000000000000000000000000000000000001 treasury" {
					t.Fatalf("admin resource=%q action=%q args=%v", backend.adminResource, backend.adminAction, backend.adminArgs)
				}
			},
		},
		{
			name: "admin repair list", args: []string{"admin", "repair", "list", "--limit", "25", "--config=" + path},
			assert: func(t *testing.T, backend *fakeBackend) {
				if backend.adminResource != "repair" || backend.adminAction != "list" || strings.Join(backend.adminArgs, " ") != "--limit 25" {
					t.Fatalf("admin resource=%q action=%q args=%v", backend.adminResource, backend.adminAction, backend.adminArgs)
				}
			},
		},
		{
			name: "admin user set role",
			args: []string{
				"admin", "user", "set-role",
				"--address", "0x0000000000000000000000000000000000000001",
				"--config", path, "--role", "admin",
			},
			assert: func(t *testing.T, backend *fakeBackend) {
				if backend.adminResource != "user" || backend.adminAction != "set-role" ||
					strings.Join(backend.adminArgs, " ") !=
						"--address 0x0000000000000000000000000000000000000001 --role admin" {
					t.Fatalf(
						"admin resource=%q action=%q args=%v",
						backend.adminResource, backend.adminAction, backend.adminArgs,
					)
				}
			},
		},
		{
			name: "admin user set status",
			args: []string{
				"admin", "user", "set-status", "--status", "disabled",
				"--config=" + path,
				"--address", "0x0000000000000000000000000000000000000001",
			},
			assert: func(t *testing.T, backend *fakeBackend) {
				if backend.adminResource != "user" || backend.adminAction != "set-status" ||
					strings.Join(backend.adminArgs, " ") !=
						"--status disabled --address 0x0000000000000000000000000000000000000001" {
					t.Fatalf(
						"admin resource=%q action=%q args=%v",
						backend.adminResource, backend.adminAction, backend.adminArgs,
					)
				}
			},
		},
		{
			name: "admin user revoke sessions",
			args: []string{
				"admin", "user", "revoke-sessions",
				"--address=0x0000000000000000000000000000000000000001",
				"--config", path,
			},
			assert: func(t *testing.T, backend *fakeBackend) {
				if backend.adminResource != "user" ||
					backend.adminAction != "revoke-sessions" ||
					strings.Join(backend.adminArgs, " ") !=
						"--address=0x0000000000000000000000000000000000000001" {
					t.Fatalf(
						"admin resource=%q action=%q args=%v",
						backend.adminResource, backend.adminAction, backend.adminArgs,
					)
				}
			},
		},
		{
			name: "admin billing inspect",
			args: []string{
				"admin", "billing", "inspect",
				"--id", "00000000-0000-4000-8000-000000000001",
				"--config", path,
			},
			assert: func(t *testing.T, backend *fakeBackend) {
				if backend.adminResource != "billing" ||
					backend.adminAction != "inspect" ||
					strings.Join(backend.adminArgs, " ") !=
						"--id 00000000-0000-4000-8000-000000000001" {
					t.Fatalf(
						"admin resource=%q action=%q args=%v",
						backend.adminResource, backend.adminAction, backend.adminArgs,
					)
				}
			},
		},
		{
			name: "admin billing reconcile settled",
			args: []string{
				"admin", "billing", "reconcile",
				"--id=00000000-0000-4000-8000-000000000001",
				"--outcome", "settled",
				"--transaction-hash", "0x" + strings.Repeat("11", 32),
				"--config=" + path,
			},
			assert: func(t *testing.T, backend *fakeBackend) {
				if backend.adminResource != "billing" ||
					backend.adminAction != "reconcile" ||
					strings.Join(backend.adminArgs, " ") !=
						"--id=00000000-0000-4000-8000-000000000001 "+
							"--outcome settled --transaction-hash 0x"+
							strings.Repeat("11", 32) {
					t.Fatalf(
						"admin resource=%q action=%q args=%v",
						backend.adminResource, backend.adminAction, backend.adminArgs,
					)
				}
			},
		},
		{
			name: "admin billing reconcile failed",
			args: []string{
				"admin", "billing", "reconcile",
				"--outcome=failed",
				"--id", "00000000-0000-4000-8000-000000000001",
				"--config", path,
			},
			assert: func(t *testing.T, backend *fakeBackend) {
				if backend.adminResource != "billing" ||
					backend.adminAction != "reconcile" ||
					strings.Join(backend.adminArgs, " ") !=
						"--outcome=failed --id 00000000-0000-4000-8000-000000000001" {
					t.Fatalf(
						"admin resource=%q action=%q args=%v",
						backend.adminResource, backend.adminAction, backend.adminArgs,
					)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &fakeBackend{}
			var stderr bytes.Buffer
			program := Program{Backend: backend, Stdout: io.Discard, Stderr: &stderr}
			if code := program.Run(context.Background(), test.args); code != 0 {
				t.Fatalf("code=%d stderr=%s", code, stderr.String())
			}
			test.assert(t, backend)
		})
	}
}

func TestAdminUserAndBillingUseNonAPIRoleSecretLoading(t *testing.T) {
	unsetCLIEnvironment(t, "ETHERVIEW_SESSION_PEPPER")
	unsetCLIEnvironment(t, "ETHERVIEW_X402_FINGERPRINT_PEPPER")
	unsetCLIEnvironment(t, "ETHERVIEW_X402_FACILITATOR_HEADERS")
	t.Setenv("ETHERVIEW_FEATURE_USER_AUTH", "true")
	t.Setenv("ETHERVIEW_FEATURE_X402_BILLING", "true")
	t.Setenv("ETHERVIEW_SESSION_PEPPER_FILE", "/does/not/exist/session")
	t.Setenv(
		"ETHERVIEW_X402_FINGERPRINT_PEPPER_FILE",
		"/does/not/exist/fingerprint",
	)
	t.Setenv(
		"ETHERVIEW_X402_FACILITATOR_HEADERS_FILE",
		"/does/not/exist/facilitator-headers",
	)
	t.Setenv("ETHERVIEW_ROLES", "all")

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
server:
  public_url: https://explorer.example
database:
  url: postgres://localhost/etherview
features:
  user_auth: true
  x402_billing: true
billing:
  facilitator_url: https://facilitator.example
  facilitator_allowed_cidrs: [203.0.113.0/24]
  network: eip155:84532
  asset: "0x1111111111111111111111111111111111111111"
  asset_decimals: 6
  asset_eip712_name: USDC
  asset_eip712_version: "2"
  recipient: "0x2222222222222222222222222222222222222222"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		resource string
		action   string
		args     []string
	}{
		{
			name: "user", resource: "user", action: "set-role",
			args: []string{
				"admin", "user", "set-role",
				"--address", "0x0000000000000000000000000000000000000001",
				"--role", "admin", "--config", path,
			},
		},
		{
			name: "billing", resource: "billing", action: "inspect",
			args: []string{
				"admin", "billing", "inspect",
				"--id", "00000000-0000-4000-8000-000000000001",
				"--config", path,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &fakeBackend{}
			var stderr bytes.Buffer
			program := Program{
				Backend: backend, Stdout: io.Discard, Stderr: &stderr,
			}
			if code := program.Run(
				context.Background(), test.args,
			); code != 0 {
				t.Fatalf("code=%d stderr=%s", code, stderr.String())
			}
			if backend.adminResource != test.resource ||
				backend.adminAction != test.action {
				t.Fatalf(
					"admin resource=%q action=%q",
					backend.adminResource, backend.adminAction,
				)
			}
			if !slices.Equal(
				backend.adminConfig.Runtime.Roles,
				[]string{"maintenance"},
			) {
				t.Fatalf(
					"admin runtime roles=%v",
					backend.adminConfig.Runtime.Roles,
				)
			}
			if backend.adminConfig.UserAuth.SessionPepper != "" ||
				backend.adminConfig.Billing.FingerprintPepper != "" ||
				len(backend.adminConfig.Billing.FacilitatorHeaders) != 0 {
				t.Fatal("admin command loaded API-only authentication or billing secrets")
			}
		})
	}
}
