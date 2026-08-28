package x402wire

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/jsonstrict"
	x402 "github.com/x402-foundation/x402/go/v2"
	"golang.org/x/net/http/httpguts"
)

const (
	maximumFacilitatorRequestBytes = 64 << 10
	maximumFacilitatorHeaders      = 32
	maximumFacilitatorHeaderBytes  = 16 << 10
	minimumFacilitatorTimeout      = 100 * time.Millisecond
	maximumFacilitatorTimeout      = time.Minute
)

var facilitatorJSONLimits = jsonstrict.Limits{
	MaxDepth:         16,
	MaxNodes:         4096,
	SafeIntegersOnly: true,
}

type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

// ClientOptions defines a fixed facilitator origin and its explicit egress
// policy. RootCAs, Resolver, and Dialer exist for deterministic tests and
// private PKI; TLS verification itself cannot be disabled. UnsafeAllowHTTP is
// an explicit plaintext escape hatch used only by the repository-owned local
// Compose fixture and never relaxes DNS/CIDR pinning.
type ClientOptions struct {
	BaseURL          string
	UnsafeAllowHTTP  bool
	AllowedCIDRs     []string
	Timeout          time.Duration
	MaxResponseBytes int64
	Headers          map[string]string
	RootCAs          *x509.CertPool
	Resolver         Resolver
	Dialer           Dialer
}

// Client is an x402 v2 facilitator client with no proxy, redirects, retries, or
// unbounded reads. Every new connection re-resolves and validates every DNS
// answer against the configured CIDRs before dialing.
type Client struct {
	baseURL          string
	host             string
	port             string
	allowedCIDRs     []netip.Prefix
	maxResponseBytes int64
	headers          http.Header
	resolver         Resolver
	dialer           Dialer
	httpClient       *http.Client
	originDigest     [sha256.Size]byte
}

var _ x402.FacilitatorClient = (*Client)(nil)

// NewClient validates the immutable network policy and constructs a restricted
// facilitator client.
func NewClient(options ClientOptions) (*Client, error) {
	parsed, err := parseFacilitatorOrigin(options.BaseURL, options.UnsafeAllowHTTP)
	if err != nil ||
		options.Timeout < minimumFacilitatorTimeout ||
		options.Timeout > maximumFacilitatorTimeout ||
		options.MaxResponseBytes <= 0 || options.MaxResponseBytes > AbsoluteMaxResponseBytes {
		return nil, boundaryError(PhaseSupported, FailureInvalid, CodeFacilitatorConfigInvalid)
	}
	allowed, err := parseAllowedCIDRs(options.AllowedCIDRs)
	if err != nil {
		return nil, boundaryError(PhaseSupported, FailureInvalid, CodeFacilitatorConfigInvalid)
	}
	headers, err := validatedFacilitatorHeaders(options.Headers)
	if err != nil {
		return nil, boundaryError(PhaseSupported, FailureInvalid, CodeFacilitatorConfigInvalid)
	}

	resolver := options.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := options.Dialer
	if dialer == nil {
		dialer = &net.Dialer{Timeout: options.Timeout, KeepAlive: 30 * time.Second}
	}

	baseURL := strings.TrimSuffix(parsed.String(), "/")
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "http" {
			port = "80"
		} else {
			port = "443"
		}
	}
	client := &Client{
		baseURL:          baseURL,
		host:             parsed.Hostname(),
		port:             port,
		allowedCIDRs:     allowed,
		maxResponseBytes: options.MaxResponseBytes,
		headers:          headers,
		resolver:         resolver,
		dialer:           dialer,
		originDigest:     sha256.Sum256([]byte(baseURL)),
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = client.safeDial
	transport.DisableCompression = true
	transport.MaxIdleConns = 8
	transport.MaxIdleConnsPerHost = 2
	transport.MaxConnsPerHost = 8
	transport.IdleConnTimeout = 30 * time.Second
	transport.ResponseHeaderTimeout = options.Timeout
	transport.TLSHandshakeTimeout = options.Timeout
	transport.ExpectContinueTimeout = time.Second
	transport.MaxResponseHeaderBytes = 32 << 10
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		transport.TLSClientConfig.MinVersion = tls.VersionTLS12
	}
	if options.RootCAs != nil {
		transport.TLSClientConfig.RootCAs = options.RootCAs.Clone()
	}

	client.httpClient = &http.Client{
		Transport: transport,
		Timeout:   options.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("x402_redirect_rejected")
		},
	}
	return client, nil
}

