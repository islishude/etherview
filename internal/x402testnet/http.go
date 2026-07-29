package x402testnet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/billing/x402wire"
	"github.com/islishude/etherview/internal/jsonstrict"
	x402 "github.com/x402-foundation/x402/go/v2"
	x402http "github.com/x402-foundation/x402/go/v2/http"
	x402evm "github.com/x402-foundation/x402/go/v2/mechanisms/evm"
	exactevmclient "github.com/x402-foundation/x402/go/v2/mechanisms/evm/exact/client"
	evmsigners "github.com/x402-foundation/x402/go/v2/signers/evm"
)

const (
	defaultPaymentTimeout            = 10 * time.Second
	maxPaymentTimeout                = 60 * time.Second
	defaultChallengeBodyBytes  int64 = 1 << 20
	absoluteChallengeBodyBytes int64 = 1 << 20
	defaultFinalBodyBytes      int64 = 8 << 20
	maxFinalBodyBytes          int64 = 8 << 20
	maxResponseHeaderBytes     int64 = 64 << 10
	maxRequirementTimeout            = 60
	maxChallengeJSONDepth            = 16
	maxChallengeJSONNodes            = 128
	maxFinalJSONDepth                = 128
	maxFinalJSONNodes                = 2 << 20

	codePaymentConfigurationInvalid  = "x402_testnet_payment_configuration_invalid"
	codePaymentChallengeInvalid      = "x402_testnet_payment_challenge_invalid"
	codePaymentChallengeChanged      = "x402_testnet_payment_challenge_changed"
	codePaymentUnavailable           = "x402_testnet_payment_unavailable"
	codePaymentSigningFailed         = "x402_testnet_payment_signing_failed"
	codePaymentGuardFailed           = "x402_testnet_payment_guard_failed"
	codePaymentRetryBlocked          = "x402_testnet_payment_retry_blocked"
	codePaidOutcomeUnknown           = "x402_testnet_paid_outcome_unknown"
	codePaidReconciliationIncomplete = "x402_testnet_paid_reconciliation_incomplete"
)

// HTTPOptions binds the one-shot payment to one exact Base Sepolia resource.
// PrivateKey is copied while the official SDK signer is constructed and is
// never retained in evidence or an error.
type HTTPOptions struct {
	TargetURL             string
	ExpectedResourceURL   string
	Network               string
	Asset                 string
	AmountAtomic          string
	Recipient             string
	ExpectedPayer         string
	PrivateKey            []byte
	AssetEIP712Name       string
	AssetEIP712Version    string
	MaxTimeoutSeconds     int
	Timeout               time.Duration
	MaxPaymentHeaderBytes int
	MaxChallengeBodyBytes int64
	MaxFinalBodyBytes     int64

	transport http.RoundTripper
}

// PaymentEvidence is the non-secret HTTP result that later ledger and chain
// checks must corroborate. It deliberately excludes the response body and the
// payment authorization.
type PaymentEvidence struct {
	StatusCode           int
	Payer                string
	Network              string
	Asset                string
	AmountAtomic         string
	Recipient            string
	TransactionHash      string
	RequirementDigest    [sha256.Size]byte
	ResourceDigest       [sha256.Size]byte
	CallDataPrefixBytes  int
	CallDataPrefixSHA256 [sha256.Size]byte
	FinalBodyBytes       int64
	FinalBodySHA256      [sha256.Size]byte
}

type normalizedHTTPOptions struct {
	targetURL             string
	expected              x402wire.Requirement
	expectedPayer         string
	privateKeyHex         string
	timeout               time.Duration
	maxChallengeBodyBytes int64
	maxFinalBodyBytes     int64
	codec                 *x402wire.Codec
	transport             http.RoundTripper
}

