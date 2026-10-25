package verify

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func bytecodeDBTestClient(t *testing.T, handler http.HandlerFunc) *BytecodeDBClient {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	client, err := NewBytecodeDBClient(BytecodeDBOptions{BaseURL: server.URL, Timeout: time.Second, MaxResponseBytes: 1 << 20, MaxInputBytes: 5 << 20, UnsafeAllowPrivateNetworks: true})
	if err != nil {
		t.Fatal(err)
	}
	// Trust only the fixture certificate; retain the production dialer and policy.
	client.http.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	t.Cleanup(client.http.CloseIdleConnections)
	return client
}

func TestBytecodeDBSearchContractAndHostileResponses(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		want       string
	}{
		{"empty", `{"sources":[]}`, 200, ""},
		{"missing", `{}`, 200, "invalid_response"},
		{"duplicate", `{"sources":[],"sources":[]}`, 200, "invalid_response"},
		{"null", `{"sources":null}`, 200, "invalid_response"},
		{"deep", `{"sources":[],"extra":` + strings.Repeat("[", 150) + `0` + strings.Repeat("]", 150) + `}`, 200, "invalid_response"},
		{"large", strings.Repeat("x", (1<<20)+1), 200, "response_too_large"},
		{"rate", `secret upstream URL`, 429, "rate_limited"},
		{"unavailable", `secret`, 503, "unavailable"},
		{"rejected", `secret`, 400, "rejected"},
		{"redirect", `secret`, 302, "unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := bytecodeDBTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/api/v2/bytecodes/sources:search" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				var request map[string]string
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if request["bytecode"] != "0x6001" || request["bytecodeType"] != "DEPLOYED_BYTECODE" || len(request) != 2 {
					t.Errorf("request=%v", request)
				}
				w.Header().Set("Retry-After", "999999")
				if tc.status == 302 {
					w.Header().Set("Location", "https://example.com/private")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			sources, err := client.Search(t.Context(), []byte{0x60, 1}, "DEPLOYED_BYTECODE")
			if tc.want == "" {
				if err != nil || len(sources) != 0 {
					t.Fatalf("sources=%v err=%v", sources, err)
				}
				return
			}
			var upstream BytecodeDBError
			if !errors.As(err, &upstream) || upstream.Code != tc.want || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error=%v", err)
			}
			if tc.status == 429 && upstream.RetryAfter != 24*time.Hour {
				t.Fatalf("retry=%s", upstream.RetryAfter)
			}
		})
	}
}