// OriginDigest is the non-secret SHA-256 identity of the fixed facilitator
// origin, suitable for persistent request binding.
func (c *Client) OriginDigest() [sha256.Size]byte {
	if c == nil {
		return [sha256.Size]byte{}
	}
	return c.originDigest
}

// VerifyPayment validates the complete local binding before calling /verify.
func (c *Client) VerifyPayment(
	ctx context.Context,
	payment Payment,
	requirement Requirement,
) (*x402.VerifyResponse, error) {
	if err := requirement.Match(payment); err != nil {
		return nil, err
	}
	return c.Verify(ctx, payment.PayloadJSON(), requirement.JSON())
}

// SettlePayment validates the complete local binding before calling /settle.
func (c *Client) SettlePayment(
	ctx context.Context,
	payment Payment,
	requirement Requirement,
) (*x402.SettleResponse, error) {
	if err := requirement.Match(payment); err != nil {
		return nil, err
	}
	return c.Settle(ctx, payment.PayloadJSON(), requirement.JSON())
}

// Verify implements x402.FacilitatorClient for the v2 exact-EVM subset.
func (c *Client) Verify(
	ctx context.Context,
	payloadBytes []byte,
	requirementsBytes []byte,
) (*x402.VerifyResponse, error) {
	payment, requirement, body, err := prepareFacilitatorRequest(
		PhaseVerify,
		payloadBytes,
		requirementsBytes,
	)
	if err != nil {
		return nil, err
	}
	status, responseBody, err := c.do(ctx, PhaseVerify, http.MethodPost, "/verify", body)
	if err != nil {
		return nil, err
	}
	return parseVerifyResponse(status, responseBody, payment.Payer(), requirement)
}

// Settle implements x402.FacilitatorClient for the v2 exact-EVM subset.
func (c *Client) Settle(
	ctx context.Context,
	payloadBytes []byte,
	requirementsBytes []byte,
) (*x402.SettleResponse, error) {
	payment, requirement, body, err := prepareFacilitatorRequest(
		PhaseSettle,
		payloadBytes,
		requirementsBytes,
	)
	if err != nil {
		return nil, err
	}
	status, responseBody, err := c.do(ctx, PhaseSettle, http.MethodPost, "/settle", body)
	if err != nil {
		return nil, err
	}
	return parseSettleResponse(status, responseBody, payment.Payer(), requirement)
}

// GetSupported implements x402.FacilitatorClient without automatic retries.
func (c *Client) GetSupported(ctx context.Context) (x402.SupportedResponse, error) {
	status, responseBody, err := c.do(ctx, PhaseSupported, http.MethodGet, "/supported", nil)
	if err != nil {
		return x402.SupportedResponse{}, err
	}
	if status != http.StatusOK {
		return x402.SupportedResponse{}, boundaryError(
			PhaseSupported,
			FailureUnavailable,
			CodeFacilitatorUnavailable,
		)
	}
	return parseSupportedResponse(responseBody)
}

// CheckSupported is the doctor/startup boundary for one configured exact-EVM
// network. A syntactically valid /supported response that omits the configured
// network is an explicit capability mismatch, not permission to attempt paid
// requests optimistically.
func (c *Client) CheckSupported(ctx context.Context, network string) error {
	normalized, ok := canonicalEVMNetwork(network)
	if !ok || normalized != network {
		return boundaryError(
			PhaseSupported,
			FailureInvalid,
			CodeFacilitatorConfigInvalid,
		)
	}
	response, err := c.GetSupported(ctx)
	if err != nil {
		return err
	}
	for _, kind := range response.Kinds {
		if kind.X402Version == X402Version &&
			kind.Scheme == ExactScheme &&
			kind.Network == normalized {
			return nil
		}
	}
	return boundaryError(
		PhaseSupported,
		FailureUnavailable,
		CodeFacilitatorUnsupported,
	)
}

type facilitatorRequestWire struct {
	X402Version         int             `json:"x402Version"`
	PaymentPayload      json.RawMessage `json:"paymentPayload"`
	PaymentRequirements json.RawMessage `json:"paymentRequirements"`
}

