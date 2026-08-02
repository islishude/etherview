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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/api/gen"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/testcompose"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	hardhat3E2ETimeout      = 30 * time.Minute
	hardhatCompilerVersion  = "0.8.30+commit.73712a01"
	hardhatCatalogSource    = "https://binaries.soliditylang.org/emscripten-wasm32/list.json"
	hardhatCompilerArtifact = "https://binaries.soliditylang.org/emscripten-wasm32/solc-emscripten-wasm32-v0.8.30+commit.73712a01.js"
	hardhatCompilerDigest   = "81475c98b6d2094a821fd9d7b6278556d8095ccc23e0b8a1029b1c08a89cd4b2"
)

type hardhatRuntime struct {
	image string
}

type hardhatDeployment struct {
	OpenZeppelinVersion string `json:"openzeppelinVersion"`
	Owner               string `json:"owner"`
	Implementation      string `json:"implementation"`
	ImplementationV2    string `json:"implementationV2"`
	Proxy               string `json:"proxy"`
	Implementations     struct {
		BadUUID string `json:"badUUID"`
	} `json:"implementations"`
	Transparent struct {
		Proxy string `json:"proxy"`
		Admin string `json:"admin"`
	} `json:"transparent"`
	UUPS struct {
		Proxy string `json:"proxy"`
	} `json:"uups"`
	Beacon struct {
		Beacon  string   `json:"beacon"`
		Proxies []string `json:"proxies"`
	} `json:"beacon"`
	Clones struct {
		Factory           string `json:"factory"`
		Standard          string `json:"standard"`
		ImmutableArgs     string `json:"immutableArgs"`
		ImmutableArgsData string `json:"immutableArgsData"`
	} `json:"clones"`
	InitializationData struct {
		Transparent  string `json:"transparent"`
		UUPS         string `json:"uups"`
		BeaconProxyA string `json:"beaconProxyA"`
		BeaconProxyB string `json:"beaconProxyB"`
	} `json:"initializationData"`
	Transactions map[string]hardhatTransaction `json:"transactions"`
}

type hardhatTransaction struct {
	Number string `json:"number"`
	Hash   string `json:"hash"`
	Status string `json:"status"`
	To     string `json:"to"`
}

type hardhatUpgradeReport struct {
	OpenZeppelinVersion   string               `json:"openzeppelinVersion"`
	FailedBadUUIDUpgrade  hardhatTransaction   `json:"failedBadUUIDUpgrade"`
	TransparentUpgrade    hardhatTransaction   `json:"transparentUpgrade"`
	UUPSUpgrade           hardhatTransaction   `json:"uupsUpgrade"`
	BeaconUpgrade         hardhatTransaction   `json:"beaconUpgrade"`
	BeaconInitializers    []hardhatTransaction `json:"beaconReinitializations"`
	CurrentImplementation string               `json:"currentUUPSImplementation"`
	Impact                map[string][]string  `json:"impact"`
}

