package enrich

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/islishude/etherview/internal/ethrpc"
)

type proxyRPCCall struct {
	method    string
	address   string
	blockHash string
}

type proxyStateCaller struct {
	mu           sync.Mutex
	calls        []proxyRPCCall
	code         map[Address][]byte
	storage      map[string]Word
	beacon       map[Address]Address
	err          error
	methodErrors map[string]error
	beaconRaw    map[Address][]byte
}

func (caller *proxyStateCaller) Call(_ context.Context, method string, params []any, result any) error {
	if caller.err != nil {
		return caller.err
	}
	if err := caller.methodErrors[method]; err != nil {
		return err
	}
	if len(params) < 2 {
		return fmt.Errorf("%s has too few parameters", method)
	}
	blockReference, ok := params[len(params)-1].(map[string]any)
	if !ok || blockReference["requireCanonical"] != true {
		return errors.New("proxy RPC did not use a canonical EIP-1898 selector")
	}
	blockHash, ok := blockReference["blockHash"].(string)
	if !ok {
		return errors.New("proxy RPC block hash is missing")
	}
	var address Address
	switch method {
	case "eth_getCode", "eth_getStorageAt":
		parsed, err := ParseAddress(params[0].(string))
		if err != nil {
			return err
		}
		address = parsed
	case "eth_call":
		request, ok := params[0].(map[string]any)
		if !ok {
			return errors.New("beacon call is malformed")
		}
		parsed, err := ParseAddress(request["to"].(string))
		if err != nil {
			return err
		}
		address = parsed
	default:
		return fmt.Errorf("unexpected proxy RPC method %s", method)
	}
	caller.mu.Lock()
	caller.calls = append(caller.calls, proxyRPCCall{method: method, address: address.String(), blockHash: blockHash})
	caller.mu.Unlock()
	switch method {
	case "eth_getCode":
		destination, ok := result.(*ethrpc.Data)
		if !ok {
			return errors.New("code destination is invalid")
		}
		*destination = ethrpc.DataFromBytes(caller.code[address])
	case "eth_getStorageAt":
		slot := params[1].(string)
		destination, ok := result.(*ethrpc.Data)
		if !ok {
			return errors.New("storage destination is invalid")
		}
		*destination = ethrpc.DataFromBytes(wordBytes(caller.storage[address.String()+":"+slot]))
	case "eth_call":
		if raw, exists := caller.beaconRaw[address]; exists {
			destination, ok := result.(*ethrpc.Data)
			if !ok {
				return errors.New("beacon destination is invalid")
			}
			*destination = ethrpc.DataFromBytes(raw)
			return nil
		}
		implementation, ok := caller.beacon[address]
		if !ok {
			return errors.New("unknown beacon")
		}
		destination, ok := result.(*ethrpc.Data)
		if !ok {
			return errors.New("beacon destination is invalid")
		}
		*destination = ethrpc.DataFromBytes(wordBytes(addressWord(implementation)))
	}
	return nil
}

