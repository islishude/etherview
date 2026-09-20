package verifiedselector

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	pgx "github.com/jackc/pgx/v5"

	dbgen "github.com/islishude/etherview/internal/db/gen"
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

func Persist(ctx context.Context, tx pgx.Tx, identity Identity, abiJSON []byte) error {
	selectors, parseErr := enrich.NormalizeVerifiedFunctionSelectors(abiJSON)
	status, warning := "complete", ""
	if parseErr != nil {
		status, warning = "invalid", "verified_abi_invalid"
		selectors = nil
	}
	result, err := func() (int64, error) {
		var queryValue0 pgtype.UUID
		if err := queryValue0.Scan(identity.JobID); err != nil {
			return 0, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(identity.ChainID); err != nil {
			return 0, err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(strconv.FormatUint(identity.ValidFromBlock, 10)); err != nil {
			return 0, err
		}
		if len(selectors) < -2147483648 || len(selectors) > 2147483647 {
			return 0, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).VerifiedSelectorWritePersistStatement1(ctx, dbgen.VerifiedSelectorWritePersistStatement1Params{VerificationJobID: queryValue0, RequestDigest: identity.RequestDigest, ChainID: queryValue1, Address: identity.Address, CodeHash: identity.CodeHash, ValidFromBlock: queryValue2, Status: status, FunctionCount: int32(len(selectors)), Warning: warning})
	}()
	if err != nil {
		return fmt.Errorf("persist verified function selector set: %w", err)
	}
	inserted := result
	if inserted == 0 {
		return nil
	}
	for _, selector := range selectors {
		if err := func() error {
			var queryValue0 pgtype.UUID
			if err := queryValue0.Scan(identity.JobID); err != nil {
				return err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(identity.ChainID); err != nil {
				return err
			}
			return dbgen.New(tx).VerifiedSelectorWritePersistStatement2(ctx, dbgen.VerifiedSelectorWritePersistStatement2Params{VerificationJobID: queryValue0, ChainID: queryValue1, Address: identity.Address, CodeHash: identity.CodeHash, Selector: selector.Selector[:], Signature: selector.Signature, FunctionName: selector.Name, AbiEntry: []byte(string(selector.ABIEntry))})
		}(); err != nil {
			return fmt.Errorf("persist verified function selector: %w", err)
		}
	}
	return nil
}
