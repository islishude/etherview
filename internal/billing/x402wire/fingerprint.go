package x402wire

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
)

const fingerprintDomain = "etherview/x402/exact-eip3009/authorization-fingerprint/v1\x00"

type authorizationFingerprintWire struct {
	X402Version   int               `json:"x402Version"`
	Scheme        string            `json:"scheme"`
	Network       string            `json:"network"`
	Asset         string            `json:"asset"`
	AssetName     string            `json:"assetName"`
	AssetVersion  string            `json:"assetVersion"`
	Authorization authorizationWire `json:"authorization"`
}

// Fingerprint computes the cross-replica replay identity for one normalized
// EIP-3009 authorization and its exact EIP-712 domain. The signature proof and
// outer request/price bindings are deliberately excluded. PostgreSQL uses this
// one global identity to fence reuse across operations and resources, then
// compares the stored method, operation, resource, requirement, amount, and
// recipient before a duplicate can be accepted as the same reservation.
func Fingerprint(pepper []byte, payment Payment) ([sha256.Size]byte, error) {
	if len(pepper) < sha256.Size {
		return [sha256.Size]byte{}, boundaryError(PhaseHeader, FailureInvalid, CodeFingerprintInvalid)
	}
	if len(payment.payloadJSON) == 0 || payment.payload.X402Version != X402Version {
		return [sha256.Size]byte{}, boundaryError(
			PhaseHeader,
			FailureInvalid,
			CodeFingerprintInvalid,
		)
	}

	value, err := requirementFromSDK(payment.payload.Accepted, PhaseHeader)
	if err != nil || value.Extra == nil {
		return [sha256.Size]byte{}, boundaryError(
			PhaseHeader,
			FailureInvalid,
			CodeFingerprintInvalid,
		)
	}
	authorization := payment.authorization
	if authorization.From == "" || authorization.To == "" ||
		authorization.Value == "" || authorization.ValidAfter == "" ||
		authorization.ValidBefore == "" || authorization.Nonce == "" {
		return [sha256.Size]byte{}, boundaryError(
			PhaseHeader,
			FailureInvalid,
			CodeFingerprintInvalid,
		)
	}

	material, err := json.Marshal(authorizationFingerprintWire{
		X402Version:  X402Version,
		Scheme:       ExactScheme,
		Network:      value.Network,
		Asset:        value.Asset,
		AssetName:    value.Extra.Name,
		AssetVersion: value.Extra.Version,
		Authorization: authorizationWire{
			From:        authorization.From,
			To:          authorization.To,
			Value:       authorization.Value,
			ValidAfter:  authorization.ValidAfter,
			ValidBefore: authorization.ValidBefore,
			Nonce:       authorization.Nonce,
		},
	})
	if err != nil {
		return [sha256.Size]byte{}, boundaryError(
			PhaseHeader,
			FailureInvalid,
			CodeFingerprintInvalid,
		)
	}
	mac := hmac.New(sha256.New, pepper)
	_, _ = mac.Write([]byte(fingerprintDomain))
	_, _ = mac.Write(material)
	var result [sha256.Size]byte
	copy(result[:], mac.Sum(nil))
	return result, nil
}
