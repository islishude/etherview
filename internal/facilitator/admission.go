package facilitator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"

	"github.com/ethereum/go-ethereum/common"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	x402 "github.com/x402-foundation/x402/go/v2"
)

// rejectUnsubmitted atomically refuses a new authorization without overwriting
// an existing reservation. A lookup alone cannot exclude a concurrent submit.
// The failed journal row also fences later retries from signing this authorization.
func (s *Service) rejectUnsubmitted(ctx context.Context, ch *chainRuntime, in paymentRequest, id, payer, reason string) (any, error) {
	body, _ := canonicalJSON(in)
	digest := sha256.Sum256(body)
	existing, err := s.queries.FacilitatorGet(ctx, id)
	if err == nil {
		return observedSettlement(existing, in, payer, digest[:])
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, &operationError{code: "journal_unavailable"}
	}
	// Capacity may refuse signing, but must not reject an authorization that
	// another sender already paid. Admit it only for reconciliation, never signing.
	if proof, e := proofFor(in); e == nil {
		head, e := ch.adapter.client.HeaderByNumber(ctx, nil)
		if e != nil {
			return nil, &operationError{code: "chain_unavailable"}
		}
		used, e := proof.consumed(ctx, ch, head.Hash())
		if e != nil {
			return nil, &operationError{code: "chain_unavailable"}
		}
		if used {
			count, e := s.queries.FacilitatorReserve(ctx, dbgen.FacilitatorReserveParams{AuthorizationID: id, RequestDigest: digest[:], RequestBody: body, Network: in.PaymentRequirements.Network, SignerAddress: ch.adapter.signer.Address().Bytes()})
			if e != nil {
				return nil, &operationError{code: "journal_unavailable"}
			}
			if count == 1 {
				if _, e = s.queries.FacilitatorReconcileUnsigned(ctx, id); e != nil {
					return nil, &operationError{code: "journal_unavailable"}
				}
			}
			row, e := s.queries.FacilitatorGet(ctx, id)
			if e != nil {
				return nil, &operationError{code: "journal_unavailable"}
			}
			return observedSettlement(row, in, payer, digest[:])
		}
	}
	response := x402.SettleResponse{Success: false, ErrorReason: reason, Network: x402.Network(in.PaymentRequirements.Network), Payer: payer, Amount: in.PaymentRequirements.Amount}
	encoded, _ := json.Marshal(response)
	_, err = s.queries.FacilitatorRejectUnsubmitted(ctx, dbgen.FacilitatorRejectUnsubmittedParams{
		AuthorizationID: id, RequestDigest: digest[:], RequestBody: body, Network: in.PaymentRequirements.Network, SignerAddress: ch.adapter.signer.Address().Bytes(), Response: encoded,
	})
	if err != nil {
		return nil, &operationError{code: "journal_unavailable"}
	}
	row, err := s.queries.FacilitatorGet(ctx, id)
	if err != nil {
		return nil, &operationError{code: "journal_unavailable"}
	}
	return observedSettlement(row, in, payer, digest[:])
}

// observedSettlement never reconciles an unsigned reservation owned by another call.
func observedSettlement(row dbgen.FacilitatorSettlement, in paymentRequest, payer string, digest []byte) (any, error) {
	response := x402.SettleResponse{Success: false, Network: x402.Network(in.PaymentRequirements.Network), Payer: payer, Amount: in.PaymentRequirements.Amount}
	if !bytes.Equal(row.RequestDigest, digest) {
		return nil, &operationError{code: "authorization_conflict"}
	}
	if len(row.Response) > 0 {
		if json.Unmarshal(row.Response, &response) != nil {
			return nil, &operationError{code: "journal_unavailable"}
		}
		return response, nil
	}
	// Do not reconcile an unsigned reservation while its owner may be signing.
	if len(row.TransactionHash) == 0 {
		return nil, &operationError{code: "chain_busy"}
	}
	response.ErrorReason = "settlement_pending"
	response.Transaction = common.BytesToHash(row.TransactionHash).Hex()
	return response, nil
}
