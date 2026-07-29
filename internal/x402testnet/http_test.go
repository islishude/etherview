package x402testnet

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/islishude/etherview/internal/billing/x402wire"
	x402 "github.com/x402-foundation/x402/go/v2"
	x402http "github.com/x402-foundation/x402/go/v2/http"
	exactevmclient "github.com/x402-foundation/x402/go/v2/mechanisms/evm/exact/client"
	evmsigners "github.com/x402-foundation/x402/go/v2/signers/evm"
)

const (
	httpTestAsset         = "0x1111111111111111111111111111111111111111"
	httpTestRecipient     = "0x2222222222222222222222222222222222222222"
	httpTestTxHash        = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	httpTestAmount        = "125000"
	httpTestChallengeBody = `{"error":{"code":"payment_required","message":"payment is required","request_id":"test"}}`
	httpTestNativeBody    = `{"data":{"paid":true},"meta":{"request_id":"test","chain_id":"84532"}}`
)

func TestExecutePaymentUsesOfficialSDKForOneBoundedPayment(t *testing.T) {
	t.Parallel()
	codec := httpTestCodec(t)
	privateKey, payer := httpTestSigner(t, 1)
	var unsignedRequests atomic.Int32
	var signedRequests atomic.Int32
	var challengeHeader string
	var requirement x402wire.Requirement

	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.Method != http.MethodGet ||
			len(request.Header.Values("Cookie")) != 0 ||
			len(request.Header.Values("X-API-Key")) != 0 ||
			len(request.Header.Values("Authorization")) != 0 {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(request.Header.Values(x402wire.PaymentSignatureHeader)) == 0 {
			unsignedRequests.Add(1)
			writer.Header().Set(
				x402wire.PaymentRequiredHeader,
				challengeHeader,
			)
			writer.Header().Set("Content-Type", "application/json; charset=utf-8")
			writer.WriteHeader(http.StatusPaymentRequired)
			_, _ = io.WriteString(writer, httpTestChallengeBody)
			return
		}
		signedRequests.Add(1)
		payment, err := codec.DecodePaymentSignature(request.Header)
		if err != nil || requirement.Match(payment) != nil ||
			payment.Authorization().From != payer {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		responseHeader, err := codec.EncodePaymentResponse(x402.SettleResponse{
			Success:     true,
			Payer:       payer,
			Transaction: httpTestTxHash,
			Network:     x402.Network(baseSepoliaNetwork),
			Amount:      httpTestAmount,
		})
		if err != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		writer.Header().Set(x402wire.PaymentResponseHeader, responseHeader)
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		writer.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(writer, httpTestNativeBody)
	}))
	defer server.Close()

	target := server.URL + "/api/v1/blocks?limit=1"
	requirement = httpTestRequirement(t, target, "")
	challengeHeader = httpTestChallengeHeader(t, codec, requirement)
	options := httpTestOptions(
		t,
		target,
		privateKey,
		payer,
		server.Client().Transport,
	)
	evidence, err := ExecutePayment(context.Background(), options)
	if err != nil {
		t.Fatalf("ExecutePayment(): %v", err)
	}
	if unsignedRequests.Load() != 2 || signedRequests.Load() != 1 {
		t.Fatalf(
			"requests unsigned=%d signed=%d, want 2 and 1",
			unsignedRequests.Load(),
			signedRequests.Load(),
		)
	}
	body := []byte(httpTestNativeBody)
	if evidence.StatusCode != http.StatusOK ||
		evidence.Payer != payer ||
		evidence.Network != baseSepoliaNetwork ||
		evidence.Asset != httpTestAsset ||
		evidence.AmountAtomic != httpTestAmount ||
		evidence.Recipient != httpTestRecipient ||
		evidence.TransactionHash != httpTestTxHash ||
		evidence.RequirementDigest != requirement.RequirementDigest() ||
		evidence.ResourceDigest != requirement.ResourceDigest() ||
		evidence.CallDataPrefixBytes != 4+9*32 ||
		zeroDigest(evidence.CallDataPrefixSHA256) ||
		evidence.FinalBodyBytes != int64(len(body)) ||
		evidence.FinalBodySHA256 != sha256.Sum256(body) {
		t.Fatalf("PaymentEvidence = %#v", evidence)
	}
}

