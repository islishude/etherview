//go:build runtimee2e && verificatione2e

package runtimee2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/verify"
)

type bytecodeDBProductionFixture struct {
	source   verify.BytecodeDBSource
	creation string
	runtime  string
}

func TestBytecodeDBProductionE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Minute)
	defer cancel()
	root := repositoryRoot(t)
	fixtures := []bytecodeDBProductionFixture{
		loadBytecodeDBFixture(t, root, "solidity/input.no-cbor.json", "solidity/output.no-cbor.json", "SOLIDITY", "0.8.30+commit.73712a01"),
		loadBytecodeDBFixture(t, root, "vyper/versions/0.4.3/plain.input.json", "vyper/versions/0.4.3/plain.output.json", "VYPER", "v0.4.3+commit.bff19ea2"),
	}
	runVerificationTopologies(t, ctx, func(t *testing.T, mode string) []string {
		h := startVerificationTopology(t, ctx, root, mode, "bytecode-db", "e2e/runtime/bytecode_db.compose.yaml", func(h *harness) { h.setupBytecodeDBProvider(ctx, fixtures) })
		var results []string
		for _, fixture := range fixtures {
			h.enterPhase("automatic " + fixture.source.SourceType + " source discovery")
			hash := h.sendTransaction(ctx, map[string]any{"from": h.fixture.accounts[0], "data": fixture.creation, "gas": "0x4c4b40"})
			h.mine(ctx, uint64(time.Now().Unix())+uint64(len(results)+1))
			receipt := h.waitReceipt(ctx, hash)
			if receipt.Status != "0x1" || receipt.ContractAddress == "" {
				t.Fatalf("deployment=%+v", receipt)
			}
			var origin string
			waitFor(t, ctx, "automatic local source publication", func() (bool, string, error) {
				var count int
				err := h.db.QueryRow(ctx, `SELECT count(*) FROM verified_contracts v JOIN bytecode_db_attempts a ON a.job_id=v.verification_job_id WHERE v.address=$1`, common.HexToAddress(receipt.ContractAddress).Bytes()).Scan(&count)
				return count == 1, fmt.Sprintf("publications=%d", count), err
			})
			var response gen.VerifiedContractResponse
			h.mustGetJSON(ctx, hardhatContractAPIPath(receipt.ContractAddress, "/verification", nil), &response)
			origin = string(response.Data.VerificationOrigin)
			if origin != "eth_bytecode_db" || response.Data.ContractName != fixture.source.ContractName || response.Data.Abi == nil || len(*response.Data.Abi) == 0 {
				t.Fatalf("automatic artifact=%+v", response.Data)
			}
			for filename, content := range fixture.source.SourceFiles {
				published, ok := response.Data.Sources[filename].(map[string]any)
				if !ok || published["content"] != content {
					t.Fatalf("source projection differs from locally verified input: %s", filename)
				}
			}
			var executor string
			if err := h.db.QueryRow(ctx, `SELECT j.executor_kind FROM verification_jobs j JOIN bytecode_db_attempts a ON a.job_id=j.id WHERE j.address=$1 AND j.outcome_kind='verification_success'`, common.HexToAddress(receipt.ContractAddress).Bytes()).Scan(&executor); err != nil {
				t.Fatal(err)
			}
			if executor != "node_solcjs_v1" && executor != "node_vyper_wasm_v1" {
				t.Fatalf("not locally compiled: %s", executor)
			}
			results = append(results, fixture.source.SourceType+":"+origin+":"+executor)
		}
		h.writeJSONArtifact("bytecode-db-results.json", results)
		return results
	})
}

func loadBytecodeDBFixture(t *testing.T, root, inputFile, outputFile, language, version string) bytecodeDBProductionFixture {
	t.Helper()
	base := filepath.Join(root, "internal/verify/testdata/compiler")
	input, err := os.ReadFile(filepath.Join(base, inputFile))
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(filepath.Join(base, outputFile))
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Sources map[string]struct {
			Content string `json:"content"`
		} `json:"sources"`
		Settings json.RawMessage `json:"settings"`
	}
	if err = json.Unmarshal(input, &source); err != nil {
		t.Fatal(err)
	}
	type code struct {
		Object string `json:"object"`
	}
	var compiled struct {
		Contracts map[string]map[string]struct {
			EVM struct {
				Bytecode code `json:"bytecode"`
				Deployed code `json:"deployedBytecode"`
			} `json:"evm"`
		} `json:"contracts"`
	}
	if err = json.Unmarshal(output, &compiled); err != nil {
		t.Fatal(err)
	}
	fixture := bytecodeDBProductionFixture{source: verify.BytecodeDBSource{SourceType: language, CompilerVersion: version, CompilerSettings: string(source.Settings), SourceFiles: map[string]string{}, MatchType: "FULL"}}
	for name, entry := range source.Sources {
		fixture.source.SourceFiles[name] = entry.Content
	}
	contractCount := 0
	for file, contracts := range compiled.Contracts {
		for name, contract := range contracts {
			contractCount++
			fixture.source.FileName = file
			fixture.source.ContractName = name
			fixture.creation = "0x" + strings.TrimPrefix(contract.EVM.Bytecode.Object, "0x")
			fixture.runtime = "0x" + strings.TrimPrefix(contract.EVM.Deployed.Object, "0x")
		}
	}
	if contractCount != 1 || fixture.creation == "0x" || fixture.runtime == "0x" {
		t.Fatal("fixture must contain exactly one contract with creation and runtime bytecode")
	}
	return fixture
}

