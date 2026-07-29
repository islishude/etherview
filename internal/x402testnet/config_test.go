package x402testnet

import (
	"bytes"
	"encoding/hex"
	"maps"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestLoadConfigAcceptsExplicitBaseSepoliaContract(t *testing.T) {
	t.Parallel()
	values, keyBytes := validConfigEnvironment(t)
	cfg, err := loadConfig(mapLookup(values), readSecretFile)
	if err != nil {
		t.Fatalf("loadConfig(): %v", err)
	}
	defer cfg.ZeroSecrets()

	if cfg.Revision != strings.Repeat("a", 40) ||
		cfg.ExpectedOperation != "listBlocks" ||
		cfg.ExpectedAccess != "x402" ||
		cfg.ExpectedAssetDecimals != 6 ||
		cfg.ExpectedMaxTimeoutSeconds != 60 ||
		cfg.LedgerChainID != baseSepoliaChainID ||
		cfg.RPCURL != "https://rpc.example/v3/private-token" ||
		cfg.WriterDatabaseURL !=
			"postgres://etherview:private-password@writer.example/etherview?sslmode=require" ||
		!bytes.Equal(cfg.PrivateKey, keyBytes) {
		t.Fatalf("config = %#v", cfg)
	}
}

func TestLoadConfigValidatesEveryPublicInputBeforeReadingSecrets(t *testing.T) {
	t.Parallel()
	base, _ := validConfigEnvironment(t)
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "confirmation", key: "CONFIRM", value: "yes"},
		{name: "revision", key: "REVISION", value: "revision"},
		{name: "target scheme", key: "TARGET_URL", value: "http://explorer.example/api/v1/blocks?limit=1"},
		{name: "target userinfo", key: "TARGET_URL", value: "https://user@explorer.example/api/v1/blocks?limit=1"},
		{name: "target fragment", key: "TARGET_URL", value: "https://explorer.example/api/v1/blocks?limit=1#secret"},
		{name: "target operation mismatch", key: "TARGET_URL", value: "https://explorer.example/api/v1/tokens?limit=1"},
		{name: "target duplicate query", key: "TARGET_URL", value: "https://explorer.example/api/v1/blocks?limit=1&limit=2"},
		{name: "target unknown query", key: "TARGET_URL", value: "https://explorer.example/api/v1/blocks?api_key=secret&limit=1"},
		{name: "resource origin mismatch", key: "EXPECTED_RESOURCE_URL", value: "https://other.example/api/v1/blocks?limit=1"},
		{name: "resource noncanonical query", key: "EXPECTED_RESOURCE_URL", value: "https://explorer.example/api/v1/blocks?limit=1&cursor=abc"},
		{name: "free operation", key: "EXPECTED_OPERATION", value: "getStatus"},
		{name: "unknown operation", key: "EXPECTED_OPERATION", value: "notAnOperation"},
		{name: "access", key: "EXPECTED_ACCESS", value: "free"},
		{name: "asset lowercase", key: "EXPECTED_ASSET", value: strings.ToLower(base[testnetEnvPrefix+"EXPECTED_ASSET"])},
		{name: "asset zero", key: "EXPECTED_ASSET", value: common.Address{}.Hex()},
		{name: "decimals leading zero", key: "EXPECTED_ASSET_DECIMALS", value: "06"},
		{name: "decimals overflow", key: "EXPECTED_ASSET_DECIMALS", value: "256"},
		{name: "asset name empty", key: "EXPECTED_ASSET_EIP712_NAME", value: " "},
		{name: "asset name control", key: "EXPECTED_ASSET_EIP712_NAME", value: "USD\nCoin"},
		{name: "asset version empty", key: "EXPECTED_ASSET_EIP712_VERSION", value: ""},
		{name: "amount zero", key: "EXPECTED_AMOUNT_ATOMIC", value: "0"},
		{name: "amount leading zero", key: "EXPECTED_AMOUNT_ATOMIC", value: "01"},
		{name: "amount uint256 overflow", key: "EXPECTED_AMOUNT_ATOMIC", value: "115792089237316195423570985008687907853269984665640564039457584007913129639936"},
		{name: "recipient zero", key: "EXPECTED_RECIPIENT", value: common.Address{}.Hex()},
		{name: "timeout zero", key: "EXPECTED_MAX_TIMEOUT_SECONDS", value: "0"},
		{name: "timeout too high", key: "EXPECTED_MAX_TIMEOUT_SECONDS", value: "61"},
		{name: "timeout noncanonical", key: "EXPECTED_MAX_TIMEOUT_SECONDS", value: "01"},
		{name: "ledger chain", key: "LEDGER_CHAIN_ID", value: "1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := cloneEnvironment(base)
			values[testnetEnvPrefix+test.key] = test.value
			readCalls := 0
			_, err := loadConfig(
				mapLookup(values),
				func(string, int64) ([]byte, error) {
					readCalls++
					return nil, boundaryError("unexpected_secret_read")
				},
			)
			if ErrorCode(err) != codeConfigInvalid {
				t.Fatalf("ErrorCode() = %q", ErrorCode(err))
			}
			if readCalls != 0 {
				t.Fatalf("secret reads = %d, want 0", readCalls)
			}
		})
	}
}

