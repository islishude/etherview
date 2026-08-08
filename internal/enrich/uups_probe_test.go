package enrich

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestProbeUUPSImplementationAtBlockRequiresExactDirectResponses(t *testing.T) {
	t.Parallel()
	implementation := testAddress(201)
	code := []byte{0x60, 0x80, 0x60, 0x40}
	uuidInput, err := packStateProbe("proxiableUUID")
	if err != nil {
		t.Fatal(err)
	}
	versionInput, err := packStateProbe("UPGRADE_INTERFACE_VERSION")
	if err != nil {
		t.Fatal(err)
	}
	validVersion := uupsTestVersionResponse("5.0.0")
	wrongUUID := EIP1967ImplementationSlot
	wrongUUID[0] ^= 0x01
	tests := []struct {
		name          string
		probeRaw      map[string][]byte
		state         uupsProbeState
		rejection     uupsProbeRejection
		expectedCalls int
	}{
		{
			name: "compatible",
			probeRaw: map[string][]byte{
				proxyProbeKey(implementation, uuidInput):    wordBytes(EIP1967ImplementationSlot),
				proxyProbeKey(implementation, versionInput): validVersion,
			},
			state: uupsProbeCompatible, expectedCalls: 3,
		},
		{
			name: "missing UUID",
			probeRaw: map[string][]byte{
				proxyProbeKey(implementation, versionInput): validVersion,
			},
			state: uupsProbeRejected, rejection: uupsRejectUUIDUnavailable, expectedCalls: 2,
		},
		{
			name: "wrong UUID",
			probeRaw: map[string][]byte{
				proxyProbeKey(implementation, uuidInput):    wordBytes(wrongUUID),
				proxyProbeKey(implementation, versionInput): validVersion,
			},
			state: uupsProbeRejected, rejection: uupsRejectUUIDInvalid, expectedCalls: 2,
		},
		{
			name: "missing interface version",
			probeRaw: map[string][]byte{
				proxyProbeKey(implementation, uuidInput): wordBytes(EIP1967ImplementationSlot),
			},
			state: uupsProbeRejected, rejection: uupsRejectVersionUnavailable, expectedCalls: 3,
		},
		{
			name: "wrong interface version",
			probeRaw: map[string][]byte{
				proxyProbeKey(implementation, uuidInput):    wordBytes(EIP1967ImplementationSlot),
				proxyProbeKey(implementation, versionInput): uupsTestVersionResponse("4.5.0"),
			},
			state: uupsProbeRejected, rejection: uupsRejectVersionInvalid, expectedCalls: 3,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			caller := &proxyStateCaller{
				code:     map[common.Address][]byte{implementation: code},
				probeRaw: test.probeRaw,
			}
			job := Job{
				ID: "uups-probe", Stage: ProxyStage, ChainID: "1",
				BlockNumber: 901, BlockHash: uintWord(901),
			}
			result, probeErr := probeUUPSImplementationAtBlock(
				t.Context(), caller, job, uupsImplementationProbeTarget{
					address: implementation, codeHash: codeHash(code),
					verificationJobID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
				}, 4096,
			)
			if probeErr != nil {
				t.Fatal(probeErr)
			}
			if result.state != test.state || result.rejection != test.rejection {
				t.Fatalf("probe result=%+v", result)
			}
			if result.compatible() {
				if result.proxiableUUID != EIP1967ImplementationSlot ||
					result.upgradeInterface != "5.0.0" {
					t.Fatalf("compatible evidence=%+v", result)
				}
			} else if result.proxiableUUID != (common.Hash{}) || result.upgradeInterface != "" {
				t.Fatalf("rejected result retained promotable evidence=%+v", result)
			}
			if err := result.validate(); err != nil {
				t.Fatal(err)
			}
			caller.mu.Lock()
			calls := append([]proxyRPCCall(nil), caller.calls...)
			caller.mu.Unlock()
			if len(calls) != test.expectedCalls {
				t.Fatalf("RPC calls=%+v, want %d", calls, test.expectedCalls)
			}
			for _, call := range calls {
				if call.address != implementation.String() || call.blockHash != job.BlockHash.String() {
					t.Fatalf("probe escaped direct exact implementation identity: %+v", calls)
				}
			}
		})
	}
}

