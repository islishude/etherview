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
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
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
	hardhatSafeRuntimeHash  = "0xd7d408ebcd99b2b70be43e20253d6d92a8ea8fab29bd3be7f55b10032331fb4c"
)

type hardhatRuntime struct {
	image string
}

type hardhatDeployment struct {
	SchemaVersion       int    `json:"schemaVersion"`
	OpenZeppelinVersion string `json:"openzeppelinVersion"`
	SafeVersion         string `json:"safeVersion"`
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
	CWIA struct {
		Factory        string `json:"factory"`
		ArtifactSource string `json:"artifactSource"`
		Implementation string `json:"implementation"`
		Account        string `json:"account"`
		Owner          string `json:"owner"`
		Number         string `json:"number"`
		Data           string `json:"data"`
		ImmutableArgs  string `json:"immutableArgs"`
		Stored         string `json:"stored"`
	} `json:"cwia"`
	Diamond struct {
		Address string   `json:"address"`
		Facets  []string `json:"facets"`
		Value   string   `json:"value"`
		Doubled string   `json:"doubled"`
	} `json:"diamond"`
	Safe struct {
		Factory         string `json:"factory"`
		Singleton       string `json:"singleton"`
		Proxy           string `json:"proxy"`
		Initializer     string `json:"initializer"`
		RuntimeCodeHash string `json:"runtimeCodeHash"`
	} `json:"safe"`
	InitializationData struct {
		Transparent  string `json:"transparent"`
		UUPS         string `json:"uups"`
		BeaconProxyA string `json:"beaconProxyA"`
		BeaconProxyB string `json:"beaconProxyB"`
	} `json:"initializationData"`
	Transactions map[string]hardhatTransaction `json:"transactions"`
}

type hardhatTransaction struct {
	Number      string `json:"number"`
	BlockNumber string `json:"blockNumber"`
	BlockHash   string `json:"blockHash"`
	Hash        string `json:"hash"`
	Status      string `json:"status"`
	To          string `json:"to"`
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
	AddressJobs         int64
	DerivedJobs         int64
	YulJobs             int64
	ProxyJobs           int64
	CompilerResults     int64
	DerivedResults      int64
	YulResults          int64
	ProxyResults        int64
	ProxyBindings       int64
	DerivedPublications int64
	DerivedAttempts     int64
	CatalogEntries      int64
	CurrentProxy        string
	CurrentImpl         string
	CurrentProxyKind    string
	ExecutorProvenance  bool
	YulProvenance       bool
	CompilerProvenance  bool
	DiamondState        string
	DiamondFacets       int64
	DiamondSelectors    int64
	DiamondCuts         int64
	DiamondSingular     int64
	SafeState           string
	SafeDetector        string
	SafeFamily          string
	SafeVariant         string
	SafeRole            string
	SafeImplementation  string
	SafeCanonicalShell  bool
	SafeOfficial        bool
	SafeLegacyRows      int64
	SafeTraceCreates    int64
}

