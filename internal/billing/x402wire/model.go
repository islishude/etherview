package x402wire

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/netip"
	"net/url"
	"strings"
	"unicode/utf8"

	x402 "github.com/x402-foundation/x402/go/v2"
)

const (
	X402Version                    = 2
	ExactScheme                    = "exact"
	PaymentSignatureHeader         = "Payment-Signature"
	PaymentRequiredHeader          = "Payment-Required"
	PaymentResponseHeader          = "Payment-Response"
	DefaultMaxHeaderBytes          = 16 << 10
	AbsoluteMaxHeaderBytes         = 16 << 10
	DefaultMaxResponseBytes  int64 = 1 << 20
	AbsoluteMaxResponseBytes int64 = 1 << 20
)

var maxUint256 = func() *big.Int {
	value := new(big.Int).Lsh(big.NewInt(1), 256)
	return value.Sub(value, big.NewInt(1))
}()

// Authorization is the normalized EIP-3009 authorization carried by an exact
// EVM x402 payment. Decimal and hexadecimal fields are canonicalized.
type Authorization struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Value       string `json:"value"`
	ValidAfter  string `json:"validAfter"`
	ValidBefore string `json:"validBefore"`
	Nonce       string `json:"nonce"`
	Signature   string `json:"signature"`
}

// Payment is a strictly decoded and normalized v2 exact-EVM payment.
type Payment struct {
	payload       x402.PaymentPayload
	authorization Authorization
	payloadJSON   []byte
}

// Payload returns an independent SDK value suitable for the facilitator wire.
func (p Payment) Payload() x402.PaymentPayload {
	return clonePayload(p.payload)
}

// PayloadJSON returns the canonical normalized payment payload.
func (p Payment) PayloadJSON() []byte {
	return append([]byte(nil), p.payloadJSON...)
}

// Authorization returns the normalized one-time authorization.
func (p Payment) Authorization() Authorization {
	return p.authorization
}

type paymentPayloadWire struct {
	X402Version int                        `json:"x402Version"`
	Payload     exactPayloadWire           `json:"payload"`
	Accepted    paymentRequirementWire     `json:"accepted"`
	Resource    *resourceWire              `json:"resource,omitempty"`
	Extensions  map[string]json.RawMessage `json:"extensions,omitempty"`
}

type exactPayloadWire struct {
	Signature     string            `json:"signature"`
	Authorization authorizationWire `json:"authorization"`
}

type authorizationWire struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Value       string `json:"value"`
	ValidAfter  string `json:"validAfter"`
	ValidBefore string `json:"validBefore"`
	Nonce       string `json:"nonce"`
}

type paymentRequirementWire struct {
	Scheme            string          `json:"scheme"`
	Network           string          `json:"network"`
	Asset             string          `json:"asset"`
	Amount            string          `json:"amount"`
	PayTo             string          `json:"payTo"`
	MaxTimeoutSeconds int             `json:"maxTimeoutSeconds"`
	Extra             *assetExtraWire `json:"extra,omitempty"`
}

