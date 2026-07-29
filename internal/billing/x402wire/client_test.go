package x402wire

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	x402 "github.com/x402-foundation/x402/go/v2"
)

func testClientForServer(
	t *testing.T,
	server *httptest.Server,
	maxResponseBytes int64,
	headers map[string]string,
) *Client {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	client, err := NewClient(ClientOptions{
		BaseURL:          server.URL,
		AllowedCIDRs:     []string{"127.0.0.0/8"},
		Timeout:          2 * time.Second,
		MaxResponseBytes: maxResponseBytes,
		Headers:          headers,
		RootCAs:          pool,
	})
	if err != nil {
		t.Fatalf("NewClient(): %v", err)
	}
	return client
}

func testPaymentAndRequirement(t *testing.T) (Payment, Requirement) {
	t.Helper()
	requirement := testRequirement(t)
	codec, err := NewCodec(DefaultMaxHeaderBytes)
	if err != nil {
		t.Fatalf("NewCodec(): %v", err)
	}
	return decodeTestPayment(t, codec, testSDKPayment(requirement)), requirement
}

func writeJSON(t *testing.T, writer http.ResponseWriter, status int, value any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func TestClientInteroperatesWithFacilitatorV2Wire(t *testing.T) {
	t.Parallel()
	payment, requirement := testPaymentAndRequirement(t)
	var verifyCalls atomic.Int32
	var settleCalls atomic.Int32
	var supportedCalls atomic.Int32

	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer facilitator-secret" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get(PaymentSignatureHeader) != "" {
			t.Errorf("raw payment header escaped to facilitator")
		}
		switch request.URL.Path {
		case "/supported":
			supportedCalls.Add(1)
			if request.Method != http.MethodGet {
				t.Errorf("supported method = %s", request.Method)
			}
			writeJSON(t, writer, http.StatusOK, x402.SupportedResponse{
				Kinds: []x402.SupportedKind{
					{X402Version: 2, Scheme: "exact", Network: "eip155:84532"},
					{X402Version: 2, Scheme: "exact", Network: "solana:devnet"},
				},
				Extensions: []string{"bazaar"},
				Signers:    map[string][]string{},
			})
		case "/verify", "/settle":
			if request.Method != http.MethodPost {
				t.Errorf("%s method = %s", request.URL.Path, request.Method)
			}
			if got := request.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q", got)
			}
			var raw map[string]json.RawMessage
			if err := json.NewDecoder(request.Body).Decode(&raw); err != nil {
				t.Errorf("decode facilitator request: %v", err)
				return
			}
			if len(raw) != 3 {
				t.Errorf("request keys = %v", raw)
			}
			var version int
			if err := json.Unmarshal(raw["x402Version"], &version); err != nil ||
				version != X402Version {
				t.Errorf("x402Version = %d, err = %v", version, err)
			}
			var officialPayload x402.PaymentPayload
			if err := json.Unmarshal(raw["paymentPayload"], &officialPayload); err != nil {
				t.Errorf("official payment payload decode: %v", err)
			}
			var officialRequirement x402.PaymentRequirements
			if err := json.Unmarshal(raw["paymentRequirements"], &officialRequirement); err != nil {
				t.Errorf("official requirement decode: %v", err)
			}
			if officialPayload.Accepted.Network != officialRequirement.Network ||
				officialRequirement.Amount != requirement.SDK().Amount {
				t.Errorf(
					"payload requirement = %#v, explicit requirement = %#v",
					officialPayload.Accepted,
					officialRequirement,
				)
			}

			if request.URL.Path == "/verify" {
				verifyCalls.Add(1)
				writeJSON(t, writer, http.StatusOK, x402.VerifyResponse{
					IsValid: true,
					Payer:   testPayer,
				})
				return
			}
			settleCalls.Add(1)
			writeJSON(t, writer, http.StatusOK, x402.SettleResponse{
				Success:     true,
				Payer:       testPayer,
				Transaction: testTxHash,
				Network:     x402.Network(requirement.SDK().Network),
				Amount:      requirement.SDK().Amount,
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := testClientForServer(
		t,
		server,
		AbsoluteMaxResponseBytes,
		map[string]string{"Authorization": "Bearer facilitator-secret"},
	)
	supported, err := client.GetSupported(context.Background())
	if err != nil {
		t.Fatalf("GetSupported(): %v", err)
	}
	if len(supported.Kinds) != 1 ||
		supported.Kinds[0].Network != requirement.SDK().Network {
		t.Fatalf("supported = %#v", supported)
	}
	if err := client.CheckSupported(
		context.Background(),
		requirement.SDK().Network,
	); err != nil {
		t.Fatalf("CheckSupported(configured network): %v", err)
	}
	err = client.CheckSupported(context.Background(), "eip155:1")
	var unsupported *BoundaryError
	if !errors.As(err, &unsupported) ||
		unsupported.Code != CodeFacilitatorUnsupported ||
		unsupported.Class != FailureUnavailable {
		t.Fatalf("CheckSupported(other network) error = %#v", err)
	}
	err = client.CheckSupported(context.Background(), "eip155:01")
	var invalidNetwork *BoundaryError
	if !errors.As(err, &invalidNetwork) ||
		invalidNetwork.Code != CodeFacilitatorConfigInvalid ||
		invalidNetwork.Class != FailureInvalid {
		t.Fatalf("CheckSupported(invalid network) error = %#v", err)
	}
	verified, err := client.VerifyPayment(context.Background(), payment, requirement)
	if err != nil {
		t.Fatalf("VerifyPayment(): %v", err)
	}
	if !verified.IsValid || verified.Payer != testPayer {
		t.Fatalf("verified = %#v", verified)
	}
	settled, err := client.SettlePayment(context.Background(), payment, requirement)
	if err != nil {
		t.Fatalf("SettlePayment(): %v", err)
	}
	if !settled.Success || settled.Payer != testPayer ||
		settled.Transaction != testTxHash ||
		settled.Amount != requirement.SDK().Amount {
		t.Fatalf("settled = %#v", settled)
	}
	if supportedCalls.Load() != 3 || verifyCalls.Load() != 1 || settleCalls.Load() != 1 {
		t.Fatalf(
			"calls supported=%d verify=%d settle=%d",
			supportedCalls.Load(),
			verifyCalls.Load(),
			settleCalls.Load(),
		)
	}
	if got, want := client.OriginDigest(), sha256.Sum256([]byte(server.URL)); got != want {
		t.Fatalf("OriginDigest() = %x, want %x", got, want)
	}
}

func TestClientRawInterfaceRejectsAuthorizationMismatchBeforeNetwork(t *testing.T) {
	t.Parallel()
	requirement := testRequirement(t)
	sdkPayment := testSDKPayment(requirement)
	sdkPayment.Payload["authorization"].(map[string]any)["value"] = "125001"
	payload := paymentJSON(t, sdkPayment)
	client := &Client{}

	for _, test := range []struct {
		name string
		call func() error
	}{
		{
			name: "verify",
			call: func() error {
				_, err := client.Verify(context.Background(), payload, requirement.JSON())
				return err
			},
		},
		{
			name: "settle",
			call: func() error {
				_, err := client.Settle(context.Background(), payload, requirement.JSON())
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.call()
			var boundary *BoundaryError
			if !errors.As(err, &boundary) ||
				boundary.Phase != PhaseHeader ||
				boundary.Class != FailureInvalid ||
				boundary.Code != CodePaymentMismatch {
				t.Fatalf("error = %#v", err)
			}
		})
	}
}

func TestClientRejectsUnsafeConfiguration(t *testing.T) {
	t.Parallel()
	valid := ClientOptions{
		BaseURL:          "https://facilitator.example",
		AllowedCIDRs:     []string{"203.0.113.0/24"},
		Timeout:          time.Second,
		MaxResponseBytes: 1024,
		Headers:          map[string]string{"Authorization": "Bearer secret"},
	}
	tests := []struct {
		name   string
		mutate func(*ClientOptions)
	}{
		{
			name: "HTTP origin",
			mutate: func(value *ClientOptions) {
				value.BaseURL = "http://facilitator.example"
			},
		},
		{
			name: "origin credentials",
			mutate: func(value *ClientOptions) {
				value.BaseURL = "https://user:pass@facilitator.example"
			},
		},
		{
			name: "origin path",
			mutate: func(value *ClientOptions) {
				value.BaseURL = "https://facilitator.example/api"
			},
		},
		{
			name: "origin query",
			mutate: func(value *ClientOptions) {
				value.BaseURL = "https://facilitator.example/?key=secret"
			},
		},
		{
			name: "origin fragment",
			mutate: func(value *ClientOptions) {
				value.BaseURL = "https://facilitator.example/#fragment"
			},
		},
		{
			name: "missing CIDRs",
			mutate: func(value *ClientOptions) {
				value.AllowedCIDRs = nil
			},
		},
		{
			name: "noncanonical CIDR",
			mutate: func(value *ClientOptions) {
				value.AllowedCIDRs = []string{"203.0.113.1/24"}
			},
		},
		{
			name: "duplicate CIDR",
			mutate: func(value *ClientOptions) {
				value.AllowedCIDRs = []string{"203.0.113.0/24", "203.0.113.0/24"}
			},
		},
		{
			name: "zero timeout",
			mutate: func(value *ClientOptions) {
				value.Timeout = 0
			},
		},
		{
			name: "too short timeout",
			mutate: func(value *ClientOptions) {
				value.Timeout = minimumFacilitatorTimeout - time.Millisecond
			},
		},
		{
			name: "too long timeout",
			mutate: func(value *ClientOptions) {
				value.Timeout = maximumFacilitatorTimeout + time.Millisecond
			},
		},
		{
			name: "oversized response budget",
			mutate: func(value *ClientOptions) {
				value.MaxResponseBytes = AbsoluteMaxResponseBytes + 1
			},
		},
		{
			name: "noncanonical header",
			mutate: func(value *ClientOptions) {
				value.Headers = map[string]string{"authorization": "Bearer secret"}
			},
		},
		{
			name: "raw payment header",
			mutate: func(value *ClientOptions) {
				value.Headers = map[string]string{PaymentSignatureHeader: "secret"}
			},
		},
		{
			name: "proxy credential header",
			mutate: func(value *ClientOptions) {
				value.Headers = map[string]string{"Proxy-Authorization": "secret"}
			},
		},
		{
			name: "host header",
			mutate: func(value *ClientOptions) {
				value.Headers = map[string]string{"Host": "other.example"}
			},
		},
		{
			name: "header newline",
			mutate: func(value *ClientOptions) {
				value.Headers = map[string]string{"Authorization": "secret\r\nX-Evil: yes"}
			},
		},
		{
			name: "empty header",
			mutate: func(value *ClientOptions) {
				value.Headers = map[string]string{"Authorization": ""}
			},
		},
		{
			name: "too many headers",
			mutate: func(value *ClientOptions) {
				value.Headers = make(map[string]string, maximumFacilitatorHeaders+1)
				for index := range maximumFacilitatorHeaders + 1 {
					value.Headers["X-Test-"+string(rune('A'+index))] = "opaque"
				}
			},
		},
		{
			name: "oversized headers",
			mutate: func(value *ClientOptions) {
				value.Headers = map[string]string{
					"Authorization": strings.Repeat(
						"x",
						maximumFacilitatorHeaderBytes,
					),
				}
			},
		},
		{
			name: "zoned IPv6 origin",
			mutate: func(value *ClientOptions) {
				value.BaseURL = "https://[fe80::1%25en0]"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := valid
			options.AllowedCIDRs = append([]string(nil), valid.AllowedCIDRs...)
			options.Headers = map[string]string{"Authorization": "Bearer secret"}
			test.mutate(&options)
			_, err := NewClient(options)
			var boundary *BoundaryError
			if !errors.As(err, &boundary) ||
				boundary.Code != CodeFacilitatorConfigInvalid ||
				boundary.Class != FailureInvalid {
				t.Fatalf("NewClient() error = %#v", err)
			}
		})
	}
}

type staticResolver struct {
	addresses []net.IPAddr
	err       error
	calls     atomic.Int32
}

func (r *staticResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	r.calls.Add(1)
	return append([]net.IPAddr(nil), r.addresses...), r.err
}

type countingDialer struct {
	calls atomic.Int32
}

func (d *countingDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	d.calls.Add(1)
	return nil, errors.New("test dial refused")
}

func TestClientRequiresEveryDNSAnswerInsideAllowedCIDRs(t *testing.T) {
	t.Parallel()
	resolver := &staticResolver{addresses: []net.IPAddr{
		{IP: net.ParseIP("127.0.0.1")},
		{IP: net.ParseIP("203.0.113.7")},
	}}
	dialer := &countingDialer{}
	client, err := NewClient(ClientOptions{
		BaseURL:          "https://facilitator.example",
		AllowedCIDRs:     []string{"127.0.0.0/8"},
		Timeout:          time.Second,
		MaxResponseBytes: 1024,
		Resolver:         resolver,
		Dialer:           dialer,
	})
	if err != nil {
		t.Fatalf("NewClient(): %v", err)
	}
	_, err = client.GetSupported(context.Background())
	if !IsFailure(err, FailureUnavailable) {
		t.Fatalf("GetSupported() error = %#v", err)
	}
	if resolver.calls.Load() != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.calls.Load())
	}
	if dialer.calls.Load() != 0 {
		t.Fatalf("dialer calls = %d, want 0", dialer.calls.Load())
	}
}

