package x402wire

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"reflect"
	"strings"
	"testing"

	x402 "github.com/x402-foundation/x402/go/v2"
	x402http "github.com/x402-foundation/x402/go/v2/http"
	"github.com/x402-foundation/x402/go/v2/mechanisms/evm"
)

const (
	testPayer     = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testRecipient = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testAsset     = "0xcccccccccccccccccccccccccccccccccccccccc"
	testNonce     = "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	testSignature = "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	testTxHash    = "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
)

func testRequirement(t *testing.T) Requirement {
	t.Helper()
	requirement, err := NewRequirement(RequirementOptions{
		Network:            "eip155:84532",
		Asset:              testAsset,
		Amount:             "125000",
		PayTo:              testRecipient,
		MaxTimeoutSeconds:  300,
		AssetEIP712Name:    "USD Coin",
		AssetEIP712Version: "2",
		Resource: x402.ResourceInfo{
			URL:         "https://explorer.example/api/v1/blocks/123?include=transactions",
			Description: "Block details",
			MimeType:    "application/json",
			ServiceName: "Etherview",
			Tags:        []string{"explorer", "block"},
			IconUrl:     "https://explorer.example/assets/icon.png",
		},
	})
	if err != nil {
		t.Fatalf("NewRequirement(): %v", err)
	}
	return requirement
}

func testSDKPayment(requirement Requirement) x402.PaymentPayload {
	resource := requirement.Resource()
	accepted := requirement.SDK()
	return x402.PaymentPayload{
		X402Version: X402Version,
		Payload: map[string]any{
			"signature": "0x" + strings.ToUpper(testSignature[2:]),
			"authorization": map[string]any{
				"from":        "0xAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAa",
				"to":          accepted.PayTo,
				"value":       accepted.Amount,
				"validAfter":  "0",
				"validBefore": "9999999999",
				"nonce":       "0xDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDd",
			},
		},
		Accepted: accepted,
		Resource: &resource,
	}
}

func paymentJSON(t *testing.T, payment x402.PaymentPayload) []byte {
	t.Helper()
	data, err := json.Marshal(payment)
	if err != nil {
		t.Fatalf("json.Marshal(payment): %v", err)
	}
	return data
}

func paymentHeader(t *testing.T, payment x402.PaymentPayload) http.Header {
	t.Helper()
	header := make(http.Header)
	header.Set(PaymentSignatureHeader, base64.StdEncoding.EncodeToString(paymentJSON(t, payment)))
	return header
}

func decodeTestPayment(t *testing.T, codec *Codec, payment x402.PaymentPayload) Payment {
	t.Helper()
	decoded, err := codec.DecodePaymentSignature(paymentHeader(t, payment))
	if err != nil {
		t.Fatalf("DecodePaymentSignature(): %v", err)
	}
	return decoded
}

func TestCodecDecodesOfficialV2TypesAndMatchesRequirement(t *testing.T) {
	t.Parallel()
	codec, err := NewCodec(DefaultMaxHeaderBytes)
	if err != nil {
		t.Fatalf("NewCodec(): %v", err)
	}
	requirement := testRequirement(t)
	decoded := decodeTestPayment(t, codec, testSDKPayment(requirement))

	if got := decoded.Authorization(); got.From != testPayer ||
		got.To != testRecipient || got.Nonce != testNonce ||
		got.Signature != testSignature {
		t.Fatalf("normalized authorization = %#v", got)
	}
	if err := requirement.Match(decoded); err != nil {
		t.Fatalf("Requirement.Match(): %v", err)
	}

	var official x402.PaymentPayload
	if err := json.Unmarshal(decoded.PayloadJSON(), &official); err != nil {
		t.Fatalf("official SDK payload decode: %v", err)
	}
	if official.X402Version != X402Version ||
		official.Accepted.Network != requirement.SDK().Network ||
		official.Resource == nil ||
		official.Resource.URL != requirement.Resource().URL {
		t.Fatalf("official payload = %#v", official)
	}
}

