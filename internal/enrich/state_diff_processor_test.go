package enrich

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestProxyRelevantStateChangeRecognizesOnlyCodeAndERC1967Slots(t *testing.T) {
	t.Parallel()
	address := common.HexToAddress("0x0000000000000000000000000000000000000001")
	tests := []struct {
		name   string
		change stateChange
		want   bool
	}{
		{name: "code", change: stateChange{address: address, kind: "code"}, want: true},
		{name: "implementation", change: stateChange{address: address, kind: "storage", key: EIP1967ImplementationSlot[:]}, want: true},
		{name: "beacon", change: stateChange{address: address, kind: "storage", key: EIP1967BeaconSlot[:]}, want: true},
		{name: "admin", change: stateChange{address: address, kind: "storage", key: EIP1967AdminSlot[:]}, want: true},
		{name: "ordinary storage", change: stateChange{address: address, kind: "storage", key: common.HexToHash("0x01").Bytes()}, want: false},
		{name: "balance", change: stateChange{address: address, kind: "balance"}, want: false},
		{name: "short storage key", change: stateChange{address: address, kind: "storage", key: []byte{1}}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := proxyRelevantStateChange(test.change); got != test.want {
				t.Fatalf("proxy relevance=%t want=%t change=%+v", got, test.want, test.change)
			}
		})
	}
}

func TestNormalizeStateDiffCanonicalizesAndSortsChanges(t *testing.T) {
	t.Parallel()
	addressA := "0x0000000000000000000000000000000000000001"
	addressB := "0x0000000000000000000000000000000000000002"
	key := "0x" + strings.Repeat("00", 31) + "01"
	beforeStorage := "0x" + strings.Repeat("00", 31) + "02"
	afterStorage := "0x" + strings.Repeat("00", 31) + "03"
	raw := json.RawMessage(`{
		"pre": {
			"` + addressB + `": {"nonce":"0x1"},
			"` + addressA + `": {
				"balance":"0x1",
				"code":"0x6000",
				"storage":{"` + key + `":"` + beforeStorage + `"}
			}
		},
		"post": {
			"` + addressA + `": {
				"balance":"0x2",
				"code":"0x6001",
				"storage":{"` + key + `":"` + afterStorage + `"}
			},
			"` + addressB + `": {"nonce":"0x2"}
		}
	}`)

	changes, counts, err := normalizeStateDiff(raw, DefaultStateDiffLimits())
	if err != nil {
		t.Fatal(err)
	}
	if counts.accounts != 2 || counts.slots != 1 || counts.code != 4 || counts.text == 0 {
		t.Fatalf("counts = %+v", counts)
	}
	if len(changes) != 4 {
		t.Fatalf("changes = %+v", changes)
	}
	if changes[0].address.Hex() != "0x0000000000000000000000000000000000000001" ||
		changes[0].kind != "balance" || changes[1].kind != "code" ||
		changes[2].kind != "storage" || changes[3].kind != "nonce" {
		t.Fatalf("changes are not stable and sorted: %+v", changes)
	}
	if changes[0].before == nil || *changes[0].before != "1" ||
		changes[0].after == nil || *changes[0].after != "2" {
		t.Fatalf("balance change = %+v", changes[0])
	}
}

func TestNormalizeStateDiffAcceptsGethNumericNonce(t *testing.T) {
	t.Parallel()
	address := "0x0000000000000000000000000000000000000001"
	raw := json.RawMessage(`{
		"pre":{"` + address + `":{"balance":"0x1"}},
		"post":{"` + address + `":{"balance":"0x1","nonce":1}}
	}`)

	changes, _, err := normalizeStateDiff(raw, DefaultStateDiffLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].kind != "nonce" ||
		changes[0].key == nil || len(changes[0].key) != 0 ||
		changes[0].before != nil || changes[0].after == nil || *changes[0].after != "1" {
		t.Fatalf("numeric nonce changes = %+v", changes)
	}
}

func TestNormalizeStateDiffTreatsOmittedPostScalarsAsUnchanged(t *testing.T) {
	t.Parallel()
	address := "0x0000000000000000000000000000000000000001"
	key := "0x" + strings.Repeat("00", 31) + "01"
	beforeStorage := "0x" + strings.Repeat("00", 31) + "02"
	afterStorage := "0x" + strings.Repeat("00", 31) + "03"
	raw := json.RawMessage(`{
		"pre":{"` + address + `":{
			"balance":"0x2a","nonce":"0x7","code":"0x6000",
			"storage":{"` + key + `":"` + beforeStorage + `"}
		}},
		"post":{"` + address + `":{
			"storage":{"` + key + `":"` + afterStorage + `"}
		}}
	}`)

	changes, counts, err := normalizeStateDiff(raw, DefaultStateDiffLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].kind != "storage" {
		t.Fatalf("sparse post-state changes = %+v, want only storage", changes)
	}
	if counts.code != 2 {
		t.Fatalf("validated code bytes = %d, want 2", counts.code)
	}
}