func TestLoadConfigRejectsRevisionDriftBeforeReadingSecrets(t *testing.T) {
	t.Parallel()
	values, _ := validConfigEnvironment(t)
	readCalls := 0
	_, err := loadConfigWithRevision(
		mapLookup(values),
		func(string, int64) ([]byte, error) {
			readCalls++
			return nil, boundaryError("unexpected_secret_read")
		},
		func(string) bool { return false },
	)
	if ErrorCode(err) != codeConfigInvalid {
		t.Fatalf("ErrorCode() = %q", ErrorCode(err))
	}
	if readCalls != 0 {
		t.Fatalf("secret reads = %d, want 0", readCalls)
	}
}

func TestMatchesBuildRevisionRequiresExactCleanGitState(t *testing.T) {
	t.Parallel()
	const revision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	valid := &debug.BuildInfo{
		Main: debug.Module{Path: "github.com/islishude/etherview"},
		Settings: []debug.BuildSetting{
			{Key: "vcs", Value: "git"},
			{Key: "vcs.revision", Value: revision},
			{Key: "vcs.modified", Value: "false"},
		},
	}
	if !matchesBuildRevision(revision, valid, true) {
		t.Fatal("exact clean build revision was rejected")
	}
	tests := []struct {
		name   string
		ok     bool
		mutate func(*debug.BuildInfo)
	}{
		{name: "missing build info", ok: false},
		{
			name: "wrong module", ok: true,
			mutate: func(info *debug.BuildInfo) {
				info.Main.Path = "example.invalid/fork"
			},
		},
		{
			name: "wrong revision", ok: true,
			mutate: func(info *debug.BuildInfo) {
				info.Settings[1].Value =
					"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			},
		},
		{
			name: "dirty worktree", ok: true,
			mutate: func(info *debug.BuildInfo) {
				info.Settings[2].Value = "true"
			},
		},
		{
			name: "duplicate revision", ok: true,
			mutate: func(info *debug.BuildInfo) {
				info.Settings = append(
					info.Settings,
					debug.BuildSetting{
						Key: "vcs.revision", Value: revision,
					},
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := *valid
			candidate.Settings = append(
				[]debug.BuildSetting(nil),
				valid.Settings...,
			)
			if test.mutate != nil {
				test.mutate(&candidate)
			}
			if matchesBuildRevision(revision, &candidate, test.ok) {
				t.Fatal("invalid build provenance was accepted")
			}
		})
	}
}

func TestLoadConfigRequiresEveryPublicInput(t *testing.T) {
	t.Parallel()
	base, _ := validConfigEnvironment(t)
	for _, name := range []string{
		"CONFIRM",
		"REVISION",
		"TARGET_URL",
		"EXPECTED_RESOURCE_URL",
		"EXPECTED_OPERATION",
		"EXPECTED_ACCESS",
		"EXPECTED_ASSET",
		"EXPECTED_ASSET_DECIMALS",
		"EXPECTED_ASSET_EIP712_NAME",
		"EXPECTED_ASSET_EIP712_VERSION",
		"EXPECTED_AMOUNT_ATOMIC",
		"EXPECTED_RECIPIENT",
		"EXPECTED_PAYER",
		"EXPECTED_MAX_TIMEOUT_SECONDS",
		"LEDGER_CHAIN_ID",
	} {
		t.Run(name, func(t *testing.T) {
			values := cloneEnvironment(base)
			delete(values, testnetEnvPrefix+name)
			readCalls := 0
			_, err := loadConfig(
				mapLookup(values),
				func(string, int64) ([]byte, error) {
					readCalls++
					return nil, boundaryError("unexpected_secret_read")
				},
			)
			if ErrorCode(err) != codeConfigInvalid || readCalls != 0 {
				t.Fatalf("error=%q reads=%d", ErrorCode(err), readCalls)
			}
		})
	}
}

func TestLoadConfigRejectsPlaintextSecretAlternatives(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"PRIVATE_KEY",
		"RPC_URL",
		"WRITER_DATABASE_URL",
	} {
		t.Run(name, func(t *testing.T) {
			values, _ := validConfigEnvironment(t)
			values[testnetEnvPrefix+name] = "must-not-be-read"
			readCalls := 0
			_, err := loadConfig(
				mapLookup(values),
				func(string, int64) ([]byte, error) {
					readCalls++
					return nil, boundaryError("unexpected_secret_read")
				},
			)
			if ErrorCode(err) != codeConfigInvalid || readCalls != 0 {
				t.Fatalf("error=%q reads=%d", ErrorCode(err), readCalls)
			}
		})
	}
}

func TestLoadConfigRequiresEverySecretFileInput(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"PRIVATE_KEY_FILE",
		"RPC_URL_FILE",
		"WRITER_DATABASE_URL_FILE",
	} {
		t.Run(name, func(t *testing.T) {
			values, _ := validConfigEnvironment(t)
			delete(values, testnetEnvPrefix+name)
			readCalls := 0
			_, err := loadConfig(
				mapLookup(values),
				func(string, int64) ([]byte, error) {
					readCalls++
					return nil, boundaryError("unexpected_secret_read")
				},
			)
			if ErrorCode(err) != codeConfigInvalid || readCalls != 0 {
				t.Fatalf("error=%q reads=%d", ErrorCode(err), readCalls)
			}
		})
	}
}

