package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type staticResolver struct {
	addresses []net.IPAddr
	err       error
}

func (r staticResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return r.addresses, r.err
}

func TestRejectsUnsafeSchemesCredentialsAndPrivateResolution(t *testing.T) {
	t.Parallel()
	client, err := New(Policy{}, staticResolver{addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"http://example.com/a.json", "file:///etc/passwd", "https://user:pass@example.com/a", "https://example.com/a#fragment"} {
		if _, err := client.Fetch(context.Background(), raw, KindJSON); err == nil {
			t.Fatalf("expected rejection for %s", raw)
		}
	}
	_, err = client.safeDial(context.Background(), "tcp", "example.com:443")
	if err == nil || !strings.Contains(err.Error(), "disallowed network") {
		t.Fatalf("unexpected dial result: %v", err)
	}
}

func TestFetchJSONLimitsSizeAndMIME(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		contentType string
		body        string
		max         int64
		wantError   string
	}{
		{"valid", "application/json", `{"name":"token"}`, 1024, ""},
		{"invalid json", "application/json", `{`, 1024, "not valid JSON"},
		{"html", "text/html", `<script>bad()</script>`, 1024, "not allowed"},
		{"large", "application/json", `{"value":"` + strings.Repeat("x", 200) + `"}`, 32, "size limit"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := New(Policy{AllowHTTP: true, UnsafeAllowPrivateNetworks: true, MaxBytes: test.max, Timeout: time.Second}, net.DefaultResolver)
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Fetch(context.Background(), server.URL, KindJSON)
			if test.wantError == "" {
				if err != nil || string(result.Body) != test.body {
					t.Fatalf("result=%#v err=%v", result, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("err=%v, want %q", err, test.wantError)
			}
		})
	}
}

func TestImageRejectsSVG(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte(`<svg/>`))
	}))
	defer server.Close()
	client, err := New(Policy{AllowHTTP: true, UnsafeAllowPrivateNetworks: true}, net.DefaultResolver)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Fetch(context.Background(), server.URL, KindImage)
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestImageRejectsHTMLDisguisedAsPNG(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte(`<html><script>bad()</script></html>`))
	}))
	defer server.Close()
	client, err := New(Policy{AllowHTTP: true, UnsafeAllowPrivateNetworks: true}, net.DefaultResolver)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Fetch(context.Background(), server.URL, KindImage); err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMetadataTransportDoesNotInheritEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:3128")
	client, err := New(Policy{}, staticResolver{addresses: []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}})
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.http.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatalf("untrusted metadata transport inherited a proxy: %#v", client.http.Transport)
	}
}