type hardhatCompilerCacheFile struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Size       string `json:"size"`
	Inode      string `json:"inode"`
	Mode       string `json:"mode"`
	ModifiedNS string `json:"modified_ns"`
	ChangedNS  string `json:"changed_ns"`
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
	project.Env = runtimeEnvironment(root, uint64(time.Now().UTC().Truncate(time.Hour).Unix()), false)
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
	if deployment.SchemaVersion != 5 {
		t.Fatalf("Hardhat deployment schema = %d, want 5", deployment.SchemaVersion)
	}
	if deployment.OpenZeppelinVersion != "5.6.1" {
		t.Fatalf("OpenZeppelin fixture version = %q, want 5.6.1", deployment.OpenZeppelinVersion)
	}
	if deployment.SafeVersion != "1.4.1" {
		t.Fatalf("Safe fixture version = %q, want 1.4.1", deployment.SafeVersion)
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
		"cwiaFactory":             deployment.CWIA.Factory,
		"cwiaArtifactSource":      deployment.CWIA.ArtifactSource,
		"cwiaImplementation":      deployment.CWIA.Implementation,
		"cwiaAccount":             deployment.CWIA.Account,
		"diamond":                 deployment.Diamond.Address,
		"safeFactory":             deployment.Safe.Factory,
		"safeSingleton":           deployment.Safe.Singleton,
		"safeProxy":               deployment.Safe.Proxy,
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
	if len(deployment.Diamond.Facets) != 2 || deployment.Diamond.Value != "2535" ||
		deployment.Diamond.Doubled != "42" {
		t.Fatalf("Diamond deployment = %#v", deployment.Diamond)
	}
	for index, address := range deployment.Diamond.Facets {
		if !common.IsHexAddress(address) {
			t.Fatalf("Diamond facet %d address = %q", index, address)
		}
	}
	if !strings.EqualFold(deployment.Proxy, deployment.UUPS.Proxy) ||
		!strings.HasPrefix(deployment.InitializationData.UUPS, "0x") ||
		!strings.HasPrefix(deployment.Clones.ImmutableArgsData, "0x") ||
		!strings.EqualFold(deployment.CWIA.Owner, deployment.Owner) ||
		len(deployment.CWIA.ImmutableArgs) != 2+2*(20+32+2+11) ||
		deployment.CWIA.Data != "0x68656c6c6f2c776f726c64" ||
		deployment.CWIA.Number == "" || deployment.CWIA.Stored != "777" {
		t.Fatalf("primary UUPS fixture is incomplete: %#v", deployment)
	}
	if deployment.Safe.Initializer != "0x" ||
		!strings.EqualFold(deployment.Safe.RuntimeCodeHash, hardhatSafeRuntimeHash) {
		t.Fatalf("Safe fixture is incomplete: %#v", deployment.Safe)
	}
	for _, name := range []string{
		"implementationV1", "implementationV2", "badUUID", "transparent", "uups",
		"beacon", "beaconProxyA", "beaconProxyB", "cloneFactory", "standardClone",
		"safeSingleton", "safeFactory", "safeCreate",
		"standardCloneInitialization", "immutableArgsClone", "immutableArgsCloneInitialization",
		"cwiaFactory", "cwiaArtifactSource", "cwiaCreate", "cwiaSetStored",
		"diamondValueFacet", "diamondMathFacet", "diamond", "diamondSetValue",
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
	waitHardhatProxyObservation(t, ctx, h, deployment.CWIA.Account, deployment.CWIA.Implementation)
	waitHardhatDiamond(t, ctx, h, deployment)
	waitHardhatSafe(t, ctx, h, deployment)
	waitHardhatClone(t, ctx, h, deployment.Clones.Standard, deployment.Implementation, "eip1167", "0x")
	waitHardhatClone(t, ctx, h, deployment.Clones.ImmutableArgs,
		deployment.Implementation, "eip1167", deployment.Clones.ImmutableArgsData)
	waitHardhatClone(t, ctx, h, deployment.CWIA.Account,
		deployment.CWIA.Implementation, "cwia", deployment.CWIA.ImmutableArgs)
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
		deployment.CWIA.Factory, deployment.CWIA.ArtifactSource,
		deployment.CWIA.Implementation, deployment.CWIA.Account,
		deployment.Safe.Factory, deployment.Safe.Singleton, deployment.Safe.Proxy,
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

	h.enterPhase("production CLI API key and first real compiler")
	cloneInitialization := deployment.Transactions["standardCloneInitialization"]
	initializeSignature := "initialize(address,uint256)"
	initializeSelector := "0x" + fmt.Sprintf("%x", crypto.Keccak256([]byte(initializeSignature))[:4])
	assertHardhatHistoricalMethod(
		t, ctx, h, deployment.Clones.Standard, cloneInitialization.Hash,
		initializeSelector, "",
	)
	waitHardhatCompilerCatalog(t, ctx, h)
	apiKey := createHardhatAPIKey(t, ctx, h)
	submitAndWaitHardhatYul(t, ctx, h, apiKey)
	cacheBefore := inspectHardhatCompilerCache(t, ctx, h)

	h.enterPhase("persistent compiler cache across owner replacement")
	recreateHardhatCompilerOwner(t, ctx, h)
	cacheAfterRecreate := inspectHardhatCompilerCache(t, ctx, h)
	if cacheAfterRecreate != cacheBefore {
		t.Fatalf("compiler cache changed across %s replacement: before=%#v after=%#v",
			h.apiService, cacheBefore, cacheAfterRecreate)
	}
	verifyHardhatAddress(t, ctx, h, apiKey, "implementation-v1",
		"contracts/Implementation.sol:Implementation", deployment.Implementation, nil)
	verifyHardhatAddress(t, ctx, h, apiKey, "cwia-artifact-source",
		"contracts/MyAccount.sol:MyAccount", deployment.CWIA.ArtifactSource, nil)
	assertHardhatCWIAImplementationCodeHashABI(t, ctx, h, deployment)
	assertHardhatHistoricalMethod(
		t, ctx, h, deployment.Clones.Standard, cloneInitialization.Hash,
		"initialize", initializeSignature,
	)
	cacheAfterReuse := inspectHardhatCompilerCache(t, ctx, h)
	if cacheAfterReuse != cacheBefore {
		t.Fatalf("compiler cache was reinstalled after same-version reuse: before=%#v after=%#v",
			cacheBefore, cacheAfterReuse)
	}

	h.enterPhase("real Hardhat address verification")
	verifyHardhatAddress(t, ctx, h, apiKey, "uups-proxy",
		"@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol:ERC1967Proxy",
		deployment.Proxy, []any{deployment.Implementation, deployment.InitializationData.UUPS})
	verifyHardhatAddress(t, ctx, h, apiKey, "transparent-proxy",
		"@openzeppelin/contracts/proxy/transparent/TransparentUpgradeableProxy.sol:TransparentUpgradeableProxy",
		deployment.Transparent.Proxy, []any{
			deployment.Implementation, deployment.Owner, deployment.InitializationData.Transparent,
		})
	waitHardhatDerivedProxyAdmin(t, ctx, h, apiKey, deployment)
	for index, proxy := range deployment.Beacon.Proxies {
		initializer := []string{
			deployment.InitializationData.BeaconProxyA,
			deployment.InitializationData.BeaconProxyB,
		}[index]
		verifyHardhatAddressWithForce(t, ctx, h, apiKey,
			fmt.Sprintf("beacon-proxy-%d", index+1),
			"@openzeppelin/contracts/proxy/beacon/BeaconProxy.sol:BeaconProxy",
			proxy, []any{deployment.Beacon.Beacon, initializer}, index > 0)
	}
	for _, address := range []string{
		deployment.Implementation, deployment.CWIA.ArtifactSource,
		deployment.Proxy, deployment.Transparent.Proxy,
		deployment.Beacon.Proxies[0], deployment.Beacon.Proxies[1],
	} {
		waitHardhatSource(t, ctx, h, apiKey, address)
	}
	verifyHardhatAddressWithForce(t, ctx, h, apiKey, "cwia-implementation",
		"contracts/MyAccount.sol:MyAccount", deployment.CWIA.Implementation, nil, true)
	waitHardhatSource(t, ctx, h, apiKey, deployment.CWIA.Implementation)
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
		"beacon": deployment.Beacon.Proxies[0],
	} {
		if _, err := submitHardhatProxy(t, ctx, h, apiKey, proxy, deployment.Implementation); !errors.Is(err, errHardhatProxyTargetUnavailable) {
			t.Fatalf("%s unverified management error = %v", name, err)
		}
	}
	if after := hardhatProxyJobCount(t, ctx, h); after != before {
		t.Fatalf("unverified management created jobs: before=%d after=%d", before, after)
	}

	h.enterPhase("verified management and durable proxy bindings")
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
		waitHardhatVerifiedProxyBinding(t, ctx, h, clone, deployment.Implementation)
	}
	submitAndWaitHardhatProxy(t, ctx, h, apiKey, deployment.CWIA.Account, deployment.CWIA.Implementation)
	waitHardhatVerifiedProxyBinding(t, ctx, h, deployment.CWIA.Account, deployment.CWIA.Implementation)
	assertHardhatProxySource(t, ctx, h, apiKey, deployment.CWIA.Account, deployment.CWIA.Implementation, true)
	assertHardhatHistoricalMethod(
		t, ctx, h, deployment.CWIA.Account, deployment.Transactions["cwiaSetStored"].Hash,
		"setStored", "setStored(uint256)",
	)

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
		waitHardhatVerifiedProxyBinding(t, ctx, h, proxy, deployment.Implementation)
	}
	waitHardhatVerifiedProxyBinding(
		t, ctx, h, deployment.CWIA.Account, deployment.CWIA.Implementation,
	)
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
		deployment.Clones.Standard, deployment.Clones.ImmutableArgs, deployment.CWIA.Account)

	snapshot := captureHardhatProxySnapshot(
		t, ctx, h, deployment.Proxy, deployment.Diamond.Address,
		deployment.Safe.Proxy, deployment.Safe.Singleton,
	)
	h.writeJSONArtifact(mode+"-proxy-summary.json", snapshot)
	return snapshot
}

