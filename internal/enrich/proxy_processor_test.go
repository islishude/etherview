package enrich

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/db/gen"
)

func TestProxyReplayCandidatesLoadOnlyGenerationFencedVerificationTargets(t *testing.T) {
	t.Parallel()
	query := strings.Join(strings.Fields(dbgen.EnrichLegacyProxyReplayCandidates), " ")
	for _, required := range []string{
		"FROM proxy_replay_targets AS target",
		"SELECT target.address, target.target_kind",
		"JOIN durable_job_replay_requests AS replay_request",
		"replay_request.job_id = $6::bigint",
		"target.source_verification_job_id::text = replay_request.source_key",
		"JOIN durable_jobs AS replay_job",
		"replay_job.status = 'leased'",
		"replay_job.claimed_generation = $7::bigint",
		"replay_job.leased_generation = $7::bigint",
		"LEFT JOIN verified_contract_proxy_artifacts AS artifact",
		"artifact.artifact_kind = 'uups_implementation'",
		"artifact.runtime_immutable_address = target.address",
		"LEFT JOIN verified_contracts AS verified",
		"verified.valid_to_block IS NULL OR verified.valid_to_block >= target.block_number",
		"replay_request.requested_generation > replay_job.completed_generation",
		"replay_request.requested_generation <= $7::bigint",
	} {
		if !strings.Contains(query, strings.Join(strings.Fields(required), " ")) {
			t.Fatalf("proxy replay candidate query lacks %q: %s", required, query)
		}
	}
	for _, forbidden := range []string{
		"FROM proxy_observations",
		"FROM beacon_implementation_observations",
		"replay_request.source_key::uuid",
	} {
		if strings.Contains(query, forbidden) {
			t.Fatalf("proxy replay candidate query contains %q: %s", forbidden, query)
		}
	}
}