func TestBytecodeDBRejectsPrivateNetworkAndInvalidConfig(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("private request reached server") }))
	defer server.Close()
	options := BytecodeDBOptions{BaseURL: server.URL, Timeout: time.Second, MaxResponseBytes: 1024, MaxInputBytes: 1024}
	client, err := NewBytecodeDBClient(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Search(t.Context(), []byte{1}, "DEPLOYED_BYTECODE"); err == nil {
		t.Fatal("private network allowed")
	}
	for _, raw := range []string{"http://example.com", "https://user:secret@example.com", "https://example.com?key=secret", "https://example.com/%2e%2e/", "https://example.com/#secret"} {
		options.BaseURL = raw
		if _, err = NewBytecodeDBClient(options); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestBytecodeDBSearchCancellation(t *testing.T) {
	client := bytecodeDBTestClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.Search(ctx, []byte{1}, "DEPLOYED_BYTECODE"); err == nil {
		t.Fatal("cancelled search succeeded")
	}
}

func bytecodeDBSourceFixture() BytecodeDBSource {
	return BytecodeDBSource{FileName: "A.sol", ContractName: "A", SourceType: "SOLIDITY", CompilerVersion: "v0.8.30+commit.73712a01", CompilerSettings: `{}`, SourceFiles: map[string]string{"A.sol": "contract A {}"}, MatchType: "FULL"}
}

func TestBytecodeDBCandidateOrderingDedupAndBudget(t *testing.T) {
	sources := []BytecodeDBSource{}
	for i := 7; i >= 0; i-- {
		source := bytecodeDBSourceFixture()
		source.ContractName = fmt.Sprintf("A%d", i)
		sources = append(sources, source)
	}
	sources = append(sources, sources[0])
	sources[0].MatchType = "PARTIAL"
	candidates, limited := bytecodeDBCandidates(sources)
	if len(candidates) != 5 || !limited || candidates[0].ContractName != "A0" || candidates[4].ContractName != "A4" {
		t.Fatalf("candidates=%v limited=%v", candidates, limited)
	}
}

func TestBytecodeDBBuildsOnlyLocalCompilerInputs(t *testing.T) {
	source := bytecodeDBSourceFixture()
	target := VerificationTarget{ChainID: 1, Address: "0x" + strings.Repeat("11", 20), CodeHash: "0x" + strings.Repeat("22", 32), AtBlockHash: "0x" + strings.Repeat("33", 32), CreationBytecode: "0x6001", RuntimeBytecode: "0x6002"}
	for _, language := range []string{"SOLIDITY", "YUL", "VYPER"} {
		t.Run(language, func(t *testing.T) {
			source.SourceType = language
			if language == "VYPER" {
				source.CompilerVersion = "v0.4.3+commit.bff19ea2"
			}
			request, err := bytecodeDBSubmission(source, target, 5<<20)
			if err != nil {
				t.Fatal(err)
			}
			if *request.Target != target || request.Bytecodes[0].Runtime != target.RuntimeBytecode || request.BytecodeDB != nil {
				t.Fatalf("target changed: %+v", request)
			}
		})
	}
	for _, mutate := range []func(*BytecodeDBSource){
		func(s *BytecodeDBSource) { s.SourceType = "GEAS" },
		func(s *BytecodeDBSource) { s.CompilerSettings = `{"x":1,"x":2}` },
		func(s *BytecodeDBSource) { s.SourceFiles = map[string]string{"../A.sol": "bad"} },
		func(s *BytecodeDBSource) { s.FileName = "missing.sol" },
		func(s *BytecodeDBSource) { s.SourceFiles["A.sol"] = strings.Repeat("x", 1025) },
	} {
		source = bytecodeDBSourceFixture()
		mutate(&source)
		if _, err := bytecodeDBSubmission(source, target, 1024); err == nil {
			t.Fatal("invalid candidate accepted")
		}
	}
}

func TestBytecodeDBProvenanceCannotBeSubmittedByPublicService(t *testing.T) {
	service := Service{repository: &verifyMemoryRepository{}, maxInputBytes: 1024}
	_, _, err := service.SubmitV2(t.Context(), SubmissionV2{BytecodeDB: &BytecodeDBProvenance{Provider: "forged"}})
	if err == nil {
		t.Fatal("public caller forged automatic origin")
	}
}

func TestBytecodeDBSearchTimeoutIsSanitized(t *testing.T) {
	client := bytecodeDBTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	})
	client.http.Timeout = 100 * time.Millisecond
	_, err := client.Search(t.Context(), []byte{1}, "DEPLOYED_BYTECODE")
	var upstream BytecodeDBError
	if !errors.As(err, &upstream) || upstream.Code != "unavailable" || err.Error() != "bytecode database unavailable" {
		t.Fatalf("timeout leaked diagnostics: %v", err)
	}
}

func TestBytecodeDBVyperReleaseVersionMapping(t *testing.T) {
	source := bytecodeDBSourceFixture()
	source.SourceType = "VYPER"
	source.FileName = "A.vy"
	source.SourceFiles = map[string]string{"A.vy": "@external\ndef value() -> uint256:\n    return 1\n"}
	for _, version := range []string{"0.4.3", "v0.4.3", "v0.4.3+commit.bff19ea2"} {
		source.CompilerVersion = version
		request, err := bytecodeDBSubmission(source, VerificationTarget{}, 5<<20)
		if err != nil || request.CompilerVersion != "0.4.3" {
			t.Fatalf("version %q: mapped=%q err=%v", version, request.CompilerVersion, err)
		}
	}
	for _, version := range []string{"0.4.3rc1", "v0.4.3-rc.1", "v0.4.3+commit.nothex00", "v0.4.3+custom", "v0.4.3+commit.bff19ea2.extra", "v00.4.3"} {
		source.CompilerVersion = version
		if _, err := bytecodeDBSubmission(source, VerificationTarget{}, 5<<20); err == nil {
			t.Fatalf("accepted unstable or malformed Vyper version %q", version)
		}
	}
}

