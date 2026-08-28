package x402wire

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
)

const (
	fingerprintDomainV1        = "etherview/x402/exact-eip3009/authorization-fingerprint/v1\x00"
	fingerprintDomainEIP3009V2 = "etherview/x402/exact-eip3009/authorization-fingerprint/v2\x00"
	fingerprintDomainPermit2V2 = "etherview/x402/exact-permit2/authorization-fingerprint/v2\x00"
)

type authorizationFingerprintWire struct {
	X402Version   int               `json:"x402Version"`
	Scheme        string            `json:"scheme"`
	Network       string            `json:"network"`
	Asset         string            `json:"asset"`
	AssetName     string            `json:"assetName"`
	AssetVersion  string            `json:"assetVersion"`
	Authorization authorizationWire `json:"authorization"`
}

type fingerprintV2Wire struct {
	X402Version    int                     `json:"x402Version"`
	Scheme         string                  `json:"scheme"`
	Network        string                  `json:"network"`
	Asset          string                  `json:"asset"`
	TransferMethod string                  `json:"assetTransferMethod"`
	PaymentFlow    string                  `json:"paymentFlow"`
	EIP3009        *authorizationWire      `json:"eip3009,omitempty"`
	Permit2        *permit2FingerprintWire `json:"permit2,omitempty"`
}

type permit2FingerprintWire struct {
	From       string `json:"from"`
	Token      string `json:"token"`
	Amount     string `json:"amount"`
	Spender    string `json:"spender"`
	Nonce      string `json:"nonce"`
	Deadline   string `json:"deadline"`
	To         string `json:"to"`
	ValidAfter string `json:"validAfter"`
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
	material, domain, err := fingerprintMaterial(payment, value)
	if err != nil {
		return [sha256.Size]byte{}, boundaryError(
			PhaseHeader,
			FailureInvalid,
			CodeFingerprintInvalid,
		)
	}
	mac := hmac.New(sha256.New, pepper)
	_, _ = mac.Write([]byte(domain))
	_, _ = mac.Write(material)
	var result [sha256.Size]byte
	copy(result[:], mac.Sum(nil))
	return result, nil
}

func fingerprintMaterial(payment Payment, value paymentRequirementWire) ([]byte, string, error) {
	if payment.fingerprintVersion == 1 {
		authorization := payment.authorization
		if !validEIP3009FingerprintAuthorization(authorization) {
			return nil, "", boundaryError(PhaseHeader, FailureInvalid, CodeFingerprintInvalid)
		}
		material, err := json.Marshal(authorizationFingerprintWire{
			X402Version: X402Version, Scheme: ExactScheme, Network: value.Network,
			Asset: value.Asset, AssetName: value.Extra.Name, AssetVersion: value.Extra.Version,
			Authorization: authorizationWire{
				From: authorization.From, To: authorization.To, Value: authorization.Value,
				ValidAfter: authorization.ValidAfter, ValidBefore: authorization.ValidBefore,
				Nonce: authorization.Nonce,
			},
		})
		return material, fingerprintDomainV1, err
	}
	wire := fingerprintV2Wire{
		X402Version: X402Version, Scheme: ExactScheme, Network: value.Network,
		Asset: value.Asset, TransferMethod: payment.transferMethod,
		PaymentFlow: payment.paymentFlow,
	}
	domain := ""
	switch payment.transferMethod {
	case TransferMethodEIP3009:
		if !validEIP3009FingerprintAuthorization(payment.authorization) {
			return nil, "", boundaryError(PhaseHeader, FailureInvalid, CodeFingerprintInvalid)
		}
		wire.EIP3009 = &authorizationWire{
			From: payment.authorization.From, To: payment.authorization.To,
			Value: payment.authorization.Value, ValidAfter: payment.authorization.ValidAfter,
			ValidBefore: payment.authorization.ValidBefore, Nonce: payment.authorization.Nonce,
		}
		domain = fingerprintDomainEIP3009V2
	case TransferMethodPermit2:
		if payment.permit2 == nil || payment.permit2.From == "" || payment.permit2.Token == "" ||
			payment.permit2.Amount == "" || payment.permit2.Spender == "" ||
			payment.permit2.Nonce == "" || payment.permit2.Deadline == "" ||
			payment.permit2.To == "" || payment.permit2.ValidAfter == "" {
			return nil, "", boundaryError(PhaseHeader, FailureInvalid, CodeFingerprintInvalid)
		}
		wire.Permit2 = &permit2FingerprintWire{
			From: payment.permit2.From, Token: payment.permit2.Token,
			Amount: payment.permit2.Amount, Spender: payment.permit2.Spender,
			Nonce: payment.permit2.Nonce, Deadline: payment.permit2.Deadline,
			To: payment.permit2.To, ValidAfter: payment.permit2.ValidAfter,
		}
		domain = fingerprintDomainPermit2V2
	default:
		return nil, "", boundaryError(PhaseHeader, FailureInvalid, CodeFingerprintInvalid)
	}
	material, err := json.Marshal(wire)
	return material, domain, err
}

func validEIP3009FingerprintAuthorization(value Authorization) bool {
	return value.From != "" && value.To != "" && value.Value != "" &&
		value.ValidAfter != "" && value.ValidBefore != "" && value.Nonce != ""
}
