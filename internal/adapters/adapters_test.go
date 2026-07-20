package adapters

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/metadata"
)

func TestStrictHTTPSURLRejectsCredentialsFragmentsAndPlainHTTP(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"http://adapter.example/v1", "https://user:secret@adapter.example/v1",
		"https://adapter.example/v1#fragment", "//adapter.example/v1", "https:opaque",
	} {
		if _, err := strictHTTPSURL(value); err == nil || stringContains(err.Error(), value) {
			t.Fatalf("URL %q error=%v", value, err)
		}
	}
	if got, err := strictHTTPSURL("https://adapter.example/v1"); err != nil || got != "https://adapter.example/v1" {
		t.Fatalf("got=%q error=%v", got, err)
	}
}

func TestAdapterDecodersEnforceCanonicalValuesIdentityAndFreshness(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	priceJSON := []byte(`{"native_usd":"3500.25","native_btc":"0.05","observed_at":"2026-07-20T09:59:00Z"}`)
	price, err := decodeNativePrice(priceJSON, now, 5*time.Minute)
	if err != nil || price.USD != "3500.25" || price.BTC != "0.05" {
		t.Fatalf("price=%+v error=%v", price, err)
	}
	for _, raw := range [][]byte{
		[]byte(`{"native_usd":"03500","native_btc":"0.05","observed_at":"2026-07-20T09:59:00Z"}`),
		[]byte(`{"native_usd":"3500.0","native_btc":"0.05","observed_at":"2026-07-20T09:59:00Z"}`),
		[]byte(`{"native_usd":"3500","native_btc":"0.05","observed_at":"2026-07-20T09:00:00Z"}`),
		append(append([]byte(nil), priceJSON...), []byte(` {}`)...),
	} {
		if _, err := decodeNativePrice(raw, now, 5*time.Minute); err == nil {
			t.Fatalf("invalid price accepted: %s", raw)
		}
	}

	nameJSON := []byte(`{"name":"alice.eth","address":"0x52908400098527886e0f7030069857d2e4169ee7","registry":"0xde709f2102306220921060314715629080e2fb77","block_number":"10","block_hash":"0x000000000000000000000000000000000000000000000000000000000000000a","observed_at":"2026-07-20T09:59:00Z"}`)
	name, err := decodeNameObservation(nameJSON, "alice.eth", now, 5*time.Minute)
	if err != nil || name.Name != "alice.eth" || name.BlockNumber != "10" {
		t.Fatalf("name=%+v error=%v", name, err)
	}
	if _, err := decodeNameObservation(nameJSON, "bob.eth", now, 5*time.Minute); err == nil {
		t.Fatal("mismatched name identity was accepted")
	}
	for _, blockNumber := range []string{"", "00", "01", "+1", "-1", "1.0", strings.Repeat("9", 79)} {
		invalid := []byte(strings.Replace(string(nameJSON), `"block_number":"10"`, `"block_number":"`+blockNumber+`"`, 1))
		if _, err := decodeNameObservation(invalid, "alice.eth", now, 5*time.Minute); err == nil {
			t.Fatalf("invalid name block number %q was accepted", blockNumber)
		}
	}
	for _, blockNumber := range []string{"0", strings.Repeat("9", 78)} {
		valid := []byte(strings.Replace(string(nameJSON), `"block_number":"10"`, `"block_number":"`+blockNumber+`"`, 1))
		if _, err := decodeNameObservation(valid, "alice.eth", now, 5*time.Minute); err != nil {
			t.Fatalf("canonical name block number %q was rejected: %v", blockNumber, err)
		}
	}
}

func TestCapabilityErrorsAndFetchClassificationNeverExposeNestedText(t *testing.T) {
	t.Parallel()
	secret := "https://user:secret@example.invalid/private"
	err := CapabilityError{Capability: "price", State: "failed", Code: secret}
	if !errors.Is(err, ErrUnavailable) || stringContains(err.Error(), secret) {
		t.Fatalf("capability error=%q", err)
	}
	code, state := classifyFetchFailure(&metadata.FetchError{Kind: metadata.FailureUnsafeURL, Err: errors.New(secret)})
	if code != "unsafe_url" || state != "unavailable" || stringContains(code, secret) {
		t.Fatalf("code=%q state=%q", code, state)
	}
}

func TestNameProviderKeyIsStableIsolatedAndDoesNotExposeURL(t *testing.T) {
	t.Parallel()
	secretURL := "https://name.example/v1?token=operator-secret"
	first := nameProviderKey(secretURL)
	if first != nameProviderKey(secretURL) {
		t.Fatal("name provider key is not stable")
	}
	if first == nameProviderKey("https://name.example/v2?token=operator-secret") {
		t.Fatal("different provider URLs share a cache namespace")
	}
	if !strings.HasPrefix(first, "sha256:") || len(first) != len("sha256:")+64 ||
		strings.Contains(first, "operator-secret") || strings.Contains(first, "name.example") {
		t.Fatalf("unsafe name provider key %q", first)
	}
}

func stringContains(value, fragment string) bool {
	return len(fragment) > 0 && len(value) >= len(fragment) && strings.Contains(value, fragment)
}