func assertHardhatHistoricalMethod(
	t *testing.T,
	ctx context.Context,
	h *harness,
	address, transactionHash, method, signature string,
) {
	t.Helper()
	var response gen.TransactionListResponse
	h.mustGetJSON(ctx, "/api/v1/addresses/"+address+"/transactions?limit=100", &response)
	for _, transaction := range response.Data {
		if !strings.EqualFold(string(transaction.Hash), transactionHash) {
			continue
		}
		if transaction.Method == nil || *transaction.Method != method {
			t.Fatalf("historical method for %s = %v, want %q", transactionHash, transaction.Method, method)
		}
		if signature == "" {
			if transaction.MethodSignature != nil {
				t.Fatalf("pre-verification historical signature for %s = %v", transactionHash, transaction.MethodSignature)
			}
		} else if transaction.MethodSignature == nil || *transaction.MethodSignature != signature {
			t.Fatalf("historical signature for %s = %v, want %q", transactionHash, transaction.MethodSignature, signature)
		}
		return
	}
	t.Fatalf("historical transaction %s absent from address %s", transactionHash, address)
}

func inspectHardhatCompilerCache(
	t *testing.T,
	ctx context.Context,
	h *harness,
) hardhatCompilerCacheFile {
	t.Helper()
	path := "/var/lib/etherview/compilers/cache/solidity-sha256-" +
		hardhatCompilerDigest + ".js"
	output, err := h.project.Run(
		ctx,
		"run", "--rm", "--no-deps", "hardhat", "node", "cache-inspect.mjs", path,
	)
	if err != nil {
		t.Fatalf("inspect compiler cache: %v", err)
	}
	const inspectionPrefix = "ETHERVIEW_CACHE_INSPECTION_V1="
	var payload []byte
	for line := range bytes.SplitSeq(output, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte(inspectionPrefix)) {
			continue
		}
		if payload != nil {
			t.Fatalf("multiple compiler cache inspection records in %q", output)
		}
		payload = bytes.TrimPrefix(line, []byte(inspectionPrefix))
	}
	if payload == nil {
		t.Fatalf("compiler cache inspection record absent from %q", output)
	}
	var file hardhatCompilerCacheFile
	if err := json.Unmarshal(payload, &file); err != nil {
		t.Fatalf("decode compiler cache inspection: %v", err)
	}
	if file.Path != path || file.SHA256 != hardhatCompilerDigest ||
		file.Size == "" || file.Size == "0" || file.Inode == "" ||
		file.Mode != "400" || file.ModifiedNS == "" || file.ChangedNS == "" {
		t.Fatalf("invalid compiler cache file: %#v", file)
	}
	return file
}

