//go:build runtimee2e && hardhat3e2e

package runtimee2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/testcompose"
)

const hardhat3E2ETimeout = 30 * time.Minute

type hardhatRuntime struct {
	image string
}

type hardhatDeployment struct {
	Implementation   string `json:"implementation"`
	ImplementationV2 string `json:"implementationV2"`
	Proxy            string `json:"proxy"`
}

type hardhatProxySnapshot struct {
	AddressJobs        int64
	YulJobs            int64
	ProxyJobs          int64
	CompilerResults    int64
	YulResults         int64
	ProxyResults       int64
	ProxyBindings      int64
	CatalogEntries     int64
	CurrentProxy       string
	CurrentImpl        string
	CurrentProxyKind   string
	ExecutorProvenance bool
	YulProvenance      bool
	CompilerProvenance bool
}

func TestHardhat3ProductionE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), hardhat3E2ETimeout)
	defer cancel()
	root := repositoryRoot(t)
	runtime := prepareHardhatRuntime(t, ctx)

	results := make(map[string]hardhatProxySnapshot, 2)
	for _, mode := range []string{"monolith", "distributed"} {
		if !t.Run(mode, func(t *testing.T) {
			results[mode] = runHardhat3Mode(t, ctx, root, mode, runtime)
		}) {
			return
		}
	}
	if !reflect.DeepEqual(results["monolith"], results["distributed"]) {
		t.Fatalf("Hardhat 3 topology parity mismatch\nmonolith: %#v\ndistributed: %#v",
			results["monolith"], results["distributed"])
	}
}

func prepareHardhatRuntime(t *testing.T, ctx context.Context) hardhatRuntime {
	t.Helper()
	tag := valueOrDefault("ETHERVIEW_HARDHAT3_IMAGE", "etherview-hardhat3:local")
	docker := dockerCommand()
	inspect := exec.CommandContext(ctx, docker, "image", "inspect", "--format", "{{.Id}}", tag)
	output, err := inspect.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect Hardhat client image %q: %v\n%s", tag, err, strings.TrimSpace(string(output)))
	}
	imageID := strings.TrimSpace(string(output))
	if !strings.HasPrefix(imageID, "sha256:") || len(strings.TrimPrefix(imageID, "sha256:")) != 64 {
		t.Fatalf("Hardhat client image ID = %q, want exact sha256 digest", imageID)
	}
	assertNativeImageArchitecture(t, ctx, docker, valueOrDefault("IMAGE", "etherview:local"))
	return hardhatRuntime{image: tag}
}

func assertNativeImageArchitecture(
	t *testing.T,
	ctx context.Context,
	docker, image string,
) {
	t.Helper()
	inspect := exec.CommandContext(
		ctx, docker, "image", "inspect", "--format", "{{.Architecture}}", image,
	)
	imageOutput, err := inspect.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect application image architecture %q: %v\n%s",
			image, err, strings.TrimSpace(string(imageOutput)))
	}
	info := exec.CommandContext(ctx, docker, "info", "--format", "{{.Architecture}}")
	hostOutput, err := info.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect Docker host architecture: %v\n%s",
			err, strings.TrimSpace(string(hostOutput)))
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
	if imageArchitecture == "" || hostArchitecture == "" ||
		imageArchitecture != hostArchitecture {
		t.Fatalf("application image architecture %q does not match Docker host architecture %q",
			imageArchitecture, hostArchitecture)
	}
}