func TestCodecAcceptsOfficialV219EIP3009HeaderVector(t *testing.T) {
	t.Parallel()
	codec, err := NewCodec(DefaultMaxHeaderBytes)
	if err != nil {
		t.Fatalf("NewCodec(): %v", err)
	}
	requirement := testRequirement(t)
	resource := requirement.Resource()
	exactPayload := (&evm.ExactEIP3009Payload{
		Signature: testSignature,
		Authorization: evm.ExactEIP3009Authorization{
			From:        testPayer,
			To:          testRecipient,
			Value:       "125000",
			ValidAfter:  "0",
			ValidBefore: "9999999999",
			Nonce:       testNonce,
		},
	}).ToMap()
	officialPayload := x402.PaymentPayload{
		X402Version: X402Version,
		Payload:     exactPayload,
		Accepted:    requirement.SDK(),
		Resource:    &resource,
	}
	payloadJSON, err := json.Marshal(officialPayload)
	if err != nil {
		t.Fatalf("json.Marshal(official payload): %v", err)
	}
	officialHeaders, err := x402http.Newx402HTTPClient(nil).
		EncodePaymentSignatureHeader(payloadJSON)
	if err != nil {
		t.Fatalf("official EncodePaymentSignatureHeader(): %v", err)
	}
	header := make(http.Header)
	header.Set(PaymentSignatureHeader, officialHeaders["PAYMENT-SIGNATURE"])
	payment, err := codec.DecodePaymentSignature(header)
	if err != nil {
		t.Fatalf("DecodePaymentSignature(official vector): %v", err)
	}
	if err := requirement.Match(payment); err != nil {
		t.Fatalf("Requirement.Match(official vector): %v", err)
	}
	roundTripped, err := evm.PayloadFromMap(payment.Payload().Payload)
	if err != nil {
		t.Fatalf("official PayloadFromMap(): %v", err)
	}
	if roundTripped.Signature != testSignature ||
		roundTripped.Authorization.Nonce != testNonce ||
		roundTripped.Authorization.From != testPayer {
		t.Fatalf("official round trip = %#v", roundTripped)
	}
}

func TestCodecRequiresOneStrictBoundedPaymentHeader(t *testing.T) {
	t.Parallel()
	codec, err := NewCodec(1024)
	if err != nil {
		t.Fatalf("NewCodec(): %v", err)
	}
	tests := []struct {
		name   string
		header http.Header
		code   string
	}{
		{
			name:   "missing",
			header: make(http.Header),
			code:   CodeHeaderMissing,
		},
		{
			name: "multiple",
			header: http.Header{
				PaymentSignatureHeader: []string{"e30=", "e30="},
			},
			code: CodeHeaderMultiple,
		},
		{
			name: "oversized",
			header: http.Header{
				PaymentSignatureHeader: []string{strings.Repeat("A", 1025)},
			},
			code: CodeHeaderOversized,
		},
		{
			name: "leading whitespace",
			header: http.Header{
				PaymentSignatureHeader: []string{" e30="},
			},
			code: CodeHeaderMalformed,
		},
		{
			name: "embedded newline",
			header: http.Header{
				PaymentSignatureHeader: []string{"e3\n0="},
			},
			code: CodeHeaderMalformed,
		},
		{
			name: "raw unpadded base64",
			header: http.Header{
				PaymentSignatureHeader: []string{base64.RawStdEncoding.EncodeToString([]byte(`{}`))},
			},
			code: CodeHeaderMalformed,
		},
		{
			name: "url alphabet",
			header: http.Header{
				PaymentSignatureHeader: []string{"____"},
			},
			code: CodeHeaderMalformed,
		},
		{
			name: "combined field value",
			header: http.Header{
				PaymentSignatureHeader: []string{"e30=,e30="},
			},
			code: CodeHeaderMalformed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := codec.DecodePaymentSignature(test.header)
			var boundary *BoundaryError
			if !errors.As(err, &boundary) || boundary.Code != test.code ||
				boundary.Class != FailureInvalid {
				t.Fatalf("error = %#v, want code %q", err, test.code)
			}
		})
	}
}