func recreateHardhatCompilerOwner(
	t *testing.T,
	ctx context.Context,
	h *harness,
) {
	t.Helper()
	if _, err := h.project.Run(
		ctx,
		"up", "-d", "--no-deps", "--force-recreate", h.apiService,
	); err != nil {
		t.Fatal(err)
	}
	h.resolveAPI(ctx)
	h.waitReady(ctx)
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
	verifyHardhatAddressWithForce(t, ctx, h, apiKey, artifact, contract, address,
		constructorArguments, false)
}

func verifyHardhatAddressWithForce(
	t *testing.T,
	ctx context.Context,
	h *harness,
	apiKey, artifact, contract, address string,
	constructorArguments []any,
	force bool,
) {
	t.Helper()
	environment := map[string]string{"ETHERVIEW_API_KEY": apiKey}
	arguments := []string{
		"--build-profile", "production", "--network", "etherview",
		"verify", "etherscan", "--contract", contract,
	}
	if force {
		arguments = append(arguments, "--force")
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

func waitHardhatDerivedProxyAdmin(
	t *testing.T,
	ctx context.Context,
	h *harness,
	apiKey string,
	deployment hardhatDeployment,
) {
	t.Helper()
	admin := common.HexToAddress(deployment.Transparent.Admin).Bytes()
	proxy := common.HexToAddress(deployment.Transparent.Proxy).Bytes()
	transaction := common.HexToHash(deployment.Transactions["transparent"].Hash).Bytes()
	waitFor(t, ctx, "factory-derived ProxyAdmin verification", func() (bool, string, error) {
		var addressJobs, derivedJobs, results, publications, attempts int64
		var exactHistoricalEpoch bool
		err := h.db.QueryRow(ctx, `
			SELECT
			  (SELECT count(*) FROM verification_jobs
			   WHERE kind = 'address' AND chain_id = 1 AND address = $1),
			  (SELECT count(*) FROM verification_jobs
			   WHERE kind = 'derived' AND status = 'succeeded'
			     AND chain_id = 1 AND address = $1),
			  (SELECT count(*) FROM verification_results AS result
			   JOIN verification_jobs AS job ON job.id = result.job_id
			   WHERE job.kind = 'derived' AND job.address = $1
			     AND result.outcome_kind = 'verification_success'),
			  (SELECT count(*) FROM verified_contracts AS publication
			   JOIN verification_jobs AS job ON job.id = publication.verification_job_id
			   WHERE job.kind = 'derived' AND publication.address = $1
			     AND publication.contract_name = 'ProxyAdmin'),
			  (SELECT count(*) FROM derived_verification_attempts AS attempt
			   WHERE attempt.status = 'matched' AND attempt.creator_address = $2
			     AND attempt.created_address = $1 AND attempt.transaction_hash = $3
			     AND attempt.call_type = 'CREATE' AND attempt.contract_name = 'ProxyAdmin'),
			  EXISTS (
			    SELECT 1
			    FROM derived_verification_attempts AS attempt
			    JOIN derived_verification_scans AS scan
			      ON scan.compilation_id = attempt.compilation_id
			     AND scan.creator_address = attempt.creator_address
			    JOIN verification_compilation_units AS unit
			      ON unit.id = attempt.compilation_id
			    JOIN verified_contracts AS parent
			      ON parent.verification_job_id = unit.source_job_id
			    WHERE attempt.creator_address = $2 AND attempt.created_address = $1
			      AND attempt.transaction_hash = $3 AND attempt.status = 'matched'
			      AND scan.valid_from_block = attempt.block_number
			      AND parent.valid_from_block > attempt.block_number
			  )`, admin, proxy, transaction).Scan(
			&addressJobs, &derivedJobs, &results, &publications, &attempts,
			&exactHistoricalEpoch,
		)
		state := fmt.Sprintf(
			"address=%d derived=%d results=%d publications=%d attempts=%d historical=%t",
			addressJobs, derivedJobs, results, publications, attempts, exactHistoricalEpoch,
		)
		return err == nil && addressJobs == 0 && derivedJobs == 1 && results == 1 &&
			publications == 1 && attempts == 1 && exactHistoricalEpoch, state, err
	})
	waitHardhatSource(t, ctx, h, apiKey, deployment.Transparent.Admin)
	var child gen.VerifiedContractResponse
	h.mustGetJSON(ctx, "/api/v1/contracts/"+deployment.Transparent.Admin+"/verification", &child)
	if child.Data.VerificationOrigin != gen.VerificationOriginFactoryDerived ||
		child.Data.DerivedFrom == nil ||
		!strings.EqualFold(child.Data.DerivedFrom.CreatorAddress, deployment.Transparent.Proxy) ||
		!strings.EqualFold(child.Data.DerivedFrom.CreatedAddress, deployment.Transparent.Admin) ||
		!strings.EqualFold(child.Data.DerivedFrom.TransactionHash,
			deployment.Transactions["transparent"].Hash) ||
		child.Data.DerivedFrom.CallType != gen.DerivedVerificationProvenanceCallTypeCREATE ||
		child.Data.DerivedFrom.ParentContractName != "TransparentUpgradeableProxy" ||
		!strings.HasSuffix(child.Data.DerivedFrom.ParentFileName,
			"/proxy/transparent/TransparentUpgradeableProxy.sol") ||
		child.Data.ContractName != "ProxyAdmin" {
		t.Fatalf("factory-derived ProxyAdmin API = %#v", child.Data)
	}
	var parent gen.VerifiedContractResponse
	h.mustGetJSON(ctx, "/api/v1/contracts/"+deployment.Transparent.Proxy+"/verification", &parent)
	if len(parent.Data.DerivedChildren) != 1 ||
		!strings.EqualFold(parent.Data.DerivedChildren[0].Address, deployment.Transparent.Admin) ||
		parent.Data.DerivedChildren[0].Status != gen.DerivedContractStatusMatched ||
		!parent.Data.DerivedChildren[0].AutoVerified {
		t.Fatalf("Transparent proxy derived children = %#v", parent.Data.DerivedChildren)
	}
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

func waitHardhatDiamond(
	t *testing.T,
	ctx context.Context,
	h *harness,
	deployment hardhatDeployment,
) {
	t.Helper()
	diamond := common.HexToAddress(deployment.Diamond.Address)
	waitFor(t, ctx, "published ERC-2535 Diamond "+deployment.Diamond.Address, func() (bool, string, error) {
		var state, completeness, validation, standardCut string
		var facets, selectors, cuts, singular int64
		err := h.db.QueryRow(ctx, `
			WITH latest AS (
			    SELECT snapshot.id, snapshot.detection_state,
			           snapshot.completeness, snapshot.validation,
			           snapshot.standard_diamond_cut
			    FROM published_diamond_loupe_snapshots AS snapshot
			    JOIN canonical_blocks AS canonical
			      ON canonical.chain_id = snapshot.chain_id
			     AND canonical.number = snapshot.block_number
			     AND canonical.block_hash = snapshot.block_hash
			    WHERE snapshot.chain_id = 1
			      AND snapshot.diamond_address = $1
			      AND snapshot.canonical
			    ORDER BY snapshot.block_number DESC, snapshot.id DESC
			    LIMIT 1
			)
			SELECT
			  COALESCE((SELECT detection_state FROM latest), ''),
			  COALESCE((SELECT completeness FROM latest), ''),
			  COALESCE((SELECT validation FROM latest), ''),
			  COALESCE((SELECT standard_diamond_cut FROM latest), ''),
			  (SELECT count(*) FROM diamond_loupe_facets
			   WHERE snapshot_id IN (SELECT id FROM latest)),
			  (SELECT count(*) FROM diamond_loupe_selectors
			   WHERE snapshot_id IN (SELECT id FROM latest)),
			  (SELECT count(*) FROM diamond_cut_events AS event
			   JOIN canonical_blocks AS canonical
			     ON canonical.chain_id = event.chain_id
			    AND canonical.number = event.block_number
			    AND canonical.block_hash = event.block_hash
			   WHERE event.chain_id = 1 AND event.diamond_address = $1
			     AND event.canonical),
			  (SELECT count(*) FROM proxy_observations
			   WHERE chain_id = 1 AND proxy_address = $1)`, diamond.Bytes()).Scan(
			&state, &completeness, &validation, &standardCut,
			&facets, &selectors, &cuts, &singular,
		)
		diagnostic := fmt.Sprintf(
			"%s/%s/%s/%s facets=%d selectors=%d cuts=%d singular=%d",
			state, completeness, validation, standardCut,
			facets, selectors, cuts, singular,
		)
		return err == nil && state == "confirmed" && completeness == "complete" &&
			validation == "full" && standardCut == "absent" && facets == 3 &&
			selectors == 8 && cuts == 1 && singular == 0, diagnostic, err
	})

	var detail gen.ProxyDetailsResponse
	path := hardhatContractAPIPath(deployment.Diamond.Address, "/proxy", nil)
	if err := h.getJSON(ctx, path, &detail); err != nil {
		t.Fatalf("read Diamond proxy detail: %v", err)
	}
	if detail.Data.Status != gen.ProxyDetailStatusDetectedUnverified ||
		detail.Data.Implementation != nil || detail.Data.ImplementationAddresses == nil ||
		len(*detail.Data.ImplementationAddresses) != 2 || detail.Data.ProxyDetectionV2 == nil ||
		detail.Data.ProxyDetectionV2.Primary == nil ||
		detail.Data.ProxyDetectionV2.Primary.Family == nil ||
		*detail.Data.ProxyDetectionV2.Primary.Family != gen.ProxyDetectionV2FamilyErc2535 ||
		detail.Data.ProxyDetectionV2.Primary.Diamond == nil ||
		len(detail.Data.ProxyDetectionV2.Primary.Diamond.Facets) != 3 ||
		len(detail.Data.ProxyDetectionV2.Primary.Diamond.SelectorToFacet) != 8 ||
		detail.Data.ProxyDetectionV2.Primary.Diamond.StandardDiamondCut.Status != gen.DiamondCutPresenceAbsent {
		t.Fatalf("public Diamond proxy detail = %#v", detail)
	}
	wantFacets := make(map[common.Address]bool, len(deployment.Diamond.Facets))
	for _, address := range deployment.Diamond.Facets {
		wantFacets[common.HexToAddress(address)] = true
	}
	for _, address := range *detail.Data.ImplementationAddresses {
		if !wantFacets[common.HexToAddress(string(address))] {
			t.Fatalf("public Diamond implementation address %s not in %#v", address, deployment.Diamond.Facets)
		}
	}

	var history gen.DiamondCutHistoryResponse
	h.mustGetJSON(ctx, hardhatContractAPIPath(
		deployment.Diamond.Address, "/proxy/diamond-cuts", url.Values{"limit": {"10"}},
	), &history)
	if !strings.EqualFold(string(history.Data.DiamondAddress), deployment.Diamond.Address) ||
		len(history.Data.Items) != 1 || len(history.Data.Items[0].Cuts) != 3 ||
		common.HexToAddress(string(history.Data.Items[0].InitAddress)) != (common.Address{}) {
		t.Fatalf("public DiamondCut history = %#v", history)
	}
	for _, cut := range history.Data.Items[0].Cuts {
		if cut.Action != gen.DiamondFacetCutActionAdd {
			t.Fatalf("constructor DiamondCut action = %#v", cut)
		}
	}
}

func waitHardhatVerifiedProxyBinding(
	t *testing.T,
	ctx context.Context,
	h *harness,
	proxy, implementation string,
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
			  AND binding.proxy_pattern = 'clone'
			  AND proxy_interaction_coverage_contains(
				  binding.chain_id,
				  binding.observation_block_number,
				  binding.observation_block_hash,
				  tip.number,
				  tip.block_hash
			  )`, common.HexToAddress(proxy).Bytes(),
			common.HexToAddress(implementation).Bytes()).Scan(&count)
		if err != nil || count == 1 {
			return err == nil && count == 1, fmt.Sprintf("bindings=%d", count), err
		}
		var diagnostic string
		diagnosticErr := h.db.QueryRow(ctx, `
			WITH canonical_tip AS (
				SELECT number, block_hash
				FROM canonical_blocks
				WHERE chain_id = 1
				ORDER BY number DESC
				LIMIT 1
			)
			SELECT COALESCE((
				SELECT jsonb_build_object(
					'observation_block_number', binding.observation_block_number,
					'observation_block_hash', '0x' || encode(binding.observation_block_hash, 'hex'),
					'implementation', '0x' || encode(binding.implementation_address, 'hex'),
					'pattern', binding.proxy_pattern,
					'observation_canonical', EXISTS (
						SELECT 1 FROM canonical_blocks AS canonical
						WHERE canonical.chain_id = binding.chain_id
						  AND canonical.number = binding.observation_block_number
						  AND canonical.block_hash = binding.observation_block_hash
					),
					'coverage', proxy_interaction_coverage_contains(
						binding.chain_id, binding.observation_block_number,
						binding.observation_block_hash, tip.number, tip.block_hash
					),
					'tip_number', tip.number,
					'tip_hash', '0x' || encode(tip.block_hash, 'hex')
				)
				FROM verified_proxy_bindings AS binding
				CROSS JOIN canonical_tip AS tip
				WHERE binding.chain_id = 1 AND binding.proxy_address = $1
				ORDER BY binding.created_at DESC
				LIMIT 1
			), '{}'::jsonb)::text`, common.HexToAddress(proxy).Bytes()).Scan(&diagnostic)
		return false, fmt.Sprintf("bindings=%d diagnostic=%s", count, diagnostic), diagnosticErr
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
	clone, implementation, kind, immutableArgs string,
) {
	t.Helper()
	waitFor(t, ctx, "exact clone "+clone, func() (bool, string, error) {
		var gotImplementation, gotKind, gotPattern, gotArgs string
		err := h.db.QueryRow(ctx, `
			SELECT '0x' || encode(observation.implementation_address, 'hex'),
			       observation.proxy_kind, observation.proxy_pattern,
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
			&gotImplementation, &gotKind, &gotPattern, &gotArgs,
		)
		matched := err == nil && gotKind == kind && gotPattern == "clone" &&
			strings.EqualFold(gotImplementation, implementation) &&
			strings.EqualFold(gotArgs, immutableArgs)
		return matched, fmt.Sprintf("kind=%s pattern=%s implementation=%s args=%s",
			gotKind, gotPattern, gotImplementation, gotArgs), err
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

func assertHardhatCWIAImplementationCodeHashABI(
	t *testing.T,
	ctx context.Context,
	h *harness,
	deployment hardhatDeployment,
) {
	t.Helper()
	transactionHash := deployment.Transactions["cwiaSetStored"].Hash
	assertHardhatHistoricalMethod(
		t, ctx, h, deployment.CWIA.Account, transactionHash,
		"setStored", "setStored(uint256)",
	)

	var calldata gen.TransactionCalldataResponse
	h.mustGetJSON(ctx, "/api/v1/transactions/"+transactionHash+"/calldata", &calldata)
	if calldata.Data.Decoding.Status != gen.TransactionCalldataDecodingStatusDecoded ||
		calldata.Data.Decoding.FunctionName == nil ||
		*calldata.Data.Decoding.FunctionName != "setStored" ||
		calldata.Data.Decoding.Signature == nil ||
		*calldata.Data.Decoding.Signature != "setStored(uint256)" ||
		len(calldata.Data.Decoding.Inputs) != 1 {
		t.Fatalf("CWIA code-hash calldata = %#v", calldata.Data)
	}
	assertHardhatCWIAABISource(
		t, calldata.Data.Decoding.AbiSource, deployment.CWIA.ArtifactSource,
		gen.ABISourceKindProxyImplementation,
	)

	var logs gen.TransactionLogResponse
	h.mustGetJSON(ctx, "/api/v1/transactions/"+transactionHash+"/logs?limit=100", &logs)
	if len(logs.Data.Items) != 1 ||
		logs.Data.Items[0].Decoding.Status != gen.TransactionLogDecodingStatusDecoded ||
		logs.Data.Items[0].Decoding.EventName == nil ||
		*logs.Data.Items[0].Decoding.EventName != "StoredSet" ||
		logs.Data.Items[0].Decoding.Signature == nil ||
		*logs.Data.Items[0].Decoding.Signature != "StoredSet(uint256)" ||
		len(logs.Data.Items[0].Decoding.Arguments) != 1 {
		t.Fatalf("CWIA code-hash log = %#v", logs.Data)
	}
	assertHardhatCWIAABISource(
		t, logs.Data.Items[0].Decoding.AbiSource, deployment.CWIA.ArtifactSource,
		gen.ABISourceKindCodeHash, gen.ABISourceKindProxyImplementation,
	)

	var trace gen.TransactionTraceResponse
	h.mustGetJSON(ctx, "/api/v1/transactions/"+transactionHash+"/trace", &trace)
	var root *gen.TraceFrame
	for index := range trace.Data.Frames {
		if len(trace.Data.Frames[index].Path) == 0 {
			root = &trace.Data.Frames[index]
			break
		}
	}
	if root == nil || root.Decoding == nil ||
		root.Decoding.Status != gen.TraceCallDecodingStatusDecoded ||
		root.Decoding.FunctionName == nil || *root.Decoding.FunctionName != "setStored" ||
		root.Decoding.Signature == nil || *root.Decoding.Signature != "setStored(uint256)" ||
		len(root.Decoding.Inputs) != 1 {
		t.Fatalf("CWIA code-hash Trace = %#v", trace.Data)
	}
	assertHardhatCWIAABISource(
		t, root.Decoding.AbiSource, deployment.CWIA.ArtifactSource,
		gen.ABISourceKindProxyImplementation,
	)
}

func assertHardhatCWIAABISource(
	t *testing.T,
	source *gen.ABISource,
	artifactSource string,
	kinds ...gen.ABISourceKind,
) {
	t.Helper()
	if source == nil || source.Address == nil || source.CodeHash == nil ||
		!strings.EqualFold(string(*source.Address), artifactSource) ||
		!common.IsHexHash(string(*source.CodeHash)) || !slices.Contains(kinds, source.Kind) {
		t.Fatalf("CWIA ABI source = %#v, want address %s and one of %v", source, artifactSource, kinds)
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
	cwiaOwner           string
	cwiaNumber          string
	cwiaData            string
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
	assertHardhatAnonymousArtifact(t, ctx, h, deployment.CWIA.Implementation, "MyAccount")
	assertHardhatAnonymousArtifact(t, ctx, h, deployment.Transparent.Proxy, "TransparentUpgradeableProxy")

	immutableArgs := deployment.Clones.ImmutableArgsData
	cwiaArgs := deployment.CWIA.ImmutableArgs
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
		{
			address: deployment.CWIA.Account, pattern: gen.ProxyPatternClone,
			mechanism: gen.ProxyMechanismCwia, implementation: deployment.CWIA.Implementation,
			immutableArgs: &cwiaArgs, cwiaOwner: deployment.CWIA.Owner,
			cwiaNumber: deployment.CWIA.Number, cwiaData: deployment.CWIA.Data,
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
		response.Data.Resolution != gen.ContractArtifactResolutionExactAddress ||
		!strings.EqualFold(response.Data.Target.Address, address) ||
		response.Data.Target.ChainId != "1" || response.Data.ContractName != contractName ||
		!common.IsHexHash(response.Data.Target.CodeHash) ||
		!strings.EqualFold(response.Data.Source.Address, address) ||
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
	if expectation.cwiaOwner != "" {
		decoding := response.Data.ImmutableArgsDecoding
		if decoding == nil || decoding.Status != gen.CWIAImmutableArgsDecodingStatusDecoded ||
			decoding.Schema == nil || !common.IsHexHash(string(decoding.Schema.Sha256)) ||
			decoding.Schema.Version != gen.CWIAImmutableArgSchemaVersionN2 ||
			decoding.Schema.Source != gen.SolidityAst ||
			decoding.Schema.Encoding != gen.SoladyCwiaOffsets ||
			decoding.SchemaResolution == nil ||
			*decoding.SchemaResolution != gen.CWIASchemaResolutionExactAddress ||
			len(decoding.Schema.Fields) != 4 || len(decoding.Arguments) != 4 ||
			decoding.Arguments[0].Name != "owner" || decoding.Arguments[0].Type != "address" ||
			!strings.EqualFold(fmt.Sprint(decoding.Arguments[0].Value), expectation.cwiaOwner) ||
			decoding.Arguments[1].Name != "number" || decoding.Arguments[1].Type != "uint256" ||
			fmt.Sprint(decoding.Arguments[1].Value) != expectation.cwiaNumber ||
			decoding.Arguments[2].Name != "data_length" || decoding.Arguments[2].Type != "uint16" ||
			fmt.Sprint(decoding.Arguments[2].Value) != "11" ||
			decoding.Arguments[3].Name != "data" || decoding.Arguments[3].Type != "bytes" ||
			!strings.EqualFold(fmt.Sprint(decoding.Arguments[3].Value), expectation.cwiaData) {
			t.Fatalf("CWIA immutable argument decoding %s = %#v", expectation.address, decoding)
		}
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
