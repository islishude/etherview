//go:build runtimee2e && foundrye2e

package runtimee2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/testcompose"
)

const (
	foundryE2ETimeout       = 30 * time.Minute
	foundryContract         = "src/FoundryVerification.sol:FoundryVerification"
	foundryCompilerVersion  = "0.8.30+commit.73712a01"
	foundryCatalogSource    = "https://binaries.soliditylang.org/emscripten-wasm32/list.json"
	foundryCompilerArtifact = "https://binaries.soliditylang.org/emscripten-wasm32/solc-emscripten-wasm32-v0.8.30+commit.73712a01.js"
	foundryCompilerDigest   = "81475c98b6d2094a821fd9d7b6278556d8095ccc23e0b8a1029b1c08a89cd4b2"
	foundryConstructorWord  = "000000000000000000000000000000000000000000000000000000000000002a"
	foundryVersion          = "1.7.1"
	foundryRevision         = "4072e48705af9d93e3c0f6e29e93b5e9a40caed8"
)

type foundryRuntime struct {
	image string
}

type foundryDeployment struct {
	Deployer        string `json:"deployer"`
	DeployedTo      string `json:"deployedTo"`
	TransactionHash string `json:"transactionHash"`
}

type foundryVerificationSnapshot struct {
	AddressJobs              int64
	SuccessfulJobs           int64
	Results                  int64
	Publications             int64
	CatalogEntries           int64
	StandardJSON             bool
	Kind                     string
	Language                 string
	CompilerVersion          string
	CompilerPlatform         string
	CompilerDigest           string
	CatalogSource            string
	CompilerArtifact         string
	ExecutorKind             string
	ExecutionPolicy          string
	ExecutorDigest           string
	FileName                 string
	ContractName             string
	MatchType                string
	ConstructorArguments     string
	ResultImmutableGroups    int64
	PublishedImmutableGroups int64
	PublicationMatchesResult bool
	PublicABIEntries         int
	PublicExpectedFunctions  bool
	PublicContractName       string
	PublicCompilerVersion    string
	PublicMatchType          string
}

type foundryEtherscanEnvelope struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Result  json.RawMessage `json:"result"`
}

func TestFoundryProductionVerificationE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), foundryE2ETimeout)
	defer cancel()
	root := repositoryRoot(t)
	runtime := prepareFoundryRuntime(t, ctx)

	results := make(map[string]foundryVerificationSnapshot, 2)
	for _, mode := range []string{"monolith", "distributed"} {
		if !t.Run(mode, func(t *testing.T) {
			results[mode] = runFoundryMode(t, ctx, root, mode, runtime)
		}) {
			return
		}
	}
	if !reflect.DeepEqual(results["monolith"], results["distributed"]) {
		t.Fatalf("Foundry topology parity mismatch\nmonolith: %#v\ndistributed: %#v",
			results["monolith"], results["distributed"])
	}
}

func prepareFoundryRuntime(t *testing.T, ctx context.Context) foundryRuntime {
	t.Helper()
	tag := valueOrDefault("ETHERVIEW_FOUNDRY_IMAGE", "etherview-foundry:local")
	docker := dockerCommand()
	inspect := exec.CommandContext(ctx, docker, "image", "inspect", "--format",
		"{{.Id}}|{{index .Config.Labels \"org.opencontainers.image.version\"}}|{{index .Config.Labels \"org.opencontainers.image.revision\"}}", tag)
	output, err := inspect.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect Foundry client image %q: %v\n%s", tag, err, strings.TrimSpace(string(output)))
	}
	parts := strings.Split(strings.TrimSpace(string(output)), "|")
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "sha256:") ||
		len(strings.TrimPrefix(parts[0], "sha256:")) != 64 {
		t.Fatalf("Foundry client image identity = %q, want exact sha256 digest and labels", output)
	}
	if parts[1] != "v"+foundryVersion || parts[2] != foundryRevision {
		t.Fatalf("Foundry client base identity = version %q revision %q", parts[1], parts[2])
	}
	version := exec.CommandContext(ctx, docker, "run", "--rm", tag, "forge", "--version")
	versionOutput, err := version.CombinedOutput()
	if err != nil {
		t.Fatalf("run pinned Foundry client: %v\n%s", err, strings.TrimSpace(string(versionOutput)))
	}
	versionText := string(versionOutput)
	if !strings.Contains(versionText, "forge Version: "+foundryVersion) ||
		!strings.Contains(versionText, "Commit SHA: "+foundryRevision) {
		t.Fatalf("Foundry version output = %q", strings.TrimSpace(versionText))
	}
	assertFoundryNativeImageArchitecture(t, ctx, docker, valueOrDefault("IMAGE", "etherview:local"))
	assertFoundryNativeImageArchitecture(t, ctx, docker, tag)
	return foundryRuntime{image: tag}
}

