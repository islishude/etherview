//go:build runtimee2e && verificatione2e

package runtimee2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	vyper "github.com/islishude/etherview/compiler/vyper"
	"github.com/islishude/etherview/internal/api/gen"
)

var vyperProductionVersions = [...]string{"0.2.1", "0.3.1", "0.3.4", "0.3.10", "0.4.0", "0.4.3"}
var vyperProductionProtocols = [...]string{"native", "etherscan"}

const vyperProductionJobCount = int64(len(vyperProductionVersions) * len(vyperProductionProtocols))

func verifyVyperProduction(t *testing.T, ctx context.Context, h *harness, key string) []vyperApplicationCase {
	var results []vyperApplicationCase
	for _, version := range vyperProductionVersions {
		results = append(results, verifyVyperProductionCase(t, ctx, h, key, version)...)
	}
	return results
}

func verifyVyperProductionCase(t *testing.T, ctx context.Context, h *harness, key, version string) []vyperApplicationCase {
	var results []vyperApplicationCase
	t.Helper()
	h.enterPhase("Vyper native and Etherscan source verification")
	root := filepath.Join(h.root, "internal/verify/testdata/compiler/vyper/versions", version)
	fixture := "immutable"
	if version == "0.2.1" {
		fixture = "constructor"
	}
	expectedCreation := gen.VerificationMatchTypePartial
	if version == "0.4.3" {
		expectedCreation = gen.VerificationMatchTypeFull
	}
	input, err := os.ReadFile(filepath.Join(root, fixture+".input.json"))
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(filepath.Join(root, fixture+".output.json"))
	if err != nil {
		t.Fatal(err)
	}
	var compiled struct {
		Contracts map[string]map[string]struct {
			EVM struct {
				Bytecode struct {
					Object string `json:"object"`
				} `json:"bytecode"`
			} `json:"evm"`
		} `json:"contracts"`
	}
	if err := json.Unmarshal(output, &compiled); err != nil {
		t.Fatal(err)
	}
	creation := compiled.Contracts["A.vy"]["A"].EVM.Bytecode.Object
	var catalog gen.CompilerCatalogResponse
	h.mustGetJSON(ctx, "/api/v1/verifier/compilers?language=vyper", &catalog)
	slices.Sort(catalog.Data.Versions)
	if !slices.Equal(catalog.Data.Versions, applicationVyperVersions(t)) {
		t.Fatalf("Vyper catalog=%+v", catalog.Data)
	}
	for _, protocol := range vyperProductionProtocols {
		word := strings.Repeat("0", 62) + "2a"
		tx := h.sendTransaction(ctx, map[string]any{"from": h.fixture.accounts[0], "data": creation + word, "gas": "0x100000"})
		var mined any
		h.rpcCall(ctx, &mined, "evm_mine")
		receipt := h.waitReceipt(ctx, tx)
		if receipt.Status != "0x1" || !common.IsHexAddress(receipt.ContractAddress) {
			t.Fatalf("Vyper deploy failed: %+v", receipt)
		}
		address := strings.ToLower(receipt.ContractAddress)
		waitVerificationCanonicalTip(t, ctx, h)
		waitVerificationContractCode(t, ctx, h, address)
		var actual string
		selector := "0x" + common.Bytes2Hex(crypto.Keccak256([]byte("value()"))[:4])
		h.rpcCall(ctx, &actual, "eth_call", map[string]string{"to": address, "data": selector}, "latest")
		if actual != "0x"+word {
			t.Fatalf("Vyper immutable call=%s", actual)
		}
		if protocol == "native" {
			payload, _ := json.Marshal(map[string]any{"language": "vyper", "compiler_version": version, "input_kind": "standard_json", "target_file": "A.vy", "input": json.RawMessage(input)})
			var accepted gen.VerificationJobResponse
			vyperNativeRequest(t, ctx, h, key, http.MethodPost, "/api/v1/contracts/"+address+"/verification", payload, &accepted, http.StatusAccepted)
			waitFor(t, ctx, "Vyper native verification", func() (bool, string, error) {
				var job gen.VerificationJobResponse
				vyperNativeRequest(t, ctx, h, key, http.MethodGet, "/api/v1/verifier/jobs/"+accepted.Data.Id.String(), nil, &job, http.StatusOK)
				return job.Data.Status == gen.VerificationJobStatusSucceeded, string(job.Data.Status), nil
			})
		} else {
			values := url.Values{"action": {"verifysourcecode"}, "contractaddress": {address}, "codeformat": {"vyper-json"}, "compilerversion": {"vyper:" + version}, "contractname": {"A.vy:A"}, "sourceCode": {string(input)}, "optimizationUsed": {"1"}}
			result, err := verificationEtherscanRequest(t, ctx, h, key, http.MethodPost, values)
			if err != nil {
				t.Fatal(err)
			}
			var guid string
			if json.Unmarshal(result, &guid) != nil || guid == "" {
				t.Fatalf("Vyper GUID=%s", result)
			}
			waitFor(t, ctx, "Vyper Etherscan verification", func() (bool, string, error) {
				result, err := verificationEtherscanRequest(t, ctx, h, key, http.MethodGet, url.Values{"action": {"checkverifystatus"}, "guid": {guid}})
				return err == nil && bytes.Contains(result, []byte("Pass - Verified")), string(result), nil
			})
		}
		var artifact gen.VerifiedContractResponse
		h.mustGetJSON(ctx, "/api/v1/contracts/"+address+"/verification", &artifact)
		abiBytes, _ := json.Marshal(artifact.Data.Abi)
		if artifact.Data.Language != "vyper" || artifact.Data.CompilerVersion != version ||
			artifact.Data.RuntimeMatch == nil || artifact.Data.RuntimeMatch.MatchType != "partial" ||
			artifact.Data.CreationMatch == nil || artifact.Data.CreationMatch.MatchType != expectedCreation ||
			artifact.Data.ConstructorArguments == nil || *artifact.Data.ConstructorArguments != "0x"+word ||
			artifact.Data.Resolution != "exact_address" ||
			!bytes.Contains(abiBytes, []byte("value")) || len(artifact.Data.Sources) != 1 {
			t.Fatalf("Vyper artifact=%+v", artifact.Data)
		}
		source, err := verificationEtherscanRequest(t, ctx, h, key, http.MethodGet, url.Values{"action": {"getsourcecode"}, "address": {address}})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(source, []byte(`"CompilerType":"vyper"`)) || !bytes.Contains(source, []byte(`"CompilerVersion":"vyper:`+version+`"`)) {
			t.Fatalf("Vyper source=%s", source)
		}
		abi, err := verificationEtherscanRequest(t, ctx, h, key, http.MethodGet, url.Values{"action": {"getabi"}, "address": {address}})
		if err != nil || !bytes.Contains(abi, []byte("value")) {
			t.Fatalf("Vyper ABI=%s err=%v", abi, err)
		}
		entry := vyperApplicationCase{Version: version, Protocol: protocol, Constructor: *artifact.Data.ConstructorArguments, CreationMatch: string(artifact.Data.CreationMatch.MatchType), RuntimeMatch: string(artifact.Data.RuntimeMatch.MatchType)}
		entry.ABI = fmt.Sprintf("%x", sha256.Sum256(abiBytes))
		sourceBytes, err := json.Marshal(artifact.Data.Sources)
		if err != nil {
			t.Fatal(err)
		}
		entry.Sources = fmt.Sprintf("%x", sha256.Sum256(sourceBytes))
		if err := h.db.QueryRow(ctx, `SELECT encode(compiler_digest,'hex'), encode(executor_digest,'hex') FROM verification_jobs
    WHERE address=$1 AND compiler_version=$2 AND language='vyper' AND kind='address' AND status='succeeded'
    AND compiler_platform='python-wheel' AND catalog_language='vyper' AND catalog_generation_id IS NOT NULL
    AND executor_kind='node_vyper_wasm_v1' AND execution_policy='trusted_subprocess'
    AND octet_length(compiler_digest)=32 AND octet_length(executor_digest)=32`, common.HexToAddress(address).Bytes(), version).Scan(&entry.CompilerDigest, &entry.ExecutorDigest); err != nil {
			t.Fatal(err)
		}
		results = append(results, entry)
	}
	return results
}

