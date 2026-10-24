//go:build runtimee2e

package runtimee2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/api/gen"
)

type runtimeWatchSession struct {
	cookie *http.Cookie
	csrf   string
}

func (h *harness) watchRequest(ctx context.Context, s runtimeWatchSession, method, path string, body any) ([]byte, *http.Response) {
	h.t.Helper()
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, h.baseURL+"/api/v1"+path, bytes.NewReader(data))
	if err != nil {
		h.t.Fatal(err)
	}
	request.Header.Set("Origin", "https://explorer.example.com")
	request.Header.Set("Content-Type", "application/json")
	if s.cookie != nil {
		request.AddCookie(s.cookie)
		request.Header.Set("X-CSRF-Token", s.csrf)
	}
	response, err := h.http.Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	result, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		h.t.Fatal(err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		h.t.Fatalf("watchlist %s %s status=%d body=%s", method, path, response.StatusCode, result)
	}
	return result, response
}
func (h *harness) startRuntimeWatch(ctx context.Context) runtimeWatchSession {
	h.t.Helper()
	h.assertPushWorker(ctx)
	h.secrets = append(h.secrets, h.project.Env["ETHERVIEW_RUNTIME_SESSION_PEPPER"])
	s := runtimeWatchSession{}
	address := crypto.PubkeyToAddress(h.fixture.sponsorKey.PublicKey).Hex()
	data, _ := h.watchRequest(ctx, s, "POST", "/auth/challenge", map[string]string{"address": address})
	var challenge gen.AuthChallengeResponse
	if err := json.Unmarshal(data, &challenge); err != nil {
		h.t.Fatal(err)
	}
	signature, err := crypto.Sign(accounts.TextHash([]byte(challenge.Data.Message)), h.fixture.sponsorKey)
	if err != nil {
		h.t.Fatal(err)
	}
	signature[64] += 27
	data, response := h.watchRequest(ctx, s, "POST", "/auth/verify", map[string]string{"challenge_id": challenge.Data.ChallengeId.String(), "signature": hexutil.Encode(signature)})
	var authenticated gen.AuthSessionResponse
	if err = json.Unmarshal(data, &authenticated); err != nil {
		h.t.Fatal(err)
	}
	if authenticated.Data.CsrfToken == nil || len(response.Cookies()) != 1 {
		h.t.Fatal("missing SIWE cookie/CSRF")
	}
	s.cookie = response.Cookies()[0]
	s.csrf = *authenticated.Data.CsrfToken
	h.watchRequest(ctx, s, "POST", "/users/me/watchlist", gen.WatchInput{Address: h.fixture.accounts[0], Label: "Runtime watch", Kinds: []gen.WatchInputKinds{"transaction", "erc20", "erc721", "erc1155"}, Direction: "both", Enabled: true})
	h.watchRequest(ctx, s, "POST", "/users/me/push-subscriptions", h.pushInput)
	listed, _ := h.watchRequest(ctx, s, "GET", "/users/me/push-subscriptions", nil)
	var subscriptions gen.PushSubscriptionsResponse
	if err := json.Unmarshal(listed, &subscriptions); err != nil || !subscriptions.Data.Enabled || len(subscriptions.Data.Items) != 1 {
		h.t.Fatal("browser subscription API did not retain session binding")
	}
	if bytes.Contains(listed, []byte(h.pushInput.Endpoint)) || bytes.Contains(listed, []byte(h.pushInput.Auth)) {
		h.t.Fatal("browser subscription API exposed private endpoint or key")
	}

	return s
}

func (h *harness) assertPushWorker(ctx context.Context) {
	h.t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.baseURL+"/push-sw.js", nil)
	if err != nil {
		h.t.Fatal(err)
	}
	request.Header.Set("Service-Worker", "script")
	response, err := h.http.Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 32<<10))
	if err != nil {
		h.t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "javascript") ||
		!workerRegistersEvent(body, "push") || !workerRegistersEvent(body, "notificationclick") {
		h.t.Fatalf("production push worker missing: status=%d content-type=%s", response.StatusCode, response.Header.Get("Content-Type"))
	}
	if strings.Contains(response.Header.Get("Cache-Control"), "immutable") {
		h.t.Fatal("production push worker cannot update")
	}
}

func workerRegistersEvent(body []byte, event string) bool {
	// Vite may emit single/double quotes or a static template literal.
	for _, quote := range []string{`"`, "'", "`"} {
		pattern := `\baddEventListener\s*\(\s*` + regexp.QuoteMeta(quote+event+quote) + `\s*,`
		if regexp.MustCompile(pattern).Match(body) {
			return true
		}
	}
	return false
}

func (h *harness) assertRuntimeCSV(ctx context.Context, s runtimeWatchSession) {
	h.t.Helper()
	data, response := h.watchRequest(ctx, s, "POST", "/users/me/exports/address-activity", gen.AddressExportRequest{Address: h.fixture.accounts[0], Kind: "transaction", Direction: "both", From: time.Unix(int64(h.baseTimestamp), 0), To: time.Unix(int64(h.baseTimestamp+3600), 0)})
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/csv") || !bytes.Contains(data, []byte(h.fixture.creationHash)) || bytes.Contains(data, []byte(h.fixture.orphanDelegationHash)) {
		h.t.Fatalf("CSV canonical contract failed: %s", data)
	}
	h.writeArtifact(h.mode+"-address-export.csv", data)
}