func TestCodecRejectsAmbiguousAndUnsupportedPayloads(t *testing.T) {
	t.Parallel()
	codec, err := NewCodec(DefaultMaxHeaderBytes)
	if err != nil {
		t.Fatalf("NewCodec(): %v", err)
	}
	requirement := testRequirement(t)
	valid := paymentJSON(t, testSDKPayment(requirement))

	withExtraTransferMethod := testSDKPayment(requirement)
	withExtraTransferMethod.Accepted.Extra["assetTransferMethod"] = "permit2"

	withExtension := testSDKPayment(requirement)
	withExtension.Extensions = map[string]any{"hostile": true}

	withoutResource := testSDKPayment(requirement)
	withoutResource.Resource = nil

	overflow := testSDKPayment(requirement)
	overflow.Accepted.Amount = new(big.Int).Add(maxUint256, big.NewInt(1)).String()

	tests := []struct {
		name string
		json []byte
	}{
		{
			name: "duplicate key",
			json: bytes.Replace(
				valid,
				[]byte(`"x402Version":2`),
				[]byte(`"x402Version":2,"x402Version":2`),
				1,
			),
		},
		{
			name: "trailing value",
			json: append(append([]byte(nil), valid...), []byte(` {}`)...),
		},
		{
			name: "unsafe JSON number",
			json: bytes.Replace(
				valid,
				[]byte(`"x402Version":2`),
				[]byte(`"x402Version":9007199254740992`),
				1,
			),
		},
		{
			name: "fractional JSON number",
			json: bytes.Replace(
				valid,
				[]byte(`"x402Version":2`),
				[]byte(`"x402Version":2.0`),
				1,
			),
		},
		{
			name: "unknown root field",
			json: append([]byte(`{"unknown":true,`), valid[1:]...),
		},
		{
			name: "version one",
			json: bytes.Replace(
				valid,
				[]byte(`"x402Version":2`),
				[]byte(`"x402Version":1`),
				1,
			),
		},
		{
			name: "permit2 requirement",
			json: paymentJSON(t, withExtraTransferMethod),
		},
		{
			name: "extension",
			json: paymentJSON(t, withExtension),
		},
		{
			name: "missing resource",
			json: paymentJSON(t, withoutResource),
		},
		{
			name: "uint256 overflow",
			json: paymentJSON(t, overflow),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			header := make(http.Header)
			header.Set(PaymentSignatureHeader, base64.StdEncoding.EncodeToString(test.json))
			_, err := codec.DecodePaymentSignature(header)
			var boundary *BoundaryError
			if !errors.As(err, &boundary) || boundary.Class != FailureInvalid {
				t.Fatalf("error = %#v, want stable invalid boundary error", err)
			}
			if strings.Contains(err.Error(), "unknown") ||
				strings.Contains(err.Error(), string(test.json)) {
				t.Fatalf("error leaked hostile input: %q", err)
			}
		})
	}
}

