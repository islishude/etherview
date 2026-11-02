package billing

import (
	"context"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/billing/x402wire"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

func (ledger *PrepaidLedger) ObserveTopupProgress(ctx context.Context, paymentID, owner, intentID string, progress x402wire.SettlementProgress, observedAt time.Time) error {
	payment, fence, intent, err := topupTransitionIDs(paymentID, owner, intentID)
	if err != nil || observedAt.IsZero() {
		return ErrInvalidInput
	}
	if progress.Stage != "submitting" && progress.Stage != "confirming" && progress.Stage != "unknown" {
		return ErrInvalidInput
	}
	var hash []byte
	if progress.Transaction != "" {
		value, valid := transactionHashFromHex(progress.Transaction)
		if !valid || value == (common.Hash{}) {
			return ErrInvalidInput
		}
		hash = value.Bytes()
	}
	if (progress.Stage == "confirming" && hash == nil) || (progress.Stage == "submitting" && hash != nil) {
		return ErrInvalidInput
	}
	err = dbaccess.WithQueries(ctx, ledger.db, func(queries *dbgen.Queries) error {
		_, queryErr := queries.ObserveBillingTopupProgress(ctx, dbgen.ObserveBillingTopupProgressParams{
			PaymentID: payment, ReservationOwner: fence, IntentID: intent,
			Progress: progress.Stage, TransactionHash: hash, ObservedAt: postgresTime(observedAt),
		})
		return queryErr
	})
	return topupTransitionError("observe top-up progress", err)
}

// The original settlement remains the only mutating facilitator request. Join
// the observer before publishing its outcome so no late observation can win.
func (dispatcher *TopupDispatcher) observeProgress(ctx context.Context, payment x402wire.Payment, requirement x402wire.Requirement, paymentID, owner, intentID string) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pollCtx, stop := context.WithTimeout(ctx, time.Second)
				progress, err := dispatcher.facilitator.PaymentProgress(pollCtx, payment, requirement)
				if err == nil && progress != nil {
					_ = dispatcher.accounts.ObserveTopupProgress(pollCtx, paymentID, owner, intentID, *progress, dispatcher.now().UTC())
				}
				stop()
			}
		}
	}()
	return func() { cancel(); <-done }
}