func TestBytecodeDBPrivateNetworkOptInRetainsTLSAndRedirectPolicy(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/redirect/api/v2/bytecodes/sources:search" {
			http.Redirect(w, r, "/redirect-target", http.StatusFound)
			return
		}
		if r.URL.Path != "/api/v2/bytecodes/sources:search" {
			t.Error("redirect target reached")
		}
		_, _ = w.Write([]byte(`{"sources":[]}`))
	}))
	defer server.Close()
	for _, tc := range []struct {
		name                             string
		unsafe, trust, redirect, success bool
	}{
		{"default blocks trusted local endpoint", false, true, false, false},
		{"opt in admits trusted local endpoint", true, true, false, true},
		{"opt in still verifies TLS", true, false, false, false},
		{"opt in still rejects redirect", true, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := BytecodeDBOptions{BaseURL: server.URL, Timeout: time.Second, MaxResponseBytes: 1024, MaxInputBytes: 1024, UnsafeAllowPrivateNetworks: tc.unsafe}
			if tc.redirect {
				options.BaseURL += "/redirect"
			}
			client, err := NewBytecodeDBClient(options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.http.CloseIdleConnections)
			transport := client.http.Transport.(*http.Transport)
			if transport.Proxy != nil || (transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify) {
				t.Fatal("proxy or TLS boundary changed")
			}
			if tc.trust {
				transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			} else {
				transport.TLSClientConfig = &tls.Config{RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS12}
			}
			before := requests.Load()
			_, err = client.Search(t.Context(), []byte{1}, "DEPLOYED_BYTECODE")
			if (err == nil) != tc.success {
				t.Fatalf("search: %v", err)
			}
			wantRequests := int32(0)
			if tc.unsafe && tc.trust {
				wantRequests = 1
			}
			if requests.Load()-before != wantRequests {
				t.Fatalf("requests = %d, want %d", requests.Load()-before, wantRequests)
			}
			if err != nil && err.Error() != "bytecode database unavailable" {
				t.Fatalf("unsanitized error: %v", err)
			}
			options.BaseURL = "http://localhost"
			if _, err := NewBytecodeDBClient(options); err == nil {
				t.Fatal("HTTP accepted")
			}
		})
	}
}

func TestBytecodeDBPrivateNetworkDNSPolicy(t *testing.T) {
	for _, unsafe := range []bool{false, true} {
		for _, addresses := range [][]string{
			{"198.18.1.49"}, {"10.0.0.1"}, {"127.0.0.1"}, {"::1"}, {"fd00::1"},
			{"169.254.169.254"}, {"8.8.8.8", "198.18.1.49"},
		} {
			t.Run(fmt.Sprintf("%v/unsafe=%t", addresses, unsafe), func(t *testing.T) {
				resolver := fixedOutboundResolver{}
				for _, ip := range addresses {
					resolver.addresses = append(resolver.addresses, net.IPAddr{IP: net.ParseIP(ip)})
				}
				// Cancellation prevents actual network traffic; an admitted address reaches
				// the dial stage, while a disallowed DNS answer fails before dialing.
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				_, err := dialRestrictedOutboundHost(ctx, "tcp", "bytecode.example:443", resolver, unsafe, time.Second)
				want := "restricted outbound host resolves to a disallowed network"
				if unsafe {
					want = "dial restricted outbound host"
				}
				if err == nil || err.Error() != want {
					t.Fatalf("DNS policy: %v, want %s", err, want)
				}
			})
		}
	}
}