func TestCodecPaymentRequiredAndResponseInteroperateWithSDKTypes(t *testing.T) {
	t.Parallel()
	codec, err := NewCodec(DefaultMaxHeaderBytes)
	if err != nil {
		t.Fatalf("NewCodec(): %v", err)
	}
	requirement := testRequirement(t)

	requiredHeader, err := codec.EncodePaymentRequired(
		requirement.PaymentRequired(CodeHeaderMissing),
	)
	if err != nil {
		t.Fatalf("EncodePaymentRequired(): %v", err)
	}
	if len(requiredHeader)%4 != 0 {
		t.Fatalf("PAYMENT-REQUIRED is not padded base64: %q", requiredHeader)
	}
	requiredJSON, err := base64.StdEncoding.Strict().DecodeString(requiredHeader)
	if err != nil {
		t.Fatalf("decode PAYMENT-REQUIRED: %v", err)
	}
	var officialRequired x402.PaymentRequired
	if err := json.Unmarshal(requiredJSON, &officialRequired); err != nil {
		t.Fatalf("official PaymentRequired decode: %v", err)
	}
	if officialRequired.X402Version != X402Version ||
		len(officialRequired.Accepts) != 1 ||
		!reflect.DeepEqual(officialRequired.Accepts[0], requirement.SDK()) ||
		officialRequired.Resource == nil ||
		officialRequired.Resource.URL != requirement.Resource().URL {
		t.Fatalf("official PaymentRequired = %#v", officialRequired)
	}
	requiredHeaders := make(http.Header)
	requiredHeaders.Set(PaymentRequiredHeader, requiredHeader)
	decodedRequired, err := codec.DecodePaymentRequired(requiredHeaders)
	if err != nil {
		t.Fatalf("DecodePaymentRequired(): %v", err)
	}
	if decodedRequired.RequirementDigest() != requirement.RequirementDigest() ||
		decodedRequired.ResourceDigest() != requirement.ResourceDigest() {
		t.Fatal("decoded PAYMENT-REQUIRED changed its immutable binding")
	}

	responseHeader, err := codec.EncodePaymentResponse(x402.SettleResponse{
		Success:     true,
		Payer:       "0xAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAa",
		Transaction: "0x" + strings.ToUpper(testTxHash[2:]),
		Network:     "eip155:84532",
		Amount:      "125000",
	})
	if err != nil {
		t.Fatalf("EncodePaymentResponse(): %v", err)
	}
	responseJSON, err := base64.StdEncoding.Strict().DecodeString(responseHeader)
	if err != nil {
		t.Fatalf("decode PAYMENT-RESPONSE: %v", err)
	}
	var officialResponse x402.SettleResponse
	if err := json.Unmarshal(responseJSON, &officialResponse); err != nil {
		t.Fatalf("official SettleResponse decode: %v", err)
	}
	if !officialResponse.Success || officialResponse.Payer != testPayer ||
		officialResponse.Transaction != testTxHash ||
		officialResponse.Network != "eip155:84532" ||
		officialResponse.Amount != "125000" {
		t.Fatalf("official SettleResponse = %#v", officialResponse)
	}
	responseHeaders := make(http.Header)
	responseHeaders.Set(PaymentResponseHeader, responseHeader)
	decodedResponse, err := codec.DecodePaymentResponse(responseHeaders)
	if err != nil {
		t.Fatalf("DecodePaymentResponse(): %v", err)
	}
	if !reflect.DeepEqual(decodedResponse, officialResponse) {
		t.Fatalf("decoded PAYMENT-RESPONSE = %#v, want %#v", decodedResponse, officialResponse)
	}

	withoutAmount, err := codec.EncodePaymentResponse(x402.SettleResponse{
		Success:     true,
		Payer:       testPayer,
		Transaction: testTxHash,
		Network:     "eip155:84532",
	})
	if err != nil {
		t.Fatalf("EncodePaymentResponse(without amount): %v", err)
	}
	withoutAmountHeaders := make(http.Header)
	withoutAmountHeaders.Set(PaymentResponseHeader, withoutAmount)
	decodedWithoutAmount, err := codec.DecodePaymentResponse(withoutAmountHeaders)
	if err != nil {
		t.Fatalf("DecodePaymentResponse(without amount): %v", err)
	}
	if decodedWithoutAmount.Amount != "" {
		t.Fatalf("decoded omitted amount = %q", decodedWithoutAmount.Amount)
	}
}

