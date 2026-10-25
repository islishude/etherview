package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func bytecodeDBTestClient(t *testing.T, handler http.HandlerFunc) *BytecodeDBClient {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	client, err := NewBytecodeDBClient(BytecodeDBOptions{BaseURL: server.URL, Timeout: time.Second, MaxResponseBytes: 1 << 20, MaxInputBytes: 5 << 20})
	if err != nil {
		t.Fatal(err)
	}
	client.http = restrictedOutboundClient(server.Client(), time.Second, nil, false)
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
