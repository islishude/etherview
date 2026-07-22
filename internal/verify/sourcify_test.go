package verify

import (
	"context"
	"encoding/hex"
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

type sourcifyJobReaderFunc func(context.Context, string) (VerificationJob, bool, error)

func (reader sourcifyJobReaderFunc) Job(
	ctx context.Context,
	id string,
) (VerificationJob, bool, error) {
	return reader(ctx, id)
}

func durableSourcifyJob(request Request, sequence int) VerificationJob {
	encoded, err := json.Marshal(request)
	if err != nil {
		panic(err)
	}
	return VerificationJob{
		ID: verificationID(sequence), Request: request,
		RequestDigest: verificationRequestDigest(encoded, false), Status: JobQueued,
	}
}

func sourcifyReader(job VerificationJob) SourcifyJobReader {
	return sourcifyJobReaderFunc(func(_ context.Context, id string) (VerificationJob, bool, error) {
		return job, id == job.ID, nil
	})
}

func TestSourcifyRejectsRedirectsAndPrivateResolution(t *testing.T) {
	address := "0x" + strings.Repeat("11", 20)
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(sourcifyContractFixture(address, "0x6001", "0x6001"))
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, target.URL, http.StatusFound)
	}))
	defer redirect.Close()
	client, err := newSourcifyClient(
		SourcifyOptions{BaseURL: redirect.URL}, redirect.Client(), nil, true, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Lookup(context.Background(), 1, address); !errors.Is(err, ErrSourcifyUnavailable) {
		t.Fatalf("redirect error=%v", err)
	}
	if targetHits.Load() != 0 {
		t.Fatalf("redirect target received %d requests", targetHits.Load())
	}

	client, err = newSourcifyClient(
		SourcifyOptions{BaseURL: "https://sourcify.example/server", Timeout: time.Second},
		nil,
		fixedOutboundResolver{addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}},
		false,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Lookup(context.Background(), 1, address); !errors.Is(err, ErrSourcifyUnavailable) {
		t.Fatalf("private-resolution error=%v", err)
	}
}

