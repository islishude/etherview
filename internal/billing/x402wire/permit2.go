package x402wire

import (
	"encoding/json"
	"math/big"
	"strings"

	x402evm "github.com/x402-foundation/x402/go/v2/mechanisms/evm"
)

type Permit2Authorization struct {
	From       string
	Token      string
	Amount     string
	Spender    string
	Nonce      string
	Deadline   string
	To         string
	ValidAfter string
	Signature  string
}

type permit2PayloadWire struct {
	Signature            string                   `json:"signature"`
	Permit2Authorization permit2AuthorizationWire `json:"permit2Authorization"`
}

type permit2AuthorizationWire struct {
	From      string                 `json:"from"`
	Permitted permit2PermissionsWire `json:"permitted"`
	Spender   string                 `json:"spender"`
	Nonce     string                 `json:"nonce"`
	Deadline  string                 `json:"deadline"`
	Witness   permit2WitnessWire     `json:"witness"`
}

type permit2PermissionsWire struct {
	Token  string `json:"token"`
	Amount string `json:"amount"`
}

type permit2WitnessWire struct {
	To         string `json:"to"`
	ValidAfter string `json:"validAfter"`
}

func decodePermit2Payload(data json.RawMessage) (Permit2Authorization, permit2PayloadWire, error) {
	var wire permit2PayloadWire
	if len(data) == 0 || decodeTypedJSON(data, &wire) != nil {
		return Permit2Authorization{}, permit2PayloadWire{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	from, ok := canonicalAddress(wire.Permit2Authorization.From)
	if !ok {
		return Permit2Authorization{}, permit2PayloadWire{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	token, ok := canonicalAddress(wire.Permit2Authorization.Permitted.Token)
	if !ok {
		return Permit2Authorization{}, permit2PayloadWire{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	amount, ok := canonicalUint256(wire.Permit2Authorization.Permitted.Amount, true)
	if !ok {
		return Permit2Authorization{}, permit2PayloadWire{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	spender, ok := canonicalAddress(wire.Permit2Authorization.Spender)
	if !ok || !strings.EqualFold(spender, x402evm.X402ExactPermit2ProxyAddress) {
		return Permit2Authorization{}, permit2PayloadWire{}, boundaryError(PhaseHeader, FailureInvalid, CodePaymentMismatch)
	}
	nonce, ok := canonicalUint256(wire.Permit2Authorization.Nonce, false)
	if !ok {
		return Permit2Authorization{}, permit2PayloadWire{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	deadline, ok := canonicalUint256(wire.Permit2Authorization.Deadline, true)
	if !ok {
		return Permit2Authorization{}, permit2PayloadWire{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	to, ok := canonicalAddress(wire.Permit2Authorization.Witness.To)
	if !ok {
		return Permit2Authorization{}, permit2PayloadWire{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	validAfter, ok := canonicalUint256(wire.Permit2Authorization.Witness.ValidAfter, false)
	if !ok {
		return Permit2Authorization{}, permit2PayloadWire{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	deadlineInt, _ := new(big.Int).SetString(deadline, 10)
	validAfterInt, _ := new(big.Int).SetString(validAfter, 10)
	if deadlineInt.Cmp(validAfterInt) <= 0 {
		return Permit2Authorization{}, permit2PayloadWire{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	signature, ok := canonicalVariableHex(wire.Signature)
	if !ok {
		return Permit2Authorization{}, permit2PayloadWire{}, boundaryError(PhaseHeader, FailureInvalid, CodeHeaderMalformed)
	}
	normalized := permit2PayloadWire{
		Signature: signature,
		Permit2Authorization: permit2AuthorizationWire{
			From:      from,
			Permitted: permit2PermissionsWire{Token: token, Amount: amount},
			Spender:   spender, Nonce: nonce, Deadline: deadline,
			Witness: permit2WitnessWire{To: to, ValidAfter: validAfter},
		},
	}
	return Permit2Authorization{
		From: from, Token: token, Amount: amount, Spender: spender,
		Nonce: nonce, Deadline: deadline, To: to, ValidAfter: validAfter,
		Signature: signature,
	}, normalized, nil
}

func permit2PayloadMap(value Permit2Authorization) map[string]any {
	return map[string]any{
		"signature": value.Signature,
		"permit2Authorization": map[string]any{
			"from":      value.From,
			"permitted": map[string]any{"token": value.Token, "amount": value.Amount},
			"spender":   value.Spender, "nonce": value.Nonce, "deadline": value.Deadline,
			"witness": map[string]any{"to": value.To, "validAfter": value.ValidAfter},
		},
	}
}
