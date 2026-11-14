package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sourceFixture(t *testing.T) (inventory, bundles) {
	t.Helper()
	inv, sources, err := loadInventory("../../preview/verification", "../../preview/genesis.json")
	if err != nil {
		t.Fatal(err)
	}
	return inv, sources
}

func TestInventoryAndSubmissions(t *testing.T) {
	inv, sources := sourceFixture(t)
	if len(inv.Contracts) != 47 {
		t.Fatalf("inventory count: %d", len(inv.Contracts))
	}
	names := map[string]bool{}
	for _, c := range inv.Contracts {
		names[c.Name] = true
		body, err := submission(c, sources)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]json.RawMessage
		if err = json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		if !present(decoded["compiler_version"]) || present(decoded["creation_entrypoint"]) {
			t.Fatalf("invalid submission: %s", c.Name)
		}
		switch {
		case c.Entrypoint != "":
			if string(decoded["language"]) != `"geas"` || string(decoded["input_kind"]) != `"geas_sources"` {
				t.Fatal("invalid Geas submission")
			}
			if string(decoded["sources"]) != compact(t, sources[c.Bundle]["sources"]) {
				t.Fatal("Geas sources changed")
			}
		case present(sources[c.Bundle]["input_kind"]):
			if string(decoded["input"]) != compact(t, sources[c.Bundle]["input"]) {
				t.Fatal("standard input changed")
			}
		default:
			var input map[string]json.RawMessage
			if err = json.Unmarshal(decoded["input"], &input); err != nil {
				t.Fatal(err)
			}
			if string(input["sources"]) != compact(t, sources[c.Bundle]["sources"]) {
				t.Fatal("sources changed")
			}
		}
	}
	for _, name := range []string{"PreviewDeposit", "SenderCreator", "SimpleAccount", "LocalBillingToken", "BuilderExit", "Permit2", "x402ExactPermit2Proxy"} {
		if !names[name] {
			t.Fatalf("missing %s", name)
		}
	}
}

