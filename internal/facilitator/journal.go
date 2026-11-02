package facilitator

import (
	"context"

	dbgen "github.com/islishude/etherview/internal/db/gen"
)

// settlementJournal is the durable boundary shared by settlement and recovery.
// The implementation is the generated PostgreSQL query set.
type settlementJournal interface {
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
