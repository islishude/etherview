package facilitator

import (
	"context"
	"errors"

	"github.com/ethereum/go-ethereum/common"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

// pendingJournal adapts the SDK cache interface to the durable authorization journal.
// SDK signature keys are deliberately not authoritative; the validated nonce identity
// in the request context is shared by every signature encoding of that authorization.
type pendingJournal struct{ queries *dbgen.Queries }

func (p pendingJournal) Get(ctx context.Context, _ string) (string, bool, error) {
	id, ok := ctx.Value(authorizationContext{}).(string)
	if !ok {
		return "", false, errors.New("missing_reservation")
	}
	row, err := p.queries.FacilitatorGet(ctx, id)
	if err != nil {
		return "", false, err
	}
	if len(row.TransactionHash) == 0 {
		return "", false, nil
	}
	return common.BytesToHash(row.TransactionHash).Hex(), true, nil
}
func (p pendingJournal) Set(ctx context.Context, key, hash string) error {
	saved, ok, err := p.Get(ctx, key)
	if err != nil {
		return err
	}
	if !ok || saved != hash {
		return errors.New("journal_hash_mismatch")
	}
	return nil
}
func (p pendingJournal) Delete(context.Context, string) error {
	// Only the outer receipt/transfer reconciliation may publish a terminal journal state.
	return nil
}