func TestClientValidatesAllowedAddressBeforeEveryDial(t *testing.T) {
	t.Parallel()
	resolver := &staticResolver{addresses: []net.IPAddr{
		{IP: net.ParseIP("127.0.0.1")},
	}}
	dialer := &countingDialer{}
	client, err := NewClient(ClientOptions{
		BaseURL:          "https://facilitator.example",
		AllowedCIDRs:     []string{"127.0.0.0/8"},
		Timeout:          time.Second,
		MaxResponseBytes: 1024,
		Resolver:         resolver,
		Dialer:           dialer,
	})
	if err != nil {
		t.Fatalf("NewClient(): %v", err)
	}
	for range 2 {
		_, err := client.GetSupported(context.Background())
		if !IsFailure(err, FailureUnavailable) {
			t.Fatalf("GetSupported() error = %#v", err)
		}
	}
	if resolver.calls.Load() != 2 || dialer.calls.Load() != 2 {
		t.Fatalf(
			"resolver calls = %d, dialer calls = %d, want 2 each",
			resolver.calls.Load(),
			dialer.calls.Load(),
		)
	}
}

func TestClientIgnoresEnvironmentProxy(t *testing.T) {
	var proxyCalls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		proxyCalls.Add(1)
		http.Error(writer, "proxy must not be used", http.StatusBadGateway)
	}))
	defer proxy.Close()
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("https_proxy", proxy.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(t, writer, http.StatusOK, x402.SupportedResponse{
			Kinds: []x402.SupportedKind{
				{X402Version: 2, Scheme: "exact", Network: "eip155:1"},
			},
			Extensions: []string{},
			Signers:    map[string][]string{},
		})
	}))
	defer server.Close()
	client := testClientForServer(t, server, 4096, nil)
	if _, err := client.GetSupported(context.Background()); err != nil {
		t.Fatalf("GetSupported(): %v", err)
	}
	if proxyCalls.Load() != 0 {
		t.Fatalf("environment proxy received %d requests", proxyCalls.Load())
	}
}