type assetExtraWire struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type resourceWire struct {
	URL         string   `json:"url"`
	Description string   `json:"description,omitempty"`
	MimeType    string   `json:"mimeType,omitempty"`
	ServiceName string   `json:"serviceName,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	IconURL     string   `json:"iconUrl,omitempty"`
}

type canonicalPaymentWire struct {
	X402Version int                    `json:"x402Version"`
	Payload     canonicalExactPayload  `json:"payload"`
	Accepted    paymentRequirementWire `json:"accepted"`
	Resource    *resourceWire          `json:"resource,omitempty"`
}

type canonicalExactPayload struct {
	Signature     string            `json:"signature"`
	Authorization authorizationWire `json:"authorization"`
}

type paymentRequiredWire struct {
	X402Version int                        `json:"x402Version"`
	Error       string                     `json:"error,omitempty"`
	Resource    *resourceWire              `json:"resource,omitempty"`
	Accepts     []paymentRequirementWire   `json:"accepts"`
	Extensions  map[string]json.RawMessage `json:"extensions,omitempty"`
}

func normalizeAuthorization(payload exactPayloadWire) (Authorization, error) {
	from, ok := canonicalAddress(payload.Authorization.From)
	if !ok {
		return Authorization{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	to, ok := canonicalAddress(payload.Authorization.To)
	if !ok {
		return Authorization{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	value, ok := canonicalUint256(payload.Authorization.Value, true)
	if !ok {
		return Authorization{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	validAfter, ok := canonicalUint256(payload.Authorization.ValidAfter, false)
	if !ok {
		return Authorization{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	validBefore, ok := canonicalUint256(payload.Authorization.ValidBefore, false)
	if !ok {
		return Authorization{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	afterInt, _ := new(big.Int).SetString(validAfter, 10)
	beforeInt, _ := new(big.Int).SetString(validBefore, 10)
	if beforeInt.Cmp(afterInt) <= 0 {
		return Authorization{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	nonce, ok := canonicalFixedHex(payload.Authorization.Nonce, 32)
	if !ok {
		return Authorization{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	signature, ok := canonicalVariableHex(payload.Signature)
	if !ok {
		return Authorization{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	return Authorization{
		From:        from,
		To:          to,
		Value:       value,
		ValidAfter:  validAfter,
		ValidBefore: validBefore,
		Nonce:       nonce,
		Signature:   signature,
	}, nil
}

func normalizeRequirement(wire paymentRequirementWire, phase Phase) (paymentRequirementWire, error) {
	if wire.Scheme != ExactScheme {
		return paymentRequirementWire{}, boundaryError(phase, FailureInvalid, CodePaymentUnsupported)
	}
	network, ok := canonicalEVMNetwork(wire.Network)
	if !ok {
		return paymentRequirementWire{}, boundaryError(phase, FailureInvalid, CodeRequirementInvalid)
	}
	asset, ok := canonicalAddress(wire.Asset)
	if !ok {
		return paymentRequirementWire{}, boundaryError(phase, FailureInvalid, CodeRequirementInvalid)
	}
	amount, ok := canonicalUint256(wire.Amount, true)
	if !ok {
		return paymentRequirementWire{}, boundaryError(phase, FailureInvalid, CodeRequirementInvalid)
	}
	payTo, ok := canonicalAddress(wire.PayTo)
	if !ok {
		return paymentRequirementWire{}, boundaryError(phase, FailureInvalid, CodeRequirementInvalid)
	}
	if wire.MaxTimeoutSeconds <= 0 || wire.MaxTimeoutSeconds > 300 || wire.Extra == nil {
		return paymentRequirementWire{}, boundaryError(phase, FailureInvalid, CodeRequirementInvalid)
	}
	if !boundedText(wire.Extra.Name, 1, 128) || !boundedText(wire.Extra.Version, 1, 32) {
		return paymentRequirementWire{}, boundaryError(phase, FailureInvalid, CodeRequirementInvalid)
	}
	return paymentRequirementWire{
		Scheme:            ExactScheme,
		Network:           network,
		Asset:             asset,
		Amount:            amount,
		PayTo:             payTo,
		MaxTimeoutSeconds: wire.MaxTimeoutSeconds,
		Extra: &assetExtraWire{
			Name:    wire.Extra.Name,
			Version: wire.Extra.Version,
		},
	}, nil
}

func canonicalEVMNetwork(value string) (string, bool) {
	const prefix = "eip155:"
	if !strings.HasPrefix(value, prefix) {
		return "", false
	}
	chainID, ok := canonicalUint256(strings.TrimPrefix(value, prefix), true)
	if !ok {
		return "", false
	}
	return prefix + chainID, true
}

func canonicalAddress(value string) (string, bool) {
	if len(value) != 42 || !strings.HasPrefix(value, "0x") {
		return "", false
	}
	if _, err := hex.DecodeString(value[2:]); err != nil {
		return "", false
	}
	return strings.ToLower(value), true
}

func canonicalFixedHex(value string, bytes int) (string, bool) {
	if len(value) != 2+bytes*2 || !strings.HasPrefix(value, "0x") {
		return "", false
	}
	if _, err := hex.DecodeString(value[2:]); err != nil {
		return "", false
	}
	return strings.ToLower(value), true
}

func canonicalVariableHex(value string) (string, bool) {
	if len(value) <= 2 || !strings.HasPrefix(value, "0x") || len(value)%2 != 0 {
		return "", false
	}
	if _, err := hex.DecodeString(value[2:]); err != nil {
		return "", false
	}
	return strings.ToLower(value), true
}

func canonicalUint256(value string, positive bool) (string, bool) {
	if value == "" || value == "0" && positive || len(value) > 1 && value[0] == '0' {
		return "", false
	}
	for index := range len(value) {
		if value[index] < '0' || value[index] > '9' {
			return "", false
		}
	}
	parsed := new(big.Int)
	if _, ok := parsed.SetString(value, 10); !ok || parsed.Sign() < 0 || parsed.Cmp(maxUint256) > 0 {
		return "", false
	}
	return parsed.String(), true
}

func normalizeResource(resource resourceWire, phase Phase) (resourceWire, error) {
	if !canonicalPublicURL(resource.URL, 4096) ||
		!boundedText(resource.Description, 0, 512) ||
		!boundedText(resource.MimeType, 0, 128) ||
		resource.ServiceName != "" &&
			!boundedPrintableASCII(resource.ServiceName, 1, 32) {
		return resourceWire{}, boundaryError(phase, FailureInvalid, CodeRequirementInvalid)
	}
	if resource.IconURL != "" && !canonicalPublicURL(resource.IconURL, 2048) {
		return resourceWire{}, boundaryError(phase, FailureInvalid, CodeRequirementInvalid)
	}
	if len(resource.Tags) > 5 {
		return resourceWire{}, boundaryError(phase, FailureInvalid, CodeRequirementInvalid)
	}
	tags := make([]string, len(resource.Tags))
	for index, tag := range resource.Tags {
		if !boundedPrintableASCII(tag, 1, 32) {
			return resourceWire{}, boundaryError(phase, FailureInvalid, CodeRequirementInvalid)
		}
		tags[index] = tag
	}
	resource.Tags = tags
	return resource, nil
}

func canonicalPublicURL(value string, maxLength int) bool {
	if len(value) == 0 || len(value) > maxLength || !utf8.ValidString(value) {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" ||
		parsed.ForceQuery {
		return false
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !loopbackHostname(parsed.Hostname()) {
			return false
		}
	default:
		return false
	}
	return parsed.String() == value
}

func loopbackHostname(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.IsLoopback()
}

func boundedText(value string, minLength, maxLength int) bool {
	return len(value) >= minLength && len(value) <= maxLength && utf8.ValidString(value)
}

func boundedPrintableASCII(value string, minLength, maxLength int) bool {
	if len(value) < minLength || len(value) > maxLength {
		return false
	}
	for index := range len(value) {
		if value[index] < 0x20 || value[index] > 0x7e {
			return false
		}
	}
	return true
}

func requirementToSDK(wire paymentRequirementWire) x402.PaymentRequirements {
	extra := map[string]interface{}{
		"name":    wire.Extra.Name,
		"version": wire.Extra.Version,
	}
	return x402.PaymentRequirements{
		Scheme:            wire.Scheme,
		Network:           wire.Network,
		Asset:             wire.Asset,
		Amount:            wire.Amount,
		PayTo:             wire.PayTo,
		MaxTimeoutSeconds: wire.MaxTimeoutSeconds,
		Extra:             extra,
	}
}

func resourceToSDK(wire resourceWire) x402.ResourceInfo {
	return x402.ResourceInfo{
		URL:         wire.URL,
		Description: wire.Description,
		MimeType:    wire.MimeType,
		ServiceName: wire.ServiceName,
		Tags:        append([]string(nil), wire.Tags...),
		IconUrl:     wire.IconURL,
	}
}

func resourceFromSDK(resource x402.ResourceInfo) resourceWire {
	return resourceWire{
		URL:         resource.URL,
		Description: resource.Description,
		MimeType:    resource.MimeType,
		ServiceName: resource.ServiceName,
		Tags:        append([]string(nil), resource.Tags...),
		IconURL:     resource.IconUrl,
	}
}

func clonePayload(payload x402.PaymentPayload) x402.PaymentPayload {
	cloned := payload
	cloned.Payload = cloneMap(payload.Payload)
	cloned.Accepted.Extra = cloneMap(payload.Accepted.Extra)
	cloned.Extensions = cloneMap(payload.Extensions)
	if payload.Resource != nil {
		resource := *payload.Resource
		resource.Tags = append([]string(nil), resource.Tags...)
		cloned.Resource = &resource
	}
	return cloned
}

func cloneMap(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return nil
	}
	output := make(map[string]interface{}, len(input))
	for key, value := range input {
		switch typed := value.(type) {
		case map[string]interface{}:
			output[key] = cloneMap(typed)
		case []interface{}:
			output[key] = append([]interface{}(nil), typed...)
		default:
			output[key] = typed
		}
	}
	return output
}