func TestSettlementCallDataBindingPinsOfficialVRSVector(t *testing.T) {
	t.Parallel()
	codec := httpTestCodec(t)
	requirement := httpTestRequirement(
		t,
		"https://paid.example/api/v1/blocks?limit=1",
		"",
	)
	resource := requirement.Resource()
	for _, test := range []struct {
		name       string
		wireV      string
		wantDigest string
	}{
		{
			name:       "zero becomes 27",
			wireV:      "00",
			wantDigest: "3f24998f432479997e3a5d980f892388448f2cb4110f0ba005df5062f338ee3f",
		},
		{
			name:       "one becomes 28",
			wireV:      "01",
			wantDigest: "eaded9f5c2df8ba8e95494132c8161ea4eb72c5adcd08274ab579af2ef7d9596",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payload := x402.PaymentPayload{
				X402Version: x402wire.X402Version,
				Payload: map[string]any{
					"signature": "0x" +
						strings.Repeat("11", 32) +
						strings.Repeat("22", 32) +
						test.wireV,
					"authorization": map[string]any{
						"from":        "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
						"to":          httpTestRecipient,
						"value":       httpTestAmount,
						"validAfter":  "1",
						"validBefore": "9999999999",
						"nonce":       "0x" + strings.Repeat("33", 32),
					},
				},
				Accepted: requirement.SDK(),
				Resource: &resource,
			}
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			header := make(http.Header)
			header.Set(
				x402wire.PaymentSignatureHeader,
				base64.StdEncoding.EncodeToString(encoded),
			)
			payment, err := codec.DecodePaymentSignature(header)
			if err != nil {
				t.Fatalf("DecodePaymentSignature(): %v", err)
			}
			digest, size, err := settlementCallDataBinding(payment)
			if err != nil {
				t.Fatalf("settlementCallDataBinding(): %v", err)
			}
			if size != 292 || hex.EncodeToString(digest[:]) != test.wantDigest {
				t.Fatalf(
					"binding size=%d digest=%x, want 292 and %s",
					size,
					digest,
					test.wantDigest,
				)
			}
		})
	}
}

func TestExecutePaymentRejectsChallengeTOCTOUDriftBeforeSigning(t *testing.T) {
	t.Parallel()
	codec := httpTestCodec(t)
	privateKey, payer := httpTestSigner(t, 2)
	var requests atomic.Int32
	var signed atomic.Int32
	var firstHeader string
	var secondHeader string
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		count := requests.Add(1)
		if len(request.Header.Values(x402wire.PaymentSignatureHeader)) != 0 {
			signed.Add(1)
		}
		if count == 1 {
			writer.Header().Set(x402wire.PaymentRequiredHeader, firstHeader)
		} else {
			writer.Header().Set(x402wire.PaymentRequiredHeader, secondHeader)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusPaymentRequired)
		_, _ = io.WriteString(writer, httpTestChallengeBody)
	}))
	defer server.Close()

	target := server.URL + "/api/v1/blocks?limit=1"
	firstHeader = httpTestChallengeHeader(
		t,
		codec,
		httpTestRequirement(t, target, ""),
	)
	firstJSON, err := base64.StdEncoding.Strict().DecodeString(firstHeader)
	if err != nil {
		t.Fatalf("decode first challenge: %v", err)
	}
	secondHeader = base64.StdEncoding.EncodeToString(
		append([]byte("{ "), firstJSON[1:]...),
	)
	_, err = ExecutePayment(
		context.Background(),
		httpTestOptions(t, target, privateKey, payer, server.Client().Transport),
	)
	if got := ErrorCode(err); got != codePaymentChallengeChanged {
		t.Fatalf("ErrorCode() = %q, want %q", got, codePaymentChallengeChanged)
	}
	if requests.Load() != 2 || signed.Load() != 0 {
		t.Fatalf(
			"requests=%d signed=%d, want 2 and 0",
			requests.Load(),
			signed.Load(),
		)
	}
}