func TestClientRejectsRedirectWithoutFollowingIt(t *testing.T) {
	t.Parallel()
	payment, requirement := testPaymentAndRequirement(t)
	var settleCalls atomic.Int32
	var targetCalls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/settle":
			settleCalls.Add(1)
			http.Redirect(writer, request, "/target", http.StatusTemporaryRedirect)
		case "/target":
			targetCalls.Add(1)
			writeJSON(t, writer, http.StatusOK, x402.SettleResponse{
				Success:     true,
				Payer:       testPayer,
				Transaction: testTxHash,
				Network:     x402.Network(requirement.SDK().Network),
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := testClientForServer(t, server, 4096, nil)

	_, err := client.SettlePayment(context.Background(), payment, requirement)
	if !IsFailure(err, FailureSettlementUnknown) ||
		err.Error() != CodeSettlementUnknown {
		t.Fatalf("SettlePayment() error = %#v", err)
	}
	if settleCalls.Load() != 1 || targetCalls.Load() != 0 {
		t.Fatalf(
			"settle calls = %d, target calls = %d",
			settleCalls.Load(),
			targetCalls.Load(),
		)
	}
}

func TestClientBoundsAndRedactsHostileResponses(t *testing.T) {
	t.Parallel()
	payment, requirement := testPaymentAndRequirement(t)
	const hostile = "TOP-SECRET-HOSTILE-REMOTE-BODY"
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(
			writer,
			`{"hostile":"`+strings.Repeat(hostile, 32)+`"}`,
		)
	}))
	defer server.Close()
	client := testClientForServer(t, server, 128, nil)

	_, err := client.SettlePayment(context.Background(), payment, requirement)
	if !IsFailure(err, FailureSettlementUnknown) ||
		strings.Contains(err.Error(), hostile) {
		t.Fatalf("SettlePayment() error = %#v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("settle calls = %d, want exactly 1", calls.Load())
	}
}