func TestReadSecretFileRejectsUnsafeFilesystemBoundaries(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	validPath := writeSecret(t, directory, "valid", "secret\n", 0o600)
	if value, err := readSecretFile(validPath, 16); err != nil ||
		string(value) != "secret" {
		t.Fatalf("valid secret = %q, %v", value, err)
	}

	worldReadable := writeSecret(
		t, directory, "world-readable", "secret", 0o644,
	)
	ownerWriteOnly := writeSecret(
		t, directory, "owner-write-only", "secret", 0o200,
	)
	oversized := writeSecret(
		t, directory, "oversized", strings.Repeat("x", 17), 0o600,
	)
	empty := writeSecret(t, directory, "empty", "", 0o600)
	whitespace := writeSecret(t, directory, "whitespace", "secret\n\n", 0o600)
	symlink := filepath.Join(directory, "symlink")
	if err := os.Symlink(validPath, symlink); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		worldReadable,
		ownerWriteOnly,
		oversized,
		empty,
		whitespace,
		symlink,
		directory,
		"relative-secret",
	} {
		_, err := readSecretFile(path, 16)
		if code := ErrorCode(err); code != codeSecretFileInvalid &&
			code != codeSecretInvalid {
			t.Fatalf("ErrorCode(%q) = %q", path, code)
		}
		if strings.Contains(err.Error(), path) {
			t.Fatalf("error leaked path: %v", err)
		}
	}
}

func TestLoadConfigValidatesSecretContentsAndPayer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*testing.T, map[string]string)
	}{
		{
			name: "noncanonical private key",
			mutate: func(t *testing.T, values map[string]string) {
				rewriteSecret(
					t,
					values[testnetEnvPrefix+"PRIVATE_KEY_FILE"],
					strings.Repeat("A", 64),
				)
			},
		},
		{
			name: "private key payer mismatch",
			mutate: func(t *testing.T, values map[string]string) {
				key, err := crypto.GenerateKey()
				if err != nil {
					t.Fatal(err)
				}
				values[testnetEnvPrefix+"EXPECTED_PAYER"] =
					crypto.PubkeyToAddress(key.PublicKey).Hex()
			},
		},
		{
			name: "plaintext rpc",
			mutate: func(t *testing.T, values map[string]string) {
				rewriteSecret(
					t,
					values[testnetEnvPrefix+"RPC_URL_FILE"],
					"http://rpc.example",
				)
			},
		},
		{
			name: "rpc userinfo",
			mutate: func(t *testing.T, values map[string]string) {
				rewriteSecret(
					t,
					values[testnetEnvPrefix+"RPC_URL_FILE"],
					"https://user:password@rpc.example",
				)
			},
		},
		{
			name: "non-postgres writer",
			mutate: func(t *testing.T, values map[string]string) {
				rewriteSecret(
					t,
					values[testnetEnvPrefix+"WRITER_DATABASE_URL_FILE"],
					"mysql://writer.example/etherview",
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, _ := validConfigEnvironment(t)
			test.mutate(t, values)
			cfg, err := loadConfig(mapLookup(values), readSecretFile)
			cfg.ZeroSecrets()
			if ErrorCode(err) != codeSecretInvalid {
				t.Fatalf("ErrorCode() = %q", ErrorCode(err))
			}
			if err.Error() != codeSecretInvalid {
				t.Fatalf("error exposed more than its stable code: %v", err)
			}
		})
	}
}

