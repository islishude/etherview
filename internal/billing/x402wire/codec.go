package x402wire

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/islishude/etherview/internal/jsonstrict"
	x402 "github.com/x402-foundation/x402/go/v2"
)

var hostileJSONLimits = jsonstrict.Limits{
	MaxDepth:         12,
	MaxNodes:         256,
	SafeIntegersOnly: true,
}

// Codec implements the bounded x402 v2 HTTP header wire format.
type Codec struct {
	maxHeaderBytes int
}

// NewCodec constructs a codec whose encoded header limit cannot exceed 16 KiB.
func NewCodec(maxHeaderBytes int) (*Codec, error) {
	if maxHeaderBytes <= 0 || maxHeaderBytes > AbsoluteMaxHeaderBytes {
		return nil, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderOversized)
	}
	return &Codec{maxHeaderBytes: maxHeaderBytes}, nil
}

// DecodePaymentSignature requires exactly one PAYMENT-SIGNATURE field value.
func (c *Codec) DecodePaymentSignature(header http.Header) (Payment, error) {
	if c == nil {
		return Payment{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	decoded, err := c.decodeSingleHeader(
		header,
		PaymentSignatureHeader,
		PhaseHeader,
	)
	if err != nil {
		return Payment{}, err
	}
	return decodePaymentJSON(decoded)
}

func (c *Codec) decodeSingleHeader(
	header http.Header,
	name string,
	phase Phase,
) ([]byte, error) {
	if c == nil {
		return nil, boundaryError(phase, FailureInvalid, CodeHeaderMalformed)
	}
	values := header.Values(name)
	switch len(values) {
	case 0:
		return nil, boundaryError(phase, FailureInvalid, CodeHeaderMissing)
	case 1:
	default:
		return nil, boundaryError(phase, FailureInvalid, CodeHeaderMultiple)
	}
	value := values[0]
	if value == "" || len(value) > c.maxHeaderBytes {
		code := CodeHeaderMalformed
		if len(value) > c.maxHeaderBytes {
			code = CodeHeaderOversized
		}
		return nil, boundaryError(phase, FailureInvalid, code)
	}
	if strings.TrimSpace(value) != value || strings.ContainsAny(value, " \t\r\n") {
		return nil, boundaryError(phase, FailureInvalid, CodeHeaderMalformed)
	}

	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(value)))
	count, err := base64.StdEncoding.Strict().Decode(decoded, []byte(value))
	if err != nil {
		return nil, boundaryError(phase, FailureInvalid, CodeHeaderMalformed)
	}
	decoded = decoded[:count]
	if len(decoded) == 0 || len(decoded) > c.maxHeaderBytes {
		return nil, boundaryError(phase, FailureInvalid, CodeHeaderMalformed)
	}
	return decoded, nil
}