func assertFoundryNativeImageArchitecture(t *testing.T, ctx context.Context, docker, image string) {
	t.Helper()
	inspect := exec.CommandContext(ctx, docker, "image", "inspect", "--format", "{{.Architecture}}", image)
	imageOutput, err := inspect.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect image architecture %q: %v\n%s", image, err, strings.TrimSpace(string(imageOutput)))
	}
	info := exec.CommandContext(ctx, docker, "info", "--format", "{{.Architecture}}")
	hostOutput, err := info.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect Docker host architecture: %v\n%s", err, strings.TrimSpace(string(hostOutput)))
	}
	normalize := func(value string) string {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "amd64", "x86_64", "x86-64":
			return "amd64"
		case "arm64", "aarch64":
			return "arm64"
		default:
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	imageArchitecture := normalize(string(imageOutput))
	hostArchitecture := normalize(string(hostOutput))
	if imageArchitecture == "" || hostArchitecture == "" || imageArchitecture != hostArchitecture {
		t.Fatalf("image %q architecture %q does not match Docker host architecture %q",
			image, imageArchitecture, hostArchitecture)
	}
}

func runFoundryMode(
	t *testing.T,
	ctx context.Context,
	root, mode string,
	runtime foundryRuntime,
) foundryVerificationSnapshot {
	t.Helper()
	artifacts, err := os.MkdirTemp("", "etherview-foundry-e2e-"+mode+"-")
	if err != nil {
		t.Fatal(err)
	}
	project := testcompose.NewQuiet(
		root,
		testcompose.UniqueProjectName("etherview-foundry-"+mode),
		"compose.yaml",
		"e2e/runtime/compose.yaml",
		"e2e/foundry/compose.yaml",
	)
	project.Profiles = []string{mode}
	project.Env = runtimeEnvironment(root, uint64(time.Now().UTC().Truncate(time.Hour).Unix()))
	project.Env["ETHERVIEW_FOUNDRY_IMAGE"] = runtime.image
	project.Env["ETHERVIEW_FOUNDRY_API_SERVICE"] = map[string]string{
		"monolith": "etherview", "distributed": "api",
	}[mode]
	h := &harness{
		t: t, root: root, mode: mode, project: project,
		apiService: map[string]string{"monolith": "etherview", "distributed": "api"}[mode],
		http:       &http.Client{Timeout: 10 * time.Second},
		artifacts:  artifacts,
		phase:      "initialization",
	}
	t.Cleanup(func() { h.cleanup() })

	h.enterPhase("isolated Anvil and production topology")
	if err := project.Up(ctx, "runtime-fixture"); err != nil {
		t.Fatal(err)
	}
	binding, err := project.Port(ctx, "runtime-fixture", 8545)
	if err != nil {
		t.Fatal(err)
	}
	hostRPC := "http://" + binding
	h.rpc, err = rpc.DialContext(ctx, hostRPC)
	if err != nil {
		t.Fatal(err)
	}
	h.rpcProxy = startReceiptProxy(t, hostRPC)
	project.Env["ETHERVIEW_RUNTIME_RPC_URL"] = h.rpcProxy.containerURL()
	var genesis rpcBlock
	h.rpcCall(ctx, &genesis, "eth_getBlockByNumber", "0x0", false)
	h.fixture.genesisHash = genesis.Hash
	h.rpcCall(ctx, &h.fixture.accounts, "eth_accounts")
	if len(h.fixture.accounts) == 0 {
		t.Fatal("Anvil returned no unlocked deployment account")
	}
	h.mine(ctx, uint64(time.Now().Unix()))
	first := h.latestBlock(ctx)
	project.Env["ETHERVIEW_CHAIN_GENESIS_HASH"] = genesis.Hash
	h.startTopology(ctx)
	h.connectDatabase(ctx)
	t.Cleanup(func() { captureFoundrySQLDiagnostics(h) })
	h.resolveAPI(ctx)
	h.waitReady(ctx)
	h.waitCanonical(ctx, mustDecodeUint64(t, first.Number), first.Hash)
	h.enterPhase("official Solidity compiler catalog")
	waitFoundryCompilerCatalog(t, ctx, h)

	h.enterPhase("real Forge deployment")
	var ignored any
	h.rpcCall(ctx, &ignored, "evm_setAutomine", true)
	deploymentOutput := runFoundryCommand(t, ctx, h, "forge-create", nil,
		"forge", "create", foundryContract,
		"--broadcast", "--unlocked", "--from", h.fixture.accounts[0],
		"--rpc-url", "http://runtime-fixture:8545", "--chain", "1",
		"--offline", "--json", "--constructor-args", "42")
	var deployment foundryDeployment
	deploymentJSON := foundryJSONObject(deploymentOutput)
	if err := json.Unmarshal(deploymentJSON, &deployment); err != nil {
		t.Fatalf("decode Forge deployment: %v\n%s", err, strings.TrimSpace(string(deploymentOutput)))
	}
	if !common.IsHexAddress(deployment.Deployer) ||
		!common.IsHexAddress(deployment.DeployedTo) ||
		!common.IsHexHash(deployment.TransactionHash) {
		t.Fatalf("invalid Forge deployment: %#v", deployment)
	}
	waitFoundryCanonicalTip(t, ctx, h)

	h.enterPhase("unverified ABI probe and production API key")
	apiKey := createFoundryAPIKey(t, ctx, h)
	h.secrets = append(h.secrets, apiKey)
	assertFoundryUnverifiedABI(t, ctx, h, apiKey, deployment.DeployedTo)
	if jobs := foundryAddressJobCount(t, ctx, h, deployment.DeployedTo); jobs != 0 {
		t.Fatalf("unverified ABI probe created %d verification jobs", jobs)
	}

	h.enterPhase("real Forge Standard JSON verification and POST watch")
	verifierURL := fmt.Sprintf("http://%s:8080/v2/api?chainid=1", h.apiService)
	verificationOutput := runFoundryCommand(t, ctx, h, "forge-verify", map[string]string{
		"VERIFIER_API_KEY": apiKey,
	},
		"forge", "verify-contract", deployment.DeployedTo, foundryContract,
		"--verifier", "custom", "--verifier-url", verifierURL,
		"--chain", "1", "--watch", "--compiler-version", "v"+foundryCompilerVersion,
		"--constructor-args", foundryConstructorWord)
	for _, expected := range []string{
		"Submitting verification for",
		"Submitted contract for verification",
		"Contract verification status",
		"Contract successfully verified",
	} {
		if !bytes.Contains(verificationOutput, []byte(expected)) {
			t.Fatalf("Forge verification did not report %q:\n%s", expected, strings.TrimSpace(string(verificationOutput)))
		}
	}
	assertFoundryPOSTWatch(t, ctx, h)
	waitFoundryVerifiedABI(t, ctx, h, apiKey, deployment.DeployedTo)
	beforeRepeat := foundryAddressJobCount(t, ctx, h, deployment.DeployedTo)
	if beforeRepeat != 1 {
		t.Fatalf("successful Forge verification jobs = %d, want 1", beforeRepeat)
	}

	h.enterPhase("Forge already-verified short circuit")
	repeatOutput := runFoundryCommand(t, ctx, h, "forge-verify-repeat", map[string]string{
		"VERIFIER_API_KEY": apiKey,
	},
		"forge", "verify-contract", deployment.DeployedTo, foundryContract,
		"--verifier", "custom", "--verifier-url", verifierURL,
		"--chain", "1", "--watch", "--compiler-version", "v"+foundryCompilerVersion,
		"--constructor-args", foundryConstructorWord)
	if !bytes.Contains(repeatOutput, []byte("is already verified. Skipping verification")) {
		t.Fatalf("repeated Forge verification did not short circuit:\n%s", strings.TrimSpace(string(repeatOutput)))
	}
	if afterRepeat := foundryAddressJobCount(t, ctx, h, deployment.DeployedTo); afterRepeat != beforeRepeat {
		t.Fatalf("repeated Forge verification created a job: before=%d after=%d", beforeRepeat, afterRepeat)
	}

	h.enterPhase("durable and public verification provenance")
	snapshot := captureFoundryVerificationSnapshot(t, ctx, h, apiKey, deployment.DeployedTo)
	h.writeJSONArtifact(mode+"-foundry-summary.json", snapshot)
	return snapshot
}