func (h *harness) setupBytecodeDBProvider(ctx context.Context, fixtures []bytecodeDBProductionFixture) {
	h.t.Helper()
	tempCert, tempKey, _ := writeRuntimeTLSKeyPair(h.t)
	// Keep Compose inputs with harness artifacts until its registered teardown.
	// t.TempDir cleanup otherwise runs before startVerificationTopology cleanup.
	dir := filepath.Join(h.artifacts, "provider")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.t.Fatal(err)
	}
	cert, key := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	for destination, source := range map[string]string{cert: tempCert, key: tempKey} {
		data, err := os.ReadFile(source)
		if err != nil {
			h.t.Fatal(err)
		}
		if err = os.WriteFile(destination, data, 0o444); err != nil {
			h.t.Fatal(err)
		}
	}
	sources := map[string]verify.BytecodeDBSource{}
	for _, fixture := range fixtures {
		sources["DEPLOYED_BYTECODE:"+fixture.runtime] = fixture.source
		sources["CREATION_INPUT:"+fixture.creation] = fixture.source
	}
	encoded, _ := json.Marshal(sources)
	if err := os.WriteFile(filepath.Join(dir, "sources.json"), encoded, 0o444); err != nil {
		h.t.Fatal(err)
	}
	binary := filepath.Join(dir, "bytecodedbfixture")
	command := exec.CommandContext(ctx, "go", "build", "-o", binary, "./e2e/runtime/bytecodedbfixture")
	command.Dir = h.root
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if output, err := command.CombinedOutput(); err != nil {
		h.t.Fatalf("build source fixture: %v %s", err, output)
	}
	mount := func(source, target string) map[string]any {
		return map[string]any{"type": "bind", "source": source, "target": target, "read_only": true}
	}
	// Exercise strict public-address dialing in all-role mode and explicit
	// Fake-IP opt-in in split mode using isolated test-owned Docker networks.
	subnet, unsafePrivate := "11.255.253", "false"
	if h.mode == "distributed" {
		subnet, unsafePrivate = "198.18.252", "true"
	}
	provider := map[string]any{"image": h.project.Env["ETHERVIEW_IMAGE"], "entrypoint": []string{"/fixture/bytecodedbfixture"}, "command": []string{}, "user": "65532:65532", "read_only": true, "restart": "no", "networks": map[string]any{"bytecode-test": map[string]any{"ipv4_address": subnet + ".2", "aliases": []string{"bytecode.example"}}}, "volumes": []map[string]any{mount(cert, "/fixture/tls.crt"), mount(key, "/fixture/tls.key"), mount(binary, "/fixture/bytecodedbfixture"), mount(filepath.Join(dir, "sources.json"), "/fixture/sources.json")}}
	client := map[string]any{"environment": map[string]string{"ETHERVIEW_BYTECODE_DB_ENABLED": "true", "ETHERVIEW_BYTECODE_DB_UNSAFE_ALLOW_PRIVATE_NETWORKS": unsafePrivate, "ETHERVIEW_BYTECODE_DB_BASE_URL": "https://bytecode.example", "SSL_CERT_FILE": "/fixture/tls.crt"}, "networks": map[string]any{"default": nil, "bytecode-test": nil}, "volumes": []map[string]any{mount(cert, "/fixture/tls.crt")}}
	override := map[string]any{"services": map[string]any{"runtime-bytecode-db": provider, "etherview": client, "api": client}, "networks": map[string]any{"bytecode-test": map[string]any{"internal": true, "ipam": map[string]any{"config": []map[string]string{{"subnet": subnet + ".0/24"}}}}}}
	encoded, _ = json.Marshal(override)
	filename := filepath.Join(dir, "bytecode.compose.json")
	if err := os.WriteFile(filename, encoded, 0o600); err != nil {
		h.t.Fatal(err)
	}
	h.project.Files = append(h.project.Files, filename)
	if err := h.project.Up(ctx, "runtime-bytecode-db"); err != nil {
		h.t.Fatal(err)
	}
}
