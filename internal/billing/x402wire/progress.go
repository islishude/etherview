package x402wire

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/ethereum/go-ethereum/common"
)

// SettlementProgress is an observation, never an authorization to credit.
type SettlementProgress struct {
	Stage       string `json:"stage"`
	Transaction string `json:"transaction,omitempty"`
}

func (c *Client) PaymentProgress(ctx context.Context, payment Payment, requirement Requirement) (*SettlementProgress, error) {
	if err := requirement.Match(payment); err != nil {
		return nil, err
	}
	_, _, body, err := prepareFacilitatorRequest(PhaseProgress, payment.PayloadJSON(), requirement.JSON())
	if err != nil {
		return nil, err
	}
	status, data, err := c.do(ctx, PhaseProgress, http.MethodPost, "/progress", body)
	if err != nil {
		return nil, err
	}
	invalid := boundaryError(PhaseProgress, FailureUnavailable, CodeFacilitatorResponseInvalid)
	if status != http.StatusOK {
		return nil, boundaryError(PhaseProgress, FailureUnavailable, CodeFacilitatorUnavailable)
	}
	var result SettlementProgress
	if json.Unmarshal(data, &result) != nil {
		return nil, invalid
	}
	if result.Stage != "submitting" && result.Stage != "confirming" && result.Stage != "unknown" {
		return nil, invalid
	}
	_, valid := canonicalFixedHex(result.Transaction)
	if result.Transaction != "" && (!valid || common.HexToHash(result.Transaction) == (common.Hash{})) {
		return nil, invalid
	}
	if (result.Stage == "confirming" && result.Transaction == "") || (result.Stage == "submitting" && result.Transaction != "") {
		return nil, invalid
	}
	return &result, nil
}