func compact(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeJSON(t *testing.T, file string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryRejectsDrift(t *testing.T) {
	for _, kind := range []string{"missing", "extra", "code", "duplicate", "checksum", "path", "chain"} {
		t.Run(kind, func(t *testing.T) {
			inv, _ := sourceFixture(t)
			dir := t.TempDir()
			var chain genesis
			if err := readJSON("../../preview/genesis.json", &chain); err != nil {
				t.Fatal(err)
			}
			for name := range inv.Bundles {
				data, err := os.ReadFile(filepath.Join("../../preview/verification", name))
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "missing":
				for address, a := range chain.Alloc {
					if a.Code != "" && a.Code != "0x" {
						delete(chain.Alloc, address)
						break
					}
				}
			case "extra":
				chain.Alloc["0x1111111111111111111111111111111111111111"] = struct {
					Code string `json:"code"`
				}{"0x00"}
			case "code":
				for address, a := range chain.Alloc {
					if a.Code != "" && a.Code != "0x" {
						a.Code = "0x00"
						chain.Alloc[address] = a
						break
					}
				}
			case "duplicate":
				inv.Contracts[1] = inv.Contracts[0]
			case "checksum":
				if err := os.WriteFile(filepath.Join(dir, "local.json"), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "path":
				inv.Bundles["../escape.json"] = fmt.Sprintf("%x", sha256.Sum256(nil))
			case "chain":
				chain.Config.ChainID = "1"
			}
			writeJSON(t, filepath.Join(dir, "genesis.json"), chain)
			writeJSON(t, filepath.Join(dir, "inventory.json"), inv)
			if _, _, err := loadInventory(dir, filepath.Join(dir, "genesis.json")); err == nil {
				t.Fatal("accepted changed inventory")
			}
		})
	}
}

type verificationFixture struct {
	v         verifier
	c         contract
	artifact  map[string]any
	imported  map[string]any
	submitted bool
	posts     int
	logs      []string
}

func jsonResponse(status int, body any) response {
	data, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return response{status, data}
}

func fixture(t *testing.T) *verificationFixture {
	t.Helper()
	inv, sources := sourceFixture(t)
	inv.Contracts = inv.Contracts[:1]
	c := inv.Contracts[0]
	f := &verificationFixture{c: c}
	f.artifact = map[string]any{"kind": "verification_success", "resolution": "exact_address", "target": map[string]any{"chain_id": inv.ChainID, "address": c.Address, "code_hash": c.CodeHash}, "runtime_match": map[string]any{"match_type": "full"}}
	f.imported = map[string]any{"data": []map[string]any{{"type": "contract", "address": c.Address, "code_hash": c.CodeHash, "block_hash": "genesis"}}, "meta": map[string]any{"chain_id": inv.ChainID}}
	f.v = verifier{inventory: inv, bundles: sources, timeout: 25 * time.Millisecond, wait: func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
			return nil
		}
	}, log: func(format string, args ...any) { f.logs = append(f.logs, fmt.Sprintf(format, args...)) }}
	f.v.rpc = func(_ context.Context, method string, params []any) (json.RawMessage, error) {
		if method == "eth_chainId" {
			return json.RawMessage(`"0xbeaf"`), nil
		}
		if len(params) != 2 || params[0] != "0x0" || params[1] != false {
			t.Error("genesis must be pinned to block zero")
		}
		return json.RawMessage(`{"hash":"genesis"}`), nil
	}
	f.v.request = func(_ context.Context, path string, body any) (response, error) {
		if strings.Contains(path, "genesis/accounts") {
			return jsonResponse(200, f.imported), nil
		}
		if body != nil {
			f.posts++
			f.submitted = true
			return jsonResponse(202, map[string]any{"data": map[string]any{"id": "00000000-0000-0000-0000-000000000001"}}), nil
		}
		if strings.Contains(path, "verifier/jobs") {
			return jsonResponse(200, map[string]any{"data": map[string]any{"status": "succeeded", "outcome": f.artifact}}), nil
		}
		status := 404
		if f.submitted {
			status = 200
		}
		return jsonResponse(status, map[string]any{"data": f.artifact}), nil
	}
	return f
}

func TestPublicationAndReuse(t *testing.T) {
	f := fixture(t)
	for range 2 {
		if err := f.v.verifyAll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if f.posts != 1 || !strings.Contains(f.logs[len(f.logs)-1], "1 reused") {
		t.Fatalf("not idempotent: posts=%d logs=%v", f.posts, f.logs)
	}
}

func TestPublishedIdentity(t *testing.T) {
	for _, field := range []string{"resolution", "chain_id", "address", "code_hash", "runtime_match"} {
		t.Run(field, func(t *testing.T) {
			f := fixture(t)
			if !isPublished(jsonResponse(200, map[string]any{"data": f.artifact}), f.c, "48815") {
				t.Fatal("valid artifact rejected")
			}
			switch field {
			case "resolution":
				f.artifact[field] = "code_hash"
			case "runtime_match":
				f.artifact[field] = nil
			default:
				f.artifact["target"].(map[string]any)[field] = "wrong"
			}
			if isPublished(jsonResponse(200, map[string]any{"data": f.artifact}), f.c, "48815") {
				t.Fatal("unbound artifact accepted")
			}
		})
	}
}

func TestVerificationFailures(t *testing.T) {
	cases := []struct{ name, want string }{
		{"chain", "RPC chain mismatch"}, {"genesis", "identity mismatch"}, {"partial", "inventory mismatch"},
		{"failed", "verification failed"}, {"cancelled", "verification cancelled"}, {"false success", "invalid genesis match"}, {"creation", "invalid genesis match"},
		{"stuck", "verification timeout"}, {"unavailable import", "import timeout"}, {"unavailable compiler", "submission timeout"},
		{"unpublished", "published artifact mismatch"}, {"invalid id", "invalid verification job"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := fixture(t)
			req := f.v.request
			switch tc.name {
			case "chain":
				f.v.rpc = func(context.Context, string, []any) (json.RawMessage, error) { return json.RawMessage(`"0x1"`), nil }
			case "genesis":
				f.imported["data"].([]map[string]any)[0]["block_hash"] = "other"
			case "partial":
				f.imported["meta"].(map[string]any)["next_cursor"] = "more"
			default:
				f.v.request = func(ctx context.Context, path string, body any) (response, error) {
					switch {
					case tc.name == "unavailable import":
						return jsonResponse(503, map[string]any{}), nil
					case body != nil && tc.name == "unavailable compiler":
						return jsonResponse(503, map[string]any{}), nil
					case body != nil && tc.name == "invalid id":
						return jsonResponse(202, map[string]any{"data": map[string]any{"id": "../invalid"}}), nil
					case body == nil && strings.HasSuffix(path, "/verification") && tc.name == "unpublished":
						return jsonResponse(404, map[string]any{}), nil
					case strings.Contains(path, "verifier/jobs"):
						status := "succeeded"
						outcome := f.artifact
						switch tc.name {
						case "failed", "cancelled":
							status = tc.name
						case "false success":
							outcome = map[string]any{"kind": "verification_failure"}
						case "creation":
							outcome["creation_match"] = map[string]any{"match_type": "full"}
						case "stuck":
							status = "queued"
						}
						return jsonResponse(200, map[string]any{"data": map[string]any{"status": status, "outcome": outcome}}), nil
					}
					return req(ctx, path, body)
				}
			}
			if err := f.v.verifyAll(context.Background()); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestTransientAdmissionAndPolling(t *testing.T) {
	f := fixture(t)
	req := f.v.request
	imports, submits, polls := 0, 0, 0
	f.v.timeout = time.Second
	f.v.request = func(ctx context.Context, path string, body any) (response, error) {
		if strings.Contains(path, "genesis/accounts") {
			imports++
			if imports == 1 {
				return jsonResponse(503, map[string]any{}), nil
			}
		}
		if body != nil {
			submits++
			if submits <= 2 {
				status := 429
				if submits == 2 {
					status = 503
				}
				return jsonResponse(status, map[string]any{}), nil
			}
		}
		if strings.Contains(path, "verifier/jobs") {
			polls++
			if polls <= 2 {
				status := "queued"
				if polls == 2 {
					status = "running"
				}
				return jsonResponse(200, map[string]any{"data": map[string]any{"status": status}}), nil
			}
		}
		return req(ctx, path, body)
	}
	if err := f.v.verifyAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if imports != 2 || submits != 3 || polls != 3 || f.posts != 1 {
		t.Fatalf("unexpected retries: %d %d %d %d", imports, submits, polls, f.posts)
	}
}
