package facilitator

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/islishude/etherview/internal/x402settlement"
	x402 "github.com/x402-foundation/x402/go/v2"
	"github.com/x402-foundation/x402/go/v2/mechanisms/evm"
)

type verificationContext struct{}
type verificationObservation struct{ unavailable atomic.Bool }

func recordRPCFailure(ctx context.Context, err error) {
	if err != nil && !evm.IsContractRevert(err) {
		if o, ok := ctx.Value(verificationContext{}).(*verificationObservation); ok {
			o.unavailable.Store(true)
		}
	}
}

func verifyPayment(ctx context.Context, ch *chainRuntime, in paymentRequest) (*x402.VerifyResponse, error) {
	observed := &verificationObservation{}
	ctx = context.WithValue(ctx, verificationContext{}, observed)
	result, err := ch.scheme.Verify(ctx, in.PaymentPayload, in.PaymentRequirements, nil)
	if !observed.unavailable.Load() && err == nil && result != nil && result.IsValid {
		return result, nil
	}
	// A spent authorization may have paid before verification, or while the SDK
	// was simulating. Verify the signed identity independently before reconciling.
	if proof, e := proofFor(in); e == nil {
		header, e := ch.adapter.client.HeaderByNumber(ctx, nil)
		if e != nil {
			return nil, &operationError{code: "chain_unavailable"}
		}
		used, e := proof.consumed(ctx, ch, header.Hash())
		if e != nil {
			return nil, &operationError{code: "chain_unavailable"}
		}
		if used {
			return &x402.VerifyResponse{IsValid: false, InvalidReason: "authorization_reconciliation_required", Payer: proof.payer.Hex()}, nil
		}
	}
	// An SDK fallback probe can fail even after the authorization was spent.
	// Independent pinned nonce evidence above is sufficient to enter read-only
	// reconciliation; it is never sufficient to publish a payment or sign again.
	if observed.unavailable.Load() {
		return nil, &operationError{code: "chain_unavailable"}
	}
	reason := ""
	if failure, ok := errors.AsType[*x402.VerifyError](err); ok {
		reason = x402settlement.BusinessReason(failure.InvalidReason)
	} else if err == nil && result != nil {
		reason = x402settlement.BusinessReason(result.InvalidReason)
	}
	if reason == "" {
		return nil, &operationError{code: "chain_unavailable"}
	}
	return &x402.VerifyResponse{IsValid: false, InvalidReason: reason}, nil
}