// ExecutePayment performs exactly one bare challenge probe, one challenge
// recheck through the official x402 HTTP wrapper, and at most one signed
// request. Once the signed request reaches the transport, every uncertain
// failure is reported as an unknown paid outcome and is never retried.
func ExecutePayment(
	ctx context.Context,
	options HTTPOptions,
) (evidence PaymentEvidence, resultErr error) {
	var guard *paymentRoundTripper
	defer func() {
		if recover() == nil {
			return
		}
		evidence = PaymentEvidence{}
		if guard != nil && guard.paymentAttempted() {
			resultErr = boundaryError(codePaidOutcomeUnknown)
			return
		}
		resultErr = boundaryError(CodeFailed)
	}()
	if ctx == nil {
		return PaymentEvidence{}, boundaryError(codePaymentConfigurationInvalid)
	}
	normalized, err := normalizeHTTPOptions(options)
	if err != nil {
		return PaymentEvidence{}, err
	}
	signer, err := evmsigners.NewClientSignerFromPrivateKey(normalized.privateKeyHex)
	if err != nil || !strings.EqualFold(signer.Address(), normalized.expectedPayer) {
		return PaymentEvidence{}, boundaryError(codePaymentConfigurationInvalid)
	}

	guard = &paymentRoundTripper{
		base:                  normalized.transport,
		codec:                 normalized.codec,
		targetURL:             normalized.targetURL,
		expected:              normalized.expected,
		expectedPayer:         normalized.expectedPayer,
		maxChallengeBodyBytes: normalized.maxChallengeBodyBytes,
	}
	baseClient := newRestrictedPaymentClient(guard, normalized.timeout)

	probe, err := newPaymentRequest(ctx, normalized.targetURL)
	if err != nil {
		return PaymentEvidence{}, boundaryError(codePaymentConfigurationInvalid)
	}
	probeResponse, err := baseClient.Do(probe)
	if err != nil {
		return PaymentEvidence{}, stablePaymentError(err, guard.paymentAttempted())
	}
	if probeResponse.StatusCode != http.StatusPaymentRequired {
		discardAndClose(probeResponse.Body)
		return PaymentEvidence{}, boundaryError(codePaymentChallengeInvalid)
	}
	if err := consumeChallengeBody(
		probeResponse.Body,
		normalized.maxChallengeBodyBytes,
	); err != nil {
		return PaymentEvidence{}, err
	}
	reference, ok := guard.referenceRequirement()
	if !ok {
		return PaymentEvidence{}, boundaryError(codePaymentChallengeInvalid)
	}

	sdkClient := x402.Newx402Client(
		x402.WithBeforePaymentCreationHook(
			func(creation x402.PaymentCreationContext) (*x402.BeforePaymentCreationHookResult, error) {
				if !matchesRequirementView(reference, creation.SelectedRequirements) {
					return nil, boundaryError(codePaymentChallengeChanged)
				}
				return nil, nil
			},
		),
	)
	sdkClient.Register(
		x402.Network(baseSepoliaNetwork),
		exactevmclient.NewExactEvmScheme(signer, nil),
	)
	wrappedClient := x402http.WrapHTTPClientWithPayment(
		baseClient,
		x402http.Newx402HTTPClient(sdkClient),
	)
	request, err := newPaymentRequest(ctx, normalized.targetURL)
	if err != nil {
		return PaymentEvidence{}, boundaryError(codePaymentConfigurationInvalid)
	}
	response, err := wrappedClient.Do(request)
	if err != nil {
		return PaymentEvidence{}, stablePaymentError(err, guard.paymentAttempted())
	}
	defer response.Body.Close() //nolint:errcheck

	if !guard.completedSinglePaymentAttempt() {
		discardBody(response.Body)
		return PaymentEvidence{}, boundaryError(codePaymentChallengeChanged)
	}
	callDigest, callBytes, ok := guard.callDataBinding()
	if !ok {
		discardBody(response.Body)
		return PaymentEvidence{}, boundaryError(codePaidOutcomeUnknown)
	}
	if response.StatusCode != http.StatusOK ||
		!validNativeJSONContentType(response.Header) {
		discardBody(response.Body)
		return PaymentEvidence{}, boundaryError(codePaidOutcomeUnknown)
	}
	settlement, err := normalized.codec.DecodePaymentResponse(response.Header)
	if err != nil || !validSettlement(
		settlement,
		normalized.expected,
		normalized.expectedPayer,
	) {
		discardBody(response.Body)
		return PaymentEvidence{}, boundaryError(codePaidOutcomeUnknown)
	}
	bodyBytes, bodyDigest, err := validateAndDigestNativeBody(
		response.Body,
		normalized.maxFinalBodyBytes,
	)
	if err != nil {
		return PaymentEvidence{}, boundaryError(codePaidOutcomeUnknown)
	}

	requirement := reference.SDK()
	return PaymentEvidence{
		StatusCode:           response.StatusCode,
		Payer:                normalized.expectedPayer,
		Network:              requirement.Network,
		Asset:                requirement.Asset,
		AmountAtomic:         requirement.Amount,
		Recipient:            requirement.PayTo,
		TransactionHash:      settlement.Transaction,
		RequirementDigest:    reference.RequirementDigest(),
		ResourceDigest:       reference.ResourceDigest(),
		CallDataPrefixBytes:  callBytes,
		CallDataPrefixSHA256: callDigest,
		FinalBodyBytes:       bodyBytes,
		FinalBodySHA256:      bodyDigest,
	}, nil
}

