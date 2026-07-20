package adminstore

import (
	"strings"
	"testing"
)

func TestValidateLabel(t *testing.T) {
	t.Parallel()
	kind, key, label, err := validateLabel(" Address ", " 0x52908400098527886E0F7030069857D2E4169EE7 ", " Treasury ")
	if err != nil {
		t.Fatal(err)
	}
	if kind != "address" || key != "0x52908400098527886e0f7030069857d2e4169ee7" || label != "Treasury" {
		t.Fatalf("unexpected normalized label %q %q %q", kind, key, label)
	}
	for _, test := range []struct{ kind, key, label string }{
		{"unknown", "x", "y"},
		{"address", "", "y"},
		{"address", "0xabc", "y"},
		{"transaction", "7", "y"},
		{"block", "01", "y"},
		{"address", "x", ""},
	} {
		if _, _, _, err := validateLabel(test.kind, test.key, test.label); err == nil {
			t.Fatalf("expected error for %#v", test)
		}
	}
	for _, test := range []struct{ kind, key string }{
		{"block", "0"},
		{"block", "18446744073709551615"},
		{"block", "0x" + strings.Repeat("11", 32)},
		{"transaction", "0x" + strings.Repeat("22", 32)},
	} {
		if _, _, _, err := validateLabel(test.kind, test.key, "valid"); err != nil {
			t.Fatalf("valid %s key %q: %v", test.kind, test.key, err)
		}
	}
}

func TestNormalizeLabelKeyRejectsMalformedDeleteIdentity(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ kind, key string }{
		{"address", "alice.eth"},
		{"transaction", "0x1234"},
		{"block", "01"},
		{"unknown", "value"},
	} {
		if _, _, err := normalizeLabelKey(test.kind, test.key); err == nil {
			t.Fatalf("normalizeLabelKey(%q, %q) succeeded", test.kind, test.key)
		}
	}
}

func TestMaintenanceOperationStageValidationMatchesWorker(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		operation string
		stage     string
		valid     bool
	}{
		{"repair", "core", true},
		{"repair", "token", false},
		{"reindex", "token", true},
		{"reindex", "stats", true},
		{"reindex", "trace", true},
		{"reindex", "core", false},
		{"unknown", "core", false},
	} {
		request := RepairRequest{
			Operation: test.operation, Stage: test.stage,
			FromBlock: 1, ToBlock: 1, Reason: "test",
		}
		request = normalizeRepairRequest(request)
		err := validateRepairRequest(request)
		if (err == nil) != test.valid {
			t.Fatalf("operation=%q stage=%q valid=%v error=%v", test.operation, test.stage, test.valid, err)
		}
	}
}
