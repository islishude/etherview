package facilitator

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	x402 "github.com/x402-foundation/x402/go/v2"
)

func testConfig() Config {
	return Config{Listen: "127.0.0.1:8081", DatabaseURLFile: "db", BearerTokenFile: "token", UnsafeLocalHTTP: true, Signers: map[string]SignerConfig{"key": {Type: "keystore", Address: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8", KeystoreFile: "key", PasswordFile: "pass"}}, Chains: []ChainConfig{{Network: "eip155:31337", RPCURLFile: "rpc", Signer: "key", MaxGas: 500000, MaxFeePerGas: "100000000000", Confirmations: 1, Recipients: []string{"0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC"}, Tokens: []TokenConfig{{Address: "0x5FbDB2315678afecb367f032d93F642f64180aa3", Methods: []string{"eip3009", "permit2"}}}}}}
}
func TestConfigValidation(t *testing.T) {
	if err := testConfig().Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Config){"duplicate chain": func(c *Config) { c.Chains = append(c.Chains, c.Chains[0]) }, "duplicate token": func(c *Config) { c.Chains[0].Tokens = append(c.Chains[0].Tokens, c.Chains[0].Tokens[0]) }, "unknown signer": func(c *Config) { c.Chains[0].Signer = "missing" }, "network": func(c *Config) { c.Chains[0].Network = "eip155:031337" }, "fee": func(c *Config) { c.Chains[0].MaxFeePerGas = "0" }, "confirmations": func(c *Config) { c.Chains[0].Confirmations = 0 }, "TLS": func(c *Config) { c.UnsafeLocalHTTP = false }, "recipient": func(c *Config) { c.Chains[0].Recipients = nil }, "method": func(c *Config) { c.Chains[0].Tokens[0].Methods = []string{"arbitrary"} }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			c := testConfig()
			change(&c)
			if c.Validate() == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
}
func testRequest() paymentRequest {
	cfg := testConfig().Chains[0]
	req := x402.PaymentRequirements{Scheme: "exact", Network: cfg.Network, Asset: cfg.Tokens[0].Address, Amount: "100", PayTo: cfg.Recipients[0], MaxTimeoutSeconds: 60, Extra: map[string]any{"assetTransferMethod": "eip3009", "paymentFlow": "authorization"}}
	return paymentRequest{X402Version: 2, PaymentRequirements: req, PaymentPayload: x402.PaymentPayload{X402Version: 2, Accepted: req, Payload: map[string]any{"signature": "0x" + strings.Repeat("11", 65), "authorization": map[string]any{"from": "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266", "to": req.PayTo, "value": "100", "nonce": "0x" + strings.Repeat("01", 32), "validAfter": "0", "validBefore": "9999999999"}}}}
}
func TestNetworkAndAssetRouting(t *testing.T) {
	cfg := testConfig()
	second := cfg.Chains[0]
	second.Network = "eip155:48815"
	cfg.Chains = append(cfg.Chains, second)
	s := &Service{config: cfg, chains: map[string]*chainRuntime{}}
	for _, ch := range cfg.Chains {
		s.chains[ch.Network] = &chainRuntime{adapter: &chainClient{config: ch}}
	}
	ids := []string{}
	for _, ch := range cfg.Chains {
		in := testRequest()
		in.PaymentRequirements.Network = ch.Network
		in.PaymentPayload.Accepted = in.PaymentRequirements
		b, _ := json.Marshal(in)
		_, selected, id, _, err := s.decode(httptest.NewRequest("POST", "/settle", bytes.NewReader(b)))
		if err != nil || selected != s.chains[ch.Network] {
			t.Fatal("wrong chain route", err)
		}
		ids = append(ids, id)
	}
	if ids[0] == ids[1] {
		t.Fatal("cross-chain identity collision")
	}
	for _, change := range []func(*paymentRequest){func(p *paymentRequest) { p.PaymentRequirements.Network = "eip155:1" }, func(p *paymentRequest) {
		p.PaymentRequirements.PayTo = cfg.Signers["key"].Address
		p.PaymentPayload.Accepted = p.PaymentRequirements
	}, func(p *paymentRequest) {
		p.PaymentRequirements.Asset = cfg.Signers["key"].Address
		p.PaymentPayload.Accepted = p.PaymentRequirements
	}, func(p *paymentRequest) { p.PaymentPayload.Accepted.Amount = "101" }} {
		in := testRequest()
		change(&in)
		b, _ := json.Marshal(in)
		if _, _, _, _, err := s.decode(httptest.NewRequest("POST", "/settle", bytes.NewReader(b))); err == nil {
			t.Fatal("accepted hostile request")
		}
	}
}
func TestAuthenticationAndBodyLimit(t *testing.T) {
	s := &Service{token: []byte(strings.Repeat("t", 32)), chains: map[string]*chainRuntime{}}
	for _, path := range []string{"/supported", "/metrics", "/verify", "/settle"} {
		method := "GET"
		if path == "/verify" || path == "/settle" {
			method = "POST"
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(method, path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s accepted unauthenticated request", path)
		}
	}
	if _, _, _, _, err := s.decode(httptest.NewRequest("POST", "/settle", strings.NewReader(strings.Repeat("x", (1<<20)+1)))); err == nil {
		t.Fatal("accepted oversized request")
	}
}

func TestPermit2NonceIdentitySpansTokens(t *testing.T) {
	cfg := testConfig().Chains[0]
	cfg.Tokens = append(cfg.Tokens, TokenConfig{Address: "0x1111111111111111111111111111111111111111", Methods: []string{"permit2"}})
	s := &Service{chains: map[string]*chainRuntime{cfg.Network: {adapter: &chainClient{config: cfg}}}}
	ids := []string{}
	for _, token := range cfg.Tokens {
		request := testRequest()
		request.PaymentRequirements.Asset = token.Address
		request.PaymentRequirements.Extra["assetTransferMethod"] = "permit2"
		request.PaymentPayload.Accepted = request.PaymentRequirements
		request.PaymentPayload.Payload = map[string]any{
			"signature":            "0x" + strings.Repeat("11", 65),
			"permit2Authorization": map[string]any{"from": "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266", "spender": "0x402085c248EeA27D92E8b30b2C58ed07f9E20001", "nonce": "1", "deadline": "9999999999", "permitted": map[string]any{"token": token.Address, "amount": "100"}, "witness": map[string]any{"to": cfg.Recipients[0], "validAfter": "0"}},
		}
		raw, _ := json.Marshal(request)
		_, _, id, _, err := s.decode(httptest.NewRequest("POST", "/settle", bytes.NewReader(raw)))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if ids[0] != ids[1] {
		t.Fatal("Permit2 nonce was incorrectly scoped to the asset")
	}
}

func TestRejectMismatchedAuthorizationMethod(t *testing.T) {
	cfg := testConfig().Chains[0]
	s := &Service{chains: map[string]*chainRuntime{cfg.Network: {adapter: &chainClient{config: cfg}}}}
	for _, method := range []string{"eip3009", "permit2"} {
		for _, shape := range []string{"opposite", "mixed", "missing"} {
			t.Run(method+"/"+shape, func(t *testing.T) {
				in := testRequest()
				in.PaymentRequirements.Extra["assetTransferMethod"] = method
				in.PaymentPayload.Accepted = in.PaymentRequirements
				payload := in.PaymentPayload.Payload
				permit := map[string]any{"from": "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266", "spender": "0x402085c248EeA27D92E8b30b2C58ed07f9E20001", "nonce": "1", "deadline": "9999999999", "permitted": map[string]any{"token": cfg.Tokens[0].Address, "amount": "100"}, "witness": map[string]any{"to": cfg.Recipients[0], "validAfter": "0"}}
				if shape == "mixed" || method == "eip3009" && shape == "opposite" {
					payload["permit2Authorization"] = permit
				}
				if shape == "missing" || method == "eip3009" && shape == "opposite" {
					delete(payload, "authorization")
				}
				raw, err := json.Marshal(in)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, _, _, err := s.decode(httptest.NewRequest("POST", "/settle", bytes.NewReader(raw))); err == nil {
					t.Fatal("accepted ambiguous authorization method")
				}
			})
		}
	}
}
