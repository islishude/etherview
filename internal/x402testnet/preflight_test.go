package x402testnet

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckServerRequiresExactFreeConfiguration(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.Header.Get("X-API-Key") != "" ||
			request.Header.Get("Cookie") != "" ||
			request.Header.Get("Payment-Signature") != "" {
			t.Error("preflight sent a credential")
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v1/config":
			_, _ = writer.Write([]byte(`{
				"data":{
					"chain_id":"84532",
					"chain_name":"Base Sepolia",
					"native_symbol":"ETH",
					"native_name":"Ether",
					"native_decimals":18,
					"features":{"x402_billing":true}
				},
				"meta":{"request_id":"test"}
			}`))
		case "/api/v1/billing/config":
			_, _ = writer.Write([]byte(`{
				"data":{
					"enabled":true,
					"x402_version":2,
					"scheme":"exact",
					"network":"eip155:84532",
					"asset":"0x1111111111111111111111111111111111111111",
					"asset_decimals":6,
					"asset_eip712_name":"Test USD",
					"asset_eip712_version":"2",
					"recipient":"0x2222222222222222222222222222222222222222",
					"routes":[{
						"operation":"listBlocks",
						"access":"x402",
						"amount_atomic":"125000"
					}]
				},
				"meta":{"request_id":"test"}
			}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	options := testPreflightOptions(server.URL + "/api/v1/blocks?limit=1")
	if err := checkServer(
		context.Background(), options, server.Client(),
	); err != nil {
		t.Fatalf("checkServer(): %v", err)
	}
}

func TestCheckServerRequiresOnlyOnePricedRoute(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v1/config":
			_, _ = writer.Write([]byte(`{
				"data":{
					"chain_id":"84532",
					"chain_name":"Base Sepolia",
					"native_symbol":"ETH",
					"native_name":"Ether",
					"native_decimals":18,
					"features":{"x402_billing":true}
				},
				"meta":{"request_id":"test"}
			}`))
		case "/api/v1/billing/config":
			_, _ = writer.Write([]byte(`{
				"data":{
					"enabled":true,
					"x402_version":2,
					"scheme":"exact",
					"network":"eip155:84532",
					"asset":"0x1111111111111111111111111111111111111111",
					"asset_decimals":6,
					"asset_eip712_name":"Test USD",
					"asset_eip712_version":"2",
					"recipient":"0x2222222222222222222222222222222222222222",
					"routes":[
						{"operation":"listBlocks","access":"x402","amount_atomic":"125000"},
						{"operation":"getBlock","access":"x402","amount_atomic":"125000"}
					]
				},
				"meta":{"request_id":"test"}
			}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	err := checkServer(
		context.Background(),
		testPreflightOptions(server.URL+"/api/v1/blocks?limit=1"),
		server.Client(),
	)
	if got := ErrorCode(err); got != "preflight_billing_config_mismatch" {
		t.Fatalf("ErrorCode() = %q", got)
	}
}

func TestCheckServerFailsClosedWithoutLeakingHostileResponses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
		header func(http.Header)
	}{
		{name: "redirect", status: http.StatusTemporaryRedirect, body: "secret redirect"},
		{name: "duplicate key", status: http.StatusOK, body: `{"data":{},"data":{}}`},
		{name: "unknown field", status: http.StatusOK, body: `{"secret":"do-not-print"}`},
		{
			name: "payment header", status: http.StatusOK, body: `{}`,
			header: func(header http.Header) {
				header.Set("Payment-Required", "secret-payment")
			},
		},
		{
			name: "oversized", status: http.StatusOK,
			body: `{"padding":"` + strings.Repeat("x", int(maxPreflightBody)) + `"}`,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewTLSServer(http.HandlerFunc(func(
				writer http.ResponseWriter,
				_ *http.Request,
			) {
				writer.Header().Set("Content-Type", "application/json")
				if test.header != nil {
					test.header(writer.Header())
				}
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			err := checkServer(
				context.Background(),
				testPreflightOptions(server.URL+"/api/v1/blocks?limit=1"),
				server.Client(),
			)
			if err == nil {
				t.Fatal("checkServer() succeeded")
			}
			if strings.Contains(err.Error(), "secret") ||
				strings.Contains(err.Error(), test.body) {
				t.Fatalf("error leaked response: %q", err)
			}
		})
	}
}

func TestTargetOriginRejectsCredentialAndPlaintextURLs(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"http://explorer.example/api/v1/blocks",
		"https://user:secret@explorer.example/api/v1/blocks",
		"https://explorer.example/api/v1/blocks#fragment",
		"://invalid",
	} {
		if _, err := targetOrigin(raw); err == nil {
			t.Errorf("targetOrigin(%q) succeeded", raw)
		}
	}
}

func TestProductionPreflightClientIsRestricted(t *testing.T) {
	t.Parallel()
	client := newPreflightHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", client.Transport)
	}
	if transport.Proxy != nil ||
		transport.MaxResponseHeaderBytes != 64<<10 ||
		transport.MaxIdleConns != 8 ||
		transport.MaxIdleConnsPerHost != 2 ||
		transport.MaxConnsPerHost != 4 {
		t.Fatalf("preflight transport is not bounded: %#v", transport)
	}
	if client.Jar != nil || client.CheckRedirect == nil {
		t.Fatal("preflight client configured a Jar or omitted redirect policy")
	}
}

func testPreflightOptions(target string) PreflightOptions {
	return PreflightOptions{
		TargetURL: target, ExpectedOperation: "listBlocks",
		ExpectedAccess:        "x402",
		ExpectedAsset:         "0x1111111111111111111111111111111111111111",
		ExpectedAssetDecimals: 6, ExpectedAssetName: "Test USD",
		ExpectedAssetVersion: "2", ExpectedAmountAtomic: "125000",
		ExpectedRecipient:     "0x2222222222222222222222222222222222222222",
		ExpectedLedgerChainID: baseSepoliaChainID,
	}
}

func TestPreflightErrorBoundaryIsStable(t *testing.T) {
	t.Parallel()
	err := checkServer(
		context.Background(),
		testPreflightOptions("https://127.0.0.1:1/api/v1/blocks"),
		&http.Client{Transport: preflightRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("secret upstream detail")
		})},
	)
	if got := ErrorCode(err); got != "preflight_unavailable" {
		t.Fatalf("ErrorCode() = %q", got)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("error leaked detail: %q", err)
	}
}

type preflightRoundTripFunc func(*http.Request) (*http.Response, error)

func (function preflightRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