func TestSourcifyClassifiesRemoteFailuresWithoutDetails(t *testing.T) {
	address := "0x" + strings.Repeat("11", 20)
	for _, test := range []struct {
		name   string
		status int
		body   string
		submit bool
		want   error
	}{
		{name: "not found", status: http.StatusNotFound, body: `{"customCode":"not_found","message":"upstream-secret"}`, want: ErrSourcifyNotFound},
		{name: "bad request", status: http.StatusBadRequest, body: `{"customCode":"invalid_source","message":"upstream-secret"}`, submit: true, want: ErrSourcifyRejected},
		{name: "already verified", status: http.StatusConflict, body: `{"customCode":"already_verified","message":"upstream-secret"}`, submit: true, want: ErrSourcifyAlreadyVerified},
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{"customCode":"too_many_requests","message":"upstream-secret"}`, want: ErrSourcifyUnavailable},
		{name: "server failure", status: http.StatusInternalServerError, body: `{"customCode":"internal_error","message":"upstream-secret"}`, want: ErrSourcifyUnavailable},
		{name: "unexpected status", status: http.StatusTeapot, body: `upstream-secret`, want: ErrSourcifyInvalidResponse},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := newSourcifyClient(
				SourcifyOptions{BaseURL: server.URL}, server.Client(), nil, true, true,
			)
			if err != nil {
				t.Fatal(err)
			}
			if test.submit {
				request := validVerifyRequest()
				request.SubmitToSourcify = true
				job := durableSourcifyJob(request, 1)
				_, err = client.Submit(context.Background(), sourcifyReader(job), job.ID, true)
			} else {
				_, err = client.Lookup(context.Background(), 1, address)
			}
			if !errors.Is(err, test.want) || strings.Contains(err.Error(), "upstream-secret") {
				t.Fatalf("error=%v, want %v", err, test.want)
			}
		})
	}
}

func TestSourcifyRejectsInvalidAndOversizedResponses(t *testing.T) {
	address := "0x" + strings.Repeat("11", 20)
	invalidVersion := []byte(strings.Replace(
		string(sourcifyContractFixture(address, "0x6001", "0x6001")),
		`"compilerVersion":"0.8.30"`, `"compilerVersion":"latest"`, 1,
	))
	invalidCompiler := []byte(strings.Replace(
		string(sourcifyContractFixture(address, "0x6001", "0x6001")),
		`"compiler":"solc"`, `"compiler":"vyper"`, 1,
	))
	missingRequiredMatch := []byte(strings.Replace(
		string(sourcifyContractFixture(address, "0x6001", "0x6001")),
		`"runtimeMatch":"exact_match",`, "", 1,
	))
	nullRuntimeObject := []byte(strings.Replace(
		string(sourcifyContractFixture(address, "0x6001", "0x6001")),
		`"runtimeBytecode":{"onchainBytecode":"0x6001"}`, `"runtimeBytecode":null`, 1,
	))
	nullRuntimeMember := []byte(strings.Replace(
		string(sourcifyContractFixture(address, "0x6001", "0x6001")),
		`"runtimeBytecode":{"onchainBytecode":"0x6001"}`, `"runtimeBytecode":{"onchainBytecode":null}`, 1,
	))
	nullCompilationObject := []byte(strings.Replace(
		string(sourcifyContractFixture(address, "0x6001", "0x6001")),
		`"compilation":{"language":"Solidity","compiler":"solc","compilerVersion":"0.8.30","fullyQualifiedName":"A.sol:A"}`,
		`"compilation":null`, 1,
	))
	nullCompilationMember := []byte(strings.Replace(
		string(sourcifyContractFixture(address, "0x6001", "0x6001")),
		`"language":"Solidity"`, `"language":null`, 1,
	))
	duplicateIdentity := []byte(strings.Replace(
		string(sourcifyContractFixture(address, "0x6001", "0x6001")),
		`"chainId":"1"`, `"chainId":"1","chainId":"1"`, 1,
	))
	for _, test := range []struct {
		name        string
		contentType string
		body        []byte
		maximum     int64
	}{
		{name: "wrong content type", contentType: "text/html", body: sourcifyContractFixture(address, "0x6001", "0x6001")},
		{name: "malformed JSON", contentType: "application/json", body: []byte(`{"match":`)},
		{name: "duplicate identity", contentType: "application/json", body: duplicateIdentity},
		{name: "mismatched identity", contentType: "application/json", body: sourcifyContractFixture("0x"+strings.Repeat("22", 20), "0x6001", "0x6001")},
		{name: "mutable compiler version", contentType: "application/json", body: invalidVersion},
		{name: "language compiler mismatch", contentType: "application/json", body: invalidCompiler},
		{name: "missing required nullable match", contentType: "application/json", body: missingRequiredMatch},
		{name: "null runtime object", contentType: "application/json", body: nullRuntimeObject},
		{name: "null runtime member", contentType: "application/json", body: nullRuntimeMember},
		{name: "null compilation object", contentType: "application/json", body: nullCompilationObject},
		{name: "null compilation member", contentType: "application/json", body: nullCompilationMember},
		{name: "oversized", contentType: "application/json", body: sourcifyContractFixture(address, "0x6001", "0x6001"), maximum: 64},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				_, _ = w.Write(test.body)
			}))
			defer server.Close()
			options := SourcifyOptions{BaseURL: server.URL, MaxResponseBytes: test.maximum}
			client, err := newSourcifyClient(options, server.Client(), nil, true, true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Lookup(context.Background(), 1, address); !errors.Is(err, ErrSourcifyInvalidResponse) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestSourcifyLookupAcceptsNullableMatchesAndEmptyBytecode(t *testing.T) {
	address := "0x" + strings.Repeat("11", 20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"match":"exact_match","creationMatch":"exact_match","runtimeMatch":null,
			"chainId":"1","address":"` + address + `",
			"creationBytecode":{"onchainBytecode":"0x6001"},
			"runtimeBytecode":{"onchainBytecode":"0x"},
			"compilation":{"language":"Solidity","compiler":"solc","compilerVersion":"0.8.30","fullyQualifiedName":"A.sol:A"},
			"stdJsonInput":{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}},
			"stdJsonOutput":{"contracts":{}},"sources":{},"abi":[],"metadata":{}
		}`))
	}))
	defer server.Close()
	client, err := newSourcifyClient(
		SourcifyOptions{BaseURL: server.URL}, server.Client(), nil, true, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := client.Lookup(context.Background(), 1, address)
	if err != nil || contract.RuntimeMatch != "" || contract.RuntimeBytecode.OnchainBytecode != "0x" {
		t.Fatalf("contract=%+v error=%v", contract, err)
	}
	runtime := []byte{0x60, 0x01}
	_, err = client.Import(context.Background(), VerificationTarget{
		ChainID: 1, Address: address,
		CodeHash:    "0x" + hex.EncodeToString(keccak256Bytes(runtime)),
		AtBlockHash: "0x" + strings.Repeat("33", 32), RuntimeBytecode: "0x6001",
	})
	if !errors.Is(err, ErrSourcifyTargetMismatch) {
		t.Fatalf("creation-only import error=%v", err)
	}
}

