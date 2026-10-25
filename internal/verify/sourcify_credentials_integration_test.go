//go:build integration

package verify

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSourcifyCredentialsPostgresWorkerRestart(t *testing.T) {
	db := bytecodeDBPostgres(t)
	repository, err := NewPostgresRepository(db, RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository, 5<<20, ServiceOptions{SourcifyCredentials: testSourcifyCredentials(t, "p")})
	if err != nil {
		t.Fatal(err)
	}
	request := SubmissionV2{Kind: JobSourcifyFromEtherscan, SourcifyRequest: json.RawMessage(credentialTestRequest), SourcifyAPIKey: SourcifyAPIKey(credentialTestSecret)}
	job, created, err := service.SubmitV2(t.Context(), request)
	if err != nil || !created {
		t.Fatalf("enqueue: created=%t err=%v", created, err)
	}
	again, created, err := service.SubmitV2(t.Context(), request)
	if err != nil || created || again.ID != job.ID {
		t.Fatalf("deduplication: created=%t err=%v", created, err)
	}
	// Changing a credential must not attach the caller to an existing job.
	request.SourcifyAPIKey = "another-etherscan-key"
	different, created, err := service.SubmitV2(t.Context(), request)
	if err != nil || !created || different.ID == job.ID {
		t.Fatalf("new credential: created=%t err=%v", created, err)
	}
	var document string
	var payload []byte
	if err := db.QueryRow(t.Context(), `SELECT request::text, request_payload FROM verification_jobs WHERE id=$1`, job.ID).Scan(&document, &payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(document, credentialTestSecret) || bytes.Contains(payload, []byte(credentialTestSecret)) || !strings.Contains(document, "ciphertext") {
		t.Fatal("durable job did not contain only encrypted credentials")
	}
	const verificationID = "123e4567-e89b-42d3-a456-426614174000"
	var imports atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["apiKey"] != credentialTestSecret || r.URL.RawQuery != "" {
				t.Error("worker did not forward the recovered credential in the request body")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			imports.Add(1)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"verificationId":"` + verificationID + `"}`))
			return
		}
		_, _ = w.Write([]byte(`{"isJobCompleted":true,"verificationId":"` + verificationID + `","contract":{"match":"exact_match","creationMatch":"exact_match","runtimeMatch":"exact_match","chainId":"1","address":"0x1111111111111111111111111111111111111111"}}`))
	}))
	defer server.Close()
	// Recreate repository and codec to exercise a different API process/restart.
	restarted, err := NewPostgresRepository(db, RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client, err := newSourcifyClient(SourcifyOptions{BaseURL: server.URL, Credentials: testSourcifyCredentials(t, "p")}, server.Client(), nil, true, true)
	if err != nil {
		t.Fatal(err)
	}
	compiler := &verifyTestCompiler{provenance: testSolcJSProvenance()}
	worker, err := NewWorker(restarted, compiler, WorkerOptions{WorkerID: "credential-restart", Sourcify: client})
	if err != nil {
		t.Fatal(err)
	}
	found, err := worker.ProcessOne(t.Context())
	if err != nil || !found {
		t.Fatalf("process: found=%t err=%v", found, err)
	}
	completed, found, err := restarted.Job(t.Context(), job.ID)
	if err != nil || !found || completed.Status != JobSucceeded || !bytes.Contains(completed.Outcome, []byte("sourcify_success")) || imports.Load() != 1 || compiler.compileCalls != 0 {
		t.Fatalf("external workflow did not complete: found=%t status=%s err=%v", found, completed.Status, err)
	}
	var localResults int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM verified_contracts`).Scan(&localResults); err != nil {
		t.Fatal(err)
	}
	if localResults != 0 {
		t.Fatal("Sourcify result was published as a local verification")
	}

}