func TestExecutePaymentRejectsInvalidPaymentRequiredEnvelopeBeforeSigning(
	t *testing.T,
) {
	t.Parallel()
	codec := httpTestCodec(t)
	privateKey, payer := httpTestSigner(t, 10)
	tests := []struct {
		name        string
		contentType string
		body        string
		breakSecond bool
	}{
		{
			name:        "empty body",
			contentType: "application/json",
		},
		{
			name:        "HTML content type",
			contentType: "text/html",
			body:        httpTestChallengeBody,
		},
		{
			name:        "missing request ID",
			contentType: "application/json",
			body:        `{"error":{"code":"payment_required","message":"payment is required","request_id":""}}`,
		},
		{
			name:        "unexpected error code",
			contentType: "application/json",
			body:        `{"error":{"code":"x402_unavailable","message":"payment is required","request_id":"test"}}`,
		},
		{
			name:        "duplicate key",
			contentType: "application/json",
			body:        `{"error":{"code":"payment_required","code":"payment_required","message":"payment is required","request_id":"test"}}`,
		},
		{
			name:        "second challenge body drifts",
			contentType: "application/json",
			body:        `{"error":{"code":"payment_required","message":"changed","request_id":"test"}}`,
			breakSecond: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			var signed atomic.Int32
			var challenge string
			server := httptest.NewTLSServer(http.HandlerFunc(func(
				writer http.ResponseWriter,
				request *http.Request,
			) {
				count := requests.Add(1)
				if len(request.Header.Values(x402wire.PaymentSignatureHeader)) != 0 {
					signed.Add(1)
				}
				writer.Header().Set(x402wire.PaymentRequiredHeader, challenge)
				writer.Header().Set("Content-Type", test.contentType)
				writer.WriteHeader(http.StatusPaymentRequired)
				if test.breakSecond && count == 1 {
					_, _ = io.WriteString(writer, httpTestChallengeBody)
					return
				}
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()
			target := server.URL + "/api/v1/blocks?limit=1"
			challenge = httpTestChallengeHeader(
				t,
				codec,
				httpTestRequirement(t, target, ""),
			)
			_, err := ExecutePayment(
				context.Background(),
				httpTestOptions(
					t,
					target,
					privateKey,
					payer,
					server.Client().Transport,
				),
			)
			if got := ErrorCode(err); got != codePaymentChallengeInvalid {
				t.Fatalf(
					"ErrorCode() = %q, want %q",
					got,
					codePaymentChallengeInvalid,
				)
			}
			wantRequests := int32(1)
			if test.breakSecond {
				wantRequests = 2
			}
			if requests.Load() != wantRequests || signed.Load() != 0 {
				t.Fatalf(
					"requests=%d signed=%d, want %d and 0",
					requests.Load(),
					signed.Load(),
					wantRequests,
				)
			}
		})
	}
}

func TestExecutePaymentRejectsHostileChallengeBounds(t *testing.T) {
	t.Parallel()
	privateKey, payer := httpTestSigner(t, 3)
	tests := []struct {
		name   string
		serve  func(http.ResponseWriter)
		limit  int64
		header int
	}{
		{
			name: "duplicate payment-required",
			serve: func(writer http.ResponseWriter) {
				writer.Header().Add(x402wire.PaymentRequiredHeader, "e30=")
				writer.Header().Add(x402wire.PaymentRequiredHeader, "e30=")
				writer.WriteHeader(http.StatusPaymentRequired)
			},
		},
		{
			name:   "oversized payment-required",
			header: 128,
			serve: func(writer http.ResponseWriter) {
				writer.Header().Set(
					x402wire.PaymentRequiredHeader,
					strings.Repeat("A", 129),
				)
				writer.WriteHeader(http.StatusPaymentRequired)
			},
		},
		{
			name:  "oversized challenge body",
			limit: 8,
			serve: func(writer http.ResponseWriter) {
				writer.Header().Set(x402wire.PaymentRequiredHeader, "e30=")
				writer.WriteHeader(http.StatusPaymentRequired)
				_, _ = io.WriteString(writer, "123456789")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(
				writer http.ResponseWriter,
				_ *http.Request,
			) {
				requests.Add(1)
				test.serve(writer)
			}))
			defer server.Close()
			target := server.URL + "/api/v1/blocks?limit=1"
			options := httpTestOptions(
				t,
				target,
				privateKey,
				payer,
				server.Client().Transport,
			)
			if test.limit != 0 {
				options.MaxChallengeBodyBytes = test.limit
			}
			if test.header != 0 {
				options.MaxPaymentHeaderBytes = test.header
			}
			_, err := ExecutePayment(context.Background(), options)
			if got := ErrorCode(err); got != codePaymentChallengeInvalid {
				t.Fatalf(
					"ErrorCode() = %q, want %q",
					got,
					codePaymentChallengeInvalid,
				)
			}
			if requests.Load() != 1 {
				t.Fatalf("requests = %d, want 1", requests.Load())
			}
		})
	}
}