func TestProxyReplayCandidatesSkipDirectUngeneratedProcessor(t *testing.T) {
	t.Parallel()
	processor := &PostgresProxyProcessor{}
	targets, err := processor.loadReplayCandidates(t.Context(), Job{
		ID: "direct-proxy-fixture", Stage: ProxyStage, ChainID: "1",
		BlockHash: uintWord(29), BlockNumber: 29,
	}, func(common.Address, string, bool) error {
		t.Fatal("direct processor loaded a durable replay target")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 0 {
		t.Fatalf("direct processor loaded UUPS replay targets: %+v", targets)
	}
}

func TestProxyReplayCandidatesKeepUUPSTargetsOutOfProxyDetector(t *testing.T) {
	t.Parallel()
	proxy, beacon, implementation := testAddress(24), testAddress(25), testAddress(26)
	implementationCodeHash := uintWord(27)
	verificationJob := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	backend := &fakeSQLBackend{query: func(query string, arguments []driver.NamedValue) (driver.Rows, error) {
		if !strings.Contains(query, "FROM proxy_replay_targets AS target") {
			return nil, fmt.Errorf("unexpected query: %s", query)
		}
		if len(arguments) != 7 || arguments[5].Value != "81" || arguments[6].Value != "4" {
			return nil, fmt.Errorf("replay arguments=%+v", arguments)
		}
		return &fakeSQLRows{
			columns: []string{"address", "target_kind", "source", "code_hash", "verification_job_id"},
			values: [][]driver.Value{
				{proxy[:], "proxy", proxySourceVerification, nil, nil},
				{beacon[:], "beacon", proxySourceVerification, nil, nil},
				{implementation[:], "uups", proxySourceVerification, implementationCodeHash[:], verificationJob},
			},
		}, nil
	}}
	processor := &PostgresProxyProcessor{db: openFakeSQLDB(t, backend)}
	type loadedCandidate struct {
		address common.Address
		source  string
	}
	loaded := make([]loadedCandidate, 0, 2)
	targets, err := processor.loadReplayCandidates(t.Context(), Job{
		ID: "81", Stage: ProxyStage, ChainID: "1", Generation: 4,
		BlockNumber: 28, BlockHash: uintWord(28),
	}, func(address common.Address, source string, force bool) error {
		if !force {
			t.Fatal("replay candidate was not forced")
		}
		loaded = append(loaded, loadedCandidate{address: address, source: source})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 || loaded[0].address != proxy || loaded[0].source != proxySourceVerification ||
		loaded[1].address != beacon || loaded[1].source != proxySourceBeaconReplay {
		t.Fatalf("proxy detector candidates=%+v", loaded)
	}
	if len(targets) != 1 || targets[0].address != implementation ||
		targets[0].codeHash != implementationCodeHash || targets[0].verificationJobID != verificationJob {
		t.Fatalf("UUPS replay targets=%+v", targets)
	}
}

func TestProbeUUPSReplayTargetsCallsOncePerImplementationCodeEpoch(t *testing.T) {
	t.Parallel()
	implementation := testAddress(27)
	code := []byte{0x60, 0x80, 0x60, 0x40}
	uuidInput, err := packStateProbe("proxiableUUID")
	if err != nil {
		t.Fatal(err)
	}
	versionInput, err := packStateProbe("UPGRADE_INTERFACE_VERSION")
	if err != nil {
		t.Fatal(err)
	}
	caller := &proxyStateCaller{
		code: map[common.Address][]byte{implementation: code},
		probeRaw: map[string][]byte{
			proxyProbeKey(implementation, uuidInput):    wordBytes(EIP1967ImplementationSlot),
			proxyProbeKey(implementation, versionInput): uupsTestVersionResponse("5.0.0"),
		},
	}
	job := Job{
		ID: "uups-shared-probe", Stage: ProxyStage, ChainID: "1",
		BlockNumber: 29, BlockHash: uintWord(29),
	}
	targets := []uupsImplementationProbeTarget{
		{
			address: implementation, codeHash: codeHash(code),
			verificationJobID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		},
		{
			address: implementation, codeHash: codeHash(code),
			verificationJobID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		},
	}
	results, err := probeUUPSReplayTargets(t.Context(), caller, job, targets, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || !results[0].compatible() || !results[1].compatible() ||
		results[0].target.verificationJobID != targets[0].verificationJobID ||
		results[1].target.verificationJobID != targets[1].verificationJobID {
		t.Fatalf("shared UUPS probe results=%+v", results)
	}
	caller.mu.Lock()
	calls := append([]proxyRPCCall(nil), caller.calls...)
	caller.mu.Unlock()
	if len(calls) != 3 {
		t.Fatalf("shared UUPS implementation was probed %d times: %+v", len(calls), calls)
	}
}

func TestProxyGenerationCarryForwardExcludesEveryRedetectedAddress(t *testing.T) {
	t.Parallel()
	query := strings.Join(strings.Fields(dbgen.EnrichLegacyCarryForwardProxyGeneration), " ")
	for _, required := range []string{
		"FROM durable_stage_publications AS publication",
		"publication.job_generation < $6::bigint",
		"publication.state = 'complete'",
		"ORDER BY publication.job_generation DESC LIMIT 1",
		"SELECT generation.proxy_address AS address FROM proxy_observation_generations AS generation",
		"SELECT generation.beacon_address AS address FROM beacon_observation_generations AS generation",
		"SELECT generation.implementation_address AS address FROM uups_implementation_observation_generations AS generation",
		"SELECT evidence.address FROM proxy_detection_evidence AS evidence",
		"INSERT INTO proxy_observation_generations",
		"INSERT INTO beacon_observation_generations",
		"INSERT INTO uups_implementation_observation_generations",
		"INSERT INTO proxy_artifact_resolutions",
		"INSERT INTO proxy_detection_evidence",
		"NOT EXISTS ( SELECT 1 FROM redetected WHERE redetected.address = source.proxy_address )",
		"NOT EXISTS ( SELECT 1 FROM redetected WHERE redetected.address = source.beacon_address )",
		"NOT EXISTS ( SELECT 1 FROM redetected WHERE redetected.address = source.implementation_address )",
		"NOT EXISTS ( SELECT 1 FROM redetected WHERE redetected.address = source.address )",
	} {
		if !strings.Contains(query, strings.Join(strings.Fields(required), " ")) {
			t.Fatalf("proxy carry-forward query lacks %q: %s", required, query)
		}
	}
	if strings.Contains(query, "INSERT INTO uups_implementation_observations") {
		t.Fatalf("proxy carry-forward copied raw UUPS observations: %s", query)
	}
}

func TestPostgresProxyProcessorLoadsGenesisPredeployCandidatesOnlyAtBlockZero(t *testing.T) {
	t.Parallel()
	address := testAddress(30)
	queries := 0
	backend := &fakeSQLBackend{query: func(query string, arguments []driver.NamedValue) (driver.Rows, error) {
		if !strings.Contains(query, "FROM genesis_account_observations") {
			return nil, fmt.Errorf("unexpected query: %s", query)
		}
		queries++
		if len(arguments) != 2 || arguments[0].Value != "777" {
			t.Fatalf("genesis candidate arguments = %+v", arguments)
		}
		return &fakeSQLRows{
			columns: []string{"address"},
			values:  [][]driver.Value{{address[:]}},
		}, nil
	}}
	processor := &PostgresProxyProcessor{db: openFakeSQLDB(t, backend)}
	job := Job{
		ID: "genesis-proxy", Stage: ProxyStage, ChainID: "777",
		BlockHash: uintWord(300), BlockNumber: 0,
	}
	var candidate proxyCandidate
	if err := processor.loadGenesisCandidates(t.Context(), job, func(
		address common.Address,
		source string,
		force bool,
	) error {
		candidate = proxyCandidate{
			address: address, sources: map[string]struct{}{source: {}}, force: force,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if candidate.address != address || !candidate.force {
		t.Fatalf("genesis proxy candidate = %+v", candidate)
	}
	if _, ok := candidate.sources[proxySourceGenesis]; !ok {
		t.Fatalf("genesis proxy candidate sources = %+v", candidate.sources)
	}
	job.BlockNumber = 1
	if err := processor.loadGenesisCandidates(t.Context(), job, func(common.Address, string, bool) error {
		t.Fatal("non-zero block produced a genesis proxy candidate")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if queries != 1 {
		t.Fatalf("genesis candidate query count = %d, want 1", queries)
	}
}

type proxyRPCCall struct {
	method    string
	address   string
	blockHash string
}

type proxyStateCaller struct {
	mu           sync.Mutex
	calls        []proxyRPCCall
	code         map[common.Address][]byte
	storage      map[string]common.Hash
	beacon       map[common.Address]common.Address
	err          error
	methodErrors map[string]error
	beaconRaw    map[common.Address][]byte
	probeRaw     map[string][]byte
}

func (caller *proxyStateCaller) CallContext(_ context.Context, result any, method string, params ...any) error {
	if caller.err != nil {
		return caller.err
	}
	if err := caller.methodErrors[method]; err != nil {
		return err
	}
	if len(params) < 2 {
		return fmt.Errorf("%s has too few parameters", method)
	}
	blockReference, ok := params[len(params)-1].(rpc.BlockNumberOrHash)
	if !ok || !blockReference.RequireCanonical {
		return errors.New("proxy RPC did not use a canonical EIP-1898 selector")
	}
	if blockReference.BlockHash == nil {
		return errors.New("proxy RPC block hash is missing")
	}
	blockHash := blockReference.BlockHash.String()
	var address common.Address
	switch method {
	case "eth_getCode", "eth_getStorageAt":
		var ok bool
		address, ok = params[0].(common.Address)
		if !ok {
			return fmt.Errorf("%s address is %T", method, params[0])
		}
	case "eth_call":
		request, ok := params[0].(map[string]any)
		if !ok {
			return errors.New("beacon call is malformed")
		}
		address, ok = request["to"].(common.Address)
		if !ok {
			return fmt.Errorf("beacon address is %T", request["to"])
		}
	default:
		return fmt.Errorf("unexpected proxy RPC method %s", method)
	}
	caller.mu.Lock()
	caller.calls = append(caller.calls, proxyRPCCall{method: method, address: address.String(), blockHash: blockHash})
	caller.mu.Unlock()
	switch method {
	case "eth_getCode":
		destination, ok := result.(*hexutil.Bytes)
		if !ok {
			return errors.New("code destination is invalid")
		}
		*destination = hexutil.Bytes(common.CopyBytes(caller.code[address]))
	case "eth_getStorageAt":
		slot, ok := params[1].(common.Hash)
		if !ok {
			return fmt.Errorf("storage slot is %T", params[1])
		}
		destination, ok := result.(*hexutil.Bytes)
		if !ok {
			return errors.New("storage destination is invalid")
		}
		*destination = hexutil.Bytes(wordBytes(caller.storage[address.String()+":"+slot.String()]))
	case "eth_call":
		request := params[0].(map[string]any)
		data, ok := request["data"].(hexutil.Bytes)
		if !ok || len(data) < 4 {
			return errors.New("state probe calldata is invalid")
		}
		if raw, exists := caller.probeRaw[proxyProbeKey(address, data)]; exists {
			destination, ok := result.(*hexutil.Bytes)
			if !ok {
				return errors.New("state probe destination is invalid")
			}
			*destination = hexutil.Bytes(common.CopyBytes(raw))
			return nil
		}
		if raw, exists := caller.beaconRaw[address]; exists {
			destination, ok := result.(*hexutil.Bytes)
			if !ok {
				return errors.New("beacon destination is invalid")
			}
			*destination = hexutil.Bytes(common.CopyBytes(raw))
			return nil
		}
		implementation, ok := caller.beacon[address]
		if !ok {
			return testRPCError{code: 3, message: "execution reverted"}
		}
		destination, ok := result.(*hexutil.Bytes)
		if !ok {
			return errors.New("beacon destination is invalid")
		}
		*destination = hexutil.Bytes(wordBytes(addressWord(implementation)))
	}
	return nil
}

func proxyProbeKey(address common.Address, data []byte) string {
	if len(data) < 4 {
		return address.String()
	}
	return address.String() + ":" + hexutil.Encode(data)
}

func TestRPCProxyDetectorRecognizesAuthenticatedImmutableCloneWithoutImplementationCode(t *testing.T) {
	t.Parallel()
	proxy, implementation := testAddress(31), testAddress(32)
	code := append(append(append([]byte(nil), minimalProxyPrefix...), implementation[:]...), minimalProxySuffix...)
	code = append(code, []byte("immutable-args")...)
	caller := &proxyStateCaller{code: map[common.Address][]byte{
		proxy: code,
	}}
	job := Job{ID: "minimal", Stage: ProxyStage, ChainID: "1", BlockHash: uintWord(310), BlockNumber: 310}
	detector := rpcProxyDetector{
		caller: caller, limits: ProxyLimits{MaxCandidates: 4, MaxCodeBytes: 4096, MaxDetailsBytes: 512},
		cloneCreation: func(_ context.Context, address common.Address, runtime []byte) (bool, error) {
			return address == proxy && reflect.DeepEqual(runtime, code), nil
		},
	}
	detections, err := detector.detectBlock(t.Context(), job, []proxyCandidate{{
		address: proxy, sources: map[string]struct{}{proxySourceReceipt: {}}, force: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(detections) != 1 || detections[0].proxy == nil ||
		detections[0].proxy.kind != ProxyMinimal1167 || detections[0].proxy.implementation != implementation ||
		detections[0].proxy.minimalExact || !detections[0].proxy.immutableArgsExact ||
		detections[0].proxy.evidenceState != "exact" ||
		detections[0].proxy.implementationHash != codeHash(nil) ||
		len(detections[0].proxy.implementationCode) != 0 ||
		string(detections[0].proxy.immutableArgs) != "immutable-args" {
		t.Fatalf("detection=%+v", detections)
	}
	caller.mu.Lock()
	calls := append([]proxyRPCCall(nil), caller.calls...)
	caller.mu.Unlock()
	if got := []string{calls[0].method, calls[1].method}; !reflect.DeepEqual(got, []string{"eth_getCode", "eth_getCode"}) {
		t.Fatalf("RPC methods=%v", got)
	}
	for _, call := range calls {
		if call.blockHash != job.BlockHash.String() {
			t.Fatalf("RPC call=%+v, want block %s", call, job.BlockHash)
		}
	}
}

func TestRPCProxyDetectorRecognizesCanonicalCloneWithoutImplementationCode(t *testing.T) {
	t.Parallel()
	proxy, implementation := testAddress(37), testAddress(38)
	runtime := append(append(append([]byte(nil), minimalProxyPrefix...), implementation[:]...), minimalProxySuffix...)
	caller := &proxyStateCaller{code: map[common.Address][]byte{proxy: runtime}}
	detector := rpcProxyDetector{
		caller: caller,
		limits: ProxyLimits{MaxCandidates: 2, MaxCodeBytes: 4096, MaxDetailsBytes: 512},
	}
	detections, err := detector.detectBlock(t.Context(), Job{
		ID: "canonical-clone-empty-implementation", Stage: ProxyStage, ChainID: "1",
		BlockHash: uintWord(313), BlockNumber: 313,
	}, []proxyCandidate{{address: proxy, force: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(detections) != 1 || detections[0].rejected != "" || detections[0].proxy == nil {
		t.Fatalf("detections=%+v", detections)
	}
	resolved := detections[0].proxy
	if resolved.kind != ProxyMinimal1167 || resolved.pattern != ProxyPatternClone ||
		resolved.implementation != implementation || resolved.evidenceState != "exact" ||
		!resolved.minimalExact || resolved.immutableArgsExact || len(resolved.immutableArgs) != 0 ||
		resolved.implementationHash != codeHash(nil) || len(resolved.implementationCode) != 0 {
		t.Fatalf("resolved=%+v", resolved)
	}
}

func TestRPCProxyDetectorRequiresCreationProofForImmutableClone(t *testing.T) {
	t.Parallel()
	proxy, implementation := testAddress(33), testAddress(34)
	runtime := append(append(append([]byte(nil), minimalProxyPrefix...), implementation[:]...), minimalProxySuffix...)
	runtime = append(runtime, []byte("immutable-args")...)
	caller := &proxyStateCaller{code: map[common.Address][]byte{proxy: runtime}}
	detector := rpcProxyDetector{
		caller: caller,
		limits: ProxyLimits{MaxCandidates: 2, MaxCodeBytes: 4096, MaxDetailsBytes: 512},
	}
	detections, err := detector.detectBlock(t.Context(), Job{
		ID: "immutable-unverified", Stage: ProxyStage, ChainID: "1",
		BlockHash: uintWord(311), BlockNumber: 311,
	}, []proxyCandidate{{address: proxy, force: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(detections) != 1 || detections[0].proxy != nil ||
		detections[0].rejected != "immutable_args_creation_unverified" {
		t.Fatalf("detections=%+v", detections)
	}
	caller.mu.Lock()
	calls := append([]proxyRPCCall(nil), caller.calls...)
	caller.mu.Unlock()
	if len(calls) != 1 || calls[0].method != "eth_getCode" || calls[0].address != proxy.String() {
		t.Fatalf("RPC calls=%+v", calls)
	}
}

func TestRPCProxyDetectorRejectsInvalidMinimalCloneTargets(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name           string
		implementation func(common.Address) common.Address
		trailingBytes  int
		want           string
	}{
		{
			name: "zero implementation", implementation: func(common.Address) common.Address { return common.Address{} },
			want: "minimal_zero_implementation",
		},
		{
			name: "self implementation", implementation: func(proxy common.Address) common.Address { return proxy },
			want: "self_implementation",
		},
		{
			name: "oversized immutable args", implementation: func(common.Address) common.Address { return testAddress(36) },
			trailingBytes: MaxCloneImmutableArgs + 1, want: "immutable_args_too_large",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			proxy := testAddress(35)
			implementation := test.implementation(proxy)
			runtime := append(append(append([]byte(nil), minimalProxyPrefix...), implementation[:]...), minimalProxySuffix...)
			runtime = append(runtime, make([]byte, test.trailingBytes)...)
			caller := &proxyStateCaller{code: map[common.Address][]byte{proxy: runtime}}
			detector := rpcProxyDetector{
				caller: caller,
				limits: ProxyLimits{MaxCandidates: 2, MaxCodeBytes: 1 << 20, MaxDetailsBytes: 512},
			}
			detections, err := detector.detectBlock(t.Context(), Job{
				ID: test.name, Stage: ProxyStage, ChainID: "1",
				BlockHash: uintWord(312), BlockNumber: 312,
			}, []proxyCandidate{{address: proxy, force: true}})
			if err != nil {
				t.Fatal(err)
			}
			if len(detections) != 1 || detections[0].proxy != nil || detections[0].rejected != test.want {
				t.Fatalf("detections=%+v", detections)
			}
		})
	}
}

func TestRPCProxyDetectorResolvesEIP1967AndBeaconFinalImplementation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name           string
		kind           ProxyKind
		proxy          common.Address
		implementation common.Address
		beacon         *common.Address
	}{
		{name: "implementation", kind: ProxyEIP1967, proxy: testAddress(41), implementation: testAddress(42)},
		{name: "beacon", kind: ProxyBeacon, proxy: testAddress(51), implementation: testAddress(52), beacon: addressPointer(testAddress(53))},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			storage := map[string]common.Hash{}
			if test.beacon == nil {
				storage[test.proxy.String()+":"+EIP1967ImplementationSlot.String()] = addressWord(test.implementation)
			} else {
				storage[test.proxy.String()+":"+EIP1967BeaconSlot.String()] = addressWord(*test.beacon)
			}
			caller := &proxyStateCaller{
				code:    map[common.Address][]byte{test.proxy: {0x60, 0x01}, test.implementation: {0x60, 0x02}},
				storage: storage, beacon: map[common.Address]common.Address{},
			}
			if test.beacon != nil {
				caller.code[*test.beacon] = []byte{0x60, 0x03}
				caller.beacon[*test.beacon] = test.implementation
			}
			job := Job{ID: test.name, Stage: ProxyStage, ChainID: "1", BlockHash: uintWord(400), BlockNumber: 400}
			detector := rpcProxyDetector{caller: caller, limits: ProxyLimits{MaxCandidates: 4, MaxCodeBytes: 4096, MaxDetailsBytes: 512}}
			detections, err := detector.detectBlock(t.Context(), job, []proxyCandidate{{address: test.proxy}})
			if err != nil {
				t.Fatal(err)
			}
			resolved := detections[0].proxy
			if resolved == nil || resolved.kind != test.kind || resolved.implementation != test.implementation ||
				(resolved.beacon == nil) != (test.beacon == nil) {
				t.Fatalf("resolved=%+v", resolved)
			}
			caller.mu.Lock()
			calls := append([]proxyRPCCall(nil), caller.calls...)
			caller.mu.Unlock()
			for _, call := range calls {
				if call.blockHash != job.BlockHash.String() {
					t.Fatalf("mixed block selector: %+v", calls)
				}
			}
		})
	}
}

func TestRPCProxyDetectorKeepsVerifiedERC1967ResolutionIndependentOfSharedUUPSProbe(t *testing.T) {
	t.Parallel()
	proxy, implementation := testAddress(54), testAddress(55)
	uuidInput, err := packStateProbe("proxiableUUID")
	if err != nil {
		t.Fatal(err)
	}
	versionInput, err := packStateProbe("UPGRADE_INTERFACE_VERSION")
	if err != nil {
		t.Fatal(err)
	}
	version := make([]byte, 96)
	version[31] = 32
	version[63] = 5
	copy(version[64:], "5.0.0")
	caller := &proxyStateCaller{
		code: map[common.Address][]byte{
			proxy: {0x60, 0x01}, implementation: {0x60, 0x02},
		},
		storage: map[string]common.Hash{
			proxy.String() + ":" + EIP1967ImplementationSlot.String(): addressWord(implementation),
		},
		probeRaw: map[string][]byte{
			proxyProbeKey(implementation, uuidInput):    wordBytes(EIP1967ImplementationSlot),
			proxyProbeKey(implementation, versionInput): version,
		},
	}
	job := Job{ID: "uups-v5", Stage: ProxyStage, ChainID: "1", BlockHash: uintWord(450), BlockNumber: 450}
	detector := rpcProxyDetector{
		caller: caller,
		limits: ProxyLimits{MaxCandidates: 2, MaxCodeBytes: 4096, MaxDetailsBytes: 512},
		artifact: func(_ context.Context, address common.Address, _ common.Hash) (proxyArtifactEvidence, bool, error) {
			switch address {
			case proxy:
				return proxyArtifactEvidence{
					kind: "erc1967_proxy", standardVersion: OpenZeppelin561Standard,
					verificationJob: "11111111-1111-4111-8111-111111111111",
				}, true, nil
			case implementation:
				self := implementation
				return proxyArtifactEvidence{
					kind: "uups_implementation", standardVersion: OpenZeppelin561Standard,
					runtimeImmutable: &self,
					verificationJob:  "22222222-2222-4222-8222-222222222222",
				}, true, nil
			default:
				return proxyArtifactEvidence{}, false, nil
			}
		},
	}
	detections, err := detector.detectBlock(t.Context(), job, []proxyCandidate{{address: proxy}})
	if err != nil {
		t.Fatal(err)
	}
	resolved, exact := detections[0].proxy, detections[0].exact
	if resolved == nil || resolved.pattern != ProxyPatternUnknown || resolved.evidenceState != "generic" {
		t.Fatalf("raw resolution=%+v", resolved)
	}
	if exact == nil || exact.pattern != ProxyPatternERC1967 ||
		exact.standardVersion != OpenZeppelin561Standard || exact.evidenceState != "exact" ||
		exact.implementationArtifactJob != "" {
		t.Fatalf("exact resolution=%+v", exact)
	}
	caller.mu.Lock()
	calls := append([]proxyRPCCall(nil), caller.calls...)
	caller.mu.Unlock()
	for _, call := range calls {
		if call.blockHash != job.BlockHash.String() {
			t.Fatalf("mixed block selector: %+v", calls)
		}
		if call.method == "eth_call" && call.address == implementation.String() {
			t.Fatalf("proxy-local resolution probed UUPS implementation directly: %+v", calls)
		}
	}
}

func TestRPCProxyDetectorUsesVerifiedTransparentImmutableAdminAsAuthority(t *testing.T) {
	t.Parallel()
	proxy, implementation := testAddress(80), testAddress(81)
	immutableAdmin, compatibilityAdmin := testAddress(82), testAddress(83)
	caller := &proxyStateCaller{
		code: map[common.Address][]byte{
			proxy:              {0x60, 0x01},
			implementation:     {0x60, 0x02},
			immutableAdmin:     {0x60, 0x03},
			compatibilityAdmin: {0x60, 0x04},
		},
		storage: map[string]common.Hash{
			proxy.String() + ":" + EIP1967ImplementationSlot.String(): addressWord(implementation),
			proxy.String() + ":" + EIP1967AdminSlot.String():          addressWord(compatibilityAdmin),
		},
	}
	job := Job{
		ID: "transparent-immutable-admin", Stage: ProxyStage, ChainID: "1",
		BlockHash: uintWord(451), BlockNumber: 451,
	}
	detector := rpcProxyDetector{
		caller: caller,
		limits: ProxyLimits{MaxCandidates: 2, MaxCodeBytes: 4096, MaxDetailsBytes: 512},
		artifact: func(_ context.Context, address common.Address, _ common.Hash) (proxyArtifactEvidence, bool, error) {
			if address != proxy {
				return proxyArtifactEvidence{}, false, nil
			}
			return proxyArtifactEvidence{
				kind: "transparent_proxy", standardVersion: OpenZeppelin561Standard,
				runtimeImmutable: &immutableAdmin,
				verificationJob:  "33333333-3333-4333-8333-333333333333",
			}, true, nil
		},
	}
	detections, err := detector.detectBlock(t.Context(), job, []proxyCandidate{{address: proxy}})
	if err != nil {
		t.Fatal(err)
	}
	if len(detections) != 1 || detections[0].exact == nil {
		t.Fatalf("detections=%+v", detections)
	}
	exact := detections[0].exact
	if exact.pattern != ProxyPatternTransparent || exact.evidenceState != "exact" ||
		exact.admin == nil || *exact.admin != immutableAdmin || *exact.admin == compatibilityAdmin ||
		exact.adminHash != codeHash(caller.code[immutableAdmin]) ||
		exact.evidence["admin_authority"] != "runtime_immutable" ||
		exact.evidence["admin_slot_matches"] != false {
		t.Fatalf("exact transparent resolution=%+v evidence=%+v", exact.proxyResolution, exact.evidence)
	}
	assertProxyDetectorCallsUseBlock(t, caller, job.BlockHash)
}

func TestRPCProxyDetectorUsesVerifiedBeaconImmutableAsAuthority(t *testing.T) {
	t.Parallel()
	proxy := testAddress(84)
	immutableBeacon, compatibilityBeacon := testAddress(85), testAddress(86)
	immutableImplementation, compatibilityImplementation := testAddress(87), testAddress(88)
	caller := &proxyStateCaller{
		code: map[common.Address][]byte{
			proxy:                       {0x60, 0x01},
			immutableBeacon:             {0x60, 0x02},
			compatibilityBeacon:         {0x60, 0x03},
			immutableImplementation:     {0x60, 0x04},
			compatibilityImplementation: {0x60, 0x05},
		},
		storage: map[string]common.Hash{
			proxy.String() + ":" + EIP1967BeaconSlot.String(): addressWord(compatibilityBeacon),
		},
		beacon: map[common.Address]common.Address{
			immutableBeacon:     immutableImplementation,
			compatibilityBeacon: compatibilityImplementation,
		},
	}
	job := Job{
		ID: "beacon-immutable-authority", Stage: ProxyStage, ChainID: "1",
		BlockHash: uintWord(452), BlockNumber: 452,
	}
	detector := rpcProxyDetector{
		caller: caller,
		limits: ProxyLimits{MaxCandidates: 2, MaxCodeBytes: 4096, MaxDetailsBytes: 512},
		artifact: func(_ context.Context, address common.Address, _ common.Hash) (proxyArtifactEvidence, bool, error) {
			if address != proxy {
				return proxyArtifactEvidence{}, false, nil
			}
			return proxyArtifactEvidence{
				kind: "beacon_proxy", standardVersion: OpenZeppelin561Standard,
				runtimeImmutable: &immutableBeacon,
				verificationJob:  "44444444-4444-4444-8444-444444444444",
			}, true, nil
		},
	}
	detections, err := detector.detectBlock(t.Context(), job, []proxyCandidate{{address: proxy}})
	if err != nil {
		t.Fatal(err)
	}
	if len(detections) != 1 || detections[0].exact == nil {
		t.Fatalf("detections=%+v", detections)
	}
	exact := detections[0].exact
	if exact.pattern != ProxyPatternBeacon || exact.evidenceState != "exact" ||
		exact.beacon == nil || *exact.beacon != immutableBeacon || *exact.beacon == compatibilityBeacon ||
		exact.implementation != immutableImplementation ||
		exact.beaconHash != codeHash(caller.code[immutableBeacon]) ||
		exact.evidence["beacon_authority"] != "runtime_immutable" ||
		exact.evidence["beacon_slot_matches"] != false {
		t.Fatalf("exact beacon resolution=%+v evidence=%+v", exact.proxyResolution, exact.evidence)
	}
	assertProxyDetectorCallsUseBlock(t, caller, job.BlockHash)
}

func TestRPCProxyDetectorDoesNotConsumeRejectedSharedUUPSProbeResponses(t *testing.T) {
	t.Parallel()
	proxy, implementation := testAddress(89), testAddress(90)
	uuidInput, err := packStateProbe("proxiableUUID")
	if err != nil {
		t.Fatal(err)
	}
	versionInput, err := packStateProbe("UPGRADE_INTERFACE_VERSION")
	if err != nil {
		t.Fatal(err)
	}
	wrongUUID := EIP1967ImplementationSlot
	wrongUUID[31] ^= 1
	version := make([]byte, 96)
	version[31], version[63] = 32, 5
	copy(version[64:], "5.0.0")
	caller := &proxyStateCaller{
		code: map[common.Address][]byte{
			proxy: {0x60, 0x01}, implementation: {0x60, 0x02},
		},
		storage: map[string]common.Hash{
			proxy.String() + ":" + EIP1967ImplementationSlot.String(): addressWord(implementation),
		},
		probeRaw: map[string][]byte{
			proxyProbeKey(implementation, uuidInput):    wordBytes(wrongUUID),
			proxyProbeKey(implementation, versionInput): version,
		},
	}
	job := Job{
		ID: "uups-wrong-uuid", Stage: ProxyStage, ChainID: "1",
		BlockHash: uintWord(453), BlockNumber: 453,
	}
	detector := rpcProxyDetector{
		caller: caller,
		limits: ProxyLimits{MaxCandidates: 2, MaxCodeBytes: 4096, MaxDetailsBytes: 512},
		artifact: func(_ context.Context, address common.Address, _ common.Hash) (proxyArtifactEvidence, bool, error) {
			switch address {
			case proxy:
				return proxyArtifactEvidence{
					kind: "erc1967_proxy", standardVersion: OpenZeppelin561Standard,
					verificationJob: "55555555-5555-4555-8555-555555555555",
				}, true, nil
			case implementation:
				self := implementation
				return proxyArtifactEvidence{
					kind: "uups_implementation", standardVersion: OpenZeppelin561Standard,
					runtimeImmutable: &self,
					verificationJob:  "66666666-6666-4666-8666-666666666666",
				}, true, nil
			default:
				return proxyArtifactEvidence{}, false, nil
			}
		},
	}
	detections, err := detector.detectBlock(t.Context(), job, []proxyCandidate{{address: proxy}})
	if err != nil {
		t.Fatal(err)
	}
	if len(detections) != 1 || detections[0].exact == nil {
		t.Fatalf("detections=%+v", detections)
	}
	exact := detections[0].exact
	if exact.pattern != ProxyPatternERC1967 || exact.evidenceState != "exact" ||
		exact.implementation != implementation || exact.implementationArtifactJob != "" {
		t.Fatalf("wrong UUID promoted exact resolution=%+v", exact)
	}
	if _, exists := exact.evidence["uups_uuid"]; exists {
		t.Fatalf("wrong UUID produced UUPS evidence=%+v", exact.evidence)
	}
	caller.mu.Lock()
	calls := append([]proxyRPCCall(nil), caller.calls...)
	caller.mu.Unlock()
	for _, call := range calls {
		if call.method == "eth_call" && call.address == implementation.String() {
			t.Fatalf("proxy-local resolution consumed UUPS probe response: %+v", calls)
		}
	}
	assertProxyDetectorCallsUseBlock(t, caller, job.BlockHash)
}

func assertProxyDetectorCallsUseBlock(t *testing.T, caller *proxyStateCaller, blockHash common.Hash) {
	t.Helper()
	caller.mu.Lock()
	calls := append([]proxyRPCCall(nil), caller.calls...)
	caller.mu.Unlock()
	if len(calls) == 0 {
		t.Fatal("proxy detector made no RPC calls")
	}
	for _, call := range calls {
		if call.blockHash != blockHash.String() {
			t.Fatalf("RPC call=%+v, want block %s", call, blockHash)
		}
	}
}

func TestRPCProxyDetectorReportsMissingEIP1898AsUnavailable(t *testing.T) {
	t.Parallel()
	caller := &proxyStateCaller{err: testRPCError{code: -32602, message: "invalid argument 1: block hash selectors unsupported"}}
	detector := rpcProxyDetector{caller: caller, limits: ProxyLimits{MaxCandidates: 1, MaxCodeBytes: 1024, MaxDetailsBytes: 256}}
	job := Job{ID: "unsupported", Stage: ProxyStage, ChainID: "1", BlockHash: uintWord(500), BlockNumber: 500}
	_, err := detector.detectBlock(t.Context(), job, []proxyCandidate{{address: testAddress(50)}})
	var classified stageError
	if !errors.As(err, &classified) || classified.kind != "unavailable" || !strings.Contains(classified.Error(), "exact block-hash state") {
		t.Fatalf("error=%#v", err)
	}
}

func TestRPCProxyDetectorRejectsPoisonCandidateWithoutBlockingValidProxy(t *testing.T) {
	t.Parallel()
	poison, valid := testAddress(61), testAddress(62)
	poisonImplementation, poisonBeacon := testAddress(63), testAddress(64)
	validImplementation := testAddress(65)
	caller := &proxyStateCaller{
		code: map[common.Address][]byte{
			poison: {0x60, 0x01}, valid: {0x60, 0x02}, validImplementation: {0x60, 0x03},
		},
		storage: map[string]common.Hash{
			poison.String() + ":" + EIP1967ImplementationSlot.String(): addressWord(poisonImplementation),
			poison.String() + ":" + EIP1967BeaconSlot.String():         addressWord(poisonBeacon),
			valid.String() + ":" + EIP1967ImplementationSlot.String():  addressWord(validImplementation),
		},
	}
	job := Job{ID: "mixed", Stage: ProxyStage, ChainID: "1", BlockHash: uintWord(600), BlockNumber: 600}
	detector := rpcProxyDetector{caller: caller, limits: ProxyLimits{MaxCandidates: 4, MaxCodeBytes: 4096, MaxDetailsBytes: 512}}
	detections, err := detector.detectBlock(t.Context(), job, []proxyCandidate{{address: poison}, {address: valid}})
	if err != nil {
		t.Fatal(err)
	}
	if len(detections) != 2 || detections[0].proxy != nil || detections[0].rejected != "ambiguous_slots" ||
		len(detections[0].code) == 0 || detections[1].proxy == nil || detections[1].proxy.implementation != validImplementation {
		t.Fatalf("detections=%+v", detections)
	}
}

func TestRPCProxyDetectorTreatsInvalidCandidateStateAsLocalRejection(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		setup func(common.Address, *proxyStateCaller)
		want  string
	}{
		{
			name: "self implementation", want: "self_implementation",
			setup: func(proxy common.Address, caller *proxyStateCaller) {
				caller.storage[proxy.String()+":"+EIP1967ImplementationSlot.String()] = addressWord(proxy)
			},
		},
		{
			name: "implementation without code", want: "implementation_has_no_code",
			setup: func(proxy common.Address, caller *proxyStateCaller) {
				caller.storage[proxy.String()+":"+EIP1967ImplementationSlot.String()] = addressWord(testAddress(72))
			},
		},
		{
			name: "invalid slot address", want: "invalid_slot_address",
			setup: func(proxy common.Address, caller *proxyStateCaller) {
				word := addressWord(testAddress(73))
				word[0] = 1
				caller.storage[proxy.String()+":"+EIP1967ImplementationSlot.String()] = word
			},
		},
		{
			name: "invalid beacon return", want: "invalid_beacon_implementation",
			setup: func(proxy common.Address, caller *proxyStateCaller) {
				beacon := testAddress(74)
				caller.storage[proxy.String()+":"+EIP1967BeaconSlot.String()] = addressWord(beacon)
				caller.code[beacon] = []byte{0x60, 0x02}
				caller.beaconRaw[beacon] = []byte{1}
			},
		},
		{
			name: "beacon execution revert", want: "invalid_beacon_implementation",
			setup: func(proxy common.Address, caller *proxyStateCaller) {
				beacon := testAddress(75)
				caller.storage[proxy.String()+":"+EIP1967BeaconSlot.String()] = addressWord(beacon)
				caller.code[beacon] = []byte{0x60, 0x02}
				caller.methodErrors["eth_call"] = testRPCError{code: 3, message: "execution reverted"}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			proxy := testAddress(71)
			caller := &proxyStateCaller{
				code: map[common.Address][]byte{proxy: {0x60, 0x01}}, storage: map[string]common.Hash{},
				beacon: map[common.Address]common.Address{}, beaconRaw: map[common.Address][]byte{}, methodErrors: map[string]error{},
			}
			test.setup(proxy, caller)
			job := Job{ID: test.name, Stage: ProxyStage, ChainID: "1", BlockHash: uintWord(700), BlockNumber: 700}
			detector := rpcProxyDetector{caller: caller, limits: ProxyLimits{MaxCandidates: 2, MaxCodeBytes: 4096, MaxDetailsBytes: 512}}
			detections, err := detector.detectBlock(t.Context(), job, []proxyCandidate{{address: proxy}})
			if err != nil {
				t.Fatal(err)
			}
			if len(detections) != 1 || detections[0].proxy != nil || detections[0].rejected != test.want || len(detections[0].code) == 0 {
				t.Fatalf("detection=%+v", detections)
			}
		})
	}
}

func TestRPCProxyDetectorKeepsBeaconTransportFailureRetryable(t *testing.T) {
	t.Parallel()
	proxy, beacon := testAddress(81), testAddress(82)
	caller := &proxyStateCaller{
		code:         map[common.Address][]byte{proxy: {0x60, 0x01}, beacon: {0x60, 0x02}},
		storage:      map[string]common.Hash{proxy.String() + ":" + EIP1967BeaconSlot.String(): addressWord(beacon)},
		methodErrors: map[string]error{"eth_call": context.DeadlineExceeded},
	}
	job := Job{ID: "beacon-timeout", Stage: ProxyStage, ChainID: "1", BlockHash: uintWord(800), BlockNumber: 800}
	detector := rpcProxyDetector{caller: caller, limits: ProxyLimits{MaxCandidates: 2, MaxCodeBytes: 4096, MaxDetailsBytes: 512}}
	_, err := detector.detectBlock(t.Context(), job, []proxyCandidate{{address: proxy}})
	var classified stageError
	if err == nil || errors.As(err, &classified) {
		t.Fatalf("transport error=%#v, want retryable unclassified error", err)
	}
}
