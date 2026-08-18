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

func stringContains(value, fragment string) bool {
	return len(fragment) > 0 && len(value) >= len(fragment) && strings.Contains(value, fragment)
}