func TestSourcifyImportBindsExactLocalTarget(t *testing.T) {
	address := "0x" + strings.Repeat("11", 20)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(sourcifyContractFixture(address, "0x6001", "0x6001"))
	}))
	defer server.Close()
	client, err := newSourcifyClient(
		SourcifyOptions{BaseURL: server.URL}, server.Client(), nil, true, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	runtimeBytecode := []byte{0x60, 0x02}
	target := VerificationTarget{
		ChainID: 1, Address: address,
		CodeHash:         "0x" + hex.EncodeToString(keccak256Bytes(runtimeBytecode)),
		AtBlockHash:      "0x" + strings.Repeat("33", 32),
		CreationBytecode: "0x6001", RuntimeBytecode: "0x6002",
	}
	if _, err := client.Import(context.Background(), target); !errors.Is(err, ErrSourcifyTargetMismatch) {
		t.Fatalf("runtime mismatch error=%v", err)
	}
	target.RuntimeBytecode = "0x6001"
	target.CodeHash = "0x" + hex.EncodeToString(keccak256Bytes([]byte{0x60, 0x01}))
	target.CreationBytecode = "0x6002"
	request, err := client.Import(context.Background(), target)
	if err != nil || request.CreationBytecode != target.CreationBytecode {
		t.Fatalf("creation input must remain local: request=%+v error=%v", request, err)
	}
	before := hits.Load()
	target.CodeHash = "0x12"
	if _, err := client.Import(context.Background(), target); err == nil || err.Error() != "invalid Sourcify import target" {
		t.Fatalf("invalid target error=%v", err)
	}
	if hits.Load() != before {
		t.Fatal("invalid local target reached Sourcify")
	}
}