func TestExecutePaymentTreatsSignedResponseFailuresAsUnknown(t *testing.T) {
	t.Parallel()
	codec := httpTestCodec(t)
	privateKey, payer := httpTestSigner(t, 4)
	tests := []struct {
		name  string
		final func(http.ResponseWriter)
	}{
		{
			name: "duplicate payment-response",
			final: func(writer http.ResponseWriter) {
				value := httpTestResponseHeader(t, codec, payer)
				writer.Header().Add(x402wire.PaymentResponseHeader, value)
				writer.Header().Add(x402wire.PaymentResponseHeader, value)
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(writer, httpTestNativeBody)
			},
		},
		{
			name: "oversized payment-response",
			final: func(writer http.ResponseWriter) {
				writer.Header().Set(
					x402wire.PaymentResponseHeader,
					strings.Repeat("A", x402wire.DefaultMaxHeaderBytes+1),
				)
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(writer, httpTestNativeBody)
			},
		},
		{
			name: "oversized final body",
			final: func(writer http.ResponseWriter) {
				writer.Header().Set(
					x402wire.PaymentResponseHeader,
					httpTestResponseHeader(t, codec, payer),
				)
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(writer, "123456789")
			},
		},
		{
			name: "payer mismatch",
			final: func(writer http.ResponseWriter) {
				writer.Header().Set(
					x402wire.PaymentResponseHeader,
					httpTestResponseHeader(
						t,
						codec,
						"0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
					),
				)
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(writer, httpTestNativeBody)
			},
		},
		{
			name: "network mismatch",
			final: func(writer http.ResponseWriter) {
				value, err := codec.EncodePaymentResponse(x402.SettleResponse{
					Success:     true,
					Payer:       payer,
					Transaction: httpTestTxHash,
					Network:     "eip155:1",
					Amount:      httpTestAmount,
				})
				if err != nil {
					writer.WriteHeader(http.StatusInternalServerError)
					return
				}
				writer.Header().Set(x402wire.PaymentResponseHeader, value)
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(writer, httpTestNativeBody)
			},
		},
		{
			name: "amount mismatch",
			final: func(writer http.ResponseWriter) {
				value, err := codec.EncodePaymentResponse(x402.SettleResponse{
					Success:     true,
					Payer:       payer,
					Transaction: httpTestTxHash,
					Network:     x402.Network(baseSepoliaNetwork),
					Amount:      "125001",
				})
				if err != nil {
					writer.WriteHeader(http.StatusInternalServerError)
					return
				}
				writer.Header().Set(x402wire.PaymentResponseHeader, value)
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(writer, httpTestNativeBody)
			},
		},
		{
			name: "unexpected success status",
			final: func(writer http.ResponseWriter) {
				writer.Header().Set(
					x402wire.PaymentResponseHeader,
					httpTestResponseHeader(t, codec, payer),
				)
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusNoContent)
			},
		},
		{
			name: "missing native content type",
			final: func(writer http.ResponseWriter) {
				writer.Header().Set(
					x402wire.PaymentResponseHeader,
					httpTestResponseHeader(t, codec, payer),
				)
				writer.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(writer, httpTestNativeBody)
			},
		},
		{
			name: "invalid native envelope",
			final: func(writer http.ResponseWriter) {
				writer.Header().Set(
					x402wire.PaymentResponseHeader,
					httpTestResponseHeader(t, codec, payer),
				)
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(
					writer,
					`{"data":{"paid":true},"meta":{"request_id":"test"}}`,
				)
			},
		},
		{
			name: "server unavailable after authorization",
			final: func(writer http.ResponseWriter) {
				writer.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(writer, "hostile settlement detail")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			var signed atomic.Int32
			var challenge string
			server := httptest.NewTLSServer(http.HandlerFunc(func(
				writer http.ResponseWriter,
				request *http.Request,
			) {
				requests.Add(1)
				if len(request.Header.Values(x402wire.PaymentSignatureHeader)) == 0 {
					writer.Header().Set(
						x402wire.PaymentRequiredHeader,
						challenge,
					)
					writer.Header().Set("Content-Type", "application/json")
					writer.WriteHeader(http.StatusPaymentRequired)
					_, _ = io.WriteString(writer, httpTestChallengeBody)
					return
				}
				signed.Add(1)
				test.final(writer)
			}))
			defer server.Close()
			target := server.URL + "/api/v1/blocks?limit=1"
			challenge = httpTestChallengeHeader(
				t,
				codec,
				httpTestRequirement(t, target, ""),
			)
			options := httpTestOptions(
				t,
				target,
				privateKey,
				payer,
				server.Client().Transport,
			)
			if test.name == "oversized final body" {
				options.MaxFinalBodyBytes = 8
			}
			_, err := ExecutePayment(context.Background(), options)
			if got := ErrorCode(err); got != codePaidOutcomeUnknown {
				t.Fatalf(
					"ErrorCode() = %q, want %q",
					got,
					codePaidOutcomeUnknown,
				)
			}
			if requests.Load() != 3 || signed.Load() != 1 {
				t.Fatalf(
					"requests=%d signed=%d, want 3 and 1",
					requests.Load(),
					signed.Load(),
				)
			}
		})
	}
}