func TestNormalizeStateDiffDistinguishesExplicitCodeClearFromOmission(t *testing.T) {
	t.Parallel()
	address := "0x0000000000000000000000000000000000000001"
	raw := json.RawMessage(`{
		"pre":{"` + address + `":{"code":"0x6000"}},
		"post":{"` + address + `":{"code":"0x"}}
	}`)

	changes, _, err := normalizeStateDiff(raw, DefaultStateDiffLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].kind != "code" ||
		changes[0].before == nil || *changes[0].before != "0x6000" ||
		changes[0].after == nil || *changes[0].after != "0x" {
		t.Fatalf("explicit code clear = %+v", changes)
	}
}

func TestNormalizeStateDiffRejectsMalformedAndOverBudgetPayloads(t *testing.T) {
	t.Parallel()
	address := "0x0000000000000000000000000000000000000001"
	key := "0x" + strings.Repeat("00", 32)
	value := "0x" + strings.Repeat("00", 32)
	base := DefaultStateDiffLimits()
	tests := []struct {
		name   string
		raw    json.RawMessage
		mutate func(*StateDiffLimits)
		target error
	}{
		{name: "malformed JSON", raw: json.RawMessage(`{"pre":`)},
		{name: "missing post", raw: json.RawMessage(`{"pre":{}}`)},
		{name: "invalid address", raw: json.RawMessage(`{"pre":{"0x1":{}},"post":{}}`)},
		{name: "invalid quantity", raw: json.RawMessage(
			`{"pre":{"` + address + `":{"balance":"1"}},"post":{}}`,
		)},
		{name: "invalid numeric nonce", raw: json.RawMessage(
			`{"pre":{"` + address + `":{"nonce":-1}},"post":{}}`,
		)},
		{name: "oversized quoted nonce", raw: json.RawMessage(
			`{"pre":{"` + address + `":{"nonce":"0x10000000000000000"}},"post":{}}`,
		)},
		{name: "invalid code", raw: json.RawMessage(
			`{"pre":{"` + address + `":{"code":"xyz"}},"post":{}}`,
		)},
		{name: "invalid storage value", raw: json.RawMessage(
			`{"pre":{"` + address + `":{"storage":{"` + key + `":"0x1"}}},"post":{}}`,
		)},
		{
			name: "account budget",
			raw: json.RawMessage(
				`{"pre":{"` + address + `":{}},"post":{}}`,
			),
			mutate: func(limits *StateDiffLimits) { limits.MaxAccounts = 0 },
			target: ErrStateDiffLimit,
		},
		{
			name: "storage budget",
			raw: json.RawMessage(
				`{"pre":{"` + address + `":{"storage":{"` + key + `":"` + value + `"}}},"post":{}}`,
			),
			mutate: func(limits *StateDiffLimits) { limits.MaxStorageSlots = 0 },
			target: ErrStateDiffLimit,
		},
		{
			name: "code budget",
			raw: json.RawMessage(
				`{"pre":{"` + address + `":{"code":"0x6000"}},"post":{}}`,
			),
			mutate: func(limits *StateDiffLimits) { limits.MaxCodeBytes = 1 },
			target: ErrStateDiffLimit,
		},
		{
			name:   "payload budget",
			raw:    json.RawMessage(`{"pre":{},"post":{}}`),
			mutate: func(limits *StateDiffLimits) { limits.MaxPayloadBytes = 1 },
			target: ErrStateDiffLimit,
		},
		{
			name: "text budget",
			raw: json.RawMessage(
				`{"pre":{"` + address + `":{"balance":"0x1"}},"post":{}}`,
			),
			mutate: func(limits *StateDiffLimits) { limits.MaxTextBytes = 1 },
			target: ErrStateDiffLimit,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			limits := base
			if test.mutate != nil {
				test.mutate(&limits)
			}
			_, _, err := normalizeStateDiff(test.raw, limits)
			if err == nil {
				t.Fatal("normalizeStateDiff succeeded")
			}
			if test.target != nil && !errors.Is(err, test.target) {
				t.Fatalf("error = %v, want %v", err, test.target)
			}
		})
	}
}

func TestStateDiffBudgetEnforcesWholeBlockLimits(t *testing.T) {
	t.Parallel()
	limits := DefaultStateDiffLimits()
	limits.MaxBlockPayloadBytes = 10
	limits.MaxBlockAccounts = 2
	limits.MaxBlockStorageSlots = 2
	limits.MaxBlockCodeBytes = 2
	limits.MaxBlockTextBytes = 2
	budget := &stateDiffBudget{}
	counts := normalizedStateDiffCounts{accounts: 1, slots: 1, code: 1, text: 1}
	if err := budget.add(5, counts, limits); err != nil {
		t.Fatal(err)
	}
	if err := budget.add(6, counts, limits); !errors.Is(err, ErrStateDiffLimit) {
		t.Fatalf("error = %v, want %v", err, ErrStateDiffLimit)
	}
}