func TestCodecStrictlyDecodesPaymentRequiredAndResponseHeaders(t *testing.T) {
	t.Parallel()
	codec, err := NewCodec(2048)
	if err != nil {
		t.Fatalf("NewCodec(): %v", err)
	}
	requirement := testRequirement(t)
	requiredValue, err := codec.EncodePaymentRequired(requirement.PaymentRequired(""))
	if err != nil {
		t.Fatalf("EncodePaymentRequired(): %v", err)
	}
	responseValue, err := codec.EncodePaymentResponse(x402.SettleResponse{
		Success:     true,
		Payer:       testPayer,
		Transaction: testTxHash,
		Network:     "eip155:84532",
		Amount:      "125000",
	})
	if err != nil {
		t.Fatalf("EncodePaymentResponse(): %v", err)
	}
	requiredJSON, err := base64.StdEncoding.Strict().DecodeString(requiredValue)
	if err != nil {
		t.Fatalf("decode required fixture: %v", err)
	}
	responseJSON, err := base64.StdEncoding.Strict().DecodeString(responseValue)
	if err != nil {
		t.Fatalf("decode response fixture: %v", err)
	}

	tests := []struct {
		name     string
		header   string
		value    []byte
		multiple bool
		required bool
	}{
		{
			name:     "required missing",
			header:   PaymentRequiredHeader,
			required: true,
		},
		{
			name:     "required multiple",
			header:   PaymentRequiredHeader,
			value:    requiredJSON,
			multiple: true,
			required: true,
		},
		{
			name:     "required duplicate key",
			header:   PaymentRequiredHeader,
			value:    bytes.Replace(requiredJSON, []byte(`"x402Version":2`), []byte(`"x402Version":2,"x402Version":2`), 1),
			required: true,
		},
		{
			name:     "required trailing content",
			header:   PaymentRequiredHeader,
			value:    append(append([]byte(nil), requiredJSON...), []byte(` {}`)...),
			required: true,
		},
		{
			name:     "required unknown field",
			header:   PaymentRequiredHeader,
			value:    append([]byte(`{"unknown":true,`), requiredJSON[1:]...),
			required: true,
		},
		{
			name:     "required explicit extensions",
			header:   PaymentRequiredHeader,
			value:    bytes.Replace(requiredJSON, []byte(`"accepts":`), []byte(`"extensions":{},"accepts":`), 1),
			required: true,
		},
		{
			name:   "response missing",
			header: PaymentResponseHeader,
		},
		{
			name:     "response multiple",
			header:   PaymentResponseHeader,
			value:    responseJSON,
			multiple: true,
		},
		{
			name:   "response duplicate key",
			header: PaymentResponseHeader,
			value:  bytes.Replace(responseJSON, []byte(`"success":true`), []byte(`"success":true,"success":true`), 1),
		},
		{
			name:   "response trailing content",
			header: PaymentResponseHeader,
			value:  append(append([]byte(nil), responseJSON...), []byte(` {}`)...),
		},
		{
			name:   "response unknown field",
			header: PaymentResponseHeader,
			value:  append([]byte(`{"unknown":true,`), responseJSON[1:]...),
		},
		{
			name:   "response unsuccessful",
			header: PaymentResponseHeader,
			value:  bytes.Replace(responseJSON, []byte(`"success":true`), []byte(`"success":false`), 1),
		},
		{
			name:   "response explicit extensions",
			header: PaymentResponseHeader,
			value:  bytes.Replace(responseJSON, []byte(`"payer":`), []byte(`"extensions":{},"payer":`), 1),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			header := make(http.Header)
			if test.value != nil {
				value := base64.StdEncoding.EncodeToString(test.value)
				header.Add(test.header, value)
				if test.multiple {
					header.Add(test.header, value)
				}
			}
			if test.required {
				if _, err := codec.DecodePaymentRequired(header); err == nil {
					t.Fatal("DecodePaymentRequired() succeeded")
				}
				return
			}
			if _, err := codec.DecodePaymentResponse(header); err == nil {
				t.Fatal("DecodePaymentResponse() succeeded")
			}
		})
	}
}

