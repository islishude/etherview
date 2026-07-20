package verify

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatchBytecodeExactMetadataAndMismatch(t *testing.T) {
	t.Parallel()
	if got, err := MatchBytecode("0x6001", "6001"); err != nil || got != MatchExact {
		t.Fatalf("got %s err=%v", got, err)
	}
	core := []byte{0x60, 0x01}
	withMetadata := func(metadata []byte) string {
		value := append(append([]byte{}, core...), metadata...)
		var length [2]byte
		binary.BigEndian.PutUint16(length[:], uint16(len(metadata)))
		value = append(value, length[:]...)
		return "0x" + hex.EncodeToString(value)
	}
	left, right := withMetadata([]byte{0xa1, 0x01}), withMetadata([]byte{0xa1, 0x02})
	if got, err := MatchBytecode(left, right); err != nil || got != MatchMetadataOnly {
		t.Fatalf("got %s err=%v", got, err)
	}
	if got, err := MatchBytecode("0x6001", "0x6002"); err != nil || got != MatchMismatch {
		t.Fatalf("got %s err=%v", got, err)
	}
}

func TestExtractArtifactRejectsCompilerErrors(t *testing.T) {
	t.Parallel()
	output := json.RawMessage(`{"errors":[{"severity":"error","message":"bad source"}]}`)
	if _, err := ExtractArtifact(output, "A.sol:A"); err == nil || !strings.Contains(err.Error(), "bad source") {
		t.Fatalf("unexpected error: %v", err)
	}
	output = json.RawMessage(`{"contracts":{"A.sol":{"A":{"abi":[],"metadata":"{}","evm":{"bytecode":{"object":"6001"},"deployedBytecode":{"object":"6002"}}}}}}`)
	artifact, err := ExtractArtifact(output, "A.sol:A")
	if err != nil || artifact.RuntimeBytecode != "0x6002" {
		t.Fatalf("artifact=%#v err=%v", artifact, err)
	}
}

