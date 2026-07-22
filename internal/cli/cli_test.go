package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/config"
)

type fakeBackend struct {
	served        bool
	roles         []string
	migrate       string
	repairKind    string
	repairArgs    []string
	adminResource string
	adminAction   string
	adminArgs     []string
}

func (f *fakeBackend) Serve(_ context.Context, _ config.Config, roles []string) error {
	f.served = true
	f.roles = roles
	return nil
}
func (f *fakeBackend) Migrate(_ context.Context, _ config.Config, action string) error {
	f.migrate = action
	return nil
}
func (f *fakeBackend) Repair(_ context.Context, _ config.Config, kind string, args []string) error {
	f.repairKind = kind
	f.repairArgs = append([]string(nil), args...)
	return nil
}
func (f *fakeBackend) Admin(_ context.Context, _ config.Config, resource, action string, args []string) error {
	f.adminResource = resource
	f.adminAction = action
	f.adminArgs = append([]string(nil), args...)
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

func TestDoctorReportsRunnableRoleValidationFailure(t *testing.T) {
	t.Setenv("ETHERVIEW_DATABASE_URL", "")
	t.Setenv("ETHERVIEW_RPC_URLS", "")
	t.Setenv("ETHERVIEW_ROLES", "all")
	var stdout, stderr bytes.Buffer
	program := Program{Backend: &fakeBackend{}, Stdout: &stdout, Stderr: &stderr}
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
			name: "admin api key", args: []string{"admin", "api-key", "create", "--name", "reader", "--config", path, "--rate", "25"},
			assert: func(t *testing.T, backend *fakeBackend) {
				if backend.adminResource != "api-key" || backend.adminAction != "create" || strings.Join(backend.adminArgs, " ") != "--name reader --rate 25" {
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