func vyperNativeRequest(t *testing.T, ctx context.Context, h *harness, key, method, path string, payload []byte, result any, status int) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, h.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-API-Key", key)
	req.Header.Set("Content-Type", "application/json")
	response, err := h.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close() //nolint:errcheck
	if response.StatusCode != status {
		t.Fatalf("Vyper API status=%d", response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(result); err != nil {
		t.Fatal(err)
	}
}

func applicationVyperVersions(t *testing.T) []string {
	t.Helper()
	recipes, err := vyper.Recipes()
	if err != nil {
		t.Fatal(err)
	}
	versions := make([]string, 0, len(recipes))
	for _, recipe := range recipes {
		versions = append(versions, recipe.Version)
	}
	slices.Sort(versions)
	return versions
}

func TestVyperProductionFixtureCoverage(t *testing.T) {
	versions := applicationVyperVersions(t)
	if slices.Contains(versions, "0.2.0") {
		t.Fatal("retired compiler in production catalog")
	}
	for _, version := range vyperProductionVersions {
		if !slices.Contains(versions, version) {
			t.Fatalf("production fixture uses unsupported compiler %s", version)
		}
		name := "immutable"
		if version == "0.2.1" {
			name = "constructor"
		}
		for _, suffix := range []string{"input", "output"} {
			if _, err := os.ReadFile(filepath.Join("../../internal/verify/testdata/compiler/vyper/versions", version, name+"."+suffix+".json")); err != nil {
				t.Fatal(err)
			}
		}
	}
}