func TestExecutePaymentDoesNotRetryUnknownSignedTransportFailure(t *testing.T) {
	t.Parallel()
	codec := httpTestCodec(t)
	privateKey, payer := httpTestSigner(t, 5)
	target := "https://paid.example/api/v1/blocks?limit=1"
	requirement := httpTestRequirement(t, target, "")
	challenge := httpTestChallengeHeader(t, codec, requirement)
	var requests atomic.Int32
	var signed atomic.Int32
	transport := httpTestRoundTripFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		requests.Add(1)
		if len(request.Header.Values(x402wire.PaymentSignatureHeader)) != 0 {
			signed.Add(1)
			return nil, errors.New(
				"hostile://private-key?authorization=PAYMENT-SIGNATURE",
			)
		}
		header := make(http.Header)
		header.Set(x402wire.PaymentRequiredHeader, challenge)
		header.Set("Content-Type", "application/json")
		return &http.Response{
			StatusCode: http.StatusPaymentRequired,
			Header:     header,
			Body:       io.NopCloser(strings.NewReader(httpTestChallengeBody)),
			Request:    request,
		}, nil
	})
	options := httpTestOptions(t, target, privateKey, payer, transport)
	_, err := ExecutePayment(context.Background(), options)
	if got := ErrorCode(err); got != codePaidOutcomeUnknown {
		t.Fatalf("ErrorCode() = %q, want %q", got, codePaidOutcomeUnknown)
	}
	if strings.Contains(err.Error(), "hostile") ||
		strings.Contains(err.Error(), "PAYMENT-SIGNATURE") {
		t.Fatalf("error leaked hostile transport text: %q", err)
	}
	if requests.Load() != 3 || signed.Load() != 1 {
		t.Fatalf(
			"requests=%d signed=%d, want 3 and 1",
			requests.Load(),
			signed.Load(),
		)
	}
}

func TestPaymentGuardBlocksSecondAuthorizationBeforeNetwork(t *testing.T) {
	t.Parallel()
	codec := httpTestCodec(t)
	privateKey, payer := httpTestSigner(t, 6)
	target := "https://paid.example/api/v1/blocks?limit=1"
	requirement := httpTestRequirement(t, target, "")
	paymentHeader := httpTestSignedHeader(t, privateKey, requirement)
	var networkRequests atomic.Int32
	base := httpTestRoundTripFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		networkRequests.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("ok")),
			Request:    request,
		}, nil
	})
	guard := &paymentRoundTripper{
		base:                  base,
		codec:                 codec,
		targetURL:             target,
		expected:              requirement,
		expectedPayer:         payer,
		maxChallengeBodyBytes: 1024,
		unsignedRequests:      2,
		hasReference:          true,
		reference:             requirement,
	}
	request, err := newPaymentRequest(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(x402wire.PaymentSignatureHeader, paymentHeader)
	response, err := guard.RoundTrip(request)
	if err != nil {
		t.Fatalf("first guarded RoundTrip(): %v", err)
	}
	discardAndClose(response.Body)

	retry := request.Clone(context.Background())
	_, err = guard.RoundTrip(retry)
	if got := ErrorCode(err); got != codePaymentRetryBlocked {
		t.Fatalf("ErrorCode() = %q, want %q", got, codePaymentRetryBlocked)
	}
	if networkRequests.Load() != 1 {
		t.Fatalf("network requests = %d, want 1", networkRequests.Load())
	}
}

func TestPaymentGuardMakesOfficialCorrectiveRetryUnknownWithoutSendingIt(t *testing.T) {
	t.Parallel()
	codec := httpTestCodec(t)
	privateKey, payer := httpTestSigner(t, 9)
	target := "https://paid.example/api/v1/blocks?limit=1"
	requirement := httpTestRequirement(t, target, "")
	challenge := httpTestChallengeHeader(t, codec, requirement)
	var networkRequests atomic.Int32
	var signedNetworkRequests atomic.Int32
	base := httpTestRoundTripFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		networkRequests.Add(1)
		if len(request.Header.Values(x402wire.PaymentSignatureHeader)) != 0 {
			signedNetworkRequests.Add(1)
		}
		header := make(http.Header)
		header.Set(x402wire.PaymentRequiredHeader, challenge)
		header.Set("Content-Type", "application/json")
		return &http.Response{
			StatusCode: http.StatusPaymentRequired,
			Header:     header,
			Body:       io.NopCloser(strings.NewReader(httpTestChallengeBody)),
			Request:    request,
		}, nil
	})
	guard := &paymentRoundTripper{
		base:                  base,
		codec:                 codec,
		targetURL:             target,
		expected:              requirement,
		expectedPayer:         payer,
		maxChallengeBodyBytes: 1024,
		unsignedRequests:      1,
		hasReference:          true,
		reference:             requirement,
		referenceHeader:       challenge,
	}
	signer, err := evmsigners.NewClientSignerFromPrivateKey(
		hex.EncodeToString(privateKey),
	)
	if err != nil {
		t.Fatalf("NewClientSignerFromPrivateKey(): %v", err)
	}
	client := x402.Newx402Client(
		x402.WithOnPaymentResponseHook(
			func(
				context.Context,
				x402.PaymentResponseContext,
			) (x402.PaymentResponseResult, error) {
				return x402.PaymentResponseResult{Recovered: true}, nil
			},
		),
	)
	client.Register(
		x402.Network(baseSepoliaNetwork),
		exactevmclient.NewExactEvmScheme(signer, nil),
	)
	wrapped := x402http.WrapHTTPClientWithPayment(
		newRestrictedPaymentClient(guard, defaultPaymentTimeout),
		x402http.Newx402HTTPClient(client),
	)
	request, err := newPaymentRequest(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	response, err := wrapped.Do(request)
	if response != nil {
		discardAndClose(response.Body)
	}
	mapped := stablePaymentError(err, guard.paymentAttempted())
	if got := ErrorCode(mapped); got != codePaidOutcomeUnknown {
		t.Fatalf("ErrorCode() = %q, want %q", got, codePaidOutcomeUnknown)
	}
	if networkRequests.Load() != 2 || signedNetworkRequests.Load() != 1 {
		t.Fatalf(
			"network requests=%d signed=%d, want 2 and 1",
			networkRequests.Load(),
			signedNetworkRequests.Load(),
		)
	}
}