func prepareFacilitatorRequest(
	phase Phase,
	payloadBytes []byte,
	requirementsBytes []byte,
) (Payment, paymentRequirementWire, []byte, error) {
	if len(payloadBytes) == 0 || len(payloadBytes) > AbsoluteMaxHeaderBytes ||
		len(requirementsBytes) == 0 || len(requirementsBytes) > AbsoluteMaxHeaderBytes {
		return Payment{}, paymentRequirementWire{}, nil,
			boundaryError(phase, FailureInvalid, CodeHeaderOversized)
	}
	payment, err := decodePaymentJSON(payloadBytes)
	if err != nil {
		return Payment{}, paymentRequirementWire{}, nil, err
	}
	requirement, canonicalRequirement, err := decodeRequirementJSON(requirementsBytes)
	if err != nil {
		return Payment{}, paymentRequirementWire{}, nil, err
	}
	accepted, err := requirementFromSDK(payment.payload.Accepted, PhaseHeader)
	if err != nil || !reflect.DeepEqual(accepted, requirement) ||
		!paymentMatchesRequirementBinding(payment, requirement) {
		return Payment{}, paymentRequirementWire{}, nil,
			boundaryError(PhaseHeader, FailureInvalid, CodePaymentMismatch)
	}
	body, err := json.Marshal(facilitatorRequestWire{
		X402Version:         X402Version,
		PaymentPayload:      payment.PayloadJSON(),
		PaymentRequirements: canonicalRequirement,
	})
	if err != nil || len(body) > maximumFacilitatorRequestBytes {
		return Payment{}, paymentRequirementWire{}, nil,
			boundaryError(phase, FailureInvalid, CodeHeaderOversized)
	}
	return payment, requirement, body, nil
}

func decodeRequirementJSON(data []byte) (paymentRequirementWire, []byte, error) {
	if err := jsonstrict.Validate(data, hostileJSONLimits); err != nil {
		return paymentRequirementWire{}, nil,
			boundaryError(PhaseRequirement, FailureInvalid, CodeRequirementInvalid)
	}
	var wire paymentRequirementWire
	if err := decodeTypedJSON(data, &wire); err != nil {
		return paymentRequirementWire{}, nil,
			boundaryError(PhaseRequirement, FailureInvalid, CodeRequirementInvalid)
	}
	normalized, err := normalizeRequirement(wire, PhaseRequirement)
	if err != nil {
		return paymentRequirementWire{}, nil, err
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return paymentRequirementWire{}, nil,
			boundaryError(PhaseRequirement, FailureInvalid, CodeRequirementInvalid)
	}
	return normalized, canonical, nil
}

func (c *Client) do(
	ctx context.Context,
	phase Phase,
	method string,
	path string,
	body []byte,
) (int, []byte, error) {
	if c == nil || c.httpClient == nil || ctx == nil {
		return 0, nil, transportFailure(phase, CodeFacilitatorUnavailable)
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, nil, transportFailure(phase, CodeFacilitatorUnavailable)
	}
	request.Header = c.headers.Clone()
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return 0, nil, transportFailure(phase, CodeFacilitatorUnavailable)
	}
	defer response.Body.Close() //nolint:errcheck

	if response.ContentLength > c.maxResponseBytes || response.Header.Get("Content-Encoding") != "" {
		return 0, nil, transportFailure(phase, CodeFacilitatorResponseInvalid)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return 0, nil, transportFailure(phase, CodeFacilitatorResponseInvalid)
	}
	limited := io.LimitReader(response.Body, c.maxResponseBytes+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil || int64(len(responseBody)) > c.maxResponseBytes || len(responseBody) == 0 {
		return 0, nil, transportFailure(phase, CodeFacilitatorResponseInvalid)
	}
	if err := jsonstrict.Validate(responseBody, facilitatorJSONLimits); err != nil {
		return 0, nil, transportFailure(phase, CodeFacilitatorResponseInvalid)
	}
	return response.StatusCode, responseBody, nil
}

func transportFailure(phase Phase, code string) error {
	if phase == PhaseSettle {
		return boundaryError(phase, FailureSettlementUnknown, CodeSettlementUnknown)
	}
	return boundaryError(phase, FailureUnavailable, code)
}

type verifyResponseWire struct {
	IsValid        *bool                      `json:"isValid"`
	InvalidReason  string                     `json:"invalidReason,omitempty"`
	InvalidMessage string                     `json:"invalidMessage,omitempty"`
	Payer          string                     `json:"payer,omitempty"`
	Extensions     map[string]json.RawMessage `json:"extensions,omitempty"`
	Extra          map[string]json.RawMessage `json:"extra,omitempty"`
}