func TestClientStrictlyRejectsAmbiguousFacilitatorJSON(t *testing.T) {
	t.Parallel()
	payment, requirement := testPaymentAndRequirement(t)
	deep := strings.Repeat(`{"nested":`, facilitatorJSONLimits.MaxDepth+1) +
		`null` +
		strings.Repeat(`}`, facilitatorJSONLimits.MaxDepth+1)
	tests := []struct {
		name string
		body []byte
	}{
		{
			name: "duplicate keys",
			body: []byte(
				`{"isValid":true,"isValid":true,"payer":"` + testPayer + `"}`,
			),
		},
		{
			name: "trailing JSON",
			body: []byte(
				`{"isValid":true,"payer":"` + testPayer + `"} {}`,
			),
		},
		{
			name: "over-depth JSON",
			body: []byte(deep),
		},
		{
			name: "unsafe JSON number",
			body: []byte(`{"isValid":true,"value":9007199254740992}`),
		},
		{
			name: "invalid UTF-8",
			body: []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewTLSServer(http.HandlerFunc(
				func(writer http.ResponseWriter, _ *http.Request) {
					writer.Header().Set("Content-Type", "application/json")
					_, _ = writer.Write(test.body)
				},
			))
			defer server.Close()
			client := testClientForServer(t, server, 4096, nil)
			_, err := client.VerifyPayment(
				context.Background(),
				payment,
				requirement,
			)
			var boundary *BoundaryError
			if !errors.As(err, &boundary) ||
				boundary.Class != FailureUnavailable ||
				boundary.Code != CodeFacilitatorResponseInvalid {
				t.Fatalf("VerifyPayment() error = %#v", err)
			}
		})
	}
}

