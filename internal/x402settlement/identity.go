// Package x402settlement owns the shared, non-secret lookup identity and evidence
// contract between the isolated facilitator and the API credit publisher.
package x402settlement

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	x402 "github.com/x402-foundation/x402/go/v2"
	"github.com/x402-foundation/x402/go/v2/mechanisms/evm"
)

type Request struct {
	X402Version         int                      `json:"x402Version"`
	PaymentPayload      x402.PaymentPayload      `json:"paymentPayload"`
	PaymentRequirements x402.PaymentRequirements `json:"paymentRequirements"`
}

type Reference struct {
	AuthorizationID string `json:"authorizationId"`
	RequestDigest   string `json:"requestDigest"`
}

type Evidence struct {
	Transaction string `json:"transaction"`
	BlockHash   string `json:"blockHash"`
	BlockNumber string `json:"blockNumber"`
	LogIndex    string `json:"logIndex"`
	Source      string `json:"source"`
	Network     string `json:"network"`
	Payer       string `json:"payer"`
	Recipient   string `json:"recipient"`
	Asset       string `json:"asset"`
	Amount      string `json:"amount"`
}

type Result struct {
	Reference
	State                string               `json:"state"`
	OriginalTransaction  string               `json:"originalTransaction"`
	TransactionConfirmed bool                 `json:"transactionConfirmed"`
	Evidence             *Evidence            `json:"evidence,omitempty"`
	Response             *x402.SettleResponse `json:"response,omitempty"`
}

func Lookup(in Request) (Reference, error) {
	method := "eip3009"
	if v, ok := in.PaymentRequirements.Extra["assetTransferMethod"]; ok {
		var valid bool
		method, valid = v.(string)
		if !valid {
			return Reference{}, errors.New("invalid_method")
		}
	}
	var payer, nonce string
	scope := strings.ToLower(in.PaymentRequirements.Asset)
	switch method {
	case "eip3009":
		p, err := evm.PayloadFromMap(in.PaymentPayload.Payload)
		if err != nil {
			return Reference{}, err
		}
		payer = p.Authorization.From
		b, err := hex.DecodeString(strings.TrimPrefix(p.Authorization.Nonce, "0x"))
		if err != nil || len(b) != 32 {
			return Reference{}, errors.New("invalid_nonce")
		}
		nonce = hex.EncodeToString(b)
	case "permit2":
		p, err := evm.Permit2PayloadFromMap(in.PaymentPayload.Payload)
		if err != nil {
			return Reference{}, err
		}
		payer = p.Permit2Authorization.From
		n, ok := new(big.Int).SetString(p.Permit2Authorization.Nonce, 10)
		if !ok || n.Sign() < 0 || n.BitLen() > 256 {
			return Reference{}, errors.New("invalid_nonce")
		}
		nonce = n.String()
		scope = strings.ToLower(evm.PERMIT2Address)
	default:
		return Reference{}, errors.New("invalid_method")
	}
	if !common.IsHexAddress(payer) {
		return Reference{}, errors.New("invalid_payer")
	}
	body, err := json.Marshal(in)
	if err != nil {
		return Reference{}, err
	}
	digest := sha256.Sum256(body)
	return Reference{AuthorizationID: in.PaymentRequirements.Network + ":" + method + ":" + scope + ":" + strings.ToLower(payer) + ":" + nonce, RequestDigest: hex.EncodeToString(digest[:])}, nil
}