func normalizeHTTPOptions(options HTTPOptions) (normalizedHTTPOptions, error) {
	if options.Network != baseSepoliaNetwork ||
		options.TargetURL != options.ExpectedResourceURL ||
		!canonicalHTTPSURL(options.TargetURL) ||
		options.MaxTimeoutSeconds <= 0 ||
		options.MaxTimeoutSeconds > maxRequirementTimeout ||
		len(options.PrivateKey) != 32 {
		return normalizedHTTPOptions{}, boundaryError(codePaymentConfigurationInvalid)
	}
	expectedPayer, ok := canonicalEVMAddress(options.ExpectedPayer)
	if !ok {
		return normalizedHTTPOptions{}, boundaryError(codePaymentConfigurationInvalid)
	}
	expected, err := x402wire.NewRequirement(x402wire.RequirementOptions{
		Network:            options.Network,
		Asset:              options.Asset,
		Amount:             options.AmountAtomic,
		PayTo:              options.Recipient,
		MaxTimeoutSeconds:  options.MaxTimeoutSeconds,
		AssetEIP712Name:    options.AssetEIP712Name,
		AssetEIP712Version: options.AssetEIP712Version,
		Resource: x402.ResourceInfo{
			URL:         options.ExpectedResourceURL,
			MimeType:    testnetResourceMimeType,
			ServiceName: testnetResourceService,
		},
	})
	if err != nil {
		return normalizedHTTPOptions{}, boundaryError(codePaymentConfigurationInvalid)
	}

	timeout := options.Timeout
	if timeout == 0 {
		timeout = defaultPaymentTimeout
	}
	challengeLimit := options.MaxChallengeBodyBytes
	if challengeLimit == 0 {
		challengeLimit = defaultChallengeBodyBytes
	}
	finalLimit := options.MaxFinalBodyBytes
	if finalLimit == 0 {
		finalLimit = defaultFinalBodyBytes
	}
	headerLimit := options.MaxPaymentHeaderBytes
	if headerLimit == 0 {
		headerLimit = x402wire.DefaultMaxHeaderBytes
	}
	if timeout < 0 || timeout > maxPaymentTimeout ||
		challengeLimit < 0 || challengeLimit > absoluteChallengeBodyBytes ||
		finalLimit < 0 || finalLimit > maxFinalBodyBytes {
		return normalizedHTTPOptions{}, boundaryError(codePaymentConfigurationInvalid)
	}
	codec, err := x402wire.NewCodec(headerLimit)
	if err != nil {
		return normalizedHTTPOptions{}, boundaryError(codePaymentConfigurationInvalid)
	}
	transport := options.transport
	if transport == nil {
		transport = newPaymentHTTPTransport()
	}
	privateKey := append([]byte(nil), options.PrivateKey...)
	privateKeyHex := hex.EncodeToString(privateKey)
	clear(privateKey)
	return normalizedHTTPOptions{
		targetURL:             options.TargetURL,
		expected:              expected,
		expectedPayer:         expectedPayer,
		privateKeyHex:         privateKeyHex,
		timeout:               timeout,
		maxChallengeBodyBytes: challengeLimit,
		maxFinalBodyBytes:     finalLimit,
		codec:                 codec,
		transport:             transport,
	}, nil
}

func newPaymentHTTPTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.TLSHandshakeTimeout = 5 * time.Second
	transport.ResponseHeaderTimeout = 5 * time.Second
	transport.DisableCompression = true
	// Go may transparently replay an idempotent GET when a reused connection
	// fails after the request was written. A PAYMENT-SIGNATURE GET is not
	// semantically replayable, so every probe and the one signed request use a
	// fresh HTTP/1 connection.
	transport.DisableKeepAlives = true
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	transport.Protocols = protocols
	transport.MaxIdleConns = 0
	transport.MaxIdleConnsPerHost = 0
	transport.MaxConnsPerHost = 4
	transport.MaxResponseHeaderBytes = maxResponseHeaderBytes
	return transport
}

func newRestrictedPaymentClient(
	transport http.RoundTripper,
	timeout time.Duration,
) *http.Client {
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		Jar:       nil,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func newPaymentRequest(ctx context.Context, target string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	return request, nil
}

type paymentRoundTripper struct {
	base                  http.RoundTripper
	codec                 *x402wire.Codec
	targetURL             string
	expected              x402wire.Requirement
	expectedPayer         string
	maxChallengeBodyBytes int64

	mu               sync.Mutex
	unsignedRequests int
	signedRequests   int
	hasReference     bool
	reference        x402wire.Requirement
	referenceHeader  string
	callDataBytes    int
	callDataDigest   [sha256.Size]byte
}

func (guard *paymentRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	payment, signed, err := guard.validateRequest(request)
	if err != nil {
		return nil, err
	}
	response, err := guard.base.RoundTrip(request)
	if err != nil {
		if response != nil {
			discardAndClose(response.Body)
		}
		if signed {
			return nil, boundaryError(codePaidOutcomeUnknown)
		}
		return nil, boundaryError(codePaymentUnavailable)
	}
	if response == nil {
		if signed {
			return nil, boundaryError(codePaidOutcomeUnknown)
		}
		return nil, boundaryError(codePaymentUnavailable)
	}
	if err := guard.validateResponse(response, payment, signed); err != nil {
		discardAndClose(response.Body)
		return nil, err
	}
	return response, nil
}

func (guard *paymentRoundTripper) validateRequest(
	request *http.Request,
) (x402wire.Payment, bool, error) {
	if request == nil || request.Method != http.MethodGet ||
		request.URL == nil || request.URL.String() != guard.targetURL ||
		request.Host != "" && request.Host != request.URL.Host ||
		len(request.Header.Values("Cookie")) != 0 ||
		len(request.Header.Values("Authorization")) != 0 ||
		len(request.Header.Values("X-API-Key")) != 0 ||
		len(request.Header.Values("X-Payment")) != 0 ||
		len(request.Header.Values(x402wire.PaymentRequiredHeader)) != 0 ||
		len(request.Header.Values(x402wire.PaymentResponseHeader)) != 0 {
		return x402wire.Payment{}, false, boundaryError(codePaymentGuardFailed)
	}

	guard.mu.Lock()
	defer guard.mu.Unlock()
	values := request.Header.Values(x402wire.PaymentSignatureHeader)
	if len(values) == 0 {
		if guard.signedRequests != 0 || guard.unsignedRequests >= 2 {
			return x402wire.Payment{}, false, boundaryError(codePaymentGuardFailed)
		}
		guard.unsignedRequests++
		return x402wire.Payment{}, false, nil
	}
	if guard.signedRequests != 0 {
		return x402wire.Payment{}, true, boundaryError(codePaymentRetryBlocked)
	}
	if guard.unsignedRequests != 2 || !guard.hasReference {
		return x402wire.Payment{}, true, boundaryError(codePaymentGuardFailed)
	}
	payment, err := guard.codec.DecodePaymentSignature(request.Header)
	if err != nil ||
		guard.reference.Match(payment) != nil ||
		!strings.EqualFold(payment.Authorization().From, guard.expectedPayer) {
		return x402wire.Payment{}, true, boundaryError(codePaymentGuardFailed)
	}
	callDigest, callBytes, err := settlementCallDataBinding(payment)
	if err != nil {
		return x402wire.Payment{}, true, boundaryError(codePaymentGuardFailed)
	}
	guard.callDataDigest = callDigest
	guard.callDataBytes = callBytes
	guard.signedRequests++
	return payment, true, nil
}

func (guard *paymentRoundTripper) validateResponse(
	response *http.Response,
	_ x402wire.Payment,
	signed bool,
) error {
	if responseHeaderBytes(response.Header) > maxResponseHeaderBytes ||
		len(response.Header.Values("Set-Cookie")) != 0 ||
		len(response.Header.Values(x402wire.PaymentSignatureHeader)) != 0 {
		if signed {
			return boundaryError(codePaidOutcomeUnknown)
		}
		return boundaryError(codePaymentChallengeInvalid)
	}
	if response.StatusCode != http.StatusPaymentRequired {
		if len(response.Header.Values(x402wire.PaymentRequiredHeader)) != 0 {
			if signed {
				return boundaryError(codePaidOutcomeUnknown)
			}
			return boundaryError(codePaymentChallengeInvalid)
		}
		return nil
	}
	if len(response.Header.Values(x402wire.PaymentResponseHeader)) != 0 {
		if signed {
			return boundaryError(codePaidOutcomeUnknown)
		}
		return boundaryError(codePaymentChallengeInvalid)
	}
	body, err := readBoundedBody(response.Body, guard.maxChallengeBodyBytes)
	if err != nil {
		if signed {
			return boundaryError(codePaidOutcomeUnknown)
		}
		return boundaryError(codePaymentChallengeInvalid)
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	if !validPaymentRequiredBody(response.Header, body) {
		if signed {
			return boundaryError(codePaidOutcomeUnknown)
		}
		return boundaryError(codePaymentChallengeInvalid)
	}
	challenge, err := guard.codec.DecodePaymentRequired(response.Header)
	if err != nil {
		if signed {
			return boundaryError(codePaidOutcomeUnknown)
		}
		return boundaryError(codePaymentChallengeInvalid)
	}
	if !matchesExpectedChallenge(challenge, guard.expected) {
		if signed {
			return boundaryError(codePaidOutcomeUnknown)
		}
		if _, exists := guard.referenceRequirement(); exists {
			return boundaryError(codePaymentChallengeChanged)
		}
		return boundaryError(codePaymentChallengeInvalid)
	}

	guard.mu.Lock()
	defer guard.mu.Unlock()
	challengeHeader := response.Header.Values(x402wire.PaymentRequiredHeader)[0]
	if !guard.hasReference {
		if signed || guard.unsignedRequests != 1 {
			return boundaryError(codePaymentGuardFailed)
		}
		guard.reference = challenge
		guard.referenceHeader = challengeHeader
		guard.hasReference = true
		return nil
	}
	if challengeHeader != guard.referenceHeader ||
		challenge.RequirementDigest() != guard.reference.RequirementDigest() ||
		challenge.ResourceDigest() != guard.reference.ResourceDigest() {
		if signed {
			return boundaryError(codePaidOutcomeUnknown)
		}
		return boundaryError(codePaymentChallengeChanged)
	}
	return nil
}

func (guard *paymentRoundTripper) referenceRequirement() (x402wire.Requirement, bool) {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	return guard.reference, guard.hasReference
}

func (guard *paymentRoundTripper) paymentAttempted() bool {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	return guard.signedRequests != 0
}

func (guard *paymentRoundTripper) completedSinglePaymentAttempt() bool {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	return guard.unsignedRequests == 2 && guard.signedRequests == 1
}

func (guard *paymentRoundTripper) callDataBinding() (
	[sha256.Size]byte,
	int,
	bool,
) {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	return guard.callDataDigest,
		guard.callDataBytes,
		guard.signedRequests == 1 &&
			guard.callDataBytes > 0 &&
			!zeroDigest(guard.callDataDigest)
}

func matchesExpectedChallenge(
	actual x402wire.Requirement,
	expected x402wire.Requirement,
) bool {
	return actual.RequirementDigest() == expected.RequirementDigest() &&
		actual.ResourceDigest() == expected.ResourceDigest()
}

func matchesRequirementView(
	expected x402wire.Requirement,
	actual x402.PaymentRequirementsView,
) bool {
	if actual == nil {
		return false
	}
	extra := actual.GetExtra()
	name, nameOK := extra["name"].(string)
	version, versionOK := extra["version"].(string)
	if !nameOK || !versionOK || len(extra) != 2 {
		return false
	}
	candidate, err := x402wire.NewRequirement(x402wire.RequirementOptions{
		Network:            actual.GetNetwork(),
		Asset:              actual.GetAsset(),
		Amount:             actual.GetAmount(),
		PayTo:              actual.GetPayTo(),
		MaxTimeoutSeconds:  actual.GetMaxTimeoutSeconds(),
		AssetEIP712Name:    name,
		AssetEIP712Version: version,
		Resource:           expected.Resource(),
	})
	return err == nil &&
		candidate.RequirementDigest() == expected.RequirementDigest()
}

func settlementCallDataBinding(
	payment x402wire.Payment,
) ([sha256.Size]byte, int, error) {
	const expectedBytes = 4 + 9*32
	authorization := payment.Authorization()
	signature, err := hex.DecodeString(
		strings.TrimPrefix(authorization.Signature, "0x"),
	)
	if err != nil || len(signature) != 65 {
		return [sha256.Size]byte{}, 0,
			boundaryError(codePaymentGuardFailed)
	}
	v := signature[64]
	if v == 0 || v == 1 {
		v += 27
	}
	if v != 27 && v != 28 {
		return [sha256.Size]byte{}, 0,
			boundaryError(codePaymentGuardFailed)
	}
	var (
		r     [32]byte
		s     [32]byte
		nonce [32]byte
	)
	copy(r[:], signature[:32])
	copy(s[:], signature[32:64])
	nonceBytes, err := hex.DecodeString(
		strings.TrimPrefix(authorization.Nonce, "0x"),
	)
	if err != nil || len(nonceBytes) != len(nonce) {
		return [sha256.Size]byte{}, 0,
			boundaryError(codePaymentGuardFailed)
	}
	copy(nonce[:], nonceBytes)
	value, valueOK := new(big.Int).SetString(authorization.Value, 10)
	validAfter, afterOK := new(big.Int).SetString(
		authorization.ValidAfter,
		10,
	)
	validBefore, beforeOK := new(big.Int).SetString(
		authorization.ValidBefore,
		10,
	)
	if !valueOK || !afterOK || !beforeOK {
		return [sha256.Size]byte{}, 0,
			boundaryError(codePaymentGuardFailed)
	}
	contractABI, err := abi.JSON(
		bytes.NewReader(x402evm.TransferWithAuthorizationVRSABI),
	)
	if err != nil {
		return [sha256.Size]byte{}, 0,
			boundaryError(codePaymentGuardFailed)
	}
	callData, err := contractABI.Pack(
		x402evm.FunctionTransferWithAuthorization,
		common.HexToAddress(authorization.From),
		common.HexToAddress(authorization.To),
		value,
		validAfter,
		validBefore,
		nonce,
		v,
		r,
		s,
	)
	if err != nil || len(callData) != expectedBytes {
		return [sha256.Size]byte{}, 0,
			boundaryError(codePaymentGuardFailed)
	}
	return sha256.Sum256(callData), len(callData), nil
}

func validSettlement(
	settlement x402.SettleResponse,
	expected x402wire.Requirement,
	expectedPayer string,
) bool {
	requirement := expected.SDK()
	return settlement.Success &&
		settlement.Payer == expectedPayer &&
		string(settlement.Network) == requirement.Network &&
		(settlement.Amount == "" || settlement.Amount == requirement.Amount)
}

func stablePaymentError(err error, paid bool) error {
	if paid {
		return boundaryError(codePaidOutcomeUnknown)
	}
	if stable, ok := errors.AsType[*BoundaryError](err); ok {
		return stable
	}
	return boundaryError(codePaymentSigningFailed)
}

func canonicalHTTPSURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil &&
		parsed.Scheme == "https" &&
		parsed.Host != "" &&
		parsed.Hostname() != "" &&
		parsed.User == nil &&
		parsed.Fragment == "" &&
		parsed.Opaque == "" &&
		!parsed.ForceQuery &&
		parsed.String() == raw
}

func canonicalEVMAddress(value string) (string, bool) {
	if len(value) != 42 || !strings.HasPrefix(value, "0x") {
		return "", false
	}
	if _, err := hex.DecodeString(value[2:]); err != nil {
		return "", false
	}
	return strings.ToLower(value), true
}

func responseHeaderBytes(header http.Header) int64 {
	var size int64
	for name, values := range header {
		size += int64(len(name) + 2)
		for _, value := range values {
			size += int64(len(value) + 2)
			if size > maxResponseHeaderBytes {
				return size
			}
		}
	}
	return size
}

func readBoundedBody(body io.ReadCloser, limit int64) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	defer body.Close() //nolint:errcheck
	content, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil || int64(len(content)) > limit {
		return nil, boundaryError(codePaymentChallengeInvalid)
	}
	return content, nil
}