func TestClientTotalTimeoutUsesStablePhaseSemantics(t *testing.T) {
	t.Parallel()
	payment, requirement := testPaymentAndRequirement(t)
	server := httptest.NewTLSServer(http.HandlerFunc(
		func(_ http.ResponseWriter, request *http.Request) {
			select {
			case <-request.Context().Done():
			case <-time.After(time.Second):
			}
		},
	))
	defer server.Close()

	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	client, err := NewClient(ClientOptions{
		BaseURL:          server.URL,
		AllowedCIDRs:     []string{"127.0.0.0/8"},
		Timeout:          minimumFacilitatorTimeout,
		MaxResponseBytes: 4096,
		RootCAs:          pool,
	})
	if err != nil {
		t.Fatalf("NewClient(): %v", err)
	}
	if _, err := client.VerifyPayment(
		context.Background(),
		payment,
		requirement,
	); !IsFailure(err, FailureUnavailable) {
		t.Fatalf("VerifyPayment() timeout error = %#v", err)
	}
	if _, err := client.SettlePayment(
		context.Background(),
		payment,
		requirement,
	); !IsFailure(err, FailureSettlementUnknown) {
		t.Fatalf("SettlePayment() timeout error = %#v", err)
	}
}

func TestClientSupportedRequiresAnExactEVMKind(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(
		func(writer http.ResponseWriter, _ *http.Request) {
			writeJSON(t, writer, http.StatusOK, x402.SupportedResponse{
				Kinds: []x402.SupportedKind{
					{X402Version: 2, Scheme: "exact", Network: "solana:devnet"},
				},
				Extensions: []string{},
				Signers:    map[string][]string{},
			})
		},
	))
	defer server.Close()
	client := testClientForServer(t, server, 4096, nil)
	err := client.CheckSupported(context.Background(), "eip155:84532")
	var boundary *BoundaryError
	if !errors.As(err, &boundary) ||
		boundary.Class != FailureUnavailable ||
		boundary.Code != CodeFacilitatorUnsupported {
		t.Fatalf("CheckSupported() error = %#v", err)
	}
}