func TestProbeUUPSImplementationAtBlockRejectsVerifiedCodeMismatch(t *testing.T) {
	t.Parallel()
	implementation := testAddress(202)
	caller := &proxyStateCaller{code: map[common.Address][]byte{implementation: {0x60, 0x01}}}
	job := Job{
		ID: "uups-code-mismatch", Stage: ProxyStage, ChainID: "1",
		BlockNumber: 902, BlockHash: uintWord(902),
	}
	_, err := probeUUPSImplementationAtBlock(
		t.Context(), caller, job, uupsImplementationProbeTarget{
			address: implementation, codeHash: codeHash([]byte{0x60, 0x02}),
			verificationJobID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		}, 4096,
	)
	if err == nil || !strings.Contains(err.Error(), "verified identity") {
		t.Fatalf("code mismatch error=%v", err)
	}
	caller.mu.Lock()
	calls := append([]proxyRPCCall(nil), caller.calls...)
	caller.mu.Unlock()
	if len(calls) != 1 || calls[0].method != "eth_getCode" {
		t.Fatalf("code mismatch continued to UUPS calls: %+v", calls)
	}
}

func TestPersistUUPSImplementationProbeWritesObservationAndLeaseWitness(t *testing.T) {
	t.Parallel()
	implementation := testAddress(203)
	code := []byte{0x60, 0x03}
	job := Job{
		ID: "47", Stage: ProxyStage, ChainID: "9", Generation: 6,
		BlockNumber: 903, BlockHash: uintWord(903),
	}
	result := uupsImplementationProbeResult{
		chainID: job.ChainID, blockNumber: job.BlockNumber, blockHash: job.BlockHash,
		target: uupsImplementationProbeTarget{
			address: implementation, codeHash: codeHash(code),
			verificationJobID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		},
		code: code, state: uupsProbeCompatible,
		proxiableUUID: EIP1967ImplementationSlot, upgradeInterface: "5.0.0",
	}
	writes := 0
	backend := &fakeSQLBackend{exec: func(query string, arguments []driver.NamedValue) (driver.Result, error) {
		writes++
		switch writes {
		case 1:
			if !strings.Contains(query, "INSERT INTO uups_implementation_observations") || len(arguments) != 12 {
				return nil, fmt.Errorf("unexpected UUPS observation write: %s args=%+v", query, arguments)
			}
			if arguments[0].Value != job.ChainID || arguments[5].Value != result.target.verificationJobID ||
				arguments[8].Value != string(uupsProbeCompatible) || arguments[9].Value != nil ||
				arguments[11].Value != "5.0.0" {
				return nil, fmt.Errorf("UUPS observation arguments=%+v", arguments)
			}
		case 2:
			if !strings.Contains(query, "INSERT INTO uups_implementation_observation_generations") ||
				len(arguments) != 7 || arguments[5].Value != int64(47) || arguments[6].Value != int64(6) {
				return nil, fmt.Errorf("unexpected UUPS witness write: %s args=%+v", query, arguments)
			}
		default:
			return nil, fmt.Errorf("unexpected extra write: %s", query)
		}
		return driver.RowsAffected(1), nil
	}}
	db := openFakeSQLDB(t, backend)
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := persistUUPSImplementationProbe(t.Context(), tx, job, result); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if writes != 2 {
		t.Fatalf("writes=%d, want observation and witness", writes)
	}
}

func uupsTestVersionResponse(version string) []byte {
	result := make([]byte, 96)
	result[31] = 32
	result[63] = byte(len(version))
	copy(result[64:], version)
	return result
}