func consumeChallengeBody(body io.ReadCloser, limit int64) error {
	content, err := readBoundedBody(body, limit)
	if err != nil || int64(len(content)) > limit {
		return boundaryError(codePaymentChallengeInvalid)
	}
	return nil
}

func validNativeJSONContentType(header http.Header) bool {
	values := header.Values("Content-Type")
	if len(values) != 1 {
		return false
	}
	mediaType, parameters, err := mime.ParseMediaType(values[0])
	if err != nil || mediaType != testnetResourceMimeType {
		return false
	}
	for name, value := range parameters {
		if !strings.EqualFold(name, "charset") ||
			!strings.EqualFold(value, "utf-8") {
			return false
		}
	}
	return true
}

func validPaymentRequiredBody(header http.Header, content []byte) bool {
	if !validNativeJSONContentType(header) || len(content) == 0 ||
		jsonstrict.Validate(content, jsonstrict.Limits{
			MaxDepth:         maxChallengeJSONDepth,
			MaxNodes:         maxChallengeJSONNodes,
			SafeIntegersOnly: true,
		}) != nil {
		return false
	}
	var envelope gen.ErrorResponse
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF &&
		envelope.Error.Code == "payment_required" &&
		envelope.Error.Message == "payment is required" &&
		envelope.Error.Details == nil &&
		strings.TrimSpace(envelope.Error.RequestId) != "" &&
		len(envelope.Error.RequestId) <= 128
}

func validateAndDigestNativeBody(
	body io.Reader,
	limit int64,
) (int64, [sha256.Size]byte, error) {
	content, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil || int64(len(content)) > limit || len(content) == 0 ||
		jsonstrict.Validate(content, jsonstrict.Limits{
			MaxDepth:         maxFinalJSONDepth,
			MaxNodes:         maxFinalJSONNodes,
			SafeIntegersOnly: true,
		}) != nil {
		return 0, [sha256.Size]byte{}, boundaryError(codePaidOutcomeUnknown)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
		Meta gen.Meta        `json:"meta"`
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		return 0, [sha256.Size]byte{}, boundaryError(codePaidOutcomeUnknown)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF ||
		len(bytes.TrimSpace(envelope.Data)) == 0 ||
		bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) ||
		envelope.Meta.RequestId == "" ||
		string(envelope.Meta.ChainId) !=
			strconv.FormatUint(baseSepoliaChainID, 10) {
		return 0, [sha256.Size]byte{}, boundaryError(codePaidOutcomeUnknown)
	}
	return int64(len(content)), sha256.Sum256(content), nil
}

func discardAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	discardBody(body)
	_ = body.Close()
}

func discardBody(body io.Reader) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 4096))
}