func decodePaymentJSON(decoded []byte) (Payment, error) {
	if err := jsonstrict.Validate(decoded, hostileJSONLimits); err != nil {
		return Payment{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}

	var wire paymentPayloadWire
	if err := decodeTypedJSON(decoded, &wire); err != nil {
		return Payment{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	if wire.X402Version != X402Version || wire.Extensions != nil {
		return Payment{}, boundaryError(PhaseHeader, FailureInvalid, CodePaymentUnsupported)
	}
	if wire.Resource == nil {
		return Payment{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}

	requirement, err := normalizeRequirement(wire.Accepted, PhaseHeader)
	if err != nil {
		return Payment{}, err
	}
	resource, err := normalizeResource(*wire.Resource, PhaseHeader)
	if err != nil {
		return Payment{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	authorization, err := normalizeAuthorization(wire.Payload)
	if err != nil {
		return Payment{}, err
	}

	authorizationWire := authorizationWire{
		From:        authorization.From,
		To:          authorization.To,
		Value:       authorization.Value,
		ValidAfter:  authorization.ValidAfter,
		ValidBefore: authorization.ValidBefore,
		Nonce:       authorization.Nonce,
	}
	canonical := canonicalPaymentWire{
		X402Version: X402Version,
		Payload: canonicalExactPayload{
			Signature:     authorization.Signature,
			Authorization: authorizationWire,
		},
		Accepted: requirement,
		Resource: &resource,
	}
	canonicalJSON, err := json.Marshal(canonical)
	if err != nil {
		return Payment{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	sdkResource := resourceToSDK(resource)
	sdkPayload := x402.PaymentPayload{
		X402Version: X402Version,
		Payload: map[string]any{
			"signature": authorization.Signature,
			"authorization": map[string]any{
				"from":        authorization.From,
				"to":          authorization.To,
				"value":       authorization.Value,
				"validAfter":  authorization.ValidAfter,
				"validBefore": authorization.ValidBefore,
				"nonce":       authorization.Nonce,
			},
		},
		Accepted:   requirementToSDK(requirement),
		Resource:   &sdkResource,
		Extensions: nil,
	}
	return Payment{
		payload:       sdkPayload,
		authorization: authorization,
		payloadJSON:   canonicalJSON,
	}, nil
}

// DecodePaymentRequired strictly decodes one v2 exact-EVM PAYMENT-REQUIRED
// challenge into its immutable requirement and resource binding.
func (c *Codec) DecodePaymentRequired(header http.Header) (Requirement, error) {
	decoded, err := c.decodeSingleHeader(
		header,
		PaymentRequiredHeader,
		PhaseRequirement,
	)
	if err != nil {
		return Requirement{}, err
	}
	if err := jsonstrict.Validate(decoded, hostileJSONLimits); err != nil {
		return Requirement{}, boundaryError(
			PhaseRequirement,
			FailureInvalid,
			CodeRequirementInvalid,
		)
	}
	var wire paymentRequiredWire
	if err := decodeTypedJSON(decoded, &wire); err != nil {
		return Requirement{}, boundaryError(
			PhaseRequirement,
			FailureInvalid,
			CodeRequirementInvalid,
		)
	}
	if wire.X402Version != X402Version || wire.Resource == nil ||
		len(wire.Accepts) != 1 || wire.Extensions != nil ||
		!boundedText(wire.Error, 0, 128) {
		return Requirement{}, boundaryError(
			PhaseRequirement,
			FailureInvalid,
			CodeRequirementInvalid,
		)
	}
	requirement, err := normalizeRequirement(wire.Accepts[0], PhaseRequirement)
	if err != nil {
		return Requirement{}, err
	}
	resource, err := normalizeResource(*wire.Resource, PhaseRequirement)
	if err != nil {
		return Requirement{}, err
	}
	return NewRequirement(RequirementOptions{
		Network:            requirement.Network,
		Asset:              requirement.Asset,
		Amount:             requirement.Amount,
		PayTo:              requirement.PayTo,
		MaxTimeoutSeconds:  requirement.MaxTimeoutSeconds,
		AssetEIP712Name:    requirement.Extra.Name,
		AssetEIP712Version: requirement.Extra.Version,
		Resource:           resourceToSDK(resource),
	})
}

// DecodePaymentResponse strictly decodes one successful v2 settlement header.
func (c *Codec) DecodePaymentResponse(header http.Header) (x402.SettleResponse, error) {
	decoded, err := c.decodeSingleHeader(
		header,
		PaymentResponseHeader,
		PhaseSettle,
	)
	if err != nil {
		return x402.SettleResponse{}, err
	}
	if err := jsonstrict.Validate(decoded, hostileJSONLimits); err != nil {
		return x402.SettleResponse{}, boundaryError(
			PhaseSettle,
			FailureInvalid,
			CodeFacilitatorResponseInvalid,
		)
	}
	var wire settleResponseWire
	if err := decodeTypedJSON(decoded, &wire); err != nil || wire.Success == nil ||
		!*wire.Success || wire.ErrorReason != "" || wire.ErrorMessage != "" ||
		wire.Transaction == nil || wire.Network == nil ||
		wire.Extensions != nil || wire.Extra != nil {
		return x402.SettleResponse{}, boundaryError(
			PhaseSettle,
			FailureInvalid,
			CodeFacilitatorResponseInvalid,
		)
	}
	payer, ok := canonicalAddress(wire.Payer)
	if !ok {
		return x402.SettleResponse{}, boundaryError(
			PhaseSettle,
			FailureInvalid,
			CodeFacilitatorResponseInvalid,
		)
	}
	transaction, ok := canonicalFixedHex(*wire.Transaction, 32)
	if !ok {
		return x402.SettleResponse{}, boundaryError(
			PhaseSettle,
			FailureInvalid,
			CodeFacilitatorResponseInvalid,
		)
	}
	network, ok := canonicalEVMNetwork(*wire.Network)
	if !ok {
		return x402.SettleResponse{}, boundaryError(
			PhaseSettle,
			FailureInvalid,
			CodeFacilitatorResponseInvalid,
		)
	}
	amount := ""
	if wire.Amount != "" {
		amount, ok = canonicalUint256(wire.Amount, true)
		if !ok {
			return x402.SettleResponse{}, boundaryError(
				PhaseSettle,
				FailureInvalid,
				CodeFacilitatorResponseInvalid,
			)
		}
	}
	return x402.SettleResponse{
		Success:     true,
		Payer:       payer,
		Transaction: transaction,
		Network:     x402.Network(network),
		Amount:      amount,
	}, nil
}

// EncodePaymentRequired creates a strict padded-base64 PAYMENT-REQUIRED value.
func (c *Codec) EncodePaymentRequired(required x402.PaymentRequired) (string, error) {
	if c == nil || required.X402Version != X402Version || required.Resource == nil ||
		len(required.Accepts) != 1 || len(required.Extensions) != 0 ||
		!boundedText(required.Error, 0, 128) {
		return "", boundaryError(PhaseRequirement, FailureInvalid, CodeRequirementInvalid)
	}
	resource, err := normalizeResource(resourceFromSDK(*required.Resource), PhaseRequirement)
	if err != nil {
		return "", err
	}
	accepted, err := requirementFromSDK(required.Accepts[0], PhaseRequirement)
	if err != nil {
		return "", err
	}
	wire := paymentRequiredWire{
		X402Version: X402Version,
		Error:       required.Error,
		Resource:    &resource,
		Accepts:     []paymentRequirementWire{accepted},
	}
	return c.encodeHeader(PhaseRequirement, wire)
}

// EncodePaymentResponse creates a strict padded-base64 PAYMENT-RESPONSE value.
func (c *Codec) EncodePaymentResponse(response x402.SettleResponse) (string, error) {
	if c == nil || !response.Success || response.ErrorReason != "" || response.ErrorMessage != "" ||
		len(response.Extensions) != 0 || len(response.Extra) != 0 {
		return "", boundaryError(PhaseSettle, FailureInvalid, CodeFacilitatorResponseInvalid)
	}
	payer, ok := canonicalAddress(response.Payer)
	if !ok {
		return "", boundaryError(PhaseSettle, FailureInvalid, CodeFacilitatorResponseInvalid)
	}
	transaction, ok := canonicalFixedHex(response.Transaction, 32)
	if !ok {
		return "", boundaryError(PhaseSettle, FailureInvalid, CodeFacilitatorResponseInvalid)
	}
	network, ok := canonicalEVMNetwork(string(response.Network))
	if !ok {
		return "", boundaryError(PhaseSettle, FailureInvalid, CodeFacilitatorResponseInvalid)
	}
	amount := ""
	if response.Amount != "" {
		var valid bool
		amount, valid = canonicalUint256(response.Amount, true)
		if !valid {
			return "", boundaryError(PhaseSettle, FailureInvalid, CodeFacilitatorResponseInvalid)
		}
	}
	wire := x402.SettleResponse{
		Success:     true,
		Payer:       payer,
		Transaction: transaction,
		Network:     x402.Network(network),
		Amount:      amount,
	}
	return c.encodeHeader(PhaseSettle, wire)
}

func (c *Codec) encodeHeader(phase Phase, value any) (string, error) {
	encodedJSON, err := json.Marshal(value)
	if err != nil {
		return "", boundaryError(phase, FailureInvalid, CodeHeaderMalformed)
	}
	encodedLength := base64.StdEncoding.EncodedLen(len(encodedJSON))
	if encodedLength > c.maxHeaderBytes {
		return "", boundaryError(phase, FailureInvalid, CodeHeaderOversized)
	}
	return base64.StdEncoding.EncodeToString(encodedJSON), nil
}

func decodeTypedJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errUnexpectedJSON
	}
	return nil
}

var errUnexpectedJSON = io.ErrUnexpectedEOF

func requirementFromSDK(requirement x402.PaymentRequirements, phase Phase) (paymentRequirementWire, error) {
	if len(requirement.Extra) != 2 {
		return paymentRequirementWire{}, boundaryError(phase, FailureInvalid, CodeRequirementInvalid)
	}
	name, nameOK := requirement.Extra["name"].(string)
	version, versionOK := requirement.Extra["version"].(string)
	if !nameOK || !versionOK {
		return paymentRequirementWire{}, boundaryError(phase, FailureInvalid, CodeRequirementInvalid)
	}
	return normalizeRequirement(paymentRequirementWire{
		Scheme:            requirement.Scheme,
		Network:           requirement.Network,
		Asset:             requirement.Asset,
		Amount:            requirement.Amount,
		PayTo:             requirement.PayTo,
		MaxTimeoutSeconds: requirement.MaxTimeoutSeconds,
		Extra: &assetExtraWire{
			Name:    name,
			Version: version,
		},
	}, phase)
}