func TestPaymentClientRejectsRedirectCookieCredentialAndProxyUse(t *testing.T) {
	t.Parallel()
	transport := newPaymentHTTPTransport()
	if transport.Proxy != nil ||
		transport.MaxResponseHeaderBytes != maxResponseHeaderBytes ||
		!transport.DisableKeepAlives ||
		transport.Protocols == nil ||
		!transport.Protocols.HTTP1() ||
		transport.Protocols.HTTP2() ||
		transport.MaxIdleConns != 0 ||
		transport.MaxIdleConnsPerHost != 0 ||
		transport.MaxConnsPerHost != 4 {
		t.Fatalf("production payment transport is not bounded: %#v", transport)
	}
	client := newRestrictedPaymentClient(transport, defaultPaymentTimeout)
	if client.Jar != nil || client.CheckRedirect == nil {
		t.Fatal("restricted client configured a Jar or omitted redirect policy")
	}

	var redirected atomic.Int32
	destination := httptest.NewTLSServer(http.HandlerFunc(func(
		http.ResponseWriter,
		*http.Request,
	) {
		redirected.Add(1)
	}))
	defer destination.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Set("Location", destination.URL)
		writer.WriteHeader(http.StatusFound)
	}))
	defer source.Close()
	privateKey, payer := httpTestSigner(t, 7)
	target := source.URL + "/api/v1/blocks?limit=1"
	_, err := ExecutePayment(
		context.Background(),
		httpTestOptions(t, target, privateKey, payer, source.Client().Transport),
	)
	if got := ErrorCode(err); got != codePaymentChallengeInvalid {
		t.Fatalf("redirect ErrorCode() = %q", got)
	}
	if redirected.Load() != 0 {
		t.Fatal("payment client followed a redirect")
	}

	codec := httpTestCodec(t)
	requirement := httpTestRequirement(
		t,
		"https://paid.example/api/v1/blocks?limit=1",
		"",
	)
	for _, headerName := range []string{"Cookie", "X-API-Key"} {
		t.Run(headerName, func(t *testing.T) {
			t.Parallel()
			var networkRequests atomic.Int32
			guard := &paymentRoundTripper{
				base: httpTestRoundTripFunc(func(
					*http.Request,
				) (*http.Response, error) {
					networkRequests.Add(1)
					return nil, errors.New("must not run")
				}),
				codec:     codec,
				targetURL: requirement.Resource().URL,
				expected:  requirement,
			}
			request, requestErr := newPaymentRequest(
				context.Background(),
				requirement.Resource().URL,
			)
			if requestErr != nil {
				t.Fatal(requestErr)
			}
			request.Header.Set(headerName, "hostile-secret")
			_, requestErr = guard.RoundTrip(request)
			if got := ErrorCode(requestErr); got != codePaymentGuardFailed {
				t.Fatalf("ErrorCode() = %q", got)
			}
			if networkRequests.Load() != 0 {
				t.Fatal("credential-bearing request reached the network")
			}
		})
	}

	var cookieRequests atomic.Int32
	var challenge string
	cookieServer := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		cookieRequests.Add(1)
		writer.Header().Set(x402wire.PaymentRequiredHeader, challenge)
		writer.Header().Set("Set-Cookie", "session=hostile")
		writer.WriteHeader(http.StatusPaymentRequired)
	}))
	defer cookieServer.Close()
	cookieTarget := cookieServer.URL + "/api/v1/blocks?limit=1"
	challenge = httpTestChallengeHeader(
		t,
		codec,
		httpTestRequirement(t, cookieTarget, ""),
	)
	cookieOptions := httpTestOptions(
		t,
		cookieTarget,
		privateKey,
		payer,
		cookieServer.Client().Transport,
	)
	_, err = ExecutePayment(context.Background(), cookieOptions)
	if got := ErrorCode(err); got != codePaymentChallengeInvalid {
		t.Fatalf("Set-Cookie ErrorCode() = %q", got)
	}
	if cookieRequests.Load() != 1 {
		t.Fatalf("cookie server requests = %d, want 1", cookieRequests.Load())
	}
}

