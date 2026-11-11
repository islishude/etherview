package facilitator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/x402settlement"
	"github.com/jackc/pgx/v5"
	x402 "github.com/x402-foundation/x402/go/v2"
	"github.com/x402-foundation/x402/go/v2/mechanisms/evm"
)

type operationError struct{ code string }

func (e *operationError) Error() string { return e.code }

func (s *Service) executePayment(parent context.Context, in paymentRequest, ch *chainRuntime, id, payer string, settle bool) (any, error) {
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	if !ch.healthy.Load() || s.guard(ctx) != nil {
		return nil, &operationError{code: "chain_unavailable"}
	}
	if !settle {
		select {
		case ch.verifyGate <- struct{}{}:
			defer func() { <-ch.verifyGate }()
		default:
			return nil, &operationError{code: "chain_busy"}
		}
		return verifyPayment(ctx, ch, in)
	}
	// Only another submission is a capacity refusal. Background reconciliation
	// uses the execution gate too, including while scanning an empty journal.
	select {
	case ch.submissionGate <- struct{}{}:
		defer func() { <-ch.submissionGate }()
	default:
		return s.rejectUnsubmitted(ctx, ch, in, id, payer, "chain_busy")
	}
	// Wait for maintenance within this attempt's existing deadline. Keep journal
	// reads and signing under the same gate so recovery cannot fail a live owner.
	select {
	case ch.gate <- struct{}{}:
		defer func() { <-ch.gate }()
	case <-ctx.Done():
		return nil, &operationError{code: "chain_unavailable"}
	}
	body, _ := canonicalJSON(in)
	digest := sha256.Sum256(body)
	row, e := s.queries.FacilitatorGet(ctx, id)
	if e == nil {
		if !bytes.Equal(row.RequestDigest, digest[:]) {
			return nil, &operationError{code: "authorization_conflict"}
		}
		response, e := s.reconcile(ctx, ch, row, in)
		if e != nil {
			return nil, &operationError{code: "journal_unavailable"}
		}
		return response, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return nil, &operationError{code: "journal_unavailable"}
	}
	pending, e := s.queries.FacilitatorPendingCount(ctx, in.PaymentRequirements.Network)
	if e != nil {
		return nil, &operationError{code: "journal_unavailable"}
	}
	if pending != 0 {
		return s.rejectUnsubmitted(ctx, ch, in, id, payer, "chain_pending")
	}
	verified, e := verifyPayment(ctx, ch, in)
	if e != nil {
		return nil, e
	}
	reconciliationOnly := !verified.IsValid && verified.InvalidReason == "authorization_reconciliation_required"
	if !verified.IsValid && !reconciliationOnly {
		return x402.SettleResponse{Success: false, ErrorReason: verified.InvalidReason, Network: x402.Network(in.PaymentRequirements.Network), Payer: payer}, nil
	}

	count, e := s.queries.FacilitatorReserve(ctx, dbgen.FacilitatorReserveParams{AuthorizationID: id, RequestDigest: digest[:], RequestBody: body, Network: in.PaymentRequirements.Network, SignerAddress: ch.adapter.signer.Address().Bytes()})
	if e != nil {
		return nil, &operationError{code: "reservation_failed"}
	}
	if count != 1 {
		// A concurrent capacity refusal may have won the authorization identity
		// while verification was in flight. Never sign over that durable outcome.
		row, e = s.queries.FacilitatorGet(ctx, id)
		if e != nil {
			return nil, &operationError{code: "journal_unavailable"}
		}
		return observedSettlement(row, in, payer, digest[:])
	}
	ctx = context.WithValue(ctx, authorizationContext{}, id)
	if !reconciliationOnly {
		_, _ = ch.scheme.Settle(ctx, in.PaymentPayload, in.PaymentRequirements, nil)
	}
	// Never trust an SDK error to indicate whether broadcast occurred. The journal owns it.
	finish, done := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer done()
	row, e = s.queries.FacilitatorGet(finish, id)
	if e != nil {
		return nil, &operationError{code: "journal_unavailable"}
	}

	response, e := s.reconcile(finish, ch, row, in)
	if e != nil {
		return nil, &operationError{code: "journal_unavailable"}
	}
	return response, nil
}

