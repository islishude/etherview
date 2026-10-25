package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const credentialTestSecret = "etherscan-test-secret-DO-NOT-PERSIST"
const credentialTestRequest = `{"chain_id":"1","address":"0x1111111111111111111111111111111111111111"}`

func testSourcifyCredentials(t *testing.T, pepper string) *SourcifyCredentials {
	t.Helper()
	credentials, err := NewSourcifyCredentials([]byte(strings.Repeat(pepper, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return credentials
}

func TestSourcifyCredentialsAuthenticatedRoundTripAndDeduplication(t *testing.T) {
	credentials := testSourcifyCredentials(t, "p")
	raw, err := credentials.seal(json.RawMessage(credentialTestRequest), SourcifyAPIKey(credentialTestSecret))
	if err != nil {
		t.Fatal(err)
	}
	again, err := credentials.seal(json.RawMessage(credentialTestRequest), SourcifyAPIKey(credentialTestSecret))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(raw, again) || bytes.Contains(raw, []byte(credentialTestSecret)) {
		t.Fatal("credential was not encrypted with a fresh nonce")
	}
	fingerprint := func(raw json.RawMessage) []byte {
		request := SubmissionV2{Kind: JobSourcifyFromEtherscan, SourcifyRequest: raw}
		encoded, _ := json.Marshal(request)
		payload, err := sourcifyDigestPayload(request, encoded)
		if err != nil {
			t.Fatal(err)
		}
		return payload
	}
	if !bytes.Equal(fingerprint(raw), fingerprint(again)) {
		t.Fatal("random ciphertext changed active-job identity")
	}
	different, _ := credentials.seal(json.RawMessage(credentialTestRequest), "another-etherscan-key")
	if bytes.Equal(fingerprint(raw), fingerprint(different)) {
		t.Fatal("different credentials deduplicated")
	}
	decoded, err := decodeSourcifyEtherscanRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	restarted := testSourcifyCredentials(t, "p")
	key, err := restarted.open(decoded)
	if err != nil || key != credentialTestSecret {
		t.Fatal("independent API process could not decrypt credential")
	}
	for _, test := range []string{"wrong key", "address", "chain", "fingerprint", "ciphertext"} {
		t.Run(test, func(t *testing.T) {
			target, _ := decodeSourcifyEtherscanRequest(raw)
			codec := restarted
			switch test {
			case "wrong key":
				codec = testSourcifyCredentials(t, "q")
			case "address":
				target.Address = "0x2222222222222222222222222222222222222222"
			case "chain":
				target.ChainID = "2"
			case "fingerprint":
				target.Credential.Fingerprint = strings.Repeat("f", 64)
			case "ciphertext":
				target.Credential.Ciphertext[0] ^= 1
			}
			key, err := codec.open(target)
			if err == nil || key != "" || strings.Contains(err.Error(), credentialTestSecret) {
				t.Fatal("tampered credential was not rejected safely")
			}
		})
	}
}

type sourcifyCaptureRepository struct {
	verifyMemoryRepository
	stored []SubmissionV2
}

func (r *sourcifyCaptureRepository) SubmitV2(_ context.Context, request SubmissionV2) (VerificationJob, bool, error) {
	r.stored = append(r.stored, request)
	return VerificationJob{Kind: request.Kind, RequestV2: &request}, true, nil
}

func TestSourcifyCredentialsServiceNeverEnqueuesPlaintext(t *testing.T) {
	repository := &sourcifyCaptureRepository{}
	credentials := testSourcifyCredentials(t, "p")
	service, _ := NewService(repository, 5<<20, ServiceOptions{SourcifyCredentials: credentials})
	request := SubmissionV2{Kind: JobSourcifyFromEtherscan, SourcifyRequest: json.RawMessage(credentialTestRequest), SourcifyAPIKey: SourcifyAPIKey(credentialTestSecret)}
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("test", "key", request.SourcifyAPIKey, "request", request)
	for _, output := range []string{fmt.Sprintf("%v %+v %#v", request, request, request), logs.String()} {
		if strings.Contains(output, credentialTestSecret) {
			t.Fatal("transient credential leaked into diagnostic rendering")
		}
	}
	if _, _, err := service.SubmitV2(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(repository.stored) != 1 || repository.stored[0].SourcifyAPIKey != "" {
		t.Fatal("plaintext credential reached repository")
	}
	encoded, _ := json.Marshal(repository.stored[0])
	if bytes.Contains(encoded, []byte(credentialTestSecret)) || bytes.Contains(encoded, []byte("etherscan_api_key")) {
		t.Fatal("plaintext credential reached serialized task")
	}
	decoded, err := decodeSourcifyEtherscanRequest(repository.stored[0].SourcifyRequest)
	if err != nil || decoded.Credential == nil {
		t.Fatal("encrypted credential missing from durable task")
	}
	if _, _, err := service.SubmitV2(context.Background(), repository.stored[0]); err == nil {
		t.Fatal("public service accepted a supplied encrypted envelope")
	}
	noKey, _ := NewService(repository, 5<<20)
	if _, _, err := noKey.SubmitV2(context.Background(), request); err == nil {
		t.Fatal("missing encryption secret accepted")
	}
	for _, bad := range []string{"has space", "has\nnewline", strings.Repeat("x", 257), "密钥"} {
		request.SourcifyAPIKey = SourcifyAPIKey(bad)
		if _, _, err := service.SubmitV2(context.Background(), request); err == nil {
			t.Fatal("invalid credential accepted")
		}
	}
	request.SourcifyAPIKey = ""
	request.SourcifyRequest = json.RawMessage(`{"chain_id":"1","address":"0x1111111111111111111111111111111111111111","etherscan_api_key":"secret"}`)
	if ValidateSourcifyV2Request(request.Kind, request.SourcifyRequest, 5<<20) == nil {
		t.Fatal("plaintext key in durable JSON accepted")
	}
	if len(repository.stored) != 1 {
		t.Fatal("rejected credentials reached repository")
	}
}

func TestSourcifyCredentialsUpstreamRequestAndRedactedErrors(t *testing.T) {
	for _, useKey := range []bool{false, true} {
		t.Run(fmt.Sprint(useKey), func(t *testing.T) {
			credentials := testSourcifyCredentials(t, "p")
			raw := json.RawMessage(credentialTestRequest)
			if useKey {
				var err error
				raw, err = credentials.seal(raw, SourcifyAPIKey(credentialTestSecret))
				if err != nil {
					t.Fatal(err)
				}
			}
			requests := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodPost || r.URL.Path != "/v2/verify/etherscan/1/0x1111111111111111111111111111111111111111" || r.URL.RawQuery != "" || r.Header.Get("X-API-Key") != "" {
					t.Error("credential used outside upstream request body")
				}
				var body map[string]string
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid upstream JSON")
				}
				if useKey && body["apiKey"] != credentialTestSecret {
					t.Error("upstream credential missing")
				}
				if !useKey && len(body) != 0 {
					t.Error("omitted credential was forwarded")
				}
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(credentialTestSecret))
			}))
			defer server.Close()
			client, err := newSourcifyClient(SourcifyOptions{BaseURL: server.URL, Credentials: credentials, Attempts: 1, Timeout: time.Second}, server.Client(), nil, true, true)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.RunV2(context.Background(), JobSourcifyFromEtherscan, raw)
			if err == nil || strings.Contains(err.Error(), credentialTestSecret) || requests != 1 {
				t.Fatal("upstream failure was not redacted")
			}
		})
	}
}

func TestSourcifyCredentialsFailClosedBeforeOutboundRequest(t *testing.T) {
	credentials := testSourcifyCredentials(t, "p")
	raw, err := credentials.seal(json.RawMessage(credentialTestRequest), SourcifyAPIKey(credentialTestSecret))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("invalid credentials must never cause an outbound request")
	}))
	defer server.Close()
	for _, codec := range []*SourcifyCredentials{nil, testSourcifyCredentials(t, "q")} {
		client, err := newSourcifyClient(SourcifyOptions{BaseURL: server.URL, Credentials: codec, Attempts: 1}, server.Client(), nil, true, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.RunV2(t.Context(), JobSourcifyFromEtherscan, raw); err != errSourcifyCredential {
			t.Fatal("credential recovery did not fail closed")
		}
	}
}