func TestStablePaymentErrorMakesEveryPostSendFailureUnknown(t *testing.T) {
	t.Parallel()
	for _, err := range []error{
		errors.New("hostile transport detail"),
		boundaryError(codePaymentRetryBlocked),
		boundaryError(codePaymentChallengeChanged),
	} {
		if got := ErrorCode(stablePaymentError(err, true)); got != codePaidOutcomeUnknown {
			t.Fatalf("ErrorCode() = %q, want %q", got, codePaidOutcomeUnknown)
		}
	}
}

func TestExecutePaymentRejectsInvalidConfigBeforeNetwork(t *testing.T) {
	t.Parallel()
	privateKey, payer := httpTestSigner(t, 8)
	target := "https://paid.example/api/v1/blocks?limit=1"
	var networkRequests atomic.Int32
	transport := httpTestRoundTripFunc(func(
		*http.Request,
	) (*http.Response, error) {
		networkRequests.Add(1)
		return nil, errors.New("must not run")
	})
	base := httpTestOptions(t, target, privateKey, payer, transport)
	tests := []struct {
		name   string
		ctx    context.Context
		mutate func(*HTTPOptions)
	}{
		{
			name: "nil context",
		},
		{
			name: "non-HTTPS target",
			ctx:  context.Background(),
			mutate: func(options *HTTPOptions) {
				options.TargetURL = "http://paid.example/api/v1/blocks?limit=1"
				options.ExpectedResourceURL = options.TargetURL
			},
		},
		{
			name: "resource mismatch",
			ctx:  context.Background(),
			mutate: func(options *HTTPOptions) {
				options.ExpectedResourceURL += "&changed=true"
			},
		},
		{
			name: "payer mismatch",
			ctx:  context.Background(),
			mutate: func(options *HTTPOptions) {
				options.ExpectedPayer = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			},
		},
		{
			name: "wrong network",
			ctx:  context.Background(),
			mutate: func(options *HTTPOptions) {
				options.Network = "eip155:1"
			},
		},
		{
			name: "timeout too long",
			ctx:  context.Background(),
			mutate: func(options *HTTPOptions) {
				options.Timeout = maxPaymentTimeout + 1
			},
		},
		{
			name: "challenge body limit too large",
			ctx:  context.Background(),
			mutate: func(options *HTTPOptions) {
				options.MaxChallengeBodyBytes = absoluteChallengeBodyBytes + 1
			},
		},
		{
			name: "final body limit too large",
			ctx:  context.Background(),
			mutate: func(options *HTTPOptions) {
				options.MaxFinalBodyBytes = maxFinalBodyBytes + 1
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := base
			options.PrivateKey = append([]byte(nil), base.PrivateKey...)
			if test.mutate != nil {
				test.mutate(&options)
			}
			_, err := ExecutePayment(test.ctx, options)
			if got := ErrorCode(err); got != codePaymentConfigurationInvalid {
				t.Fatalf(
					"ErrorCode() = %q, want %q",
					got,
					codePaymentConfigurationInvalid,
				)
			}
		})
	}
	if networkRequests.Load() != 0 {
		t.Fatalf("invalid configuration made %d network requests", networkRequests.Load())
	}
}

func TestExecutePaymentRecoversPanicsWithoutLeakingOrRetrying(t *testing.T) {
	t.Parallel()
	privateKey, payer := httpTestSigner(t, 8)
	target := "https://paid.example/api/v1/blocks?limit=1"
	_, err := ExecutePayment(context.Background(), httpTestOptions(
		t,
		target,
		privateKey,
		payer,
		httpTestRoundTripFunc(func(*http.Request) (*http.Response, error) {
			panic("hostile pre-sign panic with private material")
		}),
	))
	if got := ErrorCode(err); got != CodeFailed {
		t.Fatalf("pre-sign panic ErrorCode() = %q", got)
	}
	if strings.Contains(err.Error(), "hostile") {
		t.Fatalf("pre-sign panic leaked: %q", err)
	}

	codec := httpTestCodec(t)
	requirement := httpTestRequirement(t, target, "")
	challenge := httpTestChallengeHeader(t, codec, requirement)
	var requests atomic.Int32
	var signed atomic.Int32
	transport := httpTestRoundTripFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		requests.Add(1)
		if len(request.Header.Values(x402wire.PaymentSignatureHeader)) != 0 {
			signed.Add(1)
			panic("hostile post-sign panic with authorization")
		}
		header := make(http.Header)
		header.Set(x402wire.PaymentRequiredHeader, challenge)
		header.Set("Content-Type", "application/json")
		return &http.Response{
			StatusCode: http.StatusPaymentRequired,
			Header:     header,
			Body:       io.NopCloser(strings.NewReader(httpTestChallengeBody)),
			Request:    request,
		}, nil
	})
	_, err = ExecutePayment(
		context.Background(),
		httpTestOptions(t, target, privateKey, payer, transport),
	)
	if got := ErrorCode(err); got != codePaidOutcomeUnknown {
		t.Fatalf("post-sign panic ErrorCode() = %q", got)
	}
	if strings.Contains(err.Error(), "hostile") {
		t.Fatalf("post-sign panic leaked: %q", err)
	}
	if requests.Load() != 3 || signed.Load() != 1 {
		t.Fatalf(
			"panic requests=%d signed=%d, want 3 and 1",
			requests.Load(),
			signed.Load(),
		)
	}
}