func TestCompilerCacheChecksDigestAndAllowlist(t *testing.T) {
	t.Parallel()
	payload := []byte("compiler binary")
	digest := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payload) }))
	defer server.Close()
	cache := CompilerCache{
		Root:      t.TempDir(),
		AllowHTTP: true,
		Artifacts: map[Language]map[string]CompilerArtifact{LanguageSolidity: {"1.2.3": {URL: server.URL, SHA256: hex.EncodeToString(digest[:])}}},
	}
	path, err := cache.Ensure(context.Background(), LanguageSolidity, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(payload) {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if _, err := cache.Ensure(context.Background(), LanguageVyper, "1.2.3"); err == nil {
		t.Fatal("expected allowlist rejection")
	}
	cache.Artifacts[LanguageSolidity]["bad"] = CompilerArtifact{URL: server.URL, SHA256: strings.Repeat("0", 64)}
	if _, err := cache.Ensure(context.Background(), LanguageSolidity, "bad"); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProcessCompilerRejectsPublicExecution(t *testing.T) {
	t.Parallel()
	compiler := ProcessCompiler{Public: true, Cache: &CompilerCache{}}
	if _, err := compiler.Compile(context.Background(), LanguageSolidity, "1", []byte(`{}`)); err != ErrSandboxRequired {
		t.Fatalf("got %v", err)
	}
}

func TestCompilerProvenanceUsesPinnedArtifactDigest(t *testing.T) {
	digestHex := strings.Repeat("01", 32)
	process := ProcessCompiler{Cache: &CompilerCache{Artifacts: map[Language]map[string]CompilerArtifact{
		LanguageSolidity: {"0.8.30": {SHA256: digestHex}},
	}}}
	provenance, err := process.Provenance(LanguageSolidity, "0.8.30")
	if err != nil || provenance.Kind != CompilerProcess || provenance.HardIsolated ||
		hex.EncodeToString(provenance.Digest[:]) != digestHex {
		t.Fatalf("process provenance=%+v error=%v", provenance, err)
	}

	container := ContainerCompiler{
		Runtime: "unavailable-runtime",
		Images: map[Language]map[string]string{
			LanguageSolidity: {"0.8.30": "registry.invalid/solc@sha256:" + digestHex},
		},
	}
	provenance, err = container.Provenance(LanguageSolidity, "0.8.30")
	if err != nil || provenance.Kind != CompilerContainer || provenance.HardIsolated ||
		hex.EncodeToString(provenance.Digest[:]) != digestHex {
		t.Fatalf("container provenance=%+v error=%v", provenance, err)
	}
	container.Images[LanguageSolidity]["0.8.30"] = "registry.invalid/solc:latest"
	if _, err := container.Provenance(LanguageSolidity, "0.8.30"); err == nil {
		t.Fatal("mutable container image produced compiler provenance")
	}
}

func TestContainerRequiresDigest(t *testing.T) {
	t.Parallel()
	compiler := ContainerCompiler{Images: map[Language]map[string]string{LanguageSolidity: {"1": "solc:latest"}}}
	if _, err := compiler.Compile(context.Background(), LanguageSolidity, "1", []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "pinned by digest") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestContainerIsolationRejectsUntrustedOrMissingRuntime(t *testing.T) {
	t.Parallel()
	compiler := ContainerCompiler{Runtime: "/tmp/docker-wrapper"}
	if compiler.HardIsolated() {
		t.Fatal("arbitrary runtime executable was treated as hard isolation")
	}
	if err := compiler.ValidateRuntime(context.Background()); err == nil || !strings.Contains(err.Error(), "not allowlisted") {
		t.Fatalf("unexpected runtime error: %v", err)
	}

	compiler.Runtime = "etherview-runtime-that-does-not-exist"
	if compiler.HardIsolated() {
		t.Fatal("missing runtime was treated as hard isolation")
	}
}

func TestSourcifyLookupSubmitStatusAndConsent(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/v2/contract/"):
			_, _ = w.Write([]byte(`{"match":"exact_match"}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/v2/verify/"):
			_, _ = w.Write([]byte(`{"verificationId":"job-1"}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/v2/verify/job-1"):
			_, _ = w.Write([]byte(`{"verificationId":"job-1","status":"verified"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := SourcifyClient{BaseURL: server.URL, AllowHTTP: true}
	contract, err := client.Lookup(context.Background(), 1, "0x0000000000000000000000000000000000000001")
	if err != nil || contract.Match != "exact_match" {
		t.Fatalf("contract=%#v err=%v", contract, err)
	}
	request := Request{ChainID: 1, Address: "0x0000000000000000000000000000000000000001", StandardJSON: json.RawMessage(`{}`), CompilerVersion: "1", ContractIdentifier: "A.sol:A", SubmitToSourcify: true}
	if _, err := client.Submit(context.Background(), request, false); err != ErrConsentRequired {
		t.Fatalf("got %v", err)
	}
	ticket, err := client.Submit(context.Background(), request, true)
	if err != nil || ticket.VerificationID != "job-1" {
		t.Fatalf("ticket=%#v err=%v", ticket, err)
	}
	job, err := client.Status(context.Background(), "job-1")
	if err != nil || job.Status != "verified" {
		t.Fatalf("job=%#v err=%v", job, err)
	}
}

func TestSourcifyRequiresHTTPSWithoutCredentials(t *testing.T) {
	t.Parallel()
	for _, baseURL := range []string{
		"http://sourcify.example",
		"https://user:password@sourcify.example",
	} {
		client := SourcifyClient{BaseURL: baseURL}
		if _, err := client.Lookup(context.Background(), 1, "0x0000000000000000000000000000000000000001"); err == nil || !strings.Contains(err.Error(), "invalid Sourcify base URL") {
			t.Fatalf("base URL %q error=%v", baseURL, err)
		}
	}
}

func TestRequestValidationBindsCodeAndBlock(t *testing.T) {
	t.Parallel()
	runtimeBytecode := []byte{0x60, 0x01}
	request := Request{ChainID: 1, Address: "0x" + strings.Repeat("00", 20), CodeHash: "0x" + hex.EncodeToString(keccak256Bytes(runtimeBytecode)), AtBlockHash: "0x" + strings.Repeat("22", 32), Language: LanguageSolidity, CompilerVersion: "0.8.30", ContractIdentifier: "A.sol:A", StandardJSON: json.RawMessage(`{"language":"Solidity"}`), RuntimeBytecode: "0x" + hex.EncodeToString(runtimeBytecode)}
	if err := request.Validate(1024); err != nil {
		t.Fatal(err)
	}
	request.CodeHash = "0x12"
	if err := request.Validate(1024); err == nil || !strings.Contains(err.Error(), "code hash") {
		t.Fatalf("unexpected error: %v", err)
	}
	request.CodeHash = "0x" + strings.Repeat("11", 32)
	if err := request.Validate(1024); err == nil || !strings.Contains(err.Error(), "keccak256") {
		t.Fatalf("mismatched runtime hash error: %v", err)
	}
	request.CodeHash = "0x" + hex.EncodeToString(keccak256Bytes(nil))
	request.RuntimeBytecode = "0x"
	if err := request.Validate(1024); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("empty runtime error: %v", err)
	}
}

func TestRequestValidationRejectsInvalidEtherscanMetadata(t *testing.T) {
	t.Parallel()
	base := validVerifyRequest()
	for _, test := range []struct {
		name string
		edit func(*Request)
		want string
	}{
		{name: "odd constructor arguments", edit: func(request *Request) { request.ConstructorArgs = "abc" }, want: "constructor arguments"},
		{name: "noncanonical license", edit: func(request *Request) { request.LicenseType = "03" }, want: "license type"},
		{name: "license below range", edit: func(request *Request) { request.LicenseType = "0" }, want: "license type"},
		{name: "license above range", edit: func(request *Request) { request.LicenseType = "15" }, want: "license type"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := base
			test.edit(&request)
			if err := request.Validate(1024); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestCompilerCacheUsesSafeFilename(t *testing.T) {
	t.Parallel()
	cache := CompilerCache{Root: filepath.Join(t.TempDir(), "cache"), Artifacts: map[Language]map[string]CompilerArtifact{}}
	if _, err := cache.Ensure(context.Background(), LanguageSolidity, "../../bad"); err == nil {
		t.Fatal("expected version rejection")
	}
}
