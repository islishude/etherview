package verifiedselector

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enrich"
)

type Identity struct {
	JobID          string
	RequestDigest  []byte
	ChainID        string
	Address        []byte
	CodeHash       []byte
	ValidFromBlock uint64
}

func Persist(ctx context.Context, tx *sql.Tx, identity Identity, abiJSON []byte) error {
	selectors, parseErr := enrich.NormalizeVerifiedFunctionSelectors(abiJSON)
	status, warning := "complete", ""
	if parseErr != nil {
		status, warning = "invalid", "verified_abi_invalid"
		selectors = nil
	}
	result, err := tx.ExecContext(ctx, dbgen.VerifiedSelectorWritePersistStatement1, identity.JobID, identity.RequestDigest, identity.ChainID, identity.Address,
		identity.CodeHash, strconv.FormatUint(identity.ValidFromBlock, 10), status,
		len(selectors), warning,
	)
	if err != nil {
		return fmt.Errorf("persist verified function selector set: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read verified function selector set result: %w", err)
	}
	if inserted == 0 {
		return nil
	}
	for _, selector := range selectors {
		if _, err := tx.ExecContext(ctx, dbgen.VerifiedSelectorWritePersistStatement2, identity.JobID, identity.ChainID, identity.Address, identity.CodeHash,
			selector.Selector[:], selector.Signature, selector.Name, string(selector.ABIEntry),
		); err != nil {
			return fmt.Errorf("persist verified function selector: %w", err)
		}
	}
	return nil
}