func foundryJSONObject(output []byte) []byte {
	start := bytes.IndexByte(output, '{')
	end := bytes.LastIndexByte(output, '}')
	if start < 0 || end < start {
		return bytes.TrimSpace(output)
	}
	return output[start : end+1]
}

func runFoundryCommand(
	t *testing.T,
	ctx context.Context,
	h *harness,
	artifact string,
	environment map[string]string,
	arguments ...string,
) []byte {
	t.Helper()
	keys := make([]string, 0, len(environment))
	previous := make(map[string]string, len(environment))
	present := make(map[string]bool, len(environment))
	for key, value := range environment {
		keys = append(keys, key)
		previous[key], present[key] = h.project.Env[key]
		h.project.Env[key] = value
	}
	sort.Strings(keys)
	defer func() {
		for _, key := range keys {
			if present[key] {
				h.project.Env[key] = previous[key]
			} else {
				delete(h.project.Env, key)
			}
		}
	}()
	composeArguments := []string{"run", "--rm", "--no-deps"}
	for _, key := range keys {
		composeArguments = append(composeArguments, "-e", key)
	}
	composeArguments = append(composeArguments, "foundry")
	composeArguments = append(composeArguments, arguments...)
	output, err := h.project.Run(ctx, composeArguments...)
	redacted := h.redact(output)
	h.writeArtifact(artifact+".log", output)
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", artifact, err, strings.TrimSpace(string(redacted)))
	}
	return redacted
}