func TestIPFSRewriteAndTraversal(t *testing.T) {
	t.Parallel()
	client, err := New(Policy{IPFSGateway: "https://ipfs.example/base"}, staticResolver{err: errors.New("unused")})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := client.resolveURL("ipfs://bafybeigdyrzt1234567890/metadata/1.json")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.String() != "https://ipfs.example/base/ipfs/bafybeigdyrzt1234567890/metadata/1.json" {
		t.Fatalf("resolved %s", resolved)
	}
	if _, err := client.resolveURL("ipfs://bafybeigdyrzt1234567890/../secret"); err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestIPFSNetworkPolicyDiagnosticsExposeOnlyPublicContentPath(t *testing.T) {
	t.Parallel()
	client, err := New(
		Policy{IPFSGateway: "https://ipfs.example/operator-private-prefix"},
		staticResolver{addresses: []net.IPAddr{{IP: net.ParseIP("198.18.17.210")}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	const cid = "Qma3sC19HbnWHqeLgcsQnR7Kvgus4oPQirXNH7QYBeACaq"
	_, err = client.Fetch(t.Context(), "ipfs://"+cid+"/0?signature=source-secret", KindJSON)
	var failure *FetchError
	if !errors.As(err, &failure) || failure.Kind != FailureUnsafeURL {
		t.Fatalf("fetch error=%v classification=%+v", err, failure)
	}
	diagnostic := failure.Diagnostic
	if diagnostic.SourceScheme != "ipfs" || diagnostic.RequestMethod != http.MethodGet ||
		diagnostic.RequestScheme != "https" || diagnostic.RequestHost != "ipfs.example" ||
		diagnostic.RequestPort != "443" || diagnostic.RequestPath != "/ipfs/"+cid+"/0" ||
		diagnostic.RequestPathHidden || !diagnostic.QueryPresent || diagnostic.Phase != FetchPhaseNetworkPolicy ||
		diagnostic.ResolvedIPCount != 1 || diagnostic.RejectedIPCount != 1 ||
		len(diagnostic.ResolvedIPs) != 1 || diagnostic.ResolvedIPs[0] != "198.18.17.210" ||
		len(diagnostic.RejectedReasons) != 1 || diagnostic.RejectedReasons[0] != "special_use" ||
		len(diagnostic.RejectedPrefixes) != 1 || diagnostic.RejectedPrefixes[0] != "198.18.0.0/15" {
		t.Fatalf("diagnostic=%+v", diagnostic)
	}
	encoded := fmt.Sprintf("%+v", diagnostic)
	for _, forbidden := range []string{"operator-private-prefix", "signature", "source-secret"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("diagnostic leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestHTTPSDiagnosticsHashPathAndDiscardQuery(t *testing.T) {
	t.Parallel()
	client, err := New(
		Policy{},
		staticResolver{addresses: []net.IPAddr{{IP: net.ParseIP("198.18.0.1")}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	const rawPath = "/private-path-secret/token.json"
	_, err = client.Fetch(t.Context(), "https://metadata.example"+rawPath+"?credential=query-secret", KindJSON)
	var failure *FetchError
	if !errors.As(err, &failure) {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(rawPath))
	diagnostic := failure.Diagnostic
	if diagnostic.RequestPath != "" || !diagnostic.RequestPathHidden ||
		diagnostic.RequestPathLength != len(rawPath) ||
		diagnostic.RequestPathSHA256 != hex.EncodeToString(digest[:]) || !diagnostic.QueryPresent {
		t.Fatalf("diagnostic=%+v", diagnostic)
	}
	encoded := fmt.Sprintf("%+v", diagnostic)
	for _, forbidden := range []string{"private-path-secret", "credential", "query-secret"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("diagnostic leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestIPFSDiagnosticsHideOversizedPublicPath(t *testing.T) {
	t.Parallel()
	client, err := New(
		Policy{IPFSGateway: "https://ipfs.example"},
		staticResolver{addresses: []net.IPAddr{{IP: net.ParseIP("198.18.0.1")}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	const cid = "Qma3sC19HbnWHqeLgcsQnR7Kvgus4oPQirXNH7QYBeACaq"
	_, err = client.Fetch(t.Context(), "ipfs://"+cid+"/"+strings.Repeat("x", 1025), KindJSON)
	var failure *FetchError
	if !errors.As(err, &failure) {
		t.Fatal(err)
	}
	if failure.Diagnostic.RequestPath != "" || !failure.Diagnostic.RequestPathHidden ||
		failure.Diagnostic.RequestPathLength <= maximumClearIPFSPathBytes ||
		len(failure.Diagnostic.RequestPathSHA256) != sha256.Size*2 {
		t.Fatalf("diagnostic=%+v", failure.Diagnostic)
	}
}

func TestIPFSWithoutGatewayIsUnavailableNotUnsafe(t *testing.T) {
	t.Parallel()
	client, err := New(Policy{}, staticResolver{err: errors.New("unused")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Fetch(t.Context(), "ipfs://bafybeigdyrzt1234567890/metadata.json", KindJSON)
	var failure *FetchError
	if !errors.As(err, &failure) || failure.Kind != FailureUnavailable {
		t.Fatalf("fetch error = %v classification=%+v, want unavailable", err, failure)
	}
}

func TestRedirectPolicyFailuresRemainUnsafeURL(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, "file:///etc/passwd", http.StatusFound)
	}))
	defer server.Close()
	client, err := New(Policy{
		AllowHTTP: true, UnsafeAllowPrivateNetworks: true, MaxRedirects: 1,
	}, net.DefaultResolver)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Fetch(t.Context(), server.URL, KindJSON)
	var failure *FetchError
	if !errors.As(err, &failure) || failure.Kind != FailureUnsafeURL {
		t.Fatalf("redirect error = %v classification=%+v, want unsafe URL", err, failure)
	}
	if failure.Diagnostic.Phase != FetchPhaseRedirect || failure.Diagnostic.RedirectCount != 1 ||
		failure.Diagnostic.RequestPath != "" || !failure.Diagnostic.RequestPathHidden ||
		failure.Diagnostic.RequestPathLength != len("/etc/passwd") {
		t.Fatalf("redirect diagnostic=%+v", failure.Diagnostic)
	}
}

func TestMixedPublicAndPrivateDNSIsRejected(t *testing.T) {
	t.Parallel()
	resolver := staticResolver{addresses: []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP("10.0.0.1")}}}
	client, err := New(Policy{}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Fetch(t.Context(), "https://example.com/metadata.json", KindJSON)
	var failure *FetchError
	if !errors.As(err, &failure) || failure.Kind != FailureUnsafeURL ||
		failure.Diagnostic.ResolvedIPCount != 2 || failure.Diagnostic.RejectedIPCount != 1 ||
		len(failure.Diagnostic.ResolvedIPs) != 2 || len(failure.Diagnostic.RejectedIPs) != 1 ||
		failure.Diagnostic.RejectedIPs[0] != "10.0.0.1" {
		t.Fatalf("unexpected error=%v diagnostic=%+v", err, failure)
	}
}

func TestFetchDiagnosticsBoundDNSAddresses(t *testing.T) {
	t.Parallel()
	addresses := make([]net.IPAddr, 0, 10)
	for last := 1; last <= 10; last++ {
		addresses = append(addresses, net.IPAddr{IP: net.ParseIP(fmt.Sprintf("198.18.0.%d", last))})
	}
	client, err := New(Policy{}, staticResolver{addresses: addresses})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Fetch(t.Context(), "https://metadata.example/document.json", KindJSON)
	var failure *FetchError
	if !errors.As(err, &failure) {
		t.Fatal(err)
	}
	diagnostic := failure.Diagnostic
	if diagnostic.ResolvedIPCount != 10 || len(diagnostic.ResolvedIPs) != maximumDiagnosticIPs ||
		!diagnostic.ResolvedIPsTruncated || diagnostic.RejectedIPCount != 10 ||
		len(diagnostic.RejectedIPs) != maximumDiagnosticIPs || !diagnostic.RejectedIPsTruncated {
		t.Fatalf("diagnostic=%+v", diagnostic)
	}
}

func TestFetchDiagnosticsCaptureReusedConnectionWithoutCrossRequestState(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"NFT"}`))
	}))
	defer server.Close()
	client, err := New(Policy{AllowHTTP: true, UnsafeAllowPrivateNetworks: true}, net.DefaultResolver)
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"first-secret", "second-secret"} {
		result, fetchErr := client.Fetch(t.Context(), server.URL+"/"+suffix+"?key=query-secret", KindJSON)
		if fetchErr != nil {
			t.Fatal(fetchErr)
		}
		digest := sha256.Sum256([]byte("/" + suffix))
		if result.Diagnostic.ConnectedIP != "127.0.0.1" || !result.Diagnostic.NetworkPolicyBypassed ||
			result.Diagnostic.RequestPath != "" ||
			result.Diagnostic.RequestPathSHA256 != hex.EncodeToString(digest[:]) ||
			!result.Diagnostic.QueryPresent {
			t.Fatalf("diagnostic=%+v", result.Diagnostic)
		}
	}

	const requests = 24
	diagnostics := make([]FetchDiagnostic, requests)
	errorsByRequest := make([]error, requests)
	var wait sync.WaitGroup
	for index := range requests {
		wait.Go(func() {
			suffix := fmt.Sprintf("concurrent-%d-secret", index)
			result, fetchErr := client.Fetch(t.Context(), server.URL+"/"+suffix, KindJSON)
			diagnostics[index] = result.Diagnostic
			errorsByRequest[index] = fetchErr
		})
	}
	wait.Wait()
	for index, diagnostic := range diagnostics {
		if errorsByRequest[index] != nil {
			t.Fatalf("request %d: %v", index, errorsByRequest[index])
		}
		suffix := fmt.Sprintf("/concurrent-%d-secret", index)
		digest := sha256.Sum256([]byte(suffix))
		if diagnostic.RequestPathSHA256 != hex.EncodeToString(digest[:]) ||
			diagnostic.ConnectedIP != "127.0.0.1" || !diagnostic.NetworkPolicyBypassed {
			t.Fatalf("request %d diagnostic=%+v", index, diagnostic)
		}
	}
}

func TestSpecialPurposeNetworksAreNotPublicMetadataDestinations(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"0.0.0.1",
		"100.64.0.1",
		"192.0.0.9",
		"192.0.2.1",
		"192.88.99.1",
		"198.18.0.1",
		"198.51.100.1",
		"203.0.113.1",
		"240.0.0.1",
		"64:ff9b::c0a8:1",
		"100::1",
		"2001::1",
		"2001:db8::1",
		"2002:c0a8:1::",
	} {
		if publicIP(net.ParseIP(raw)) {
			t.Errorf("special-purpose address %s was accepted as public", raw)
		}
	}
	for _, raw := range []string{"93.184.216.34", "2606:4700:4700::1111"} {
		if !publicIP(net.ParseIP(raw)) {
			t.Errorf("public address %s was rejected", raw)
		}
	}
}

func TestFetchReturnsStableFailureClassifications(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		status    int
		body      string
		mediaType string
		wantKind  FailureKind
	}{
		{name: "not found", status: http.StatusNotFound, wantKind: FailureUnavailable},
		{name: "rate limited", status: http.StatusTooManyRequests, wantKind: FailureTemporary},
		{name: "upstream error", status: http.StatusBadGateway, wantKind: FailureTemporary},
		{name: "unsafe MIME", status: http.StatusOK, body: `<html/>`, mediaType: "text/html", wantKind: FailureUnsafeContent},
		{name: "invalid JSON", status: http.StatusOK, body: `{`, mediaType: "application/json", wantKind: FailureInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.mediaType != "" {
					w.Header().Set("Content-Type", test.mediaType)
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := New(Policy{AllowHTTP: true, UnsafeAllowPrivateNetworks: true}, net.DefaultResolver)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Fetch(t.Context(), server.URL, KindJSON)
			var failure *FetchError
			if !errors.As(err, &failure) || failure.Kind != test.wantKind {
				t.Fatalf("fetch error = %v (%T), classification=%+v, want %s", err, err, failure, test.wantKind)
			}
		})
	}
}

func TestFetchClassifiesNetworkPolicyRejectionAsUnsafeURL(t *testing.T) {
	t.Parallel()
	client, err := New(Policy{}, staticResolver{addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Fetch(t.Context(), "https://metadata.example.invalid/document.json", KindJSON)
	var failure *FetchError
	if !errors.As(err, &failure) || failure.Kind != FailureUnsafeURL {
		t.Fatalf("fetch error = %v (%T), classification=%+v", err, err, failure)
	}
}