func TestRequirementValidationCoversUint256AndCanonicalResource(t *testing.T) {
	t.Parallel()
	base := RequirementOptions{
		Network:            "eip155:1",
		Asset:              testAsset,
		Amount:             "1",
		PayTo:              testRecipient,
		MaxTimeoutSeconds:  300,
		AssetEIP712Name:    "USD Coin",
		AssetEIP712Version: "2",
		Resource: x402.ResourceInfo{
			URL: "https://explorer.example/api/v1/stats",
		},
	}
	maximum := new(big.Int).Set(maxUint256).String()
	base.Amount = maximum
	if _, err := NewRequirement(base); err != nil {
		t.Fatalf("NewRequirement(max uint256): %v", err)
	}
	loopback := base
	loopback.Amount = "1"
	loopback.Resource.URL = "http://127.0.0.1:8080/api/v1/stats"
	loopback.Resource.IconUrl = "http://localhost:8080/assets/icon.png"
	if _, err := NewRequirement(loopback); err != nil {
		t.Fatalf("NewRequirement(loopback HTTP): %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*RequirementOptions)
	}{
		{
			name: "zero amount",
			mutate: func(value *RequirementOptions) {
				value.Amount = "0"
			},
		},
		{
			name: "leading-zero amount",
			mutate: func(value *RequirementOptions) {
				value.Amount = "01"
			},
		},
		{
			name: "uint256 overflow",
			mutate: func(value *RequirementOptions) {
				value.Amount = new(big.Int).Add(maxUint256, big.NewInt(1)).String()
			},
		},
		{
			name: "noncanonical network",
			mutate: func(value *RequirementOptions) {
				value.Network = "eip155:01"
			},
		},
		{
			name: "zero chain",
			mutate: func(value *RequirementOptions) {
				value.Network = "eip155:0"
			},
		},
		{
			name: "invalid address",
			mutate: func(value *RequirementOptions) {
				value.Asset = "0x1234"
			},
		},
		{
			name: "zero timeout",
			mutate: func(value *RequirementOptions) {
				value.MaxTimeoutSeconds = 0
			},
		},
		{
			name: "missing EIP712 name",
			mutate: func(value *RequirementOptions) {
				value.AssetEIP712Name = ""
			},
		},
		{
			name: "HTTP resource",
			mutate: func(value *RequirementOptions) {
				value.Resource.URL = "http://explorer.example/api/v1/stats"
			},
		},
		{
			name: "resource fragment",
			mutate: func(value *RequirementOptions) {
				value.Resource.URL = "https://explorer.example/api/v1/stats#fragment"
			},
		},
		{
			name: "oversized service name",
			mutate: func(value *RequirementOptions) {
				value.Resource.ServiceName = strings.Repeat("a", 33)
			},
		},
		{
			name: "non-ASCII service name",
			mutate: func(value *RequirementOptions) {
				value.Resource.ServiceName = "Etherview 浏览器"
			},
		},
		{
			name: "too many tags",
			mutate: func(value *RequirementOptions) {
				value.Resource.Tags = []string{"a", "b", "c", "d", "e", "f"}
			},
		},
		{
			name: "oversized tag",
			mutate: func(value *RequirementOptions) {
				value.Resource.Tags = []string{strings.Repeat("a", 33)}
			},
		},
		{
			name: "oversized icon URL",
			mutate: func(value *RequirementOptions) {
				value.Resource.IconUrl =
					"https://explorer.example/" + strings.Repeat("a", 2048)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := base
			value.Amount = "1"
			test.mutate(&value)
			if _, err := NewRequirement(value); err == nil {
				t.Fatal("NewRequirement() succeeded, want error")
			}
		})
	}
}

func TestRequirementMatchRejectsEveryOuterBindingDifference(t *testing.T) {
	t.Parallel()
	codec, err := NewCodec(DefaultMaxHeaderBytes)
	if err != nil {
		t.Fatalf("NewCodec(): %v", err)
	}
	requirement := testRequirement(t)
	tests := []struct {
		name   string
		mutate func(*x402.PaymentPayload)
	}{
		{
			name: "network",
			mutate: func(value *x402.PaymentPayload) {
				value.Accepted.Network = "eip155:1"
			},
		},
		{
			name: "asset",
			mutate: func(value *x402.PaymentPayload) {
				value.Accepted.Asset = testRecipient
			},
		},
		{
			name: "amount",
			mutate: func(value *x402.PaymentPayload) {
				value.Accepted.Amount = "125001"
			},
		},
		{
			name: "recipient",
			mutate: func(value *x402.PaymentPayload) {
				value.Accepted.PayTo = testPayer
			},
		},
		{
			name: "timeout",
			mutate: func(value *x402.PaymentPayload) {
				value.Accepted.MaxTimeoutSeconds--
			},
		},
		{
			name: "EIP712 name",
			mutate: func(value *x402.PaymentPayload) {
				value.Accepted.Extra["name"] = "Other Coin"
			},
		},
		{
			name: "EIP712 version",
			mutate: func(value *x402.PaymentPayload) {
				value.Accepted.Extra["version"] = "3"
			},
		},
		{
			name: "resource URL",
			mutate: func(value *x402.PaymentPayload) {
				value.Resource.URL = "https://explorer.example/api/v1/blocks/124"
			},
		},
		{
			name: "resource metadata",
			mutate: func(value *x402.PaymentPayload) {
				value.Resource.Description = "Other block"
			},
		},
		{
			name: "authorization recipient",
			mutate: func(value *x402.PaymentPayload) {
				value.Payload["authorization"].(map[string]any)["to"] = testPayer
			},
		},
		{
			name: "authorization amount",
			mutate: func(value *x402.PaymentPayload) {
				value.Payload["authorization"].(map[string]any)["value"] = "125001"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payment := testSDKPayment(requirement)
			test.mutate(&payment)
			decoded := decodeTestPayment(t, codec, payment)
			err := requirement.Match(decoded)
			var boundary *BoundaryError
			if !errors.As(err, &boundary) ||
				boundary.Code != CodePaymentMismatch {
				t.Fatalf("Match() error = %#v", err)
			}
		})
	}
}

func TestFingerprintCreatesOneGlobalAuthorizationReplayFence(t *testing.T) {
	t.Parallel()
	codec, err := NewCodec(DefaultMaxHeaderBytes)
	if err != nil {
		t.Fatalf("NewCodec(): %v", err)
	}
	requirement := testRequirement(t)
	pepper := bytes.Repeat([]byte{0x42}, 32)

	firstPayment := testSDKPayment(requirement)
	first := decodeTestPayment(t, codec, firstPayment)
	firstFingerprint, err := Fingerprint(pepper, first)
	if err != nil {
		t.Fatalf("Fingerprint(first): %v", err)
	}

	secondPayment := testSDKPayment(requirement)
	secondPayment.Payload["signature"] = "0x" + strings.Repeat("ab", 65)
	authorization := secondPayment.Payload["authorization"].(map[string]any)
	authorization["from"] = testPayer
	authorization["to"] = testRecipient
	authorization["nonce"] = testNonce
	secondJSON := paymentJSON(t, secondPayment)
	var indented bytes.Buffer
	if err := json.Indent(&indented, secondJSON, "", "  "); err != nil {
		t.Fatalf("json.Indent(): %v", err)
	}
	secondHeader := make(http.Header)
	secondHeader.Set(
		PaymentSignatureHeader,
		base64.StdEncoding.EncodeToString(indented.Bytes()),
	)
	second, err := codec.DecodePaymentSignature(secondHeader)
	if err != nil {
		t.Fatalf("DecodePaymentSignature(second): %v", err)
	}
	secondFingerprint, err := Fingerprint(pepper, second)
	if err != nil {
		t.Fatalf("Fingerprint(second): %v", err)
	}
	if firstFingerprint != secondFingerprint {
		t.Fatal("representation or alternate signature proof changed replay fingerprint")
	}

	authorizationChanges := []struct {
		name  string
		field string
		value string
	}{
		{name: "payer", field: "from", value: testRecipient},
		{name: "recipient", field: "to", value: testPayer},
		{name: "value", field: "value", value: "125001"},
		{name: "valid after", field: "validAfter", value: "1"},
		{name: "valid before", field: "validBefore", value: "9999999998"},
		{
			name:  "nonce",
			field: "nonce",
			value: "0xabababababababababababababababababababababababababababababababab",
		},
	}
	for _, test := range authorizationChanges {
		payment := testSDKPayment(requirement)
		payment.Payload["authorization"].(map[string]any)[test.field] = test.value
		changed := decodeTestPayment(t, codec, payment)
		got, err := Fingerprint(pepper, changed)
		if err != nil {
			t.Fatalf("Fingerprint(changed %s): %v", test.name, err)
		}
		if got == firstFingerprint {
			t.Fatalf("changed %s retained replay fingerprint", test.name)
		}
	}

	// These fields are deliberately outside the unique replay identity. The
	// ledger stores and compares them on duplicate reservation, so the same
	// authorization cannot obtain a second owner for a different operation,
	// resource, timeout, price, or recipient.
	outerChanges := []struct {
		name   string
		mutate func(*x402.PaymentPayload)
	}{
		{
			name: "canonical resource",
			mutate: func(payment *x402.PaymentPayload) {
				payment.Resource.URL =
					"https://explorer.example/api/v1/blocks/124?include=transactions"
			},
		},
		{
			name: "resource metadata",
			mutate: func(payment *x402.PaymentPayload) {
				payment.Resource.Description = "Different metadata"
			},
		},
		{
			name: "timeout",
			mutate: func(payment *x402.PaymentPayload) {
				payment.Accepted.MaxTimeoutSeconds--
			},
		},
		{
			name: "amount",
			mutate: func(payment *x402.PaymentPayload) {
				payment.Accepted.Amount = "125001"
			},
		},
		{
			name: "recipient",
			mutate: func(payment *x402.PaymentPayload) {
				payment.Accepted.PayTo = testPayer
			},
		},
	}
	outerPayments := make([]Payment, 0, len(outerChanges))
	for _, test := range outerChanges {
		payment := testSDKPayment(requirement)
		test.mutate(&payment)
		decoded := decodeTestPayment(t, codec, payment)
		got, err := Fingerprint(pepper, decoded)
		if err != nil {
			t.Fatalf("Fingerprint(%s): %v", test.name, err)
		}
		if got != firstFingerprint {
			t.Fatalf("%s changed global authorization fingerprint", test.name)
		}
		outerPayments = append(outerPayments, decoded)
	}

	results := make(chan [32]byte, len(outerPayments)*8)
	for _, payment := range outerPayments {
		for range 8 {
			go func() {
				got, fingerprintErr := Fingerprint(pepper, payment)
				if fingerprintErr != nil {
					results <- [32]byte{}
					return
				}
				results <- got
			}()
		}
	}
	for range cap(results) {
		if got := <-results; got != firstFingerprint {
			t.Fatal("concurrent outer-binding replay obtained a distinct fingerprint")
		}
	}

	domainChanges := []struct {
		name   string
		mutate func(*x402.PaymentPayload)
	}{
		{
			name: "network",
			mutate: func(payment *x402.PaymentPayload) {
				payment.Accepted.Network = "eip155:1"
			},
		},
		{
			name: "asset",
			mutate: func(payment *x402.PaymentPayload) {
				payment.Accepted.Asset = testRecipient
			},
		},
		{
			name: "EIP712 name",
			mutate: func(payment *x402.PaymentPayload) {
				payment.Accepted.Extra["name"] = "Other Coin"
			},
		},
		{
			name: "EIP712 version",
			mutate: func(payment *x402.PaymentPayload) {
				payment.Accepted.Extra["version"] = "3"
			},
		},
	}
	for _, test := range domainChanges {
		t.Run(test.name+" changes fingerprint", func(t *testing.T) {
			t.Parallel()
			payment := testSDKPayment(requirement)
			test.mutate(&payment)
			changed := decodeTestPayment(t, codec, payment)
			got, err := Fingerprint(pepper, changed)
			if err != nil {
				t.Fatalf("Fingerprint(): %v", err)
			}
			if got == firstFingerprint {
				t.Fatalf("%s retained replay fingerprint", test.name)
			}
		})
	}

	if _, err := Fingerprint([]byte("too-short"), first); err == nil {
		t.Fatal("Fingerprint() accepted a short pepper")
	}
	if _, err := Fingerprint(pepper, Payment{}); err == nil {
		t.Fatal("Fingerprint() accepted an empty payment")
	}
}

func TestRequirementDigestsAreNormalizedAndSeparated(t *testing.T) {
	t.Parallel()
	first := testRequirement(t)
	second, err := NewRequirement(RequirementOptions{
		Network:            "eip155:84532",
		Asset:              "0xCcCcCcCcCcCcCcCcCcCcCcCcCcCcCcCcCcCcCcCc",
		Amount:             "125000",
		PayTo:              "0xBbBbBbBbBbBbBbBbBbBbBbBbBbBbBbBbBbBbBbBb",
		MaxTimeoutSeconds:  300,
		AssetEIP712Name:    "USD Coin",
		AssetEIP712Version: "2",
		Resource:           first.Resource(),
	})
	if err != nil {
		t.Fatalf("NewRequirement(second): %v", err)
	}
	if first.RequirementDigest() != second.RequirementDigest() ||
		first.ResourceDigest() != second.ResourceDigest() {
		t.Fatal("equivalent normalized requirement changed digest")
	}

	changedResource := first.Resource()
	changedResource.URL = "https://explorer.example/api/v1/blocks/124"
	third, err := NewRequirement(RequirementOptions{
		Network:            first.SDK().Network,
		Asset:              first.SDK().Asset,
		Amount:             first.SDK().Amount,
		PayTo:              first.SDK().PayTo,
		MaxTimeoutSeconds:  first.SDK().MaxTimeoutSeconds,
		AssetEIP712Name:    "USD Coin",
		AssetEIP712Version: "2",
		Resource:           changedResource,
	})
	if err != nil {
		t.Fatalf("NewRequirement(third): %v", err)
	}
	if first.RequirementDigest() != third.RequirementDigest() ||
		first.ResourceDigest() == third.ResourceDigest() {
		t.Fatal("resource change did not remain separate from requirement digest")
	}
}