func (s *Service) reconcile(ctx context.Context, ch *chainRuntime, row dbgen.FacilitatorSettlement, in paymentRequest) (*x402.SettleResponse, error) {
	payer := paymentPayer(in)
	response := &x402.SettleResponse{Success: false, ErrorReason: "authorization_reconciliation_required", Network: x402.Network(row.Network), Amount: in.PaymentRequirements.Amount, Payer: payer.Hex()}
	if len(row.TransactionHash) > 0 {
		response.Transaction = common.BytesToHash(row.TransactionHash).Hex()
		response.ErrorReason = "settlement_pending"
		receipt, _, err := confirmedReceipt(ctx, ch, common.BytesToHash(row.TransactionHash))
		if err == nil {
			count, e := s.queries.FacilitatorConfirmTransaction(ctx, row.AuthorizationID, row.TransactionHash)
			if e != nil {
				return nil, e
			}
			if count != 1 {
				return nil, errors.New("journal_changed")
			}
			row.TransactionConfirmed = true
			if len(row.Response) == 0 && receipt.Status == 1 {
				if log := transferLog(receipt, in, payer); log != nil {
					return s.publishEvidence(ctx, ch, row, in, evidenceFrom(receipt, log, in, payer, "original"))
				}
			}
		}
	}
	if len(row.Response) > 0 {
		var result x402.SettleResponse
		if json.Unmarshal(row.Response, &result) != nil {
			return nil, errors.New("invalid_journal")
		}
		return &result, nil
	}
	proof, proofErr := proofFor(in)
	if row.State == "reserved" {
		var count int64
		var err error
		if proofErr != nil {
			response.ErrorReason = "settlement_rejected"
			encoded, _ := json.Marshal(response)
			count, err = s.queries.FacilitatorAbortUnsigned(ctx, row.AuthorizationID, encoded)
		} else {
			count, err = s.queries.FacilitatorReconcileUnsigned(ctx, row.AuthorizationID)
		}
		if err != nil {
			return nil, err
		}
		current, err := s.queries.FacilitatorGet(ctx, row.AuthorizationID)
		if err != nil {
			return nil, err
		}
		if count == 0 && current.State == "reserved" {
			return response, nil
		}
		return s.reconcile(ctx, ch, current, in)
	}
	if proofErr != nil {
		return response, nil
	}
	headNow, err := ch.adapter.client.HeaderByNumber(ctx, nil)
	if err != nil {
		return response, nil
	}
	usedNow, err := proof.consumed(ctx, ch, headNow.Hash())
	if err != nil {
		return response, nil
	}
	if usedNow {
		evidence, err := s.scanExternal(ctx, ch, row, in, proof)
		if err != nil {
			return response, nil
		}
		if evidence != nil {
			return s.publishEvidence(ctx, ch, row, in, evidence)
		}
		return response, nil
	}
	// Only an expired, demonstrably unused authorization can be rejected after
	// submission. A consumed nonce without matching evidence remains unresolved.
	head, err := ch.adapter.client.HeaderByNumber(ctx, nil)
	if err != nil || head.Number.Uint64()+1 < ch.adapter.config.Confirmations {
		return response, nil
	}
	final, err := ch.adapter.client.HeaderByNumber(ctx, new(big.Int).SetUint64(head.Number.Uint64()+1-ch.adapter.config.Confirmations))
	if err != nil || new(big.Int).SetUint64(final.Time).Cmp(proof.before) <= 0 {
		return response, nil
	}
	used, err := proof.consumed(ctx, ch, final.Hash())
	if err != nil || used {
		return response, nil
	}
	check, err := ch.adapter.client.HeaderByNumber(ctx, final.Number)
	if err != nil || check.Hash() != final.Hash() {
		return response, nil
	}
	response.ErrorReason = "settlement_rejected"
	encoded, _ := json.Marshal(response)
	count, err := s.queries.FacilitatorFinish(ctx, row.AuthorizationID, "failed", encoded)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return s.currentResponse(ctx, row.AuthorizationID, in, payer.Hex())
	}
	ch.failures.Add(1)
	return response, nil
}

func paymentPayer(in paymentRequest) common.Address {
	if p, e := evm.PayloadFromMap(in.PaymentPayload.Payload); e == nil && p.Authorization.From != "" {
		return common.HexToAddress(p.Authorization.From)
	}
	if p, e := evm.Permit2PayloadFromMap(in.PaymentPayload.Payload); e == nil {
		return common.HexToAddress(p.Permit2Authorization.From)
	}
	return common.Address{}
}

func (s *Service) currentResponse(ctx context.Context, id string, in paymentRequest, payer string) (*x402.SettleResponse, error) {
	row, err := s.queries.FacilitatorGet(ctx, id)
	if err != nil {
		return nil, err
	}
	result, err := observedSettlement(row, in, payer, row.RequestDigest)
	if err != nil {
		return nil, err
	}
	response, ok := result.(x402.SettleResponse)
	if !ok {
		return nil, errors.New("invalid_journal")
	}
	return &response, nil
}

func (s *Service) publishEvidence(ctx context.Context, ch *chainRuntime, row dbgen.FacilitatorSettlement, in paymentRequest, evidence *x402settlement.Evidence) (*x402.SettleResponse, error) {
	receipt, _, err := confirmedReceipt(ctx, ch, common.HexToHash(evidence.Transaction))
	if err != nil || receipt.BlockHash != common.HexToHash(evidence.BlockHash) {
		return nil, errors.New("evidence_noncanonical")
	}
	response := &x402.SettleResponse{Success: true, Transaction: evidence.Transaction, Network: x402.Network(row.Network), Payer: evidence.Payer, Amount: evidence.Amount}
	encoded, _ := json.Marshal(response)
	proof, _ := json.Marshal(evidence)
	count, err := s.queries.FacilitatorSucceed(ctx, row.AuthorizationID, encoded, proof)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return s.currentResponse(ctx, row.AuthorizationID, in, evidence.Payer)
	}
	ch.successes.Add(1)
	return response, nil
}