type hardhatSlotTamperReport struct {
	OpenZeppelinVersion   string               `json:"openzeppelinVersion"`
	CandidateTransactions []hardhatTransaction `json:"candidateTransactions"`
	Transparent           struct {
		Proxy                  string `json:"proxy"`
		RuntimeImmutableAdmin  string `json:"runtimeImmutableAdmin"`
		CompatibilitySlotAdmin string `json:"compatibilitySlotAdmin"`
	} `json:"transparent"`
	Beacon struct {
		Proxies                  []string `json:"proxies"`
		RuntimeImmutableBeacon   string   `json:"runtimeImmutableBeacon"`
		CompatibilitySlotBeacons []string `json:"compatibilitySlotBeacons"`
	} `json:"beacon"`
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
	assertCIArtifactReadable(t, deploymentFile)
	if err := json.Unmarshal(contents, &deployment); err != nil {
		t.Fatalf("decode Hardhat deployment: %v", err)
	}
	if deployment.OpenZeppelinVersion != "5.6.1" {
		t.Fatalf("OpenZeppelin fixture version = %q, want 5.6.1", deployment.OpenZeppelinVersion)
	}
	for name, address := range map[string]string{
		"owner":                   deployment.Owner,
		"implementation":          deployment.Implementation,
		"implementationV2":        deployment.ImplementationV2,
		"badUUIDImplementation":   deployment.Implementations.BadUUID,
		"proxy":                   deployment.Proxy,
		"transparentProxy":        deployment.Transparent.Proxy,
		"transparentProxyAdmin":   deployment.Transparent.Admin,
		"uupsProxy":               deployment.UUPS.Proxy,
		"upgradeableBeacon":       deployment.Beacon.Beacon,
		"cloneFactory":            deployment.Clones.Factory,
		"standardClone":           deployment.Clones.Standard,
		"immutableArgumentsClone": deployment.Clones.ImmutableArgs,
	} {
		if !common.IsHexAddress(address) {
			t.Fatalf("%s deployment address = %q", name, address)
		}
	}
	if len(deployment.Beacon.Proxies) != 2 {
		t.Fatalf("Beacon proxy count = %d, want 2", len(deployment.Beacon.Proxies))
	}
	for index, address := range deployment.Beacon.Proxies {
		if !common.IsHexAddress(address) {
			t.Fatalf("Beacon proxy %d address = %q", index, address)
		}
	}
	if !strings.EqualFold(deployment.Proxy, deployment.UUPS.Proxy) ||
		!strings.HasPrefix(deployment.InitializationData.UUPS, "0x") ||
		!strings.HasPrefix(deployment.Clones.ImmutableArgsData, "0x") {
		t.Fatalf("primary UUPS fixture is incomplete: %#v", deployment)
	}
	for _, name := range []string{
		"implementationV1", "implementationV2", "badUUID", "transparent", "uups",
		"beacon", "beaconProxyA", "beaconProxyB", "cloneFactory", "standardClone",
		"standardCloneInitialization", "immutableArgsClone", "immutableArgsCloneInitialization",
	} {
		transaction, exists := deployment.Transactions[name]
		if !exists || len(transaction.Hash) != 66 || transaction.Status != "0x1" {
			t.Fatalf("deployment transaction %s = %#v", name, transaction)
		}
	}
	waitHardhatCanonicalTip(t, ctx, h)
	waitHardhatProxyObservation(t, ctx, h, deployment.Proxy, deployment.Implementation)
	waitHardhatProxyObservation(t, ctx, h, deployment.Transparent.Proxy, deployment.Implementation)
	for _, proxy := range deployment.Beacon.Proxies {
		waitHardhatProxyObservation(t, ctx, h, proxy, deployment.Implementation)
	}
	waitHardhatProxyObservation(t, ctx, h, deployment.Clones.Standard, deployment.Implementation)
	waitHardhatProxyObservation(t, ctx, h, deployment.Clones.ImmutableArgs, deployment.Implementation)
	waitHardhatClone(t, ctx, h, deployment.Clones.Standard, deployment.Implementation, "0x")
	waitHardhatClone(t, ctx, h, deployment.Clones.ImmutableArgs,
		deployment.Implementation, deployment.Clones.ImmutableArgsData)
	for _, address := range []string{
		deployment.Proxy, deployment.Transparent.Proxy,
		deployment.Beacon.Proxies[0], deployment.Beacon.Proxies[1],
		deployment.Clones.Standard, deployment.Clones.ImmutableArgs,
	} {
		waitHardhatInitialization(t, ctx, h, address, 1)
	}
	for _, address := range []string{
		deployment.Implementation, deployment.ImplementationV2,
		deployment.Proxy, deployment.Transparent.Proxy, deployment.Transparent.Admin,
		deployment.Beacon.Beacon, deployment.Beacon.Proxies[0], deployment.Beacon.Proxies[1],
	} {
		waitHardhatContractCode(t, ctx, h, address)
	}

	h.enterPhase("runtime immutable authority over compatibility slots")
	runNodeCommand(t, ctx, h, "", "tamper-slots", nil, "tamper-slots.mjs")
	tamperFile := filepath.Join(artifacts, "slot-tamper.json")
	tamperContents, err := os.ReadFile(tamperFile)
	if err != nil {
		t.Fatal(err)
	}
	assertCIArtifactReadable(t, tamperFile)
	var tamper hardhatSlotTamperReport
	if err := json.Unmarshal(tamperContents, &tamper); err != nil {
		t.Fatalf("decode Hardhat slot-tamper report: %v", err)
	}
	assertHardhatSlotTamperReport(t, deployment, tamper)
	waitHardhatCanonicalTip(t, ctx, h)

	h.enterPhase("production CLI API key and real Hardhat verify")
	waitHardhatCompilerCatalog(t, ctx, h)
	apiKey := createHardhatAPIKey(t, ctx, h)
	submitAndWaitHardhatYul(t, ctx, h, apiKey)
	verifyHardhatAddress(t, ctx, h, apiKey, "implementation-v1",
		"contracts/Implementation.sol:Implementation", deployment.Implementation, nil)
	verifyHardhatAddress(t, ctx, h, apiKey, "uups-proxy",
		"@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol:ERC1967Proxy",
		deployment.Proxy, []any{deployment.Implementation, deployment.InitializationData.UUPS})
	verifyHardhatAddress(t, ctx, h, apiKey, "transparent-proxy",
		"@openzeppelin/contracts/proxy/transparent/TransparentUpgradeableProxy.sol:TransparentUpgradeableProxy",
		deployment.Transparent.Proxy, []any{
			deployment.Implementation, deployment.Owner, deployment.InitializationData.Transparent,
		})
	for index, proxy := range deployment.Beacon.Proxies {
		initializer := []string{
			deployment.InitializationData.BeaconProxyA,
			deployment.InitializationData.BeaconProxyB,
		}[index]
		verifyHardhatAddress(t, ctx, h, apiKey, fmt.Sprintf("beacon-proxy-%d", index+1),
			"@openzeppelin/contracts/proxy/beacon/BeaconProxy.sol:BeaconProxy",
			proxy, []any{deployment.Beacon.Beacon, initializer})
	}
	for _, address := range []string{
		deployment.Implementation, deployment.Proxy, deployment.Transparent.Proxy,
		deployment.Beacon.Proxies[0], deployment.Beacon.Proxies[1],
	} {
		waitHardhatSource(t, ctx, h, apiKey, address)
	}
	waitHardhatProxyResolution(t, ctx, h, deployment.Proxy, "uups")
	waitHardhatProxyResolution(t, ctx, h, deployment.Transparent.Proxy, "transparent")
	waitHardhatRuntimeImmutableAuthority(
		t, ctx, h, deployment.Transparent.Proxy, "transparent",
		deployment.Transparent.Admin, "admin_slot_matches",
	)
	for _, proxy := range deployment.Beacon.Proxies {
		waitHardhatProxyResolution(t, ctx, h, proxy, "beacon")
		waitHardhatRuntimeImmutableAuthority(
			t, ctx, h, proxy, "beacon", deployment.Beacon.Beacon,
			"beacon_slot_matches",
		)
	}

	h.enterPhase("unverified proxy management fails closed")
	before := hardhatProxyJobCount(t, ctx, h)
	for name, proxy := range map[string]string{
		"transparent": deployment.Transparent.Proxy,
		"beacon":      deployment.Beacon.Proxies[0],
	} {
		if _, err := submitHardhatProxy(t, ctx, h, apiKey, proxy, deployment.Implementation); !errors.Is(err, errHardhatProxyTargetUnavailable) {
			t.Fatalf("%s unverified management error = %v", name, err)
		}
	}
	if after := hardhatProxyJobCount(t, ctx, h); after != before {
		t.Fatalf("unverified management created jobs: before=%d after=%d", before, after)
	}

	h.enterPhase("verified management and durable proxy bindings")
	verifyHardhatAddress(t, ctx, h, apiKey, "proxy-admin",
		"@openzeppelin/contracts/proxy/transparent/ProxyAdmin.sol:ProxyAdmin",
		deployment.Transparent.Admin, []any{deployment.Owner})
	verifyHardhatAddress(t, ctx, h, apiKey, "upgradeable-beacon",
		"@openzeppelin/contracts/proxy/beacon/UpgradeableBeacon.sol:UpgradeableBeacon",
		deployment.Beacon.Beacon, []any{deployment.Implementation, deployment.Owner})
	waitHardhatSource(t, ctx, h, apiKey, deployment.Transparent.Admin)
	waitHardhatSource(t, ctx, h, apiKey, deployment.Beacon.Beacon)

	before = hardhatProxyJobCount(t, ctx, h)
	if _, err := submitHardhatProxy(t, ctx, h, apiKey, deployment.Proxy, deployment.ImplementationV2); err == nil {
		t.Fatal("wrong expected implementation was accepted")
	}
	if after := hardhatProxyJobCount(t, ctx, h); after != before {
		t.Fatalf("wrong expected implementation created jobs: before=%d after=%d", before, after)
	}
	initialBindings := []string{
		deployment.Proxy,
		deployment.Transparent.Proxy,
		deployment.Beacon.Proxies[0],
		deployment.Beacon.Proxies[1],
	}
	for _, proxy := range initialBindings {
		submitAndWaitHardhatProxy(t, ctx, h, apiKey, proxy, deployment.Implementation)
		assertHardhatProxySource(t, ctx, h, apiKey, proxy, deployment.Implementation, true)
	}
	for _, clone := range []string{deployment.Clones.Standard, deployment.Clones.ImmutableArgs} {
		submitAndWaitHardhatProxy(t, ctx, h, apiKey, clone, deployment.Implementation)
		waitHardhatVerifiedProxyBinding(t, ctx, h, clone, deployment.Implementation, "clone")
	}

	h.enterPhase("proxy upgrade invalidation and rebinding")
	runNodeCommand(t, ctx, h, apiKey, "upgrade", map[string]string{
		"ETHERVIEW_API_KEY": apiKey,
	}, "upgrade.mjs")
	upgradeFile := filepath.Join(artifacts, "upgrades.json")
	upgradeContents, err := os.ReadFile(upgradeFile)
	if err != nil {
		t.Fatal(err)
	}
	assertCIArtifactReadable(t, upgradeFile)
	var upgrade hardhatUpgradeReport
	if err := json.Unmarshal(upgradeContents, &upgrade); err != nil {
		t.Fatalf("decode Hardhat upgrade report: %v", err)
	}
	assertHardhatUpgradeReport(t, deployment, upgrade)
	waitHardhatCanonicalTip(t, ctx, h)
	waitHardhatProxyObservation(t, ctx, h, deployment.Proxy, deployment.ImplementationV2)
	waitHardhatProxyObservation(t, ctx, h, deployment.Transparent.Proxy, deployment.ImplementationV2)
	waitHardhatBeaconImplementation(t, ctx, h, deployment.Beacon.Beacon, deployment.ImplementationV2)
	upgradedProxies := []string{
		deployment.Proxy,
		deployment.Transparent.Proxy,
		deployment.Beacon.Proxies[0],
		deployment.Beacon.Proxies[1],
	}
	for _, proxy := range upgradedProxies {
		waitHardhatInitialization(t, ctx, h, proxy, 2)
	}
	for _, proxy := range upgradedProxies {
		assertHardhatProxySource(t, ctx, h, apiKey, proxy, "", false)
		if _, err := submitHardhatProxy(t, ctx, h, apiKey, proxy, deployment.ImplementationV2); !errors.Is(err, errHardhatImplementationUnverified) {
			t.Fatalf("unverified upgraded implementation for %s error = %v admission=%s",
				proxy, err, hardhatProxyAdmissionDiagnostics(t, ctx, h, proxy))
		}
	}
	for _, proxy := range []string{deployment.Clones.Standard, deployment.Clones.ImmutableArgs} {
		waitHardhatVerifiedProxyBinding(t, ctx, h, proxy, deployment.Implementation, "clone")
	}
	verifyHardhatAddress(t, ctx, h, apiKey, "implementation-v2",
		"contracts/Implementation.sol:ImplementationV2", deployment.ImplementationV2, nil)
	waitHardhatSource(t, ctx, h, apiKey, deployment.ImplementationV2)
	for _, proxy := range upgradedProxies {
		submitAndWaitHardhatProxy(t, ctx, h, apiKey, proxy, deployment.ImplementationV2)
		assertHardhatProxySource(t, ctx, h, apiKey, proxy, deployment.ImplementationV2, true)
	}
	h.enterPhase("anonymous verified artifact and native proxy API")
	assertHardhatNativeProxyAPI(t, ctx, h, deployment, upgrade)
	assertHardhatClonesHaveNoUpgrades(t, ctx, h,
		deployment.Clones.Standard, deployment.Clones.ImmutableArgs)

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

func waitHardhatCompilerCatalog(t *testing.T, ctx context.Context, h *harness) {
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
			  AND entry.platform = 'emscripten-wasm32'`, hardhatCompilerVersion).Scan(
			&source, &artifact, &digest,
		)
		state := fmt.Sprintf("source=%s artifact=%s digest=%s", source, artifact, digest)
		return err == nil && source == hardhatCatalogSource && artifact == hardhatCompilerArtifact &&
			digest == hardhatCompilerDigest, state, err
	})
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
	assertCIArtifactReadable(t, filepath.Join(h.artifacts, "yul-submission.json"))
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

func assertCIArtifactReadable(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o004 == 0 {
		t.Fatalf("retained CI artifact %s mode = %04o, want world-readable", path, info.Mode().Perm())
	}
}

func assertHardhatSlotTamperReport(
	t *testing.T,
	deployment hardhatDeployment,
	report hardhatSlotTamperReport,
) {
	t.Helper()
	if report.OpenZeppelinVersion != "5.6.1" ||
		!strings.EqualFold(report.Transparent.Proxy, deployment.Transparent.Proxy) ||
		!strings.EqualFold(report.Transparent.RuntimeImmutableAdmin, deployment.Transparent.Admin) ||
		strings.EqualFold(report.Transparent.CompatibilitySlotAdmin, deployment.Transparent.Admin) {
		t.Fatalf("unexpected Transparent compatibility-slot report: %#v", report.Transparent)
	}
	if !strings.EqualFold(report.Beacon.RuntimeImmutableBeacon, deployment.Beacon.Beacon) ||
		len(report.Beacon.Proxies) != len(deployment.Beacon.Proxies) ||
		len(report.Beacon.CompatibilitySlotBeacons) != len(deployment.Beacon.Proxies) {
		t.Fatalf("unexpected Beacon compatibility-slot report: %#v", report.Beacon)
	}
	for index, proxy := range deployment.Beacon.Proxies {
		if !strings.EqualFold(report.Beacon.Proxies[index], proxy) ||
			strings.EqualFold(report.Beacon.CompatibilitySlotBeacons[index], deployment.Beacon.Beacon) {
			t.Fatalf("Beacon proxy %d compatibility-slot report = %#v", index, report.Beacon)
		}
	}
	wantCandidates := append([]string{deployment.Transparent.Proxy}, deployment.Beacon.Proxies...)
	if len(report.CandidateTransactions) != len(wantCandidates) {
		t.Fatalf("tamper candidate transaction count = %d, want %d", len(report.CandidateTransactions), len(wantCandidates))
	}
	for index, transaction := range report.CandidateTransactions {
		if len(transaction.Hash) != 66 || transaction.Number == "" ||
			!strings.EqualFold(transaction.To, wantCandidates[index]) ||
			(transaction.Status != "0x0" && transaction.Status != "0x1") {
			t.Fatalf("tamper candidate transaction %d = %#v", index, transaction)
		}
	}
}

func assertHardhatUpgradeReport(
	t *testing.T,
	deployment hardhatDeployment,
	report hardhatUpgradeReport,
) {
	t.Helper()
	if report.OpenZeppelinVersion != "5.6.1" ||
		!strings.EqualFold(report.CurrentImplementation, deployment.ImplementationV2) {
		t.Fatalf("unexpected OpenZeppelin upgrade report: %#v", report)
	}
	assertTransaction := func(name string, transaction hardhatTransaction, target, status string) {
		t.Helper()
		if len(transaction.Hash) != 66 || !strings.HasPrefix(transaction.Hash, "0x") ||
			transaction.Status != status || !strings.EqualFold(transaction.To, target) {
			t.Fatalf("%s transaction = %#v, want target=%s status=%s",
				name, transaction, target, status)
		}
	}
	assertTransaction("bad UUID", report.FailedBadUUIDUpgrade, deployment.Proxy, "0x0")
	assertTransaction("transparent", report.TransparentUpgrade, deployment.Transparent.Admin, "0x1")
	assertTransaction("UUPS", report.UUPSUpgrade, deployment.Proxy, "0x1")
	assertTransaction("beacon", report.BeaconUpgrade, deployment.Beacon.Beacon, "0x1")
	if len(report.BeaconInitializers) != len(deployment.Beacon.Proxies) {
		t.Fatalf("Beacon reinitialization count = %d, want %d",
			len(report.BeaconInitializers), len(deployment.Beacon.Proxies))
	}
	for index, transaction := range report.BeaconInitializers {
		assertTransaction(fmt.Sprintf("beacon proxy %d", index), transaction,
			deployment.Beacon.Proxies[index], "0x1")
	}
	for pattern, expected := range map[string][]string{
		"transparent": {deployment.Transparent.Proxy},
		"uups":        {deployment.Proxy},
		"beacon":      deployment.Beacon.Proxies,
	} {
		actual := report.Impact[pattern]
		if len(actual) != len(expected) {
			t.Fatalf("%s impact = %#v, want %#v", pattern, actual, expected)
		}
		for index := range expected {
			if !strings.EqualFold(actual[index], expected[index]) {
				t.Fatalf("%s impact = %#v, want %#v", pattern, actual, expected)
			}
		}
	}
}

func verifyHardhatAddress(
	t *testing.T,
	ctx context.Context,
	h *harness,
	apiKey, artifact, contract, address string,
	constructorArguments []any,
) {
	t.Helper()
	environment := map[string]string{"ETHERVIEW_API_KEY": apiKey}
	arguments := []string{
		"--build-profile", "production", "--network", "etherview",
		"verify", "etherscan", "--contract", contract,
	}
	if constructorArguments != nil {
		encoded, err := json.Marshal(constructorArguments)
		if err != nil {
			t.Fatalf("encode %s constructor arguments: %v", artifact, err)
		}
		environment["ETHERVIEW_HARDHAT3_CONSTRUCTOR_ARGS"] = string(encoded)
		arguments = append(arguments, "--constructor-args-path", "constructor-args.mjs")
	}
	arguments = append(arguments, address)
	runHardhatCommand(t, ctx, h, apiKey, artifact, environment, arguments...)
}

func submitAndWaitHardhatProxy(
	t *testing.T,
	ctx context.Context,
	h *harness,
	apiKey, proxy, implementation string,
) {
	t.Helper()
	var guid string
	nextDiagnostic := time.Now().Add(5 * time.Second)
	lastDiagnostic := ""
	waitFor(t, ctx, "submittable proxy "+proxy, func() (bool, string, error) {
		var err error
		guid, err = submitHardhatProxy(t, ctx, h, apiKey, proxy, implementation)
		if errors.Is(err, errHardhatProxyTargetUnavailable) ||
			errors.Is(err, errHardhatImplementationUnverified) {
			if !time.Now().Before(nextDiagnostic) {
				lastDiagnostic = hardhatProxyAdmissionDiagnostics(t, ctx, h, proxy)
				nextDiagnostic = time.Now().Add(5 * time.Second)
			}
			state := err.Error()
			if lastDiagnostic != "" {
				state += " admission=" + lastDiagnostic
			}
			return false, state, nil
		}
		return err == nil, guid, err
	})
	waitHardhatProxyStatus(t, ctx, h, apiKey, guid)
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

var (
	errHardhatImplementationUnverified = errors.New("proxy implementation source code not verified")
	errHardhatProxyTargetUnavailable   = errors.New("proxy verification target unavailable")
)

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
		case "proxy verification target unavailable":
			return nil, errHardhatProxyTargetUnavailable
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

func waitHardhatVerifiedProxyBinding(
	t *testing.T,
	ctx context.Context,
	h *harness,
	proxy, implementation, pattern string,
) {
	t.Helper()
	waitFor(t, ctx, "current verified proxy binding "+proxy, func() (bool, string, error) {
		var count int64
		err := h.db.QueryRow(ctx, `
			WITH canonical_tip AS (
				SELECT number, block_hash
				FROM canonical_blocks
				WHERE chain_id = 1
				ORDER BY number DESC
				LIMIT 1
			)
			SELECT count(*)
			FROM verified_proxy_bindings AS binding
			JOIN canonical_blocks AS observation_block
			  ON observation_block.chain_id = binding.chain_id
			 AND observation_block.number = binding.observation_block_number
			 AND observation_block.block_hash = binding.observation_block_hash
			CROSS JOIN canonical_tip AS tip
			WHERE binding.chain_id = 1
			  AND binding.proxy_address = $1
			  AND binding.implementation_address = $2
			  AND binding.proxy_pattern = $3
			  AND proxy_interaction_coverage_contains(
				  binding.chain_id,
				  binding.observation_block_number,
				  binding.observation_block_hash,
				  tip.number,
				  tip.block_hash
			  )`, common.HexToAddress(proxy).Bytes(),
			common.HexToAddress(implementation).Bytes(), pattern).Scan(&count)
		return err == nil && count == 1, fmt.Sprintf("bindings=%d", count), err
	})
}

func waitHardhatContractCode(t *testing.T, ctx context.Context, h *harness, address string) {
	t.Helper()
	waitFor(t, ctx, "canonical contract code "+address, func() (bool, string, error) {
		var count int64
		err := h.db.QueryRow(ctx, `
			SELECT count(*)
			FROM contract_code_observations AS observation
			JOIN canonical_blocks AS canonical
			  ON canonical.chain_id = observation.chain_id
			 AND canonical.number = observation.block_number
			 AND canonical.block_hash = observation.block_hash
			WHERE observation.chain_id = 1
			  AND observation.address = $1
			  AND observation.canonical
			  AND octet_length(observation.code) > 0`, common.HexToAddress(address).Bytes()).Scan(&count)
		return err == nil && count > 0, fmt.Sprintf("observations=%d", count), err
	})
}

func waitHardhatClone(
	t *testing.T,
	ctx context.Context,
	h *harness,
	clone, implementation, immutableArgs string,
) {
	t.Helper()
	waitFor(t, ctx, "exact clone "+clone, func() (bool, string, error) {
		var gotImplementation, gotPattern, gotArgs string
		err := h.db.QueryRow(ctx, `
			SELECT '0x' || encode(observation.implementation_address, 'hex'),
			       observation.proxy_pattern,
			       COALESCE('0x' || encode(observation.immutable_args, 'hex'), '0x')
			FROM proxy_observations AS observation
			JOIN canonical_blocks AS canonical
			  ON canonical.chain_id = observation.chain_id
			 AND canonical.number = observation.block_number
			 AND canonical.block_hash = observation.block_hash
			WHERE observation.chain_id = 1
			  AND observation.proxy_address = $1
			  AND observation.canonical
			ORDER BY observation.block_number DESC
			LIMIT 1`, common.HexToAddress(clone).Bytes()).Scan(
			&gotImplementation, &gotPattern, &gotArgs,
		)
		matched := err == nil && gotPattern == "clone" &&
			strings.EqualFold(gotImplementation, implementation) &&
			strings.EqualFold(gotArgs, immutableArgs)
		return matched, fmt.Sprintf("pattern=%s implementation=%s args=%s",
			gotPattern, gotImplementation, gotArgs), err
	})
}

func assertHardhatClonesHaveNoUpgrades(
	t *testing.T,
	ctx context.Context,
	h *harness,
	clones ...string,
) {
	t.Helper()
	for _, clone := range clones {
		var count int64
		if err := h.db.QueryRow(ctx, `
			SELECT count(*)
			FROM proxy_upgrade_events
			WHERE chain_id = 1
			  AND emitter_address = $1
			  AND canonical`, common.HexToAddress(clone).Bytes()).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("clone %s has %d upgrade events", clone, count)
		}
	}
}

type hardhatProxyDetailExpectation struct {
	address             string
	pattern             gen.ProxyPattern
	mechanism           gen.ProxyMechanism
	implementation      string
	admin               string
	beacon              string
	managementKind      gen.ProxyManagementKind
	managementTarget    string
	affectedProxyCount  string
	immutableArgs       *string
	expectProxyVerified bool
}

func assertHardhatNativeProxyAPI(
	t *testing.T,
	ctx context.Context,
	h *harness,
	deployment hardhatDeployment,
	upgrade hardhatUpgradeReport,
) {
	t.Helper()
	latest := h.latestBlock(ctx)
	h.waitCanonical(ctx, mustDecodeUint64(t, latest.Number), latest.Hash)
	snapshot := gen.CatalogSnapshot{
		ChainId:     "1",
		BlockNumber: strconv.FormatUint(mustDecodeUint64(t, latest.Number), 10),
		BlockHash:   strings.ToLower(latest.Hash),
	}

	assertHardhatAnonymousArtifact(t, ctx, h, deployment.ImplementationV2, "ImplementationV2")
	assertHardhatAnonymousArtifact(t, ctx, h, deployment.Transparent.Proxy, "TransparentUpgradeableProxy")

	immutableArgs := deployment.Clones.ImmutableArgsData
	for _, expectation := range []hardhatProxyDetailExpectation{
		{
			address: deployment.Proxy, pattern: gen.ProxyPatternUups,
			mechanism: gen.ProxyMechanismEip1967, implementation: deployment.ImplementationV2,
			expectProxyVerified: true,
		},
		{
			address: deployment.Transparent.Proxy, pattern: gen.ProxyPatternTransparent,
			mechanism: gen.ProxyMechanismEip1967, implementation: deployment.ImplementationV2,
			admin: deployment.Transparent.Admin, managementKind: gen.ProxyManagementKindProxyAdmin,
			managementTarget: deployment.Transparent.Admin, expectProxyVerified: true,
		},
		{
			address: deployment.Beacon.Proxies[0], pattern: gen.ProxyPatternBeacon,
			mechanism: gen.ProxyMechanismBeacon, implementation: deployment.ImplementationV2,
			beacon: deployment.Beacon.Beacon, managementKind: gen.ProxyManagementKindUpgradeableBeacon,
			managementTarget: deployment.Beacon.Beacon, affectedProxyCount: "2",
			expectProxyVerified: true,
		},
		{
			address: deployment.Beacon.Proxies[1], pattern: gen.ProxyPatternBeacon,
			mechanism: gen.ProxyMechanismBeacon, implementation: deployment.ImplementationV2,
			beacon: deployment.Beacon.Beacon, managementKind: gen.ProxyManagementKindUpgradeableBeacon,
			managementTarget: deployment.Beacon.Beacon, affectedProxyCount: "2",
			expectProxyVerified: true,
		},
		{
			address: deployment.Clones.Standard, pattern: gen.ProxyPatternClone,
			mechanism: gen.ProxyMechanismEip1167, implementation: deployment.Implementation,
		},
		{
			address: deployment.Clones.ImmutableArgs, pattern: gen.ProxyPatternClone,
			mechanism: gen.ProxyMechanismEip1167, implementation: deployment.Implementation,
			immutableArgs: &immutableArgs,
		},
	} {
		assertHardhatProxyDetail(t, ctx, h, snapshot, expectation)
	}

	assertHardhatDirectUpgrade(t, ctx, h, snapshot, deployment.Proxy,
		deployment.Implementation, deployment.ImplementationV2,
		upgrade.UUPSUpgrade.Hash, deployment.Proxy, "", "")
	assertHardhatDirectUpgrade(t, ctx, h, snapshot, deployment.Transparent.Proxy,
		deployment.Implementation, deployment.ImplementationV2,
		upgrade.TransparentUpgrade.Hash, deployment.Transparent.Proxy,
		gen.ProxyManagementKindProxyAdmin, deployment.Transparent.Admin)
	beaconUpgradeA := assertHardhatDirectUpgrade(t, ctx, h, snapshot,
		deployment.Beacon.Proxies[0], deployment.Implementation, deployment.ImplementationV2,
		upgrade.BeaconUpgrade.Hash, deployment.Beacon.Beacon,
		gen.ProxyManagementKindUpgradeableBeacon, deployment.Beacon.Beacon)
	beaconUpgradeB := assertHardhatDirectUpgrade(t, ctx, h, snapshot,
		deployment.Beacon.Proxies[1], deployment.Implementation, deployment.ImplementationV2,
		upgrade.BeaconUpgrade.Hash, deployment.Beacon.Beacon,
		gen.ProxyManagementKindUpgradeableBeacon, deployment.Beacon.Beacon)
	if beaconUpgradeA.BlockHash != beaconUpgradeB.BlockHash ||
		beaconUpgradeA.TransactionHash == nil || beaconUpgradeB.TransactionHash == nil ||
		*beaconUpgradeA.TransactionHash != *beaconUpgradeB.TransactionHash ||
		beaconUpgradeA.LogIndex == nil || beaconUpgradeB.LogIndex == nil ||
		*beaconUpgradeA.LogIndex != *beaconUpgradeB.LogIndex {
		t.Fatalf("shared Beacon upgrade differs between related proxies: A=%#v B=%#v",
			beaconUpgradeA, beaconUpgradeB)
	}

	for _, clone := range []string{deployment.Clones.Standard, deployment.Clones.ImmutableArgs} {
		history := hardhatProxyUpgradeHistory(t, ctx, h, clone, 100, "")
		assertHardhatUpgradeHistoryContext(t, snapshot, clone, history)
		if len(history.Data.Items) != 0 || history.Meta.NextCursor != nil {
			t.Fatalf("clone %s native upgrade history = %#v, want empty", clone, history)
		}
	}
	assertHardhatUpgradePagination(t, ctx, h, snapshot, deployment.Proxy,
		upgrade.UUPSUpgrade.Hash)

	for _, expectation := range []struct {
		address        string
		implementation string
		version        string
	}{
		{deployment.Proxy, deployment.ImplementationV2, "2"},
		{deployment.Transparent.Proxy, deployment.ImplementationV2, "2"},
		{deployment.Beacon.Proxies[0], deployment.ImplementationV2, "2"},
		{deployment.Clones.Standard, deployment.Implementation, "1"},
	} {
		history := hardhatProxyInitializationHistory(t, ctx, h, expectation.address, 100, "")
		assertHardhatInitializationHistoryContext(t, snapshot, expectation.address, history)
		assertHardhatInitializationItem(t, history.Data.Items,
			expectation.version, expectation.implementation)
	}
	assertHardhatInitializationPagination(t, ctx, h, snapshot, deployment.Proxy,
		deployment.Implementation, deployment.ImplementationV2)
}

func assertHardhatAnonymousArtifact(
	t *testing.T,
	ctx context.Context,
	h *harness,
	address, contractName string,
) {
	t.Helper()
	var response gen.VerifiedContractResponse
	h.mustGetJSON(ctx, hardhatContractAPIPath(address, "/verification", nil), &response)
	if response.Meta.ChainId != "1" || response.Meta.RequestId == "" ||
		!strings.EqualFold(response.Data.Address, address) ||
		response.Data.ChainId != "1" || response.Data.ContractName != contractName ||
		!common.IsHexHash(response.Data.CodeHash) ||
		response.Data.Abi == nil || len(*response.Data.Abi) == 0 {
		t.Fatalf("anonymous verified artifact %s = %#v", address, response)
	}
}

func assertHardhatProxyDetail(
	t *testing.T,
	ctx context.Context,
	h *harness,
	snapshot gen.CatalogSnapshot,
	expectation hardhatProxyDetailExpectation,
) {
	t.Helper()
	var response gen.ProxyDetailsResponse
	path := hardhatContractAPIPath(expectation.address, "/proxy", nil)
	if err := h.getJSON(ctx, path, &response); err != nil {
		t.Fatalf("%v; database=%s", err,
			hardhatProxyDetailDatabaseDiagnostic(ctx, h, expectation.address))
	}
	assertHardhatProxyMeta(t, response.Meta)
	assertHardhatCatalogSnapshot(t, response.Data.Snapshot, snapshot)
	if response.Data.Status != gen.ProxyDetailStatusVerified || response.Data.BindingId == nil ||
		!strings.EqualFold(response.Data.Address, expectation.address) ||
		response.Data.Pattern == nil || *response.Data.Pattern != expectation.pattern ||
		response.Data.Mechanism == nil || *response.Data.Mechanism != expectation.mechanism ||
		response.Data.Proxy == nil || !strings.EqualFold(response.Data.Proxy.Address, expectation.address) ||
		response.Data.Implementation == nil ||
		!strings.EqualFold(response.Data.Implementation.Address, expectation.implementation) ||
		response.Data.Implementation.VerificationState != gen.ProxyVerificationStateVerified ||
		len(response.Data.Evidence) == 0 {
		t.Fatalf("native proxy detail %s = %#v", expectation.address, response)
	}
	if expectation.expectProxyVerified &&
		response.Data.Proxy.VerificationState != gen.ProxyVerificationStateVerified {
		t.Fatalf("proxy artifact %s verification state = %s, want verified",
			expectation.address, response.Data.Proxy.VerificationState)
	}
	if expectation.admin != "" && (response.Data.Admin == nil ||
		!strings.EqualFold(response.Data.Admin.Address, expectation.admin) ||
		response.Data.Admin.VerificationState != gen.ProxyVerificationStateVerified) {
		t.Fatalf("Transparent admin identity %s = %#v", expectation.address, response.Data.Admin)
	}
	if expectation.beacon != "" && (response.Data.Beacon == nil ||
		!strings.EqualFold(response.Data.Beacon.Address, expectation.beacon) ||
		response.Data.Beacon.VerificationState != gen.ProxyVerificationStateVerified) {
		t.Fatalf("Beacon identity %s = %#v", expectation.address, response.Data.Beacon)
	}
	if expectation.managementKind != "" {
		if response.Data.Management == nil ||
			response.Data.Management.Kind != expectation.managementKind ||
			!strings.EqualFold(response.Data.Management.Target.Address, expectation.managementTarget) ||
			response.Data.Management.Target.VerificationState != gen.ProxyVerificationStateVerified {
			t.Fatalf("proxy management %s = %#v", expectation.address, response.Data.Management)
		}
		if expectation.affectedProxyCount != "" &&
			(response.Data.Management.AffectedProxyCount == nil ||
				*response.Data.Management.AffectedProxyCount != expectation.affectedProxyCount) {
			t.Fatalf("proxy management affected count %s = %#v, want %s",
				expectation.address, response.Data.Management.AffectedProxyCount,
				expectation.affectedProxyCount)
		}
	}
	if expectation.immutableArgs != nil && (response.Data.ImmutableArgs == nil ||
		!strings.EqualFold(*response.Data.ImmutableArgs, *expectation.immutableArgs)) {
		t.Fatalf("clone %s immutable args = %#v, want %s",
			expectation.address, response.Data.ImmutableArgs, *expectation.immutableArgs)
	}
}

func hardhatProxyDetailDatabaseDiagnostic(
	ctx context.Context,
	h *harness,
	address string,
) string {
	if h == nil || h.db == nil || !common.IsHexAddress(address) {
		return "unavailable"
	}
	queries := dbgen.New(h.db)
	chainID := pgtype.Numeric{Int: common.Big1, Valid: true}
	proxyAddress := common.HexToAddress(address).Bytes()
	diagnostic := make(map[string]any, 4)
	snapshot, err := queries.GetProxyAPISnapshot(ctx, chainID)
	if err != nil {
		diagnostic["snapshot_error"] = diagnosticError(err)
	} else {
		diagnostic["snapshot"] = map[string]any{
			"number": snapshot.SnapshotNumber,
			"state":  snapshot.StageState,
		}
	}
	detection, err := queries.GetLatestPublishedProxyDetection(ctx, chainID, proxyAddress)
	if err != nil {
		diagnostic["detection_error"] = diagnosticError(err)
	} else {
		diagnostic["detection"] = map[string]any{
			"block":                detection.ObservationBlockNumber,
			"kind":                 detection.ProxyKind,
			"pattern":              detection.ProxyPattern,
			"implementation_block": detection.ImplementationObservationBlockNumber,
		}
	}
	binding, err := queries.GetCurrentVerifiedProxyBinding(ctx, chainID, proxyAddress)
	if err != nil {
		diagnostic["binding_error"] = diagnosticError(err)
	} else {
		diagnostic["binding"] = map[string]any{
			"id":                   binding.BindingID,
			"block":                binding.ObservationBlockNumber,
			"kind":                 binding.ProxyKind,
			"pattern":              binding.ProxyPattern,
			"implementation_block": binding.ImplementationObservationBlockNumber,
		}
		count, countErr := queries.CountCurrentBeaconProxies(ctx, binding.BeaconAddress, chainID)
		if countErr != nil {
			diagnostic["beacon_count_error"] = diagnosticError(countErr)
		} else {
			diagnostic["beacon_count"] = count
		}
	}
	encoded, err := json.Marshal(diagnostic)
	if err != nil {
		return "encode-error=" + diagnosticError(err)
	}
	return string(encoded)
}

func assertHardhatDirectUpgrade(
	t *testing.T,
	ctx context.Context,
	h *harness,
	snapshot gen.CatalogSnapshot,
	proxy, oldImplementation, newImplementation, transactionHash, emitter string,
	managementKind gen.ProxyManagementKind,
	managementTarget string,
) gen.ProxyUpgrade {
	t.Helper()
	history := hardhatProxyUpgradeHistory(t, ctx, h, proxy, 100, "")
	assertHardhatUpgradeHistoryContext(t, snapshot, proxy, history)
	for _, item := range history.Data.Items {
		if item.TransactionHash == nil || !strings.EqualFold(*item.TransactionHash, transactionHash) ||
			!strings.EqualFold(item.NewImplementation.Address, newImplementation) {
			continue
		}
		wantChangeType := gen.ProxyUpgradeChangeTypeImplementation
		if managementKind == gen.ProxyManagementKindUpgradeableBeacon {
			wantChangeType = gen.ProxyUpgradeChangeTypeBeaconImplementation
		}
		if item.ChangeType != wantChangeType || item.EvidenceType != gen.ProxyUpgradeEvidenceTypeEvent ||
			item.OldImplementation == nil ||
			!strings.EqualFold(item.OldImplementation.Address, oldImplementation) ||
			item.EmitterAddress == nil || !strings.EqualFold(*item.EmitterAddress, emitter) {
			t.Fatalf("upgrade history item for %s = %#v", proxy, item)
		}
		if managementKind != "" && (item.Management == nil ||
			item.Management.Kind != managementKind ||
			!strings.EqualFold(item.Management.Target.Address, managementTarget)) {
			t.Fatalf("upgrade management for %s = %#v; database=%s", proxy, item.Management,
				hardhatUpgradeManagementDatabaseDiagnostic(
					ctx, h, proxy, transactionHash,
				))
		}
		if managementKind == gen.ProxyManagementKindUpgradeableBeacon &&
			(item.Beacon == nil || !strings.EqualFold(item.Beacon.Address, managementTarget)) {
			t.Fatalf("Beacon upgrade identity for %s = %#v", proxy, item.Beacon)
		}
		return item
	}
	t.Fatalf("upgrade transaction %s was absent from %s history: %#v",
		transactionHash, proxy, history.Data.Items)
	return gen.ProxyUpgrade{}
}

func hardhatUpgradeManagementDatabaseDiagnostic(
	ctx context.Context,
	h *harness,
	proxy string,
	transactionHash string,
) string {
	if h == nil || h.db == nil || !common.IsHexAddress(proxy) ||
		!common.IsHexHash(transactionHash) {
		return "unavailable"
	}
	var diagnostic string
	err := h.db.QueryRow(ctx, `
		SELECT jsonb_build_object(
		  'transaction_to', COALESCE((
		    SELECT transaction.raw->>'to'
		    FROM transactions AS transaction
		    WHERE transaction.chain_id = 1 AND transaction.hash = $2
		  ), ''),
		  'observations', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.block_number DESC)
		    FROM (
		      SELECT observation.block_number::text,
		             observation.proxy_kind, observation.proxy_pattern,
		             observation.evidence_state,
		             '0x' || encode(observation.admin_address, 'hex') AS admin
		      FROM proxy_observations AS observation
		      JOIN canonical_blocks AS canonical
		        ON canonical.chain_id = observation.chain_id
		       AND canonical.number = observation.block_number
		       AND canonical.block_hash = observation.block_hash
		      WHERE observation.chain_id = 1
		        AND observation.proxy_address = $1
		        AND observation.canonical
		      ORDER BY observation.block_number DESC
		      LIMIT 4
		    ) AS row
		  ), '[]'::jsonb),
		  'resolutions', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.id DESC)
		    FROM (
		      SELECT resolution.id, resolution.proxy_kind,
		             resolution.proxy_pattern,
		             '0x' || encode(resolution.admin_address, 'hex') AS admin,
		             resolution.durable_job_id, resolution.job_generation
		      FROM proxy_artifact_resolutions AS resolution
		      WHERE resolution.chain_id = 1
		        AND resolution.proxy_address = $1
		      ORDER BY resolution.id DESC
		      LIMIT 4
		    ) AS row
		  ), '[]'::jsonb)
		)::text`, common.HexToAddress(proxy).Bytes(), common.HexToHash(transactionHash).Bytes(),
	).Scan(&diagnostic)
	if err != nil {
		return "diagnostic-error=" + diagnosticError(err)
	}
	return diagnostic
}

func assertHardhatUpgradePagination(
	t *testing.T,
	ctx context.Context,
	h *harness,
	snapshot gen.CatalogSnapshot,
	proxy, latestTransactionHash string,
) {
	t.Helper()
	first := hardhatProxyUpgradeHistory(t, ctx, h, proxy, 1, "")
	assertHardhatUpgradeHistoryContext(t, snapshot, proxy, first)
	if len(first.Data.Items) != 1 || first.Meta.NextCursor == nil ||
		*first.Meta.NextCursor == "" || first.Data.Items[0].TransactionHash == nil ||
		!strings.EqualFold(*first.Data.Items[0].TransactionHash, latestTransactionHash) {
		t.Fatalf("first upgrade history page for %s = %#v", proxy, first)
	}
	second := hardhatProxyUpgradeHistory(t, ctx, h, proxy, 1, *first.Meta.NextCursor)
	assertHardhatUpgradeHistoryContext(t, snapshot, proxy, second)
	if len(second.Data.Items) != 1 || second.Data.Items[0].TransactionHash == nil ||
		strings.EqualFold(*second.Data.Items[0].TransactionHash, latestTransactionHash) {
		t.Fatalf("second upgrade history page for %s = %#v", proxy, second)
	}
}

func assertHardhatInitializationPagination(
	t *testing.T,
	ctx context.Context,
	h *harness,
	snapshot gen.CatalogSnapshot,
	proxy, implementationV1, implementationV2 string,
) {
	t.Helper()
	first := hardhatProxyInitializationHistory(t, ctx, h, proxy, 1, "")
	assertHardhatInitializationHistoryContext(t, snapshot, proxy, first)
	if len(first.Data.Items) != 1 || first.Data.Items[0].Version != "2" ||
		!strings.EqualFold(first.Data.Items[0].Implementation.Address, implementationV2) ||
		first.Meta.NextCursor == nil || *first.Meta.NextCursor == "" {
		t.Fatalf("first initialization history page for %s = %#v", proxy, first)
	}
	second := hardhatProxyInitializationHistory(t, ctx, h, proxy, 1, *first.Meta.NextCursor)
	assertHardhatInitializationHistoryContext(t, snapshot, proxy, second)
	if len(second.Data.Items) != 1 || second.Data.Items[0].Version != "1" ||
		!strings.EqualFold(second.Data.Items[0].Implementation.Address, implementationV1) {
		t.Fatalf("second initialization history page for %s = %#v", proxy, second)
	}
}

func assertHardhatInitializationItem(
	t *testing.T,
	items []gen.ProxyInitialization,
	version, implementation string,
) {
	t.Helper()
	for _, item := range items {
		if item.Version == version && strings.EqualFold(item.Implementation.Address, implementation) {
			if item.Implementation.VerificationState == nil ||
				*item.Implementation.VerificationState != gen.ProxyVerificationStateVerified {
				t.Fatalf("initialization implementation %s version %s = %#v",
					implementation, version, item.Implementation)
			}
			return
		}
	}
	t.Fatalf("initialization version %s with implementation %s absent from %#v",
		version, implementation, items)
}

func hardhatProxyUpgradeHistory(
	t *testing.T,
	ctx context.Context,
	h *harness,
	proxy string,
	limit int,
	cursor string,
) gen.ProxyUpgradeHistoryResponse {
	t.Helper()
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	var response gen.ProxyUpgradeHistoryResponse
	h.mustGetJSON(ctx, hardhatContractAPIPath(proxy, "/proxy/upgrades", query), &response)
	return response
}

func hardhatProxyInitializationHistory(
	t *testing.T,
	ctx context.Context,
	h *harness,
	proxy string,
	limit int,
	cursor string,
) gen.ProxyInitializationHistoryResponse {
	t.Helper()
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	var response gen.ProxyInitializationHistoryResponse
	h.mustGetJSON(ctx, hardhatContractAPIPath(proxy, "/proxy/initializations", query), &response)
	return response
}

func assertHardhatUpgradeHistoryContext(
	t *testing.T,
	snapshot gen.CatalogSnapshot,
	proxy string,
	response gen.ProxyUpgradeHistoryResponse,
) {
	t.Helper()
	assertHardhatProxyMeta(t, response.Meta)
	assertHardhatCatalogSnapshot(t, response.Data.Snapshot, snapshot)
	if !strings.EqualFold(response.Data.ProxyAddress, proxy) ||
		response.Data.Coverage.State != gen.ProxyHistoryCoverageStateComplete ||
		response.Data.Coverage.FromBlock == nil || response.Data.Coverage.ToBlock == nil ||
		*response.Data.Coverage.ToBlock != snapshot.BlockNumber {
		t.Fatalf("upgrade history context for %s = %#v", proxy, response.Data)
	}
}

func assertHardhatInitializationHistoryContext(
	t *testing.T,
	snapshot gen.CatalogSnapshot,
	proxy string,
	response gen.ProxyInitializationHistoryResponse,
) {
	t.Helper()
	assertHardhatProxyMeta(t, response.Meta)
	assertHardhatCatalogSnapshot(t, response.Data.Snapshot, snapshot)
	if !strings.EqualFold(response.Data.ContractAddress, proxy) ||
		response.Data.Coverage.State != gen.ProxyHistoryCoverageStateComplete ||
		response.Data.Coverage.FromBlock == nil || response.Data.Coverage.ToBlock == nil ||
		*response.Data.Coverage.ToBlock != snapshot.BlockNumber {
		t.Fatalf("initialization history context for %s = %#v", proxy, response.Data)
	}
}

func assertHardhatProxyMeta(t *testing.T, meta gen.Meta) {
	t.Helper()
	if meta.ChainId != "1" || meta.RequestId == "" {
		t.Fatalf("native proxy API meta = %#v", meta)
	}
}

func assertHardhatCatalogSnapshot(
	t *testing.T,
	got, want gen.CatalogSnapshot,
) {
	t.Helper()
	if got != want {
		t.Fatalf("native proxy API snapshot = %#v, want %#v", got, want)
	}
}

func hardhatContractAPIPath(address, suffix string, query url.Values) string {
	path := "/api/v1/contracts/" + common.HexToAddress(address).Hex() + suffix
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return path
}

func waitHardhatProxyResolution(
	t *testing.T,
	ctx context.Context,
	h *harness,
	proxy, pattern string,
) {
	t.Helper()
	waitFor(t, ctx, "exact proxy resolution "+pattern+" "+proxy, func() (bool, string, error) {
		var count int64
		err := h.db.QueryRow(ctx, `
			SELECT count(*)
			FROM proxy_artifact_resolutions AS resolution
			JOIN proxy_observations AS observation
			  ON observation.chain_id = resolution.chain_id
			 AND observation.proxy_address = resolution.proxy_address
			 AND observation.block_hash = resolution.observation_block_hash
			 AND observation.stage_version = resolution.observation_stage_version
			JOIN canonical_blocks AS canonical
			  ON canonical.chain_id = observation.chain_id
			 AND canonical.number = observation.block_number
			 AND canonical.block_hash = observation.block_hash
			JOIN published_block_stage_results AS published
			  ON published.chain_id = resolution.chain_id
			 AND published.block_hash = resolution.observation_block_hash
			 AND published.stage = 'proxy'
			 AND published.stage_version = resolution.observation_stage_version
			 AND published.durable_job_id = resolution.durable_job_id
			 AND published.job_generation = resolution.job_generation
			 AND published.state = 'complete'
			WHERE resolution.chain_id = 1
			  AND resolution.proxy_address = $1
			  AND resolution.proxy_pattern = CASE $2
			      WHEN 'uups' THEN 'erc1967'
			      ELSE $2
			  END
			  AND (
			      $2 <> 'uups' OR EXISTS (
			          SELECT 1
			          FROM uups_implementation_observations AS uups
			          JOIN canonical_blocks AS uups_canonical
			            ON uups_canonical.chain_id = uups.chain_id
			           AND uups_canonical.number = uups.block_number
			           AND uups_canonical.block_hash = uups.block_hash
			          JOIN uups_implementation_observation_generations AS uups_generation
			            ON uups_generation.chain_id = uups.chain_id
			           AND uups_generation.implementation_address = uups.implementation_address
			           AND uups_generation.observation_block_hash = uups.block_hash
			           AND uups_generation.observation_stage_version = uups.stage_version
			           AND uups_generation.verification_job_id = uups.verification_job_id
			          JOIN published_block_stage_results AS uups_published
			            ON uups_published.chain_id = uups_generation.chain_id
			           AND uups_published.block_hash = uups_generation.observation_block_hash
			           AND uups_published.stage = 'proxy'
			           AND uups_published.stage_version = uups_generation.observation_stage_version
			           AND uups_published.durable_job_id = uups_generation.durable_job_id
			           AND uups_published.job_generation = uups_generation.job_generation
			           AND uups_published.state = 'complete'
			          WHERE uups.chain_id = resolution.chain_id
			            AND uups.implementation_address = resolution.implementation_address
			            AND uups.implementation_code_hash = resolution.implementation_code_hash
			            AND uups.probe_state = 'compatible'
			            AND uups.canonical = TRUE
			      )
			  )`,
			common.HexToAddress(proxy).Bytes(), pattern).Scan(&count)
		return err == nil && count > 0, fmt.Sprintf("resolutions=%d", count), err
	})
}

func waitHardhatRuntimeImmutableAuthority(
	t *testing.T,
	ctx context.Context,
	h *harness,
	proxy, pattern, authority, slotEvidence string,
) {
	t.Helper()
	waitFor(t, ctx, "runtime immutable authority "+pattern+" "+proxy, func() (bool, string, error) {
		var gotAuthority, slotMatches string
		err := h.db.QueryRow(ctx, `
			SELECT CASE $3
			           WHEN 'admin_slot_matches' THEN
			               '0x' || encode(resolution.admin_address, 'hex')
			           WHEN 'beacon_slot_matches' THEN
			               '0x' || encode(resolution.beacon_address, 'hex')
			       END,
			       resolution.evidence->>$3
			FROM proxy_artifact_resolutions AS resolution
			JOIN published_block_stage_results AS published
			  ON published.chain_id = resolution.chain_id
			 AND published.block_hash = resolution.observation_block_hash
			 AND published.stage = 'proxy'
			 AND published.stage_version = resolution.observation_stage_version
			 AND published.durable_job_id = resolution.durable_job_id
			 AND published.job_generation = resolution.job_generation
			 AND published.state = 'complete'
			WHERE resolution.chain_id = 1
			  AND resolution.proxy_address = $1
			  AND resolution.proxy_pattern = $2
			ORDER BY resolution.id DESC
			LIMIT 1`, common.HexToAddress(proxy).Bytes(), pattern, slotEvidence).Scan(
			&gotAuthority, &slotMatches,
		)
		matched := err == nil && strings.EqualFold(gotAuthority, authority) && slotMatches == "false"
		return matched, fmt.Sprintf("authority=%s %s=%s", gotAuthority, slotEvidence, slotMatches), err
	})
}

func waitHardhatBeaconImplementation(
	t *testing.T,
	ctx context.Context,
	h *harness,
	beacon, implementation string,
) {
	t.Helper()
	waitFor(t, ctx, "beacon implementation "+implementation, func() (bool, string, error) {
		var got string
		err := h.db.QueryRow(ctx, `
			SELECT '0x' || encode(observation.implementation_address, 'hex')
			FROM beacon_implementation_observations AS observation
			JOIN canonical_blocks AS canonical
			  ON canonical.chain_id = observation.chain_id
			 AND canonical.number = observation.block_number
			 AND canonical.block_hash = observation.block_hash
			WHERE observation.chain_id = 1
			  AND observation.beacon_address = $1
			  AND observation.canonical
			ORDER BY observation.block_number DESC
			LIMIT 1`, common.HexToAddress(beacon).Bytes()).Scan(&got)
		return err == nil && strings.EqualFold(got, implementation), got, err
	})
}

func waitHardhatInitialization(
	t *testing.T,
	ctx context.Context,
	h *harness,
	address string,
	version uint64,
) {
	t.Helper()
	waitFor(t, ctx, fmt.Sprintf("initialization %s version %d", address, version), func() (bool, string, error) {
		var count int64
		err := h.db.QueryRow(ctx, `
			SELECT count(*)
			FROM proxy_initialization_events AS initialization
			JOIN canonical_blocks AS canonical
			  ON canonical.chain_id = initialization.chain_id
			 AND canonical.number = initialization.block_number
			 AND canonical.block_hash = initialization.block_hash
			WHERE initialization.chain_id = 1
			  AND initialization.contract_address = $1
			  AND initialization.version = $2::numeric`,
			common.HexToAddress(address).Bytes(), strconv.FormatUint(version, 10)).Scan(&count)
		return err == nil && count > 0, fmt.Sprintf("events=%d", count), err
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

func hardhatProxyAdmissionDiagnostics(
	t *testing.T,
	ctx context.Context,
	h *harness,
	proxy string,
) string {
	t.Helper()
	var summary string
	err := h.db.QueryRow(ctx, `
		SELECT jsonb_build_object(
		  'tip', COALESCE((
		    SELECT jsonb_build_object(
		      'number', number::text,
		      'hash', '0x' || encode(block_hash, 'hex')
		    )
		    FROM canonical_blocks
		    WHERE chain_id = 1
		    ORDER BY number DESC
		    LIMIT 1
		  ), '{}'::jsonb),
		  'observations', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.block_number DESC, row.block_hash DESC)
		    FROM (
		      SELECT observation.block_number::text,
		             '0x' || encode(observation.block_hash, 'hex') AS block_hash,
		             observation.proxy_pattern,
		             observation.evidence_state,
		             '0x' || encode(observation.proxy_code_hash, 'hex') AS proxy_code_hash,
		             '0x' || encode(observation.implementation_address, 'hex') AS implementation
		      FROM proxy_observations AS observation
		      JOIN canonical_blocks AS canonical
		        ON canonical.chain_id = observation.chain_id
		       AND canonical.number = observation.block_number
		       AND canonical.block_hash = observation.block_hash
		      WHERE observation.chain_id = 1
		        AND observation.proxy_address = $1
		        AND observation.canonical
		      ORDER BY observation.block_number DESC, observation.block_hash DESC
		      LIMIT 8
		    ) AS row
		  ), '[]'::jsonb),
		  'resolutions', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.id DESC)
		    FROM (
		      SELECT resolution.id,
		             '0x' || encode(resolution.observation_block_hash, 'hex') AS block_hash,
		             resolution.proxy_pattern,
		             resolution.standard_version,
		             resolution.proxy_artifact_job_id,
		             resolution.implementation_artifact_job_id,
		             '0x' || encode(resolution.implementation_address, 'hex') AS implementation,
		             published.state AS publication_state
		      FROM proxy_artifact_resolutions AS resolution
		      LEFT JOIN published_block_stage_results AS published
		        ON published.chain_id = resolution.chain_id
		       AND published.block_hash = resolution.observation_block_hash
		       AND published.stage = 'proxy'
		       AND published.stage_version = resolution.observation_stage_version
		       AND published.durable_job_id = resolution.durable_job_id
		       AND published.job_generation = resolution.job_generation
		      WHERE resolution.chain_id = 1
		        AND resolution.proxy_address = $1
		      ORDER BY resolution.id DESC
		      LIMIT 8
		    ) AS row
		  ), '[]'::jsonb),
		  'negative_evidence', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.block_number DESC, row.job_generation DESC)
		    FROM (
		      SELECT evidence.block_number::text,
		             '0x' || encode(evidence.block_hash, 'hex') AS block_hash,
		             '0x' || encode(evidence.code_hash, 'hex') AS code_hash,
		             evidence.detection_state, evidence.reason,
		             evidence.durable_job_id, evidence.job_generation,
		             published.state AS publication_state
		      FROM proxy_detection_evidence AS evidence
		      JOIN canonical_blocks AS canonical
		        ON canonical.chain_id = evidence.chain_id
		       AND canonical.number = evidence.block_number
		       AND canonical.block_hash = evidence.block_hash
		      LEFT JOIN published_block_stage_results AS published
		        ON published.chain_id = evidence.chain_id
		       AND published.block_hash = evidence.block_hash
		       AND published.stage = 'proxy'
		       AND published.stage_version = evidence.stage_version
		       AND published.durable_job_id = evidence.durable_job_id
		       AND published.job_generation = evidence.job_generation
		      WHERE evidence.chain_id = 1
		        AND evidence.address = $1
		        AND evidence.canonical
		      ORDER BY evidence.block_number DESC, evidence.job_generation DESC
		      LIMIT 8
		    ) AS row
		  ), '[]'::jsonb),
		  'artifacts', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.valid_from_block DESC)
		    FROM (
		      SELECT artifact.artifact_kind, artifact.standard_version,
		             artifact.valid_from_block::text,
		             artifact.verification_job_id,
		             '0x' || encode(artifact.code_hash, 'hex') AS code_hash,
		             verified.valid_to_block::text
		      FROM verified_contract_proxy_artifacts AS artifact
		      JOIN verified_contracts AS verified
		        ON verified.chain_id = artifact.chain_id
		       AND verified.address = artifact.address
		       AND verified.code_hash = artifact.code_hash
		       AND verified.valid_from_block = artifact.valid_from_block
		       AND verified.verification_job_id = artifact.verification_job_id
		       AND verified.request_digest = artifact.request_digest
		      WHERE artifact.chain_id = 1
		        AND artifact.address = $1
		    ) AS row
		  ), '[]'::jsonb),
		  'code_changes', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.block_number DESC)
		    FROM (
		      SELECT change.block_number::text,
		             '0x' || encode(change.block_hash, 'hex') AS block_hash,
		             change.before_value, change.after_value
		      FROM transaction_state_changes AS change
		      JOIN canonical_blocks AS canonical
		        ON canonical.chain_id = change.chain_id
		       AND canonical.number = change.block_number
		       AND canonical.block_hash = change.block_hash
		      WHERE change.chain_id = 1
		        AND change.address = $1
		        AND change.field_kind = 'code'
		        AND change.canonical
		      ORDER BY change.block_number DESC
		      LIMIT 8
		    ) AS row
		  ), '[]'::jsonb)
		)::text`, common.HexToAddress(proxy).Bytes()).Scan(&summary)
	if err != nil {
		return "diagnostic-error=" + diagnosticError(err)
	}
	return summary
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
		  'catalog_heads', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.language)
		    FROM (
		      SELECT head.language, head.generation_id, head.updated_at
		      FROM compiler_catalog_heads AS head
		    ) AS row
		  ), '[]'::jsonb),
		  'catalog_generations', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.id)
		    FROM (
		      SELECT id, language, source_url, entry_count, fetched_at
		      FROM compiler_catalog_generations
		    ) AS row
		  ), '[]'::jsonb),
		  'jobs', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.created_at, row.id)
		    FROM (
		      SELECT id, kind, status, error_code, outcome_kind,
		             compiler_platform, catalog_generation_id,
		             encode(compiler_digest, 'hex') AS compiler_digest,
		             executor_kind, execution_policy,
		             encode(executor_digest, 'hex') AS executor_digest,
		             attempt_count, max_attempts, leased_by, lease_expires_at,
		             created_at, updated_at
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
		      SELECT verification_job_id, proxy_kind, proxy_pattern,
		             '0x' || encode(proxy_address, 'hex') AS proxy_address,
		             '0x' || encode(implementation_address, 'hex') AS implementation_address,
		             '0x' || encode(observation_block_hash, 'hex') AS observation_block_hash,
		             created_at
		      FROM verified_proxy_bindings
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
		    AND execution_policy = 'trusted_subprocess') = 8,
		  count(*) FILTER (WHERE language = 'yul' AND status = 'succeeded'
		    AND compiler_version = '0.8.30+commit.73712a01'
		    AND compiler_platform = 'emscripten-wasm32'
		    AND catalog_generation_id IS NOT NULL
		    AND compiler_digest IS NOT NULL
		    AND executor_digest IS NOT NULL
		    AND executor_kind = 'node_solcjs_v1'
		    AND execution_policy = 'trusted_subprocess') = 1,
		  count(*) FILTER (WHERE kind = 'address' AND status = 'succeeded'
		    AND compiler_digest IS NOT NULL) = 8
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
	if err := h.db.QueryRow(ctx, `SELECT count(*) FROM verified_proxy_bindings`).Scan(&result.ProxyBindings); err != nil {
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
	if result.AddressJobs != 8 || result.ProxyJobs != 10 ||
		result.CompilerResults != 9 || result.ProxyResults != 10 ||
		result.ProxyBindings != 10 || result.CatalogEntries == 0 ||
		!result.ExecutorProvenance || !result.CompilerProvenance ||
		result.CurrentProxyKind != "eip1967" {
		t.Fatalf("incomplete Hardhat/proxy persistence: %#v", result)
	}
	return result
}
