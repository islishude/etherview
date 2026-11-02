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
	"github.com/ethereum/go-ethereum/crypto"
	dbgen "github.com/islishude/etherview/internal/db/gen"
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
		result, e := ch.scheme.Verify(ctx, in.PaymentPayload, in.PaymentRequirements, nil)
		if e != nil {
			return x402.VerifyResponse{IsValid: false, InvalidReason: "verification_failed"}, nil
		}
		if !result.IsValid {
			result.InvalidReason = "invalid_payload"
			result.InvalidMessage = ""
		}
		return result, nil
	}
	select {
	case ch.gate <- struct{}{}:
		defer func() { <-ch.gate }()
	default:
		return s.rejectUnsubmitted(ctx, ch, in, id, payer, "chain_busy")
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
	verified, e := ch.scheme.Verify(ctx, in.PaymentPayload, in.PaymentRequirements, nil)
	if e != nil || !verified.IsValid {
		return x402.SettleResponse{Success: false, ErrorReason: "verification_failed", Network: x402.Network(in.PaymentRequirements.Network)}, nil
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
	_, _ = ch.scheme.Settle(ctx, in.PaymentPayload, in.PaymentRequirements, nil)
	// Never trust an SDK error to indicate whether broadcast occurred. The journal owns it.
	finish, done := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer done()
	row, e = s.queries.FacilitatorGet(finish, id)
	if e != nil {
		return nil, &operationError{code: "journal_unavailable"}
	}
	if len(row.TransactionHash) == 0 {
		response := x402.SettleResponse{Success: false, ErrorReason: "settlement_rejected", Network: x402.Network(in.PaymentRequirements.Network), Payer: payer}
		encoded, _ := json.Marshal(response)
		if _, e = s.queries.FacilitatorFinish(finish, id, "failed", encoded); e != nil {
			return nil, &operationError{code: "journal_unavailable"}
		}
		ch.failures.Add(1)
		return response, nil
	}
	response, e := s.reconcile(finish, ch, row, in)
	if e != nil {
		return nil, &operationError{code: "journal_unavailable"}
	}
	return response, nil
}

func (s *Service) reconcile(ctx context.Context, ch *chainRuntime, row dbgen.FacilitatorSettlement, in paymentRequest) (*x402.SettleResponse, error) {
	if len(row.Response) > 0 {
		var response x402.SettleResponse
		if json.Unmarshal(row.Response, &response) != nil {
			return nil, errors.New("invalid_journal")
		}
		return &response, nil
	}
	response := &x402.SettleResponse{Success: false, ErrorReason: "settlement_rejected", Network: x402.Network(row.Network), Amount: in.PaymentRequirements.Amount}
	var payer string
	if p, e := evm.PayloadFromMap(in.PaymentPayload.Payload); e == nil {
		payer = p.Authorization.From
	}
	if p, e := evm.Permit2PayloadFromMap(in.PaymentPayload.Payload); e == nil && p.Permit2Authorization.From != "" {
		payer = p.Permit2Authorization.From
	}
	response.Payer = common.HexToAddress(payer).Hex()
	if len(row.TransactionHash) == 0 {
		encoded, _ := json.Marshal(response)
		_, err := s.queries.FacilitatorFinish(ctx, row.AuthorizationID, "failed", encoded)
		return response, err
	}
	response.Transaction = common.BytesToHash(row.TransactionHash).Hex()
	response.ErrorReason = "settlement_pending"
	receipt, err := ch.adapter.WaitForTransactionReceipt(ctx, response.Transaction)
	if err != nil {
		return response, nil
	}
	response.ErrorReason = "settlement_rejected"
	topic := crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	amount, _ := new(big.Int).SetString(in.PaymentRequirements.Amount, 10)
	if receipt.Status == 1 {
		for _, log := range receipt.Logs {
			if log.Address == common.HexToAddress(in.PaymentRequirements.Asset) && len(log.Topics) == 3 && log.Topics[0] == topic && log.Topics[1] == common.BytesToHash(common.HexToAddress(payer).Bytes()) && log.Topics[2] == common.BytesToHash(common.HexToAddress(in.PaymentRequirements.PayTo).Bytes()) && len(log.Data) == 32 && new(big.Int).SetBytes(log.Data).Cmp(amount) == 0 {
				response.Success = true
				response.ErrorReason = ""
				break
			}
		}
	}
	if !response.Success && receipt.Status == 1 {
		response.ErrorReason = "settlement_pending"
		return response, nil
	}
	if !response.Success {
		response.Transaction = ""
	}
	state := "failed"
	if response.Success {
		state = "succeeded"
	}
	encoded, _ := json.Marshal(response)
	count, err := s.queries.FacilitatorFinish(ctx, row.AuthorizationID, state, encoded)
	if err != nil {
		return nil, err
	}
	if count > 0 {
		if response.Success {
			ch.successes.Add(1)
		} else {
			ch.failures.Add(1)
		}
	}
	return response, nil
}