func TestClientSettlementFailureClassification(t *testing.T) {
	t.Parallel()
	payment, requirement := testPaymentAndRequirement(t)
	tests := []struct {
		name      string
		status    int
		body      string
		wantClass FailureClass
	}{
		{
			name:   "definite facilitator rejection",
			status: http.StatusBadRequest,
			body: `{"success":false,"errorReason":"insufficient_funds",` +
				`"transaction":"","network":"eip155:84532"}`,
			wantClass: FailureRejected,
		},
		{
			name:      "failed response missing required fields",
			status:    http.StatusBadRequest,
			body:      `{"success":false,"errorReason":"insufficient_funds"}`,
			wantClass: FailureSettlementUnknown,
		},
		{
			name:      "server error",
			status:    http.StatusInternalServerError,
			body:      `{"error":"temporary"}`,
			wantClass: FailureSettlementUnknown,
		},
		{
			name:      "malformed response",
			status:    http.StatusOK,
			body:      `{"success":`,
			wantClass: FailureSettlementUnknown,
		},
		{
			name:   "failed response with transaction",
			status: http.StatusOK,
			body: `{"success":false,"errorReason":"failed",` +
				`"transaction":"` + testTxHash + `"}`,
			wantClass: FailureSettlementUnknown,
		},
		{
			name:   "wrong payer",
			status: http.StatusOK,
			body: `{"success":true,"payer":"` + testRecipient + `",` +
				`"transaction":"` + testTxHash + `",` +
				`"network":"eip155:84532","amount":"125000"}`,
			wantClass: FailureSettlementUnknown,
		},
		{
			name:   "wrong network",
			status: http.StatusOK,
			body: `{"success":true,"payer":"` + testPayer + `",` +
				`"transaction":"` + testTxHash + `",` +
				`"network":"eip155:1","amount":"125000"}`,
			wantClass: FailureSettlementUnknown,
		},
		{
			name:   "wrong amount",
			status: http.StatusOK,
			body: `{"success":true,"payer":"` + testPayer + `",` +
				`"transaction":"` + testTxHash + `",` +
				`"network":"eip155:84532","amount":"125001"}`,
			wantClass: FailureSettlementUnknown,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()
			client := testClientForServer(t, server, 4096, nil)
			response, err := client.SettlePayment(context.Background(), payment, requirement)
			if !IsFailure(err, test.wantClass) {
				t.Fatalf("SettlePayment() response = %#v, error = %#v", response, err)
			}
			if calls.Load() != 1 {
				t.Fatalf("settle calls = %d, want 1", calls.Load())
			}
			if strings.Contains(err.Error(), "insufficient_funds") ||
				strings.Contains(err.Error(), "temporary") {
				t.Fatalf("error leaked response: %q", err)
			}
		})
	}
}

func TestClientVerificationFailureClassification(t *testing.T) {
	t.Parallel()
	payment, requirement := testPaymentAndRequirement(t)
	tests := []struct {
		name      string
		status    int
		body      string
		wantClass FailureClass
	}{
		{
			name:      "payment rejected",
			status:    http.StatusOK,
			body:      `{"isValid":false,"invalidReason":"invalid_signature"}`,
			wantClass: FailureRejected,
		},
		{
			name:      "server error",
			status:    http.StatusInternalServerError,
			body:      `{"error":"unavailable"}`,
			wantClass: FailureUnavailable,
		},
		{
			name:      "malformed response",
			status:    http.StatusOK,
			body:      `{"isValid":`,
			wantClass: FailureUnavailable,
		},
		{
			name:   "wrong payer",
			status: http.StatusOK,
			body: `{"isValid":true,"payer":"` +
				testRecipient + `"}`,
			wantClass: FailureUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()
			client := testClientForServer(t, server, 4096, nil)
			response, err := client.VerifyPayment(context.Background(), payment, requirement)
			if !IsFailure(err, test.wantClass) {
				t.Fatalf("VerifyPayment() response = %#v, error = %#v", response, err)
			}
			if response != nil && response.InvalidReason != CodeFacilitatorRejected {
				t.Fatalf("response leaked remote reason: %#v", response)
			}
		})
	}
}

func TestClientTreatsTLSFailureAsUnavailable(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(t, writer, http.StatusOK, x402.SupportedResponse{})
	}))
	defer server.Close()
	client, err := NewClient(ClientOptions{
		BaseURL:          server.URL,
		AllowedCIDRs:     []string{"127.0.0.0/8"},
		Timeout:          time.Second,
		MaxResponseBytes: 4096,
		RootCAs:          x509.NewCertPool(),
	})
	if err != nil {
		t.Fatalf("NewClient(): %v", err)
	}
	if _, err := client.GetSupported(context.Background()); !IsFailure(err, FailureUnavailable) {
		t.Fatalf("GetSupported() error = %#v", err)
	}
}
