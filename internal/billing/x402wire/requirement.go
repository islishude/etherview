package x402wire

import (
	"crypto/sha256"
	"encoding/json"
	"reflect"

	x402 "github.com/x402-foundation/x402/go/v2"
)

// RequirementOptions describes one server-authored exact-EVM requirement.
type RequirementOptions struct {
	Network            string
	Asset              string
	Amount             string
	PayTo              string
	MaxTimeoutSeconds  int
	AssetEIP712Name    string
	AssetEIP712Version string
	Resource           x402.ResourceInfo
}

// Requirement is an immutable normalized requirement and resource binding.
type Requirement struct {
	value          x402.PaymentRequirements
	resource       x402.ResourceInfo
	valueJSON      []byte
	resourceJSON   []byte
	valueDigest    [sha256.Size]byte
	resourceDigest [sha256.Size]byte
}

// NewRequirement validates and normalizes the exact EVM requirement.
func NewRequirement(options RequirementOptions) (Requirement, error) {
	wire, err := normalizeRequirement(paymentRequirementWire{
		Scheme:            ExactScheme,
		Network:           options.Network,
		Asset:             options.Asset,
		Amount:            options.Amount,
		PayTo:             options.PayTo,
		MaxTimeoutSeconds: options.MaxTimeoutSeconds,
		Extra: &assetExtraWire{
			Name:    options.AssetEIP712Name,
			Version: options.AssetEIP712Version,
		},
	}, PhaseRequirement)
	if err != nil {
		return Requirement{}, err
	}
	resourceWireValue, err := normalizeResource(resourceFromSDK(options.Resource), PhaseRequirement)
	if err != nil {
		return Requirement{}, err
	}
	valueJSON, err := json.Marshal(wire)
	if err != nil {
		return Requirement{}, boundaryError(PhaseRequirement, FailureInvalid, CodeRequirementInvalid)
	}
	resourceJSON, err := json.Marshal(resourceWireValue)
	if err != nil {
		return Requirement{}, boundaryError(PhaseRequirement, FailureInvalid, CodeRequirementInvalid)
	}
	return Requirement{
		value:          requirementToSDK(wire),
		resource:       resourceToSDK(resourceWireValue),
		valueJSON:      valueJSON,
		resourceJSON:   resourceJSON,
		valueDigest:    sha256.Sum256(valueJSON),
		resourceDigest: sha256.Sum256(resourceJSON),
	}, nil
}

// SDK returns an independent SDK representation.
func (r Requirement) SDK() x402.PaymentRequirements {
	value := r.value
	value.Extra = cloneMap(r.value.Extra)
	return value
}

// JSON returns the canonical paymentRequirements JSON sent to the facilitator.
func (r Requirement) JSON() []byte {
	return append([]byte(nil), r.valueJSON...)
}

// Resource returns an independent SDK resource binding.
func (r Requirement) Resource() x402.ResourceInfo {
	resource := r.resource
	resource.Tags = append([]string(nil), r.resource.Tags...)
	return resource
}

// RequirementDigest returns the public SHA-256 identity of the normalized
// payment requirement. It is not an authorization fingerprint.
func (r Requirement) RequirementDigest() [sha256.Size]byte {
	return r.valueDigest
}

// ResourceDigest returns the public SHA-256 identity of the exact resource
// structure, including its canonical URL and bounded metadata.
func (r Requirement) ResourceDigest() [sha256.Size]byte {
	return r.resourceDigest
}

// PaymentRequired produces the v2 challenge used by EncodePaymentRequired.
func (r Requirement) PaymentRequired(errorCode string) x402.PaymentRequired {
	resource := r.Resource()
	return x402.PaymentRequired{
		X402Version: X402Version,
		Error:       errorCode,
		Resource:    &resource,
		Accepts:     []x402.PaymentRequirements{r.SDK()},
	}
}

// Match proves that the client-carried accepted requirement and resource are
// byte-for-byte equivalent after normalization, and that the signed transfer
// target and value agree with that requirement.
func (r Requirement) Match(payment Payment) error {
	if payment.payloadJSON == nil || payment.payload.X402Version != X402Version ||
		len(payment.payload.Extensions) != 0 || payment.payload.Resource == nil {
		return boundaryError(PhaseHeader, FailureInvalid, CodePaymentMismatch)
	}

	actualRequirement, err := requirementFromSDK(payment.payload.Accepted, PhaseHeader)
	if err != nil {
		return boundaryError(PhaseHeader, FailureInvalid, CodePaymentMismatch)
	}
	expectedRequirement, err := requirementFromSDK(r.value, PhaseRequirement)
	if err != nil {
		return boundaryError(PhaseRequirement, FailureInvalid, CodeRequirementInvalid)
	}
	if !reflect.DeepEqual(actualRequirement, expectedRequirement) {
		return boundaryError(PhaseHeader, FailureInvalid, CodePaymentMismatch)
	}

	actualResource, err := normalizeResource(resourceFromSDK(*payment.payload.Resource), PhaseHeader)
	if err != nil {
		return boundaryError(PhaseHeader, FailureInvalid, CodePaymentMismatch)
	}
	expectedResource, err := normalizeResource(resourceFromSDK(r.resource), PhaseRequirement)
	if err != nil {
		return boundaryError(PhaseRequirement, FailureInvalid, CodeRequirementInvalid)
	}
	if !reflect.DeepEqual(actualResource, expectedResource) {
		return boundaryError(PhaseHeader, FailureInvalid, CodePaymentMismatch)
	}
	if payment.authorization.To != expectedRequirement.PayTo ||
		payment.authorization.Value != expectedRequirement.Amount {
		return boundaryError(PhaseHeader, FailureInvalid, CodePaymentMismatch)
	}
	return nil
}