func parseVerifyResponse(
	status int,
	data []byte,
	expectedPayer string,
	requirement paymentRequirementWire,
) (*x402.VerifyResponse, error) {
	_ = requirement
	if status >= http.StatusInternalServerError ||
		status == http.StatusRequestTimeout ||
		status == http.StatusTooEarly ||
		status == http.StatusTooManyRequests ||
		status == http.StatusUnauthorized ||
		status == http.StatusForbidden {
		return nil, boundaryError(PhaseVerify, FailureUnavailable, CodeFacilitatorUnavailable)
	}
	var wire verifyResponseWire
	if err := decodeTypedJSON(data, &wire); err != nil || wire.IsValid == nil ||
		len(wire.Extensions) != 0 || len(wire.Extra) != 0 {
		return nil, boundaryError(PhaseVerify, FailureUnavailable, CodeFacilitatorResponseInvalid)
	}
	if status != http.StatusOK || !*wire.IsValid {
		if !*wire.IsValid && boundedText(wire.InvalidReason, 1, 128) {
			payer, _ := canonicalAddress(wire.Payer)
			response := &x402.VerifyResponse{
				IsValid:       false,
				InvalidReason: CodeFacilitatorRejected,
				Payer:         payer,
			}
			return response, boundaryError(PhaseVerify, FailureRejected, CodeFacilitatorRejected)
		}
		return nil, boundaryError(PhaseVerify, FailureUnavailable, CodeFacilitatorResponseInvalid)
	}
	if wire.InvalidReason != "" || wire.InvalidMessage != "" {
		return nil, boundaryError(PhaseVerify, FailureUnavailable, CodeFacilitatorResponseInvalid)
	}
	payer, ok := canonicalAddress(wire.Payer)
	if !ok || payer != expectedPayer {
		return nil, boundaryError(PhaseVerify, FailureUnavailable, CodeFacilitatorResponseInvalid)
	}
	return &x402.VerifyResponse{IsValid: true, Payer: payer}, nil
}

type settleResponseWire struct {
	Success      *bool                      `json:"success"`
	ErrorReason  string                     `json:"errorReason,omitempty"`
	ErrorMessage string                     `json:"errorMessage,omitempty"`
	Payer        string                     `json:"payer,omitempty"`
	Transaction  *string                    `json:"transaction"`
	Network      *string                    `json:"network"`
	Amount       string                     `json:"amount,omitempty"`
	Extensions   map[string]json.RawMessage `json:"extensions,omitempty"`
	Extra        map[string]json.RawMessage `json:"extra,omitempty"`
}