func createFoundryAPIKey(t *testing.T, ctx context.Context, h *harness) string {
	t.Helper()
	output, err := h.project.Run(ctx, "exec", "-T", h.apiService,
		"/etherview", "admin", "api-key", "create", "--config=/etc/etherview/config.yaml",
		"--name=foundry-e2e", "--rate=100", "--burst=200")
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(output, &response); err != nil || response.Token == "" {
		t.Fatalf("decode production API-key output: %v", err)
	}
	return response.Token
}

func foundryEtherscanRequest(
	t *testing.T,
	ctx context.Context,
	h *harness,
	apiKey string,
	values url.Values,
) foundryEtherscanEnvelope {
	t.Helper()
	values.Set("chainid", "1")
	values.Set("module", "contract")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.baseURL+"/v2/api?"+values.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-API-Key", apiKey)
	response, err := h.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close() //nolint:errcheck
	var envelope foundryEtherscanEnvelope
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		t.Fatalf("Etherscan %s status=%d body=%s", values.Get("action"), response.StatusCode, h.redact(body))
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	return envelope
}

func assertFoundryUnverifiedABI(t *testing.T, ctx context.Context, h *harness, apiKey, address string) {
	t.Helper()
	envelope := foundryEtherscanRequest(t, ctx, h, apiKey, url.Values{
		"action": {"getabi"}, "address": {address},
	})
	var result string
	if err := json.Unmarshal(envelope.Result, &result); err != nil {
		t.Fatal(err)
	}
	if envelope.Status != "0" || envelope.Message != "NOTOK" || result != "Contract source code not verified" {
		t.Fatalf("initial getabi response = %#v result=%q", envelope, result)
	}
}

