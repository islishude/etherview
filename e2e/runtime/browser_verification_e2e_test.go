//go:build runtimee2e

package runtimee2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/api/gen"
)

func TestBrowserVerificationRuntimeE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	root := repositoryRoot(t)
	runVerificationTopologies(t, ctx, func(t *testing.T, mode string) string {
		h := startVerificationTopology(t, ctx, root, mode, "browser-verification", "", func(h *harness) {
			h.project.Env["ETHERVIEW_RUNTIME_WATCHLIST"] = "true"
			h.project.Env["ETHERVIEW_RUNTIME_SESSION_PEPPER"] = strings.Repeat("b", 64)
		})
		// A deterministic key is sufficient for a real SIWE identity; this user does
		// not need funds or ownership of the contract being verified.
		key := deterministicRuntimeKey(t, "browser-verification-user")
		session := runtimeWatchSession{}
		data, _ := h.watchRequest(ctx, session, "POST", "/auth/challenge", map[string]string{"address": crypto.PubkeyToAddress(key.PublicKey).Hex()})
		var challenge gen.AuthChallengeResponse
		if err := json.Unmarshal(data, &challenge); err != nil {
			t.Fatal(err)
		}
		signature, err := crypto.Sign(accounts.TextHash([]byte(challenge.Data.Message)), key)
		if err != nil {
			t.Fatal(err)
		}
		signature[64] += 27
		data, response := h.watchRequest(ctx, session, "POST", "/auth/verify", map[string]string{"challenge_id": challenge.Data.ChallengeId.String(), "signature": hexutil.Encode(signature)})
		var login gen.AuthSessionResponse
		if err := json.Unmarshal(data, &login); err != nil || login.Data.CsrfToken == nil || len(response.Cookies()) != 1 {
			t.Fatalf("login failed: %v", err)
		}
		session.cookie, session.csrf = response.Cookies()[0], *login.Data.CsrfToken

		var statuses []string
		var nativeTarget, nativeKey string
		var nativeInput []byte
		for _, runtimeOnly := range []bool{false, true} {
			t.Logf("verify Geas runtime-only=%t", runtimeOnly)
			hash := h.sendTransaction(ctx, map[string]any{"from": h.fixture.accounts[0], "data": "0x60028060095f395ff36001", "gas": "0x100000"})
			h.mine(ctx, uint64(time.Now().Unix())+1)
			receipt := h.waitReceipt(ctx, hash)
			block := h.latestBlock(ctx)
			h.waitCanonical(ctx, mustDecodeUint64(t, block.Number), block.Hash)
			var target string
			waitFor(t, ctx, "deployed contract indexed", func() (bool, string, error) {
				var wire struct {
					ContractAddress string `json:"contractAddress"`
				}
				err := h.rpc.CallContext(ctx, &wire, "eth_getTransactionReceipt", hash)
				target = wire.ContractAddress
				if err != nil || target == "" {
					return false, "no target", err
				}
				var found bool
				err = h.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contract_code_observations WHERE chain_id=1 AND address=decode($1,'hex'))`, strings.TrimPrefix(strings.ToLower(target), "0x")).Scan(&found)
				return found, fmt.Sprint(receipt.Status), err
			})
			body := map[string]any{"address": target, "language": "geas", "compiler_version": "0.3.3", "input_kind": "geas_sources", "sources": map[string]string{"main.eas": "push 1", "ctor.eas": "push len(code)\ndup1\npush @code\npush 0\ncodecopy\npush 0\nreturn\n#bytes code: assemble(\"main.eas\")\n"}, "runtime_entrypoint": "main.eas", "creation_entrypoint": "ctor.eas"}
			if runtimeOnly {
				delete(body, "creation_entrypoint")
			}
			data, _ = h.watchRequest(ctx, session, "POST", "/verification-jobs/local", body)
			var submitted gen.BrowserVerificationJobResponse
			if err := json.Unmarshal(data, &submitted); err != nil {
				t.Fatal(err)
			}
			var result gen.BrowserVerificationJobResponse
			waitFor(t, ctx, "browser verification terminal result", func() (bool, string, error) {
				data, _ = h.watchRequest(ctx, session, "GET", "/verification-jobs/"+submitted.Data.Id.String(), nil)
				if err := json.Unmarshal(data, &result); err != nil {
					return false, "", err
				}
				if result.Data.Status == "failed" {
					return false, string(data), fmt.Errorf("verification failed")
				}
				return result.Data.Status == "succeeded", string(result.Data.Status), nil
			})
			if result.Data.Address == nil || !strings.EqualFold(*result.Data.Address, target) || result.Data.Outcome == nil || result.Data.Outcome.Kind != "verification_success" || bytes.Contains(data, []byte("push 1")) {
				t.Fatalf("invalid browser result: %s", data)
			}
			if result.Data.Outcome.RuntimeMatch == nil || result.Data.Outcome.RuntimeMatch.MatchType != "full" || (result.Data.Outcome.CreationMatch == nil) != runtimeOnly {
				t.Fatalf("runtime-only=%t: wrong match evidence: %s", runtimeOnly, data)
			}
			if runtimeOnly && bytes.Contains(data, []byte(`"creation_match"`)) {
				t.Fatalf("runtime-only result claims creation evidence: %s", data)
			}
			// The Cookie cannot replace a programmatic API key. The original key-based
			// submission still succeeds against the very same durable service.
			delete(body, "address")
			encoded, _ := json.Marshal(body)
			request, _ := http.NewRequestWithContext(ctx, "POST", h.baseURL+"/api/v1/contracts/"+target+"/verification", bytes.NewReader(encoded))
			request.Header.Set("Content-Type", "application/json")
			request.AddCookie(session.cookie)
			denied, err := h.http.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = denied.Body.Close()
			if denied.StatusCode != 401 {
				t.Fatalf("Cookie authorized native API: %d", denied.StatusCode)
			}
			apiKey := createVerificationAPIKey(t, ctx, h)
			h.secrets = append(h.secrets, apiKey)
			request, _ = http.NewRequestWithContext(ctx, "POST", h.baseURL+"/api/v1/contracts/"+target+"/verification", bytes.NewReader(encoded))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-API-Key", apiKey)
			accepted, err := h.http.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			nativeBody, _ := io.ReadAll(io.LimitReader(accepted.Body, 1<<20))
			_ = accepted.Body.Close()
			if accepted.StatusCode != 202 {
				t.Fatalf("native API status=%d body=%s", accepted.StatusCode, nativeBody)
			}
			nativeTarget, nativeKey, nativeInput = target, apiKey, encoded
			statuses = append(statuses, string(result.Data.Status))
		}
		h.assertTurnstileProtection(ctx, session, nativeTarget, nativeKey, nativeInput)
		return strings.Join(statuses, ",")
	})
}

// Enable the shared guard on the production API/all process after proving the default
// paths. No real provider request or production widget secret is needed to prove
// admission rejects missing tokens and native authentication remains independent.
func (h *harness) assertTurnstileProtection(ctx context.Context, session runtimeWatchSession, target, apiKey string, input []byte) {
	h.t.Helper()
	h.project.Env["ETHERVIEW_RUNTIME_SOURCIFY"] = "true"
	h.project.Env["ETHERVIEW_TURNSTILE_ENABLED"] = "true"
	h.project.Env["ETHERVIEW_TURNSTILE_SITE_KEY"] = "runtime-public-key"
	h.project.Env["ETHERVIEW_TURNSTILE_SECRET_KEY"] = "runtime-private-key"
	h.secrets = append(h.secrets, "runtime-private-key")
	h.compose(ctx, "up", "-d", "--no-deps", "--force-recreate", h.apiService)
	h.resolveAPI(ctx)
	h.waitReady(ctx)
	for _, path := range []string{"/verification-jobs/local", "/verification-jobs/sourcify-import", "/verification-jobs/sourcify", "/verification-jobs/sourcify-from-etherscan", "/users/me/exports/address-activity"} {
		req, _ := http.NewRequestWithContext(ctx, "POST", h.baseURL+"/api/v1"+path, strings.NewReader("{}"))
		req.Header.Set("Origin", "https://explorer.example.com")
		req.Header.Set("X-CSRF-Token", session.csrf)
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(session.cookie)
		res, err := h.http.Do(req)
		if err != nil {
			h.t.Fatal(err)
		}
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		_ = res.Body.Close()
		if res.StatusCode != 403 || !bytes.Contains(body, []byte("turnstile_invalid")) || res.Header.Get("Content-Disposition") != "" {
			h.t.Fatalf("Turnstile %s status=%d body=%s", path, res.StatusCode, body)
		}
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", h.baseURL+"/api/v1/contracts/"+target+"/verification", bytes.NewReader(input))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	res, err := h.http.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	_ = res.Body.Close()
	if res.StatusCode != 202 || bytes.Contains(body, []byte("turnstile")) {
		h.t.Fatalf("native validation changed: %d %s", res.StatusCode, body)
	}
	req, _ = http.NewRequestWithContext(ctx, "GET", h.baseURL+"/verify", nil)
	res, err = h.http.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	_ = res.Body.Close()
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-src https://challenges.cloudflare.com") {
		h.t.Fatal("production shell lacks Turnstile policy")
	}
}