func parseSettleResponse(
	status int,
	data []byte,
	expectedPayer string,
	requirement paymentRequirementWire,
) (*x402.SettleResponse, error) {
	var wire settleResponseWire
	if err := decodeTypedJSON(data, &wire); err != nil || wire.Success == nil ||
		len(wire.Extensions) != 0 || len(wire.Extra) != 0 {
		return nil, boundaryError(PhaseSettle, FailureSettlementUnknown, CodeSettlementUnknown)
	}
	if status != http.StatusOK || !*wire.Success {
		if *wire.Success || wire.Transaction == nil || wire.Network == nil ||
			!boundedText(wire.ErrorReason, 1, 128) ||
			!boundedText(wire.ErrorMessage, 0, 512) {
			return nil, boundaryError(PhaseSettle, FailureSettlementUnknown, CodeSettlementUnknown)
		}
		network, networkOK := canonicalEVMNetwork(*wire.Network)
		if !networkOK || network != requirement.Network {
			return nil, boundaryError(PhaseSettle, FailureSettlementUnknown, CodeSettlementUnknown)
		}
		payer := ""
		if wire.Payer != "" {
			var payerOK bool
			payer, payerOK = canonicalAddress(wire.Payer)
			if !payerOK || payer != expectedPayer {
				return nil, boundaryError(PhaseSettle, FailureSettlementUnknown, CodeSettlementUnknown)
			}
		}
		amount := ""
		if wire.Amount != "" {
			var amountOK bool
			amount, amountOK = canonicalUint256(wire.Amount, true)
			if !amountOK || amount != requirement.Amount {
				return nil, boundaryError(
					PhaseSettle,
					FailureSettlementUnknown,
					CodeSettlementUnknown,
				)
			}
		}
		if wire.ErrorReason == "settlement_pending" {
			if payer == "" {
				return nil, boundaryError(PhaseSettle, FailureSettlementUnknown, CodeSettlementUnknown)
			}
			transaction, transactionOK := canonicalFixedHex(*wire.Transaction)
			if !transactionOK {
				return nil, boundaryError(PhaseSettle, FailureSettlementUnknown, CodeSettlementUnknown)
			}
			response := &x402.SettleResponse{
				Success: false, ErrorReason: "settlement_pending", Payer: payer,
				Transaction: transaction, Network: x402.Network(network), Amount: amount,
			}
			return response, boundaryError(PhaseSettle, FailureSettlementPending, CodeSettlementPending)
		}
		if status >= http.StatusInternalServerError || status == http.StatusRequestTimeout ||
			status == http.StatusTooEarly || status == http.StatusTooManyRequests {
			return nil, boundaryError(PhaseSettle, FailureSettlementUnknown, CodeSettlementUnknown)
		}
		if *wire.Transaction != "" {
			return nil, boundaryError(PhaseSettle, FailureSettlementUnknown, CodeSettlementUnknown)
		}
		response := &x402.SettleResponse{
			Success:     false,
			ErrorReason: CodeFacilitatorRejected,
			Payer:       payer,
			Transaction: "",
			Network:     x402.Network(network),
			Amount:      amount,
		}
		return response, boundaryError(PhaseSettle, FailureRejected, CodeFacilitatorRejected)
	}
	if wire.ErrorReason != "" || wire.ErrorMessage != "" ||
		wire.Transaction == nil || wire.Network == nil {
		return nil, boundaryError(PhaseSettle, FailureSettlementUnknown, CodeSettlementUnknown)
	}
	payer, payerOK := canonicalAddress(wire.Payer)
	transaction, transactionOK := canonicalFixedHex(*wire.Transaction)
	network, networkOK := canonicalEVMNetwork(*wire.Network)
	if !payerOK || payer != expectedPayer || !transactionOK ||
		!networkOK || network != requirement.Network {
		return nil, boundaryError(PhaseSettle, FailureSettlementUnknown, CodeSettlementUnknown)
	}
	amount := requirement.Amount
	if wire.Amount != "" {
		var amountOK bool
		amount, amountOK = canonicalUint256(wire.Amount, true)
		if !amountOK || amount != requirement.Amount {
			return nil, boundaryError(PhaseSettle, FailureSettlementUnknown, CodeSettlementUnknown)
		}
	}
	return &x402.SettleResponse{
		Success:     true,
		Payer:       payer,
		Transaction: transaction,
		Network:     x402.Network(network),
		Amount:      amount,
	}, nil
}

func parseSupportedResponse(data []byte) (x402.SupportedResponse, error) {
	var response x402.SupportedResponse
	if err := decodeTypedJSON(data, &response); err != nil ||
		len(response.Kinds) == 0 || len(response.Kinds) > 128 ||
		len(response.Extensions) > 128 || len(response.Signers) > 32 {
		return x402.SupportedResponse{}, boundaryError(
			PhaseSupported,
			FailureUnavailable,
			CodeFacilitatorResponseInvalid,
		)
	}
	filtered := make([]x402.SupportedKind, 0, len(response.Kinds))
	for _, kind := range response.Kinds {
		network, ok := canonicalEVMNetwork(kind.Network)
		if kind.X402Version != X402Version || kind.Scheme != ExactScheme || !ok ||
			len(kind.Extra) > 32 {
			continue
		}
		kind.Network = network
		kind.Extra = cloneMap(kind.Extra)
		filtered = append(filtered, kind)
	}
	if len(filtered) == 0 {
		return x402.SupportedResponse{}, boundaryError(
			PhaseSupported,
			FailureUnavailable,
			CodeFacilitatorUnsupported,
		)
	}
	for _, extension := range response.Extensions {
		if !boundedText(extension, 1, 64) {
			return x402.SupportedResponse{}, boundaryError(
				PhaseSupported,
				FailureUnavailable,
				CodeFacilitatorResponseInvalid,
			)
		}
	}
	signers := make(map[string][]string, len(response.Signers))
	for family, values := range response.Signers {
		if !boundedText(family, 1, 64) || len(values) > 128 {
			return x402.SupportedResponse{}, boundaryError(
				PhaseSupported,
				FailureUnavailable,
				CodeFacilitatorResponseInvalid,
			)
		}
		copied := make([]string, len(values))
		for index, value := range values {
			if !boundedText(value, 1, 256) {
				return x402.SupportedResponse{}, boundaryError(
					PhaseSupported,
					FailureUnavailable,
					CodeFacilitatorResponseInvalid,
				)
			}
			copied[index] = value
		}
		signers[family] = copied
	}
	return x402.SupportedResponse{
		Kinds:      filtered,
		Extensions: append([]string(nil), response.Extensions...),
		Signers:    signers,
	}, nil
}