func waitFoundryVerifiedABI(t *testing.T, ctx context.Context, h *harness, apiKey, address string) {
	t.Helper()
	waitFor(t, ctx, "Foundry verified ABI "+address, func() (bool, string, error) {
		envelope := foundryEtherscanRequest(t, ctx, h, apiKey, url.Values{
			"action": {"getabi"}, "address": {address},
		})
		return envelope.Status == "1" && envelope.Message == "OK", string(envelope.Result), nil
	})
}

func assertFoundryPOSTWatch(t *testing.T, ctx context.Context, h *harness) {
	t.Helper()
	output, err := h.project.Run(ctx, "logs", "--no-color", h.apiService)
	if err != nil {
		t.Fatal(err)
	}
	const requestMarker = `"method":"POST","route":"/v2/api"`
	if requests := bytes.Count(output, []byte(requestMarker)); requests < 2 {
		t.Fatalf("Foundry source submission and status watch produced %d POST requests, want at least 2", requests)
	}
}

func waitFoundryCanonicalTip(t *testing.T, ctx context.Context, h *harness) {
	t.Helper()
	latest := h.latestBlock(ctx)
	h.waitCanonical(ctx, mustDecodeUint64(t, latest.Number), latest.Hash)
}

func waitFoundryCompilerCatalog(t *testing.T, ctx context.Context, h *harness) {
	t.Helper()
	waitFor(t, ctx, "official Solidity 0.8.30 compiler catalog", func() (bool, string, error) {
		var source, artifact, digest string
		err := h.db.QueryRow(ctx, `
			SELECT COALESCE(max(generation.source_url), ''),
			       COALESCE(max(entry.artifact_url), ''),
			       COALESCE(max(encode(entry.artifact_sha256, 'hex')), '')
			FROM compiler_catalog_entries AS entry
			JOIN compiler_catalog_generations AS generation
			  ON generation.id = entry.generation_id AND generation.language = entry.language
			JOIN compiler_catalog_heads AS head
			  ON head.language = entry.language AND head.generation_id = entry.generation_id
			WHERE entry.language = 'solidity'
			  AND entry.version = $1
			  AND entry.platform = 'emscripten-wasm32'`, foundryCompilerVersion).Scan(
			&source, &artifact, &digest,
		)
		state := fmt.Sprintf("source=%s artifact=%s digest=%s", source, artifact, digest)
		return err == nil && source == foundryCatalogSource && artifact == foundryCompilerArtifact &&
			digest == foundryCompilerDigest, state, err
	})
}