func httpTestOptions(
	t *testing.T,
	target string,
	privateKey []byte,
	payer string,
	transport http.RoundTripper,
) HTTPOptions {
	t.Helper()
	return HTTPOptions{
		TargetURL:             target,
		ExpectedResourceURL:   target,
		Network:               baseSepoliaNetwork,
		Asset:                 httpTestAsset,
		AmountAtomic:          httpTestAmount,
		Recipient:             httpTestRecipient,
		ExpectedPayer:         payer,
		PrivateKey:            append([]byte(nil), privateKey...),
		AssetEIP712Name:       "Test USD",
		AssetEIP712Version:    "2",
		MaxTimeoutSeconds:     60,
		MaxPaymentHeaderBytes: x402wire.DefaultMaxHeaderBytes,
		MaxChallengeBodyBytes: 64 << 10,
		MaxFinalBodyBytes:     64 << 10,
		transport:             transport,
	}
}

func httpTestCodec(t *testing.T) *x402wire.Codec {
	t.Helper()
	codec, err := x402wire.NewCodec(x402wire.DefaultMaxHeaderBytes)
	if err != nil {
		t.Fatalf("NewCodec(): %v", err)
	}
	return codec
}

func httpTestSigner(t *testing.T, value byte) ([]byte, string) {
	t.Helper()
	privateKey := make([]byte, 32)
	privateKey[31] = value
	signer, err := evmsigners.NewClientSignerFromPrivateKey(
		hex.EncodeToString(privateKey),
	)
	if err != nil {
		t.Fatalf("NewClientSignerFromPrivateKey(): %v", err)
	}
	return privateKey, strings.ToLower(signer.Address())
}

func httpTestRequirement(
	t *testing.T,
	target string,
	description string,
) x402wire.Requirement {
	t.Helper()
	requirement, err := x402wire.NewRequirement(x402wire.RequirementOptions{
		Network:            baseSepoliaNetwork,
		Asset:              httpTestAsset,
		Amount:             httpTestAmount,
		PayTo:              httpTestRecipient,
		MaxTimeoutSeconds:  60,
		AssetEIP712Name:    "Test USD",
		AssetEIP712Version: "2",
		Resource: x402.ResourceInfo{
			URL:         target,
			Description: description,
			MimeType:    "application/json",
			ServiceName: "Etherview",
		},
	})
	if err != nil {
		t.Fatalf("NewRequirement(): %v", err)
	}
	return requirement
}

func httpTestChallengeHeader(
	t *testing.T,
	codec *x402wire.Codec,
	requirement x402wire.Requirement,
) string {
	t.Helper()
	value, err := codec.EncodePaymentRequired(requirement.PaymentRequired(""))
	if err != nil {
		t.Fatalf("EncodePaymentRequired(): %v", err)
	}
	return value
}

func httpTestResponseHeader(
	t *testing.T,
	codec *x402wire.Codec,
	payer string,
) string {
	t.Helper()
	value, err := codec.EncodePaymentResponse(x402.SettleResponse{
		Success:     true,
		Payer:       payer,
		Transaction: httpTestTxHash,
		Network:     x402.Network(baseSepoliaNetwork),
		Amount:      httpTestAmount,
	})
	if err != nil {
		t.Fatalf("EncodePaymentResponse(): %v", err)
	}
	return value
}

func httpTestSignedHeader(
	t *testing.T,
	privateKey []byte,
	requirement x402wire.Requirement,
) string {
	t.Helper()
	signer, err := evmsigners.NewClientSignerFromPrivateKey(
		hex.EncodeToString(privateKey),
	)
	if err != nil {
		t.Fatalf("NewClientSignerFromPrivateKey(): %v", err)
	}
	client := x402.Newx402Client()
	client.Register(
		x402.Network(baseSepoliaNetwork),
		exactevmclient.NewExactEvmScheme(signer, nil),
	)
	resource := requirement.Resource()
	payload, err := client.CreatePaymentPayload(
		context.Background(),
		requirement.SDK(),
		&resource,
		nil,
	)
	if err != nil {
		t.Fatalf("CreatePaymentPayload(): %v", err)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal(payment): %v", err)
	}
	return base64.StdEncoding.EncodeToString(payloadJSON)
}

type httpTestRoundTripFunc func(*http.Request) (*http.Response, error)

func (function httpTestRoundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return function(request)
}