func parseFacilitatorOrigin(raw string, unsafeAllowHTTP bool) (*url.URL, error) {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return nil, errors.New("invalid origin")
	}
	parsed, err := url.Parse(raw)
	validScheme := parsed != nil && (parsed.Scheme == "https" || unsafeAllowHTTP && parsed.Scheme == "http")
	if err != nil || parsed == nil || !validScheme ||
		parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		parsed.RawPath != "" || parsed.Path != "" && parsed.Path != "/" ||
		strings.Contains(parsed.Hostname(), "%") {
		return nil, errors.New("invalid origin")
	}
	return parsed, nil
}

func parseAllowedCIDRs(rawValues []string) ([]netip.Prefix, error) {
	if len(rawValues) == 0 {
		return nil, errors.New("missing CIDRs")
	}
	prefixes := make([]netip.Prefix, 0, len(rawValues))
	seen := make(map[netip.Prefix]struct{}, len(rawValues))
	for _, raw := range rawValues {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || !prefix.IsValid() || prefix.Masked() != prefix ||
			prefix.String() != raw {
			return nil, errors.New("invalid CIDR")
		}
		if _, exists := seen[prefix]; exists {
			return nil, errors.New("duplicate CIDR")
		}
		seen[prefix] = struct{}{}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func validatedFacilitatorHeaders(values map[string]string) (http.Header, error) {
	if len(values) > maximumFacilitatorHeaders {
		return nil, errors.New("too many headers")
	}
	headers := make(http.Header, len(values))
	totalBytes := 0
	for name, value := range values {
		canonical := http.CanonicalHeaderKey(name)
		totalBytes += len(name) + len(value)
		if canonical == "" || canonical != name ||
			!httpguts.ValidHeaderFieldName(name) ||
			!httpguts.ValidHeaderFieldValue(value) ||
			value == "" || forbiddenFacilitatorHeader(canonical) ||
			totalBytes > maximumFacilitatorHeaderBytes {
			return nil, errors.New("invalid header")
		}
		headers.Set(canonical, value)
	}
	return headers, nil
}

func forbiddenFacilitatorHeader(name string) bool {
	switch name {
	case "Host", "Connection", "Content-Length", "Transfer-Encoding",
		"Proxy-Authorization", "Proxy-Authenticate", "Trailer", "Upgrade":
		return true
	default:
		return strings.HasPrefix(strings.ToLower(name), "payment-")
	}
}

func (c *Client) safeDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || !strings.EqualFold(host, c.host) || port != c.port {
		return nil, errors.New("x402_dial_rejected")
	}

	addresses, err := c.resolve(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("x402_resolve_failed")
	}
	for _, candidate := range addresses {
		if !c.allowed(candidate) {
			return nil, errors.New("x402_dial_rejected")
		}
	}
	for _, candidate := range addresses {
		connection, dialErr := c.dialer.DialContext(
			ctx,
			network,
			net.JoinHostPort(candidate.String(), port),
		)
		if dialErr == nil {
			return connection, nil
		}
	}
	return nil, errors.New("x402_dial_failed")
}

func (c *Client) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if literal, err := netip.ParseAddr(host); err == nil {
		if literal.Zone() != "" {
			return nil, errors.New("zoned IP is not allowed")
		}
		return []netip.Addr{literal.Unmap()}, nil
	}
	resolved, err := c.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	addresses := make([]netip.Addr, 0, len(resolved))
	for _, candidate := range resolved {
		if candidate.Zone != "" {
			return nil, errors.New("zoned IP is not allowed")
		}
		address, ok := netip.AddrFromSlice(candidate.IP)
		if !ok {
			return nil, errors.New("invalid IP")
		}
		addresses = append(addresses, address.Unmap())
	}
	return addresses, nil
}

func (c *Client) allowed(address netip.Addr) bool {
	for _, prefix := range c.allowedCIDRs {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
