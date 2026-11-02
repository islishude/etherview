package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func offlineConfig(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"X402_DATABASE_URL", "X402_DATABASE_URL_FILE", "X402_BEARER_TOKEN", "X402_BEARER_TOKEN_FILE"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	for name, body := range map[string]string{"db": "postgres://localhost:1/offline", "token": strings.Repeat("t", 32), "key": "not a keystore", "password": "not decrypted"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	config := `server:
  listen: "127.0.0.1:8081"
  allow_insecure_http: true
database:
  url_file: DIR/db
auth:
  bearer_token_file: DIR/token
signers:
  local:
    type: keystore
    address: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
    keystore_file: DIR/key
    password_file: DIR/password
chains:
  - network: eip155:31337
    rpc:
      url: https://localhost:1
    signer: local
    max_gas: 500000
    max_fee_per_gas: "100000000000"
    confirmations: 1
    recipients: ["0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC"]
    tokens:
      - address: "0x5FbDB2315678afecb367f032d93F642f64180aa3"
        methods: [eip3009]
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(config, "DIR", dir)), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestCheckConfigOffline(t *testing.T) {
	path := offlineConfig(t)
	if err := runArgs([]string{"--config=" + path, "--check-config"}); err != nil {
		t.Fatal(err)
	}
	if err := runArgs([]string{"--config=" + path, "--check-config", "--rebroadcast=id"}); err == nil {
		t.Fatal("accepted conflicting actions")
	}
}

func TestHelp(t *testing.T) {
	if err := runArgs([]string{"--help"}); err != nil {
		t.Fatal(err)
	}
}

// Exercise the real main logging boundary in a subprocess, including its nonzero exit.
func TestCLIErrorOutput(t *testing.T) {
	if path := os.Getenv("FACILITATOR_ERROR_HELPER"); path != "" {
		os.Args = []string{os.Args[0], "--config=" + path, "--check-config"}
		main()
		return
	}
	path := filepath.Join(t.TempDir(), "marker_SECRET.yaml")
	if err := os.WriteFile(path, []byte("unknown_marker_SECRET: credential_marker_SECRET\n"), 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestCLIErrorOutput$")
	command.Env = append(os.Environ(), "FACILITATOR_ERROR_HELPER="+path)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "invalid_config: config") || strings.Contains(string(output), "marker_SECRET") {
		t.Fatalf("unsafe CLI failure: %s (%v)", output, err)
	}
}

func TestCLIEnvironment(t *testing.T) {
	if path := os.Getenv("FACILITATOR_ENV_HELPER"); path != "" {
		os.Args = []string{os.Args[0], "--config=" + path, "--check-config"}
		main()
		return
	}
	for _, source := range []string{"direct", "file", "unset", "conflict", "missing_file", "short_token"} {
		t.Run(source, func(t *testing.T) {
			path := offlineConfig(t)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			body := strings.ReplaceAll(string(data), "database:\n  url_file: "+filepath.Dir(path)+"/db\n", "")
			body = strings.ReplaceAll(body, "auth:\n  bearer_token_file: "+filepath.Dir(path)+"/token\n", "")
			if source == "unset" {
				body = string(data)
			}
			if err = os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("X402_DATABASE_URL", "postgres://localhost:1/offline_marker_SECRET")
			t.Setenv("X402_BEARER_TOKEN", strings.Repeat("marker_SECRET", 3))
			want := ""
			switch source {
			case "unset":
				if err = os.Unsetenv("X402_DATABASE_URL"); err != nil {
					t.Fatal(err)
				}
				if err = os.Unsetenv("X402_BEARER_TOKEN"); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err = os.Unsetenv("X402_DATABASE_URL"); err != nil {
					t.Fatal(err)
				}
				if err = os.Unsetenv("X402_BEARER_TOKEN"); err != nil {
					t.Fatal(err)
				}
				t.Setenv("X402_DATABASE_URL_FILE", filepath.Join(filepath.Dir(path), "db"))
				t.Setenv("X402_BEARER_TOKEN_FILE", filepath.Join(filepath.Dir(path), "token"))
			case "conflict":
				t.Setenv("X402_DATABASE_URL_FILE", "marker_SECRET")
				want = "conflicting_sources"
			case "missing_file":
				if err = os.Unsetenv("X402_DATABASE_URL"); err != nil {
					t.Fatal(err)
				}
				t.Setenv("X402_DATABASE_URL_FILE", filepath.Join(t.TempDir(), "marker_SECRET"))
				want = "file_unavailable"
			case "short_token":
				t.Setenv("X402_BEARER_TOKEN", "marker_SECRET")
				want = "invalid_bearer_token"
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(executable, "-test.run=^TestCLIEnvironment$")
			command.Env = append(os.Environ(), "FACILITATOR_ENV_HELPER="+path)
			if source == "unset" {
				// Docker may encode unbound passthroughs as bare names without equals.
				command.Env = append(command.Env, "X402_DATABASE_URL", "X402_DATABASE_URL_FILE", "X402_BEARER_TOKEN", "X402_BEARER_TOKEN_FILE")
			}
			output, err := command.CombinedOutput()
			if (err != nil) != (want != "") || strings.Contains(string(output), "marker_SECRET") || !strings.Contains(string(output), want) {
				t.Fatalf("unexpected CLI result: %s (%v)", output, err)
			}
		})
	}
}
