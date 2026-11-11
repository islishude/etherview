package x402wire

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/x402settlement"
)

func ResultReference(payment Payment, requirement Requirement) (x402settlement.Reference, error) {
	_, _, body, err := prepareFacilitatorRequest(PhaseSettle, payment.PayloadJSON(), requirement.JSON())
	if err != nil {
		return x402settlement.Reference{}, err
	}
	var request x402settlement.Request
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if decoder.Decode(&request) != nil {
		return x402settlement.Reference{}, boundaryError(PhaseSettle, FailureInvalid, CodePaymentMismatch)
	}
	return x402settlement.Lookup(request)
}

func (c *Client) PaymentResult(ctx context.Context, ref x402settlement.Reference) (*x402settlement.Result, error) {
	body, err := json.Marshal(ref)
	if err != nil {
		return nil, err
	}
	status, data, err := c.do(ctx, PhaseProgress, http.MethodPost, "/result", body)
	if err != nil {
		return nil, err
	}
	invalid := boundaryError(PhaseProgress, FailureUnavailable, CodeFacilitatorResponseInvalid)
	if status != http.StatusOK {
		return nil, boundaryError(PhaseProgress, FailureUnavailable, CodeFacilitatorUnavailable)
	}
	var result x402settlement.Result
	if decodeTypedJSON(data, &result) != nil || result.Reference != ref {
		return nil, invalid
	}
	hashOK := func(s string) bool {
		_, ok := canonicalFixedHex(s)
		return ok && common.HexToHash(s) != (common.Hash{})
	}
	if result.OriginalTransaction != "" && !hashOK(result.OriginalTransaction) || result.TransactionConfirmed && result.OriginalTransaction == "" {
		return nil, invalid
	}
	switch result.State {
	case "pending":
		if result.Response != nil || result.Evidence != nil {
			return nil, invalid
		}
	case "failed":
		if result.Response == nil || result.Response.Success || result.Evidence != nil || result.Response.Transaction != result.OriginalTransaction {
			return nil, invalid
		}
	case "succeeded":
		e := result.Evidence
		r := result.Response
		if e == nil || r == nil || !r.Success || !hashOK(e.Transaction) || !hashOK(e.BlockHash) || r.Transaction != e.Transaction || r.Payer != e.Payer || string(r.Network) != e.Network || r.Amount != e.Amount {
			return nil, invalid
		}
		if e.Source != "original" && e.Source != "external" || e.Source == "original" && e.Transaction != result.OriginalTransaction {
			return nil, invalid
		}
		for _, a := range []string{e.Payer, e.Recipient, e.Asset} {
			if _, ok := canonicalAddress(a); !ok {
				return nil, invalid
			}
		}
		for _, n := range []string{e.BlockNumber, e.LogIndex, e.Amount} {
			if _, ok := canonicalUint256(n, false); !ok {
				return nil, invalid
			}
		}
		if _, ok := canonicalEVMNetwork(e.Network); !ok || e.Amount == "0" {
			return nil, invalid
		}
	default:
		return nil, invalid
	}
	return &result, nil
}
