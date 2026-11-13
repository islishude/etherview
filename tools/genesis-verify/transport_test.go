package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunThroughHTTP(t *testing.T) {
	inv, sources := sourceFixture(t)
	artifacts := make(map[string]map[string]any)
	imported := []map[string]any{}
	byAddress := make(map[string]contract)
	for _, c := range inv.Contracts {
		byAddress[c.Address] = c
		artifacts[c.Address] = map[string]any{"kind": "verification_success", "resolution": "exact_address", "target": map[string]any{"chain_id": inv.ChainID, "address": c.Address, "code_hash": c.CodeHash}, "runtime_match": map[string]any{"match_type": "full"}}
		imported = append(imported, map[string]any{"type": "contract", "address": c.Address, "code_hash": c.CodeHash, "block_hash": "genesis"})
	}
	submitted := make(map[string]bool)
	jobs := make(map[string]string)
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		send := func(status int, body any) {
			w.WriteHeader(status)
			if err := json.NewEncoder(w).Encode(body); err != nil {
				t.Error(err)
			}
		}
		if r.URL.Path == "/rpc" {
			if r.Header.Get("X-API-Key") != "" {
				t.Error("credential leaked to RPC")
			}
			var body struct {
				Method string `json:"method"`
				Params []any  `json:"params"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			var result any = "0xbeaf"
			if body.Method == "eth_getBlockByNumber" {
				result = map[string]any{"hash": "genesis"}
				if len(body.Params) != 2 || body.Params[0] != "0x0" {
					t.Error("unpinned genesis request")
				}
			}
			send(200, map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
			return
		}
		if r.Header.Get("X-API-Key") != "test-credential" {
			t.Error("missing API credential")
		}
		if r.URL.Path == "/api/v1/genesis/accounts" {
			send(200, map[string]any{"data": imported, "meta": map[string]any{"chain_id": inv.ChainID}})
			return
		}
		if id, ok := strings.CutPrefix(r.URL.Path, "/api/v1/verifier/jobs/"); ok {
			send(200, map[string]any{"data": map[string]any{"status": "succeeded", "outcome": artifacts[jobs[id]]}})
			return
		}
		address := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/contracts/"), "/verification")
		c, ok := byAddress[address]
		if !ok {
			t.Errorf("unexpected path %s", r.URL.Path)
			send(404, map[string]any{})
			return
		}
		if r.Method == http.MethodPost {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			expected, err := submission(c, sources)
			if err != nil {
				t.Error(err)
			}
			want, _ := json.Marshal(expected)
			var normalized map[string]any
			if err = json.Unmarshal(want, &normalized); err != nil {
				t.Error(err)
			}
			got, _ := json.Marshal(body)
			want, _ = json.Marshal(normalized)
			if !bytes.Equal(got, want) {
				t.Errorf("changed submission for %s", c.Name)
			}
			posts++
			id := fmt.Sprintf("00000000-0000-0000-0000-%012d", posts)
			jobs[id] = address
			submitted[address] = true
			send(202, map[string]any{"data": map[string]any{"id": id}})
			return
		}
		status := 404
		if submitted[address] {
			status = 200
		}
		send(status, map[string]any{"data": artifacts[address]})
	}))
	defer server.Close()
	args := []string{"-inventory-dir", "../../preview/verification", "-genesis", "../../preview/genesis.json", "-api", server.URL, "-rpc", server.URL + "/rpc"}
	var logs bytes.Buffer
	for range 2 {
		if err := run(context.Background(), args, strings.NewReader("test-credential\n"), &logs); err != nil {
			t.Fatal(err)
		}
	}
	if posts != 46 || !strings.Contains(logs.String(), "46/46 contracts verified (46 reused)") {
		t.Fatalf("not idempotent: %d posts", posts)
	}
	if strings.Contains(logs.String(), "test-credential") {
		t.Fatal("credential leaked to logs")
	}
}

func TestTransportBoundsAndRedaction(t *testing.T) {
	for _, kind := range []string{"redirect", "oversized", "invalid JSON", "RPC error", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			reached := false
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached = true; _, _ = io.WriteString(w, `{}`) }))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch kind {
				case "redirect":
					http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
				case "oversized":
					_, _ = io.WriteString(w, `"`+strings.Repeat("x", maxResponseBytes)+`"`)
				case "invalid JSON":
					_, _ = io.WriteString(w, "secret server diagnostic")
				case "RPC error":
					_, _ = io.WriteString(w, `{"error":{"message":"secret server diagnostic"}}`)
				case "cancelled":
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			client := newTransport(server.URL, server.URL, "private-key")
			duration := 2 * time.Second
			if kind == "cancelled" {
				duration = 50 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), duration)
			defer cancel()
			var err error
			if kind == "RPC error" {
				_, err = client.rpc(ctx, "eth_chainId", []any{})
			} else {
				_, err = client.request(ctx, "/", nil)
			}
			if err == nil {
				t.Fatal("accepted invalid transport response")
			}
			expected := map[string]string{"redirect": "request failed", "oversized": "response too large", "invalid JSON": "invalid response JSON", "RPC error": "genesis RPC unavailable", "cancelled": "request failed"}
			if err.Error() != expected[kind] {
				t.Fatalf("got %v, want %s", err, expected[kind])
			}
			if reached {
				t.Fatal("followed redirect")
			}
			for _, secret := range []string{server.URL, "private-key", "secret server diagnostic"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked %q", secret)
				}
			}
		})
	}
}

func TestCredentialAndCancellation(t *testing.T) {
	for _, token := range []string{"", strings.Repeat("x", 1025)} {
		if err := run(context.Background(), nil, strings.NewReader(token), io.Discard); err == nil || !strings.Contains(err.Error(), "credential") {
			t.Fatalf("got %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := wait(ctx); err != context.Canceled {
		t.Fatalf("wait ignored cancellation: %v", err)
	}
	f := fixture(t)
	if err := f.v.verifyAll(ctx); err == nil {
		t.Fatal("verification ignored cancellation")
	}
}
