package facilitator

import (
	"bytes"
	"context"
	"errors"

	dbgen "github.com/islishude/etherview/internal/db/gen"
)

// settlementJournal is the durable boundary shared by settlement and recovery.
// The implementation is the generated PostgreSQL query set.
type settlementJournal interface {
	FacilitatorTouchRecovery(context.Context, string) (int64, error)
	FacilitatorSucceed(context.Context, string, []byte, []byte) (int64, error)
	FacilitatorReconcileUnsigned(context.Context, string) (int64, error)
	FacilitatorConfirmTransaction(context.Context, string, []byte) (int64, error)
	FacilitatorScanCursor(context.Context, string, []byte) (int64, error)
	FacilitatorAbortUnsigned(ctx context.Context, authorizationID string, response []byte) (int64, error)
	FacilitatorRejectUnsubmitted(ctx context.Context, arg dbgen.FacilitatorRejectUnsubmittedParams) (int64, error)
	FacilitatorConfirmBroadcast(ctx context.Context, authorizationID string, transactionHash []byte) (int64, error)
	FacilitatorFinish(ctx context.Context, authorizationID string, state string, response []byte) (int64, error)
	FacilitatorGet(ctx context.Context, authorizationID string) (dbgen.FacilitatorSettlement, error)
	FacilitatorPending(ctx context.Context, network string) ([]dbgen.FacilitatorSettlement, error)
	FacilitatorPendingCount(ctx context.Context, network string) (int64, error)
	FacilitatorReserve(ctx context.Context, arg dbgen.FacilitatorReserveParams) (int64, error)
	FacilitatorSaveSigned(ctx context.Context, arg dbgen.FacilitatorSaveSignedParams) (int64, error)
}

var _ settlementJournal = (*dbgen.Queries)(nil)

func confirmBroadcast(ctx context.Context, queries settlementJournal, id string, hash []byte) error {
	count, err := queries.FacilitatorConfirmBroadcast(ctx, id, hash)
	if err != nil || count == 1 {
		return err
	}
	row, err := queries.FacilitatorGet(ctx, id)
	if err != nil {
		return err
	}
	if !bytes.Equal(row.TransactionHash, hash) || row.BroadcastStatus != "confirmed" {
		return errors.New("journal_changed")
	}
	return nil
}
