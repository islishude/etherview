package enrich

import (
	"errors"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestProxyFrameworkOptionalFamiliesAreIndependent(t *testing.T) {
	t.Parallel()
	proxy, target := testAddress(150), testAddress(151)
	for _, family := range []string{"safe", "diamond"} {
		for _, enabled := range []bool{false, true} {
			caller := &proxyStateCaller{code: map[common.Address][]byte{target: {0x60, 1}}}
			candidate := proxyCandidate{address: proxy}
			if family == "safe" {
				caller.code[proxy] = safeRuntimeFixture(t, "1.4.1")
				caller.storage = map[string]common.Hash{proxy.Hex() + ":" + safeSingletonSlot.Hex(): addressWord(target)}
			} else {
				caller.code[proxy] = []byte{0x60, 2}
				candidate.sources = map[string]struct{}{proxySourceDiamondCut: {}}
				caller.probeRaw = diamondProbeFixture(t, proxy, []diamondFacetRow{
					{FacetAddress: proxy, FunctionSelectors: append([][4]byte(nil), diamondRequiredLoupeSelectors[:]...)},
					{FacetAddress: target, FunctionSelectors: [][4]byte{{1, 2, 3, 4}}},
				}, false, common.Address{})
			}
			detector := rpcProxyDetector{caller: caller, limits: ProxyLimits{MaxCodeBytes: 4096},
				safeEnabled: family == "safe" && enabled, diamondEnabled: family == "diamond" && enabled}
			results, err := detector.detectBlock(t.Context(), Job{ChainID: "1", BlockNumber: 9, BlockHash: uintWord(9)}, []proxyCandidate{candidate})
			if err != nil {
				t.Fatal(err)
			}
			result := results[0]
			if result.proxy != nil || result.exact != nil {
				t.Fatal("optional family granted a singular binding")
			}
			if enabled {
				if result.resolution.Status != ProxyStatusConfirmed {
					t.Fatalf("%s: %+v", family, result.resolution)
				}
			} else {
				if result.resolution.Status != ProxyStatusNotDetected || len(caller.calls) != 4 {
					t.Fatalf("disabled %s performed optional work: %+v calls=%+v", family, result.resolution, caller.calls)
				}
			}
		}
	}
}

func TestProxyFrameworkRequiredFailuresKeepStageClassification(t *testing.T) {
	t.Parallel()
	ctx, err := newProxyDetectionContext("1", testAddress(160), 10, uintWord(10), ProxyDetectionBulk, &proxyStateCaller{}, 4096, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []error{Unavailable(errors.New("state unavailable")), Permanent(errors.New("malformed state")), errors.New("retryable failure")} {
		detector := testProxyDetector{id: "standard", version: "1", modes: []ProxyDetectionMode{ProxyDetectionBulk}, err: &requiredProxyObservationError{cause: failure}}
		if _, got := RunProxyDetectors(t.Context(), ctx, []ProxyDetector{detector}); got != failure {
			t.Fatalf("required failure changed: %v", got)
		}
		detector.err = failure
		result, got := RunProxyDetectors(t.Context(), ctx, []ProxyDetector{detector})
		if got != nil || result.Status != ProxyStatusUnknown || result.observation != nil {
			t.Fatalf("optional failure became authority: %+v %v", result, got)
		}
	}
}

func TestProxyFrameworkSharesStandardRPCAndCountsEveryRequest(t *testing.T) {
	t.Parallel()
	first, second, implementation := testAddress(170), testAddress(171), testAddress(172)
	caller := &proxyStateCaller{code: map[common.Address][]byte{first: {0x60, 1}, second: {0x60, 1}, implementation: {0x60, 2}},
		storage: map[string]common.Hash{
			first.Hex() + ":" + EIP1967ImplementationSlot.Hex():  addressWord(implementation),
			second.Hex() + ":" + EIP1967ImplementationSlot.Hex(): addressWord(implementation),
		}}
	memo := &proxyDetectionRPCMemo{}
	detector := rpcProxyDetector{caller: caller, limits: ProxyLimits{MaxCodeBytes: 4096}, memo: memo, safeEnabled: true, diamondEnabled: true}
	results, err := detector.detectBlock(t.Context(), Job{ChainID: "1", BlockNumber: 11, BlockHash: uintWord(11)}, []proxyCandidate{{address: first}, {address: second}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || len(caller.calls) != 9 || !reflect.DeepEqual(memo.counts, ProxyRPCCounters{GetCode: 3, GetStorageAt: 6}) {
		t.Fatalf("shared standard RPC: calls=%+v counters=%+v", caller.calls, memo.counts)
	}
	assertProxyDetectorCallsUseBlock(t, caller, uintWord(11))
}