func runHardhat3Mode(
	t *testing.T,
	ctx context.Context,
	root, mode string,
	runtime hardhatRuntime,
) hardhatProxySnapshot {
	t.Helper()
	artifacts, err := os.MkdirTemp("", "etherview-hardhat3-e2e-"+mode+"-")
	if err != nil {
		t.Fatal(err)
	}
	project := testcompose.NewQuiet(
		root,
		testcompose.UniqueProjectName("etherview-hardhat3-"+mode),
		"compose.yaml",
		"e2e/runtime/compose.yaml",
		"e2e/hardhat3/compose.yaml",
	)
	project.Profiles = []string{mode}
	project.Env = runtimeEnvironment(root, uint64(time.Now().UTC().Truncate(time.Hour).Unix()))
	project.Env["ETHERVIEW_HARDHAT3_IMAGE"] = runtime.image
	project.Env["ETHERVIEW_HARDHAT3_ARTIFACT_DIR"] = artifacts
	project.Env["ETHERVIEW_HARDHAT3_API_SERVICE"] = map[string]string{
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
	h.mine(ctx, uint64(time.Now().Unix()))
	first := h.latestBlock(ctx)
	project.Env["ETHERVIEW_CHAIN_GENESIS_HASH"] = genesis.Hash
	h.startTopology(ctx)
	h.connectDatabase(ctx)
	t.Cleanup(func() { captureHardhatSQLDiagnostics(h) })
	h.resolveAPI(ctx)
	h.waitReady(ctx)
	h.waitCanonical(ctx, mustDecodeUint64(t, first.Number), first.Hash)

	h.enterPhase("real Hardhat compile and deployment")
	deploymentFile := filepath.Join(artifacts, "deployment.json")
	runHardhatCommand(t, ctx, h, "", "compile", nil,
		"--build-profile", "production", "compile")
	runNodeCommand(t, ctx, h, "", "deploy", nil, "deploy.mjs")
	var deployment hardhatDeployment
	contents, err := os.ReadFile(deploymentFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, &deployment); err != nil {
		t.Fatalf("decode Hardhat deployment: %v", err)
	}
	for name, address := range map[string]string{
		"implementation":   deployment.Implementation,
		"implementationV2": deployment.ImplementationV2,
		"proxy":            deployment.Proxy,
	} {
		if !common.IsHexAddress(address) {
			t.Fatalf("%s deployment address = %q", name, address)
		}
	}
	waitHardhatCanonicalTip(t, ctx, h)
	waitHardhatProxyObservation(t, ctx, h, deployment.Proxy, deployment.Implementation)

	h.enterPhase("production CLI API key and real Hardhat verify")
	apiKey := createHardhatAPIKey(t, ctx, h)
	submitAndWaitHardhatYul(t, ctx, h, apiKey)
	verificationEnv := map[string]string{}
	verificationEnv["ETHERVIEW_API_KEY"] = apiKey
	runHardhatCommand(t, ctx, h, apiKey, "verify-implementation", verificationEnv,
		"--build-profile", "production", "--network", "etherview",
		"verify", "etherscan", "--contract", "contracts/Implementation.sol:Implementation",
		deployment.Implementation)
	verificationEnv["ETHERVIEW_HARDHAT3_PROXY_IMPLEMENTATION"] = deployment.Implementation
	runHardhatCommand(t, ctx, h, apiKey, "verify-proxy", verificationEnv,
		"--build-profile", "production", "--network", "etherview",
		"verify", "etherscan", "--contract", "contracts/TestERC1967Proxy.sol:TestERC1967Proxy",
		"--constructor-args-path", "constructor-args.mjs", deployment.Proxy)
	waitHardhatSource(t, ctx, h, apiKey, deployment.Implementation)
	waitHardhatSource(t, ctx, h, apiKey, deployment.Proxy)

	h.enterPhase("durable proxy verification")
	before := hardhatProxyJobCount(t, ctx, h)
	if _, err := submitHardhatProxy(t, ctx, h, apiKey, deployment.Proxy, deployment.ImplementationV2); err == nil {
		t.Fatal("wrong expected implementation was accepted")
	}
	if after := hardhatProxyJobCount(t, ctx, h); after != before {
		t.Fatalf("wrong expected implementation created jobs: before=%d after=%d", before, after)
	}
	guid, err := submitHardhatProxy(t, ctx, h, apiKey, deployment.Proxy, deployment.Implementation)
	if err != nil {
		t.Fatal(err)
	}
	waitHardhatProxyStatus(t, ctx, h, apiKey, guid)
	assertHardhatProxySource(t, ctx, h, apiKey, deployment.Proxy, deployment.Implementation, true)

	h.enterPhase("proxy upgrade invalidation and rebinding")
	runNodeCommand(t, ctx, h, apiKey, "upgrade", verificationEnv, "upgrade.mjs")
	waitHardhatCanonicalTip(t, ctx, h)
	waitHardhatProxyObservation(t, ctx, h, deployment.Proxy, deployment.ImplementationV2)
	assertHardhatProxySource(t, ctx, h, apiKey, deployment.Proxy, "", false)
	if _, err := submitHardhatProxy(t, ctx, h, apiKey, deployment.Proxy, deployment.ImplementationV2); !errors.Is(err, errHardhatImplementationUnverified) {
		t.Fatalf("unverified upgraded implementation error = %v", err)
	}
	runHardhatCommand(t, ctx, h, apiKey, "implementation-v2-source", verificationEnv,
		"--build-profile", "production", "--network", "etherview",
		"verify", "etherscan", "--contract", "contracts/Implementation.sol:Implementation",
		deployment.ImplementationV2)
	waitHardhatSource(t, ctx, h, apiKey, deployment.ImplementationV2)
	guid, err = submitHardhatProxy(t, ctx, h, apiKey, deployment.Proxy, deployment.ImplementationV2)
	if err != nil {
		t.Fatal(err)
	}
	waitHardhatProxyStatus(t, ctx, h, apiKey, guid)
	assertHardhatProxySource(t, ctx, h, apiKey, deployment.Proxy, deployment.ImplementationV2, true)

	snapshot := captureHardhatProxySnapshot(t, ctx, h, deployment.Proxy)
	h.writeJSONArtifact(mode+"-proxy-summary.json", snapshot)
	return snapshot
}

func createHardhatAPIKey(t *testing.T, ctx context.Context, h *harness) string {
	t.Helper()
	output, err := h.project.Run(ctx, "exec", "-T", h.apiService,
		"/etherview", "admin", "api-key", "create", "--config=/etc/etherview/config.yaml",
		"--name=hardhat3-e2e", "--rate=100", "--burst=200")
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

func submitAndWaitHardhatYul(
	t *testing.T,
	ctx context.Context,
	h *harness,
	apiKey string,
) {
	t.Helper()
	runNodeCommand(t, ctx, h, apiKey, "compile-yul", nil, "compile-yul.mjs")
	submission, err := os.ReadFile(filepath.Join(h.artifacts, "yul-submission.json"))
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		h.baseURL+"/api/v1/verifier/solidity/standard-json",
		bytes.NewReader(submission),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-API-Key", apiKey)
	response, err := h.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close() //nolint:errcheck
	var accepted struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if response.StatusCode != http.StatusAccepted ||
		json.NewDecoder(response.Body).Decode(&accepted) != nil ||
		accepted.Data.ID == "" {
		t.Fatalf("Yul verification submission status=%d response=%#v",
			response.StatusCode, accepted)
	}
	waitFor(t, ctx, "Yul verification "+accepted.Data.ID, func() (bool, string, error) {
		var status string
		var errorCode *string
		err := h.db.QueryRow(ctx, `
			SELECT status, error_code
			FROM verification_jobs
			WHERE id = $1::uuid`, accepted.Data.ID).Scan(&status, &errorCode)
		if err != nil {
			return false, "", err
		}
		if status == "failed" || status == "cancelled" {
			return false, status, fmt.Errorf("Yul verification failed with %v", errorCode)
		}
		if status != "succeeded" {
			return false, status, nil
		}
		var resultCount int64
		err = h.db.QueryRow(ctx, `
			SELECT count(*)
			FROM verification_results
			WHERE job_id = $1::uuid
			  AND outcome_kind = 'verification_success'`, accepted.Data.ID).Scan(&resultCount)
		return resultCount == 1, fmt.Sprintf("status=%s results=%d", status, resultCount), err
	})
}

func runHardhatCommand(
	t *testing.T,
	ctx context.Context,
	h *harness,
	secret, artifact string,
	environment map[string]string,
	arguments ...string,
) {
	t.Helper()
	runHardhatContainerCommand(t, ctx, h, secret, artifact,
		environment, append([]string{"npx", "hardhat"}, arguments...)...)
}

func runNodeCommand(
	t *testing.T,
	ctx context.Context,
	h *harness,
	secret, artifact string,
	environment map[string]string,
	arguments ...string,
) {
	t.Helper()
	runHardhatContainerCommand(t, ctx, h, secret, artifact,
		environment, append([]string{"node"}, arguments...)...)
}

func runHardhatContainerCommand(
	t *testing.T,
	ctx context.Context,
	h *harness,
	secret, artifact string,
	environment map[string]string,
	arguments ...string,
) {
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
	composeArguments = append(composeArguments, "hardhat")
	composeArguments = append(composeArguments, arguments...)
	output, err := h.project.Run(ctx, composeArguments...)
	redacted := output
	if secret != "" {
		redacted = bytes.ReplaceAll(redacted, []byte(secret), []byte("[REDACTED]"))
	}
	h.writeArtifact(artifact+".log", redacted)
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", artifact, err, strings.TrimSpace(string(redacted)))
	}
}

type etherscanEnvelope struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Result  json.RawMessage `json:"result"`
}