func TestRPCProxyDetectorRecognizesMinimalImmutableAndPinsBlockHash(t *testing.T) {
	t.Parallel()
	proxy, implementation := testAddress(31), testAddress(32)
	code := append(append(append([]byte(nil), minimalProxyPrefix...), implementation[:]...), minimalProxySuffix...)
	code = append(code, []byte("immutable-args")...)
	caller := &proxyStateCaller{code: map[Address][]byte{
		proxy: code, implementation: {0x60, 0x00},
	}}
	job := Job{ID: "minimal", Stage: ProxyStage, ChainID: "1", BlockHash: uintWord(310), BlockNumber: 310}
	detector := rpcProxyDetector{caller: caller, limits: ProxyLimits{MaxCandidates: 4, MaxCodeBytes: 4096, MaxDetailsBytes: 512}}
	detections, err := detector.detectBlock(t.Context(), job, []proxyCandidate{{
		address: proxy, sources: map[string]struct{}{proxySourceReceipt: {}}, force: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(detections) != 1 || detections[0].proxy == nil ||
		detections[0].proxy.kind != ProxyMinimal1167 || detections[0].proxy.implementation != implementation ||
		detections[0].proxy.minimalExact || detections[0].proxy.immutableArgsBytes != len("immutable-args") {
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

func TestRPCProxyDetectorResolvesEIP1967AndBeaconFinalImplementation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name           string
		kind           ProxyKind
		proxy          Address
		implementation Address
		beacon         *Address
	}{
		{name: "implementation", kind: ProxyEIP1967, proxy: testAddress(41), implementation: testAddress(42)},
		{name: "beacon", kind: ProxyBeacon, proxy: testAddress(51), implementation: testAddress(52), beacon: addressPointer(testAddress(53))},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			storage := map[string]Word{}
			if test.beacon == nil {
				storage[test.proxy.String()+":"+EIP1967ImplementationSlot.String()] = addressWord(test.implementation)
			} else {
				storage[test.proxy.String()+":"+EIP1967BeaconSlot.String()] = addressWord(*test.beacon)
			}
			caller := &proxyStateCaller{
				code:    map[Address][]byte{test.proxy: {0x60, 0x01}, test.implementation: {0x60, 0x02}},
				storage: storage, beacon: map[Address]Address{},
			}
			if test.beacon != nil {
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

func TestRPCProxyDetectorReportsMissingEIP1898AsUnavailable(t *testing.T) {
	t.Parallel()
	caller := &proxyStateCaller{err: &ethrpc.RPCError{Code: -32602, Message: "invalid argument 1: block hash selectors unsupported"}}
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
		code: map[Address][]byte{
			poison: {0x60, 0x01}, valid: {0x60, 0x02}, validImplementation: {0x60, 0x03},
		},
		storage: map[string]Word{
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
		setup func(Address, *proxyStateCaller)
		want  string
	}{
		{
			name: "self implementation", want: "self_implementation",
			setup: func(proxy Address, caller *proxyStateCaller) {
				caller.storage[proxy.String()+":"+EIP1967ImplementationSlot.String()] = addressWord(proxy)
			},
		},
		{
			name: "implementation without code", want: "implementation_has_no_code",
			setup: func(proxy Address, caller *proxyStateCaller) {
				caller.storage[proxy.String()+":"+EIP1967ImplementationSlot.String()] = addressWord(testAddress(72))
			},
		},
		{
			name: "invalid slot address", want: "invalid_slot_address",
			setup: func(proxy Address, caller *proxyStateCaller) {
				word := addressWord(testAddress(73))
				word[0] = 1
				caller.storage[proxy.String()+":"+EIP1967ImplementationSlot.String()] = word
			},
		},
		{
			name: "invalid beacon return", want: "invalid_beacon_implementation",
			setup: func(proxy Address, caller *proxyStateCaller) {
				beacon := testAddress(74)
				caller.storage[proxy.String()+":"+EIP1967BeaconSlot.String()] = addressWord(beacon)
				caller.beaconRaw[beacon] = []byte{1}
			},
		},
		{
			name: "beacon execution revert", want: "invalid_beacon_implementation",
			setup: func(proxy Address, caller *proxyStateCaller) {
				beacon := testAddress(75)
				caller.storage[proxy.String()+":"+EIP1967BeaconSlot.String()] = addressWord(beacon)
				caller.methodErrors["eth_call"] = &ethrpc.RPCError{Code: 3, Message: "execution reverted"}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			proxy := testAddress(71)
			caller := &proxyStateCaller{
				code: map[Address][]byte{proxy: {0x60, 0x01}}, storage: map[string]Word{},
				beacon: map[Address]Address{}, beaconRaw: map[Address][]byte{}, methodErrors: map[string]error{},
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
		code:         map[Address][]byte{proxy: {0x60, 0x01}},
		storage:      map[string]Word{proxy.String() + ":" + EIP1967BeaconSlot.String(): addressWord(beacon)},
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