func TestSourcifySubmitSendsOnlyConsentBoundCompilerInput(t *testing.T) {
	var payload map[string]json.RawMessage
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		defer request.Body.Close()
		_ = json.NewDecoder(request.Body).Decode(&payload)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"verificationId":"00000000-0000-4000-8000-000000000001"}`))
	}))
	defer server.Close()
	client, err := newSourcifyClient(
		SourcifyOptions{BaseURL: server.URL}, server.Client(), nil, true, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	request := validVerifyRequest()
	job := durableSourcifyJob(request, 1)
	if _, err := client.Submit(context.Background(), sourcifyReader(job), job.ID, true); !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("request flag was not required: %v", err)
	}
	request.SubmitToSourcify = true
	job = durableSourcifyJob(request, 1)
	var reads atomic.Int32
	countingReader := sourcifyJobReaderFunc(func(_ context.Context, id string) (VerificationJob, bool, error) {
		reads.Add(1)
		return job, id == job.ID, nil
	})
	if _, err := client.Submit(context.Background(), countingReader, job.ID, false); !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("call-site consent was not required: %v", err)
	}
	if reads.Load() != 0 {
		t.Fatal("submission read the durable request without call-site consent")
	}
	if _, err := client.Submit(context.Background(), countingReader, job.ID, true); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 1 || hits.Load() != 1 {
		t.Fatalf("durable reads=%d Sourcify hits=%d", reads.Load(), hits.Load())
	}
	if len(payload) != 3 || payload["stdJsonInput"] == nil || payload["compilerVersion"] == nil || payload["contractIdentifier"] == nil {
		t.Fatalf("unexpected Sourcify payload keys: %v", payload)
	}
	for _, forbidden := range []string{"code_hash", "at_block_hash", "runtime_bytecode", "submit_to_sourcify"} {
		if _, exists := payload[forbidden]; exists {
			t.Fatalf("Sourcify payload contains %q", forbidden)
		}
	}

	forged := job
	forged.Request.StandardJSON = json.RawMessage(`{"language":"Solidity","sources":{"B.sol":{"content":"secret"}},"settings":{}}`)
	if _, err := client.Submit(context.Background(), sourcifyReader(forged), forged.ID, true); !errors.Is(err, ErrSourcifyRequestMissing) {
		t.Fatalf("forged durable request error=%v", err)
	}
	missingReader := sourcifyJobReaderFunc(func(context.Context, string) (VerificationJob, bool, error) {
		return VerificationJob{}, false, nil
	})
	if _, err := client.Submit(context.Background(), missingReader, job.ID, true); !errors.Is(err, ErrSourcifyRequestMissing) {
		t.Fatalf("missing durable request error=%v", err)
	}
	failingReader := sourcifyJobReaderFunc(func(context.Context, string) (VerificationJob, bool, error) {
		return VerificationJob{}, false, errors.New("database-secret")
	})
	if _, err := client.Submit(context.Background(), failingReader, job.ID, true); !errors.Is(err, ErrSourcifyRequestMissing) || strings.Contains(err.Error(), "database-secret") {
		t.Fatalf("storage failure error=%v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("invalid durable requests reached Sourcify %d times", hits.Load())
	}
}

func TestSourcifyStatusValidatesCurrentV2Shape(t *testing.T) {
	verificationID := "00000000-0000-4000-8000-000000000001"
	address := "0x" + strings.Repeat("11", 20)
	for _, test := range []struct {
		name          string
		response      string
		wantCode      string
		wantCompleted bool
		wantError     bool
	}{
		{
			name:     "pending without optional contract",
			response: `{"isJobCompleted":false,"verificationId":"` + verificationID + `"}`,
		},
		{
			name: "pending",
			response: `{"isJobCompleted":false,"verificationId":"` + verificationID + `",` +
				`"contract":{"match":null,"creationMatch":null,"runtimeMatch":null,"chainId":"1","address":"` + address + `"}}`,
		},
		{
			name: "controlled failure",
			response: `{"isJobCompleted":true,"verificationId":"` + verificationID + `",` +
				`"contract":{"match":null,"creationMatch":null,"runtimeMatch":null,"chainId":"1","address":"` + address + `"},` +
				`"error":{"customCode":"compiler_error","message":"upstream-secret",` +
				`"errorId":"00000000-0000-4000-8000-0000000000aa"}}`,
			wantCode: "compiler_error", wantCompleted: true,
		},
		{
			name: "successful",
			response: `{"isJobCompleted":true,"verificationId":"` + verificationID + `",` +
				`"contract":{"match":"exact_match","creationMatch":"exact_match","runtimeMatch":"exact_match",` +
				`"chainId":"1","address":"` + address + `"}}`,
			wantCompleted: true,
		},
		{
			name: "successful creation only",
			response: `{"isJobCompleted":true,"verificationId":"` + verificationID + `",` +
				`"contract":{"match":"exact_match","creationMatch":"exact_match","runtimeMatch":null,` +
				`"chainId":"1","address":"` + address + `"}}`,
			wantCompleted: true,
		},
		{
			name: "aggregate-only pseudo success",
			response: `{"isJobCompleted":true,"verificationId":"` + verificationID + `",` +
				`"contract":{"match":"exact_match","creationMatch":null,"runtimeMatch":null,` +
				`"chainId":"1","address":"` + address + `"}}`,
			wantCompleted: true,
			wantError:     true,
		},
		{
			name: "mismatched ID",
			response: `{"isJobCompleted":false,"verificationId":"00000000-0000-4000-8000-000000000002",` +
				`"contract":{"match":null,"creationMatch":null,"runtimeMatch":null,"chainId":"1","address":"` + address + `"}}`,
			wantError: true,
		},
		{
			name:      "missing completion flag",
			response:  `{"verificationId":"` + verificationID + `"}`,
			wantError: true,
		},
		{
			name:      "missing verification ID",
			response:  `{"isJobCompleted":false}`,
			wantError: true,
		},
		{
			name: "missing required nullable match",
			response: `{"isJobCompleted":false,"verificationId":"` + verificationID + `",` +
				`"contract":{"creationMatch":null,"runtimeMatch":null,"chainId":"1","address":"` + address + `"}}`,
			wantError: true,
		},
		{
			name: "invalid controlled error",
			response: `{"isJobCompleted":true,"verificationId":"` + verificationID + `",` +
				`"error":{"customCode":"Compiler Error","message":"upstream-secret",` +
				`"errorId":"00000000-0000-4000-8000-0000000000aa"}}`,
			wantError: true,
		},
		{
			name: "pending response with terminal error",
			response: `{"isJobCompleted":false,"verificationId":"` + verificationID + `",` +
				`"error":{"customCode":"compiler_error","message":"upstream-secret",` +
				`"errorId":"00000000-0000-4000-8000-0000000000aa"}}`,
			wantError: true,
		},
		{
			name:          "completed without result",
			response:      `{"isJobCompleted":true,"verificationId":"` + verificationID + `"}`,
			wantCompleted: true,
			wantError:     true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()
			client, err := newSourcifyClient(
				SourcifyOptions{BaseURL: server.URL}, server.Client(), nil, true, true,
			)
			if err != nil {
				t.Fatal(err)
			}
			job, err := client.Status(context.Background(), verificationID)
			if test.wantError {
				if !errors.Is(err, ErrSourcifyInvalidResponse) {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if err != nil || job.ErrorCode != test.wantCode || job.IsJobCompleted != test.wantCompleted ||
				strings.Contains(fmt.Sprintf("%+v", job), "upstream-secret") {
				t.Fatalf("job=%+v error=%v", job, err)
			}
		})
	}
	if client, err := NewSourcifyClient(SourcifyOptions{}); err != nil {
		t.Fatal(err)
	} else if _, err := client.Status(context.Background(), "not-a-uuid"); err == nil || err.Error() != "invalid Sourcify verification ID" {
		t.Fatalf("invalid ID error=%v", err)
	}
}

func TestSourcifyOptionAndRequestBounds(t *testing.T) {
	for _, options := range []SourcifyOptions{
		{Timeout: time.Millisecond},
		{MaxRequestBytes: 64<<20 + 1},
		{MaxResponseBytes: 64<<20 + 1},
	} {
		if _, err := NewSourcifyClient(options); err == nil {
			t.Fatalf("accepted invalid options: %+v", options)
		}
	}
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"verificationId":"00000000-0000-4000-8000-000000000001"}`))
	}))
	defer server.Close()
	client, err := newSourcifyClient(
		SourcifyOptions{BaseURL: server.URL, MaxRequestBytes: 150}, server.Client(), nil, true, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	request := validVerifyRequest()
	request.SubmitToSourcify = true
	job := durableSourcifyJob(request, 1)
	if _, err := client.Submit(context.Background(), sourcifyReader(job), job.ID, true); err == nil || err.Error() != "Sourcify request exceeds its configured bound" {
		t.Fatalf("request bound error=%v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("oversized request reached Sourcify")
	}
}

func sourcifyContractFixture(address, creationBytecode, runtimeBytecode string) []byte {
	return []byte(`{
		"match":"exact_match","creationMatch":"exact_match","runtimeMatch":"exact_match",
		"chainId":"1","address":"` + address + `",
		"creationBytecode":{"onchainBytecode":"` + creationBytecode + `"},
		"runtimeBytecode":{"onchainBytecode":"` + runtimeBytecode + `"},
		"compilation":{"language":"Solidity","compiler":"solc","compilerVersion":"0.8.30","fullyQualifiedName":"A.sol:A"},
		"stdJsonInput":{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}},
		"stdJsonOutput":{"contracts":{}},"sources":{},"abi":[],"metadata":{}
	}`)
}