var errHardhatImplementationUnverified = errors.New("proxy implementation source code not verified")

func hardhatEtherscanRequest(
	t *testing.T,
	ctx context.Context,
	h *harness,
	apiKey, method string,
	values url.Values,
) (json.RawMessage, error) {
	t.Helper()
	values.Set("chainid", "1")
	values.Set("module", "contract")
	var request *http.Request
	var err error
	if method == http.MethodPost {
		request, err = http.NewRequestWithContext(ctx, method, h.baseURL+"/v2/api", strings.NewReader(values.Encode()))
		if err == nil {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	} else {
		request, err = http.NewRequestWithContext(ctx, method, h.baseURL+"/v2/api?"+values.Encode(), nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-API-Key", apiKey)
	response, err := h.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close() //nolint:errcheck
	var envelope etherscanEnvelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	var result string
	if response.StatusCode != http.StatusOK || envelope.Status != "1" {
		if err := json.Unmarshal(envelope.Result, &result); err != nil {
			return nil, fmt.Errorf("decode Etherscan error result: %w", err)
		}
		switch result {
		case "proxy implementation source code not verified":
			return nil, errHardhatImplementationUnverified
		default:
			return nil, fmt.Errorf("Etherscan %s: status=%s message=%s result=%s",
				values.Get("action"), envelope.Status, envelope.Message, result)
		}
	}
	return envelope.Result, nil
}

func submitHardhatProxy(
	t *testing.T,
	ctx context.Context,
	h *harness,
	apiKey, proxy, expected string,
) (string, error) {
	t.Helper()
	raw, err := hardhatEtherscanRequest(t, ctx, h, apiKey, http.MethodPost, url.Values{
		"action":                 {"verifyproxycontract"},
		"address":                {proxy},
		"expectedimplementation": {expected},
	})
	if err != nil {
		return "", err
	}
	var guid string
	if err := json.Unmarshal(raw, &guid); err != nil {
		return "", fmt.Errorf("decode proxy verification GUID: %w", err)
	}
	return guid, nil
}

func waitHardhatProxyStatus(t *testing.T, ctx context.Context, h *harness, apiKey, guid string) {
	t.Helper()
	sawPending := false
	waitFor(t, ctx, "proxy verification "+guid, func() (bool, string, error) {
		raw, err := hardhatEtherscanRequest(t, ctx, h, apiKey, http.MethodGet, url.Values{
			"action": {"checkproxyverification"}, "guid": {guid},
		})
		if err != nil {
			if strings.Contains(err.Error(), "Pending in queue") {
				sawPending = true
				return false, "pending", nil
			}
			return false, "", err
		}
		var result string
		if err := json.Unmarshal(raw, &result); err != nil {
			return false, "", err
		}
		return result == "Pass - Verified", result, nil
	})
	if !sawPending {
		t.Fatal("proxy verification completed without an observed pending state")
	}
}

func waitHardhatSource(t *testing.T, ctx context.Context, h *harness, apiKey, address string) {
	t.Helper()
	waitFor(t, ctx, "verified source "+address, func() (bool, string, error) {
		result, err := hardhatEtherscanRequest(t, ctx, h, apiKey, http.MethodGet, url.Values{
			"action": {"getsourcecode"}, "address": {address},
		})
		return err == nil && bytes.Contains(result, []byte(`"ContractName"`)), string(result), err
	})
}

func assertHardhatProxySource(
	t *testing.T,
	ctx context.Context,
	h *harness,
	apiKey, proxy, implementation string,
	wantBound bool,
) {
	t.Helper()
	result, err := hardhatEtherscanRequest(t, ctx, h, apiKey, http.MethodGet, url.Values{
		"action": {"getsourcecode"}, "address": {proxy},
	})
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Proxy          string `json:"Proxy"`
		Implementation string `json:"Implementation"`
	}
	if err := json.Unmarshal(result, &rows); err != nil || len(rows) != 1 {
		t.Fatalf("decode getsourcecode proxy result: rows=%#v error=%v", rows, err)
	}
	if wantBound {
		if rows[0].Proxy != "1" || rows[0].Implementation != common.HexToAddress(implementation).Hex() {
			t.Fatalf("proxy source binding = %#v, want implementation %s", rows[0], implementation)
		}
		return
	}
	if rows[0].Proxy != "0" || rows[0].Implementation != "" {
		t.Fatalf("stale proxy binding remained public: %#v", rows[0])
	}
}

func waitHardhatCanonicalTip(t *testing.T, ctx context.Context, h *harness) {
	t.Helper()
	latest := h.latestBlock(ctx)
	h.waitCanonical(ctx, mustDecodeUint64(t, latest.Number), latest.Hash)
}

func waitHardhatProxyObservation(
	t *testing.T,
	ctx context.Context,
	h *harness,
	proxy, implementation string,
) {
	t.Helper()
	waitFor(t, ctx, "canonical proxy observation "+implementation, func() (bool, string, error) {
		var got string
		err := h.db.QueryRow(ctx, `
			SELECT '0x' || encode(observation.implementation_address, 'hex')
			FROM proxy_observations AS observation
			JOIN canonical_blocks AS canonical
			  ON canonical.chain_id = observation.chain_id
			 AND canonical.number = observation.block_number
			 AND canonical.block_hash = observation.block_hash
			WHERE observation.chain_id = 1
			  AND observation.proxy_address = $1
			  AND observation.canonical
			ORDER BY observation.block_number DESC
			LIMIT 1`, common.HexToAddress(proxy).Bytes()).Scan(&got)
		return err == nil && strings.EqualFold(got, implementation), got, err
	})
}

func hardhatProxyJobCount(t *testing.T, ctx context.Context, h *harness) int64 {
	t.Helper()
	var count int64
	if err := h.db.QueryRow(ctx, `SELECT count(*) FROM verification_jobs WHERE kind = 'proxy'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func captureHardhatSQLDiagnostics(h *harness) {
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
		             compiler_platform, catalog_generation_id,
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
		      SELECT job_id, outcome_kind, created_at
		      FROM verification_results
		    ) AS row
		  ), '[]'::jsonb),
		  'proxy_bindings', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.created_at, row.verification_job_id)
		    FROM (
		      SELECT verification_job_id, proxy_kind,
		             '0x' || encode(proxy_address, 'hex') AS proxy_address,
		             '0x' || encode(implementation_address, 'hex') AS implementation_address,
		             '0x' || encode(observation_block_hash, 'hex') AS observation_block_hash,
		             created_at
		      FROM verified_proxy_contracts
		    ) AS row
		  ), '[]'::jsonb)
		)::text`).Scan(&summary)
	if err != nil {
		h.writeArtifact("verification-sql-summary-error.txt", []byte(diagnosticError(err)))
		return
	}
	h.writeArtifact("verification-sql-summary.json", []byte(summary))
}

func captureHardhatProxySnapshot(
	t *testing.T,
	ctx context.Context,
	h *harness,
	proxy string,
) hardhatProxySnapshot {
	t.Helper()
	var result hardhatProxySnapshot
	if err := h.db.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE kind = 'address' AND status = 'succeeded'),
		  count(*) FILTER (WHERE language = 'yul' AND status = 'succeeded'),
		  count(*) FILTER (WHERE kind = 'proxy' AND status = 'succeeded'),
		  count(*) FILTER (WHERE kind = 'address' AND status = 'succeeded'
		    AND compiler_platform = 'emscripten-wasm32'
		    AND catalog_generation_id IS NOT NULL
		    AND executor_digest IS NOT NULL
		    AND executor_kind = 'node_solcjs_v1'
		    AND execution_policy = 'trusted_subprocess') = 3,
		  count(*) FILTER (WHERE language = 'yul' AND status = 'succeeded'
		    AND compiler_version = '0.8.30+commit.73712a01'
		    AND compiler_platform = 'emscripten-wasm32'
		    AND catalog_generation_id IS NOT NULL
		    AND compiler_digest IS NOT NULL
		    AND executor_digest IS NOT NULL
		    AND executor_kind = 'node_solcjs_v1'
		    AND execution_policy = 'trusted_subprocess') = 1,
		  count(*) FILTER (WHERE kind = 'address' AND status = 'succeeded'
		    AND compiler_digest IS NOT NULL) = 3
		FROM verification_jobs`).Scan(
		&result.AddressJobs, &result.YulJobs, &result.ProxyJobs,
		&result.ExecutorProvenance, &result.YulProvenance,
		&result.CompilerProvenance,
	); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE outcome_kind = 'verification_success'),
		  count(*) FILTER (WHERE outcome_kind = 'verification_success'
		    AND job_id IN (SELECT id FROM verification_jobs WHERE language = 'yul')),
		  count(*) FILTER (WHERE outcome_kind = 'proxy_verification_success')
		FROM verification_results`).Scan(
		&result.CompilerResults, &result.YulResults, &result.ProxyResults,
	); err != nil {
		t.Fatal(err)
	}
	if result.YulJobs != 1 || result.YulResults != 1 || !result.YulProvenance {
		t.Fatalf("Yul compiler provenance is incomplete: %#v", result)
	}
	if err := h.db.QueryRow(ctx, `SELECT count(*) FROM verified_proxy_contracts`).Scan(&result.ProxyBindings); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(ctx, `SELECT count(*) FROM compiler_catalog_entries`).Scan(&result.CatalogEntries); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(ctx, `
		SELECT '0x' || encode(observation.proxy_address, 'hex'),
		       '0x' || encode(observation.implementation_address, 'hex'),
		       observation.proxy_kind
		FROM proxy_observations AS observation
		JOIN canonical_blocks AS canonical
		  ON canonical.chain_id = observation.chain_id
		 AND canonical.number = observation.block_number
		 AND canonical.block_hash = observation.block_hash
		WHERE observation.chain_id = 1
		  AND observation.proxy_address = $1
		  AND observation.canonical
		ORDER BY observation.block_number DESC
		LIMIT 1`, common.HexToAddress(proxy).Bytes()).Scan(
		&result.CurrentProxy, &result.CurrentImpl, &result.CurrentProxyKind,
	); err != nil {
		t.Fatal(err)
	}
	if result.AddressJobs != 3 || result.ProxyJobs != 2 ||
		result.CompilerResults != 4 || result.ProxyResults != 2 ||
		result.ProxyBindings != 2 || result.CatalogEntries == 0 ||
		!result.ExecutorProvenance || !result.CompilerProvenance ||
		result.CurrentProxyKind != "eip1967" {
		t.Fatalf("incomplete Hardhat/proxy persistence: %#v", result)
	}
	return result
}