func TestConfigZeroSecretsOverwritesPrivateKey(t *testing.T) {
	t.Parallel()
	privateKey := []byte("mutable-private-key")
	cfg := Config{
		PrivateKey:        privateKey,
		RPCURL:            "https://private-rpc.example",
		WriterDatabaseURL: "postgres://private-writer.example/db",
	}
	cfg.ZeroSecrets()
	if cfg.PrivateKey != nil || cfg.RPCURL != "" || cfg.WriterDatabaseURL != "" {
		t.Fatalf("secrets retained in config: %#v", cfg)
	}
	if !bytes.Equal(privateKey, make([]byte, len(privateKey))) {
		t.Fatalf("private-key backing bytes were not overwritten: %x", privateKey)
	}
}

func validConfigEnvironment(t *testing.T) (map[string]string, []byte) {
	t.Helper()
	directory := t.TempDir()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	payer := crypto.PubkeyToAddress(key.PublicKey).Hex()
	for payer == strings.ToLower(payer) {
		key, err = crypto.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		payer = crypto.PubkeyToAddress(key.PublicKey).Hex()
	}
	keyBytes := crypto.FromECDSA(key)
	privateKeyPath := writeSecret(
		t,
		directory,
		"payer-key",
		hex.EncodeToString(keyBytes)+"\n",
		0o600,
	)
	rpcPath := writeSecret(
		t,
		directory,
		"rpc-url",
		"https://rpc.example/v3/private-token\n",
		0o600,
	)
	writerPath := writeSecret(
		t,
		directory,
		"writer-url",
		"postgres://etherview:private-password@writer.example/etherview?sslmode=require\n",
		0o600,
	)
	asset := common.HexToAddress(
		"0x11111111111111111111111111111111111111a1",
	).Hex()
	recipient := common.HexToAddress(
		"0x22222222222222222222222222222222222222b2",
	).Hex()
	return map[string]string{
		testnetEnvPrefix + "CONFIRM":                       realPaymentConfirmation,
		testnetEnvPrefix + "REVISION":                      strings.Repeat("a", 40),
		testnetEnvPrefix + "TARGET_URL":                    "https://explorer.example/api/v1/blocks?limit=1",
		testnetEnvPrefix + "EXPECTED_RESOURCE_URL":         "https://explorer.example/api/v1/blocks?limit=1",
		testnetEnvPrefix + "EXPECTED_OPERATION":            "listBlocks",
		testnetEnvPrefix + "EXPECTED_ACCESS":               "x402",
		testnetEnvPrefix + "EXPECTED_ASSET":                asset,
		testnetEnvPrefix + "EXPECTED_ASSET_DECIMALS":       "6",
		testnetEnvPrefix + "EXPECTED_ASSET_EIP712_NAME":    "Test USD",
		testnetEnvPrefix + "EXPECTED_ASSET_EIP712_VERSION": "2",
		testnetEnvPrefix + "EXPECTED_AMOUNT_ATOMIC":        "1000",
		testnetEnvPrefix + "EXPECTED_RECIPIENT":            recipient,
		testnetEnvPrefix + "EXPECTED_PAYER":                payer,
		testnetEnvPrefix + "EXPECTED_MAX_TIMEOUT_SECONDS":  "60",
		testnetEnvPrefix + "LEDGER_CHAIN_ID":               "84532",
		testnetEnvPrefix + "PRIVATE_KEY_FILE":              privateKeyPath,
		testnetEnvPrefix + "RPC_URL_FILE":                  rpcPath,
		testnetEnvPrefix + "WRITER_DATABASE_URL_FILE":      writerPath,
	}, keyBytes
}

func mapLookup(values map[string]string) environmentLookup {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

func cloneEnvironment(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	maps.Copy(clone, source)
	return clone
}

func writeSecret(
	t *testing.T,
	directory, name, value string,
	mode os.FileMode,
) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(value), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func rewriteSecret(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}