func foundryAddressJobCount(t *testing.T, ctx context.Context, h *harness, address string) int64 {
	t.Helper()
	var count int64
	if err := h.db.QueryRow(ctx, `
		SELECT count(*)
		FROM verification_jobs
		WHERE kind = 'address' AND chain_id = 1 AND address = $1`,
		common.HexToAddress(address).Bytes()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func captureFoundryVerificationSnapshot(
	t *testing.T,
	ctx context.Context,
	h *harness,
	apiKey, address string,
) foundryVerificationSnapshot {
	t.Helper()
	var snapshot foundryVerificationSnapshot
	addressBytes := common.HexToAddress(address).Bytes()
	if err := h.db.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE status = 'succeeded')
		FROM verification_jobs
		WHERE kind = 'address' AND chain_id = 1 AND address = $1`, addressBytes).Scan(
		&snapshot.AddressJobs, &snapshot.SuccessfulJobs,
	); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(ctx, `
		SELECT count(*)
		FROM verification_results AS result
		JOIN verification_jobs AS job ON job.id = result.job_id
		WHERE job.kind = 'address' AND job.chain_id = 1 AND job.address = $1
		  AND result.outcome_kind = 'verification_success'`, addressBytes).Scan(&snapshot.Results); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(ctx, `
		SELECT count(*)
		FROM verified_contracts
		WHERE chain_id = 1 AND address = $1`, addressBytes).Scan(&snapshot.Publications); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(ctx, `SELECT count(*) FROM compiler_catalog_entries`).Scan(&snapshot.CatalogEntries); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(ctx, `
		SELECT
		  jsonb_typeof(job.request->'standard_json') = 'object'
		    AND job.request->'standard_json'->>'language' = 'Solidity'
		    AND job.request->>'contract_name_hint' = $2,
		  job.kind,
		  job.language,
		  job.compiler_version,
		  job.compiler_platform,
		  encode(job.compiler_digest, 'hex'),
		  generation.source_url,
		  entry.artifact_url,
		  job.executor_kind,
		  job.execution_policy,
		  encode(job.executor_digest, 'hex'),
		  result.file_name,
		  result.contract_name,
		  result.match_type,
		  encode(result.constructor_arguments, 'hex'),
		  (SELECT count(*) FROM jsonb_object_keys(
		    result.runtime_code_artifacts->'immutableReferences')),
		  (SELECT count(*) FROM jsonb_object_keys(
		    publication.runtime_code_artifacts->'immutableReferences')),
		  result.request_digest = publication.request_digest
		    AND result.file_name = publication.file_name
		    AND result.contract_name = publication.contract_name
		    AND result.runtime_code_artifacts = publication.runtime_code_artifacts
		    AND result.constructor_arguments = publication.constructor_arguments
		FROM verification_jobs AS job
		JOIN verification_results AS result ON result.job_id = job.id
		JOIN verified_contracts AS publication ON publication.verification_job_id = job.id
		JOIN compiler_catalog_entries AS entry
		  ON entry.generation_id = job.catalog_generation_id
		 AND entry.language = job.catalog_language
		 AND entry.version = job.compiler_version
		 AND entry.platform = job.compiler_platform
		 AND entry.artifact_sha256 = job.compiler_digest
		JOIN compiler_catalog_generations AS generation
		  ON generation.id = entry.generation_id AND generation.language = entry.language
		WHERE job.kind = 'address' AND job.chain_id = 1 AND job.address = $1`,
		addressBytes, foundryContract).Scan(
		&snapshot.StandardJSON,
		&snapshot.Kind,
		&snapshot.Language,
		&snapshot.CompilerVersion,
		&snapshot.CompilerPlatform,
		&snapshot.CompilerDigest,
		&snapshot.CatalogSource,
		&snapshot.CompilerArtifact,
		&snapshot.ExecutorKind,
		&snapshot.ExecutionPolicy,
		&snapshot.ExecutorDigest,
		&snapshot.FileName,
		&snapshot.ContractName,
		&snapshot.MatchType,
		&snapshot.ConstructorArguments,
		&snapshot.ResultImmutableGroups,
		&snapshot.PublishedImmutableGroups,
		&snapshot.PublicationMatchesResult,
	); err != nil {
		t.Fatal(err)
	}

	captureFoundryPublicContract(t, ctx, h, apiKey, address, &snapshot)
	if snapshot.AddressJobs != 1 || snapshot.SuccessfulJobs != 1 ||
		snapshot.Results != 1 || snapshot.Publications != 1 || snapshot.CatalogEntries == 0 ||
		!snapshot.StandardJSON || snapshot.Kind != "address" || snapshot.Language != "solidity" ||
		snapshot.CompilerVersion != foundryCompilerVersion ||
		snapshot.CompilerPlatform != "emscripten-wasm32" ||
		snapshot.CompilerDigest != foundryCompilerDigest || snapshot.CatalogSource != foundryCatalogSource ||
		snapshot.CompilerArtifact != foundryCompilerArtifact ||
		snapshot.ExecutorKind != "node_solcjs_v1" ||
		snapshot.ExecutionPolicy != "trusted_subprocess" || len(snapshot.ExecutorDigest) != 64 ||
		snapshot.FileName != "src/FoundryVerification.sol" ||
		snapshot.ContractName != "FoundryVerification" ||
		(snapshot.MatchType != "full" && snapshot.MatchType != "partial") ||
		snapshot.ConstructorArguments != foundryConstructorWord ||
		snapshot.ResultImmutableGroups == 0 || snapshot.PublishedImmutableGroups == 0 ||
		!snapshot.PublicationMatchesResult || snapshot.PublicABIEntries == 0 ||
		!snapshot.PublicExpectedFunctions || snapshot.PublicContractName != "FoundryVerification" ||
		strings.TrimPrefix(snapshot.PublicCompilerVersion, "v") != foundryCompilerVersion ||
		(snapshot.PublicMatchType != "full" && snapshot.PublicMatchType != "partial") {
		t.Fatalf("incomplete Foundry verification persistence: %#v", snapshot)
	}
	return snapshot
}

func captureFoundryPublicContract(
	t *testing.T,
	ctx context.Context,
	h *harness,
	apiKey, address string,
	snapshot *foundryVerificationSnapshot,
) {
	t.Helper()
	abiEnvelope := foundryEtherscanRequest(t, ctx, h, apiKey, url.Values{
		"action": {"getabi"}, "address": {address},
	})
	if abiEnvelope.Status != "1" || abiEnvelope.Message != "OK" {
		t.Fatalf("verified getabi response = %#v", abiEnvelope)
	}
	var encodedABI string
	if err := json.Unmarshal(abiEnvelope.Result, &encodedABI); err != nil {
		t.Fatal(err)
	}
	var entries []struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(encodedABI), &entries); err != nil {
		t.Fatal(err)
	}
	snapshot.PublicABIEntries = len(entries)
	functions := make(map[string]bool)
	for _, entry := range entries {
		if entry.Type == "function" {
			functions[entry.Name] = true
		}
	}
	snapshot.PublicExpectedFunctions = functions["owner"] && functions["seed"] && functions["score"]

	sourceEnvelope := foundryEtherscanRequest(t, ctx, h, apiKey, url.Values{
		"action": {"getsourcecode"}, "address": {address},
	})
	if sourceEnvelope.Status != "1" || sourceEnvelope.Message != "OK" {
		t.Fatalf("verified getsourcecode response = %#v", sourceEnvelope)
	}
	var rows []struct {
		ContractName    string `json:"ContractName"`
		CompilerVersion string `json:"CompilerVersion"`
		MatchKind       string `json:"MatchKind"`
	}
	if err := json.Unmarshal(sourceEnvelope.Result, &rows); err != nil || len(rows) != 1 {
		t.Fatalf("decode getsourcecode: rows=%#v error=%v", rows, err)
	}
	snapshot.PublicContractName = rows[0].ContractName
	snapshot.PublicCompilerVersion = rows[0].CompilerVersion
	snapshot.PublicMatchType = rows[0].MatchKind
}

func captureFoundrySQLDiagnostics(h *harness) {
	if h == nil || h.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var summary string
	err := h.db.QueryRow(ctx, `
		SELECT jsonb_build_object(
		  'jobs', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.created_at, row.id)
		    FROM (
		      SELECT id, kind, status, error_code, outcome_kind,
		             compiler_version, compiler_platform, catalog_generation_id,
		             encode(compiler_digest, 'hex') AS compiler_digest,
		             executor_kind, execution_policy,
		             encode(executor_digest, 'hex') AS executor_digest,
		             created_at
		      FROM verification_jobs
		    ) AS row
		  ), '[]'::jsonb),
		  'results', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.created_at, row.job_id)
		    FROM (
		      SELECT job_id, outcome_kind, file_name, contract_name, compiler_version,
		             match_type, encode(constructor_arguments, 'hex') AS constructor_arguments,
		             runtime_code_artifacts->'immutableReferences' AS immutable_references,
		             created_at
		      FROM verification_results
		    ) AS row
		  ), '[]'::jsonb),
		  'publications', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.created_at, row.verification_job_id)
		    FROM (
		      SELECT verification_job_id,
		             '0x' || encode(address, 'hex') AS address,
		             contract_name, compiler_version, match_type,
		             encode(constructor_arguments, 'hex') AS constructor_arguments,
		             runtime_code_artifacts->'immutableReferences' AS immutable_references,
		             created_at
		      FROM verified_contracts
		    ) AS row
		  ), '[]'::jsonb)
		)::text`).Scan(&summary)
	if err != nil {
		h.writeArtifact("foundry-verification-sql-summary-error.txt", []byte(diagnosticError(err)))
		return
	}
	h.writeArtifact("foundry-verification-sql-summary.json", []byte(summary))
}
