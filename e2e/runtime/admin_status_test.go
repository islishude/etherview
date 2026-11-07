//go:build runtimee2e

package runtimee2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/api/gen"
)

// Status probes use the same real SIWE and operator promotion in both topologies.
func (h *harness) statusAdminCookie(ctx context.Context) *http.Cookie {
	h.statusAdminMu.Lock()
	defer h.statusAdminMu.Unlock()
	if h.statusCookie != nil {
		return h.statusCookie
	}
	checkDenied := func(cookie *http.Cookie, expected int) {
		for _, path := range []string{"/api/v1/status", "/api/v1/admin/overview"} {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.baseURL+path, nil)
			if err != nil {
				h.t.Fatal(err)
			}
			if cookie != nil {
				request.AddCookie(cookie)
			}
			response, err := h.http.Do(request)
			if err != nil {
				h.t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != expected {
				h.t.Fatalf("status access=%d want=%d", response.StatusCode, expected)
			}
		}
	}
	checkDenied(nil, http.StatusUnauthorized)
	key, err := crypto.GenerateKey()
	if err != nil {
		h.t.Fatal(err)
	}
	address := crypto.PubkeyToAddress(key.PublicKey).Hex()
	data, _ := h.watchRequest(ctx, runtimeWatchSession{}, "POST", "/auth/challenge", map[string]string{"address": address})
	var challenge gen.AuthChallengeResponse
	if err := json.Unmarshal(data, &challenge); err != nil {
		h.t.Fatal(err)
	}
	signature, err := crypto.Sign(accounts.TextHash([]byte(challenge.Data.Message)), key)
	if err != nil {
		h.t.Fatal(err)
	}
	signature[64] += 27
	_, response := h.watchRequest(ctx, runtimeWatchSession{}, "POST", "/auth/verify", map[string]string{"challenge_id": challenge.Data.ChallengeId.String(), "signature": hexutil.Encode(signature)})
	if len(response.Cookies()) != 1 {
		h.t.Fatal("status SIWE cookie missing")
	}
	cookie := response.Cookies()[0]
	checkDenied(cookie, http.StatusForbidden)
	if _, err := h.project.Run(ctx, "exec", "-T", h.apiService, "/etherview", "admin", "user", "set-role", "--config=/etc/etherview/config.yaml", "--address="+address, "--role=admin"); err != nil {
		h.t.Fatal(err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.baseURL+"/api/v1/admin/overview", nil)
	if err != nil {
		h.t.Fatal(err)
	}
	request.AddCookie(cookie)
	response, err = h.http.Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	var overview gen.AdminOverviewResponse
	decodeErr := json.NewDecoder(response.Body).Decode(&overview)
	_ = response.Body.Close()
	if decodeErr != nil {
		h.t.Fatal(decodeErr)
	}
	count, err := strconv.ParseUint(overview.Data.UserCount, 10, 64)
	if response.StatusCode != http.StatusOK || err != nil || count < 1 {
		h.t.Fatalf("overview status=%d count=%q error=%v", response.StatusCode, overview.Data.UserCount, err)
	}
	h.statusCookie = cookie
	return cookie
}
