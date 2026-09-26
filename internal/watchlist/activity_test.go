package watchlist

import (
	"encoding/json"
	"github.com/islishude/etherview/internal/api/gen"
	"strings"
	"testing"
)

func TestExactActivityAndFormulaProtection(t *testing.T) {
	address := "0x1111111111111111111111111111111111111111"
	raw := map[string]any{"kind": "transaction", "block_number": "1", "block_hash": "0x" + strings.Repeat("11", 32), "timestamp": "1700000000", "transaction_hash": "0x" + strings.Repeat("22", 32), "transaction_index": "0", "from": address, "to": address, "value": "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "status": "0x0"}
	b, _ := json.Marshal(raw)
	a, e := decodeActivity(b, address)
	if e != nil || a.Direction != "self" || a.Status == nil || *a.Status != "failed" || a.Value == nil || *a.Value != "115792089237316195423570985008687907853269984665640564039457584007913129639935" {
		t.Fatalf("activity=%+v err=%v", a, e)
	}
	raw["value"] = "-1"
	b, _ = json.Marshal(raw)
	if _, e := decodeActivity(b, address); e == nil {
		t.Fatal("negative amount accepted")
	}
	for _, s := range []string{"=cmd()", " +cmd()", "\t@x", "\n-1"} {
		if !strings.HasPrefix(safeCell(s), "'") {
			t.Fatalf("unsafe %q", s)
		}
	}
	if safeCell("12345678901234567890") != "12345678901234567890" {
		t.Fatal("integer changed")
	}
}
func TestWatchInputBounds(t *testing.T) {
	input := gen.WatchInput{Address: "0x1111111111111111111111111111111111111111", Label: strings.Repeat("中", 64), Kinds: []gen.WatchInputKinds{"transaction"}, Direction: "both"}
	if _, _, e := validateInput(input); e != nil {
		t.Fatal(e)
	}
	for _, label := range []string{strings.Repeat("中", 65), "x\ny"} {
		bad := input
		bad.Label = label
		if _, _, e := validateInput(bad); e == nil {
			t.Fatal("bad label accepted")
		}
	}
	input.Kinds = []gen.WatchInputKinds{"erc20", "erc20"}
	if _, _, e := validateInput(input); e == nil {
		t.Fatal("duplicate type accepted")
	}
}
