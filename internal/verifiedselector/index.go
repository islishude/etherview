package verifiedselector

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

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
	result, err := tx.ExecContext(ctx, `
		INSERT INTO verified_function_selector_sets (
			verification_job_id, request_digest, chain_id, address, code_hash,
			valid_from_block, status, function_count, warning
		) VALUES ($1::uuid, $2, $3::numeric, $4, $5, $6::numeric, $7, $8, $9)
		ON CONFLICT (verification_job_id) DO NOTHING`,
		identity.JobID, identity.RequestDigest, identity.ChainID, identity.Address,
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
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO verified_function_selectors (
				verification_job_id, chain_id, address, code_hash,
				selector, signature, function_name, abi_entry
			) VALUES ($1::uuid, $2::numeric, $3, $4, $5, $6, $7, $8::jsonb)`,
			identity.JobID, identity.ChainID, identity.Address, identity.CodeHash,
			selector.Selector[:], selector.Signature, selector.Name, string(selector.ABIEntry),
		); err != nil {
			return fmt.Errorf("persist verified function selector: %w", err)
		}
	}
	return nil
}
