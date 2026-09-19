package query

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/publicquery"
)

// AddressOrigin resolves the first genesis- or transaction-backed origin for an
// account at one already-observed canonical state reference. Genesis
// allocations are authenticated independently by the canonical block-zero
// import; transaction-backed candidates still require genesis-through-
// reference Core and Trace proof before being called "first".
func (r *PostgresReader) AddressOrigin(
	ctx context.Context,
	rawAddress string,
	accountType gen.AddressSummaryType,
	referenceNumber uint64,
	referenceHash common.Hash,
) (gen.AddressOrigin, error) {
	address, err := ethrpc.ParseAddress(rawAddress)
	if err != nil {
		return gen.AddressOrigin{}, fmt.Errorf("invalid origin address: %w", err)
	}
	kind := gen.Funding
	if accountType == gen.AddressSummaryTypeContract {
		kind = gen.ContractCreation
	}
	result := gen.AddressOrigin{Kind: kind, State: gen.AddressOriginStateUnavailable}
	if accountType != gen.AddressSummaryTypeContract &&
		accountType != gen.AddressSummaryTypeEoa &&
		accountType != gen.AddressSummaryTypeDelegatedEoa {
		return result, nil
	}
	if r.startBlock != 0 {
		return result, nil
	}

	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return gen.AddressOrigin{}, fmt.Errorf("begin address origin snapshot: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)

	var canonical bool
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(fmt.Sprint(referenceNumber)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).QueryAddressOriginReference(ctx, queryValue0, queryValue1, referenceHash.Bytes())
		if err != nil {
			return err
		}
		canonical = queryRow
		return nil
	}(); err != nil {
		return gen.AddressOrigin{}, fmt.Errorf("validate address origin reference: %w", err)
	}
	if !canonical {
		return gen.AddressOrigin{}, fmt.Errorf("%w: address state reference is no longer canonical", publicquery.ErrNotReady)
	}

	var genesis bool
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).QueryGenesisAddressOrigin(ctx, queryValue0, address.Bytes())
		if err != nil {
			return err
		}
		genesis = queryRow
		return nil
	}(); err != nil {
		return gen.AddressOrigin{}, fmt.Errorf("check genesis address origin: %w", err)
	}
	if genesis {
		result.State = gen.AddressOriginStateGenesis
		if err := tx.Commit(ctx); err != nil {
			return gen.AddressOrigin{}, fmt.Errorf("commit genesis address origin snapshot: %w", err)
		}
		return result, nil
	}

	coverageEnd := referenceNumber
	var candidateBlock string
	var sourceBytes, transactionHashBytes, blockHashBytes []byte
	var originKind string
	var withdrawalIndex pgtype.Text
	chain, err := r.chainNumeric()
	if err != nil {
		return gen.AddressOrigin{}, err
	}
	queries := dbgen.New(r.db).WithTx(tx)
	if accountType == gen.AddressSummaryTypeContract {
		row, queryErr := queries.QueryFirstContractOrigin(ctx, chain, numericUint64(referenceNumber), address.Bytes())
		err = queryErr
		candidateBlock, sourceBytes, transactionHashBytes = row.BlockNumber, row.SourceAddress, row.TransactionHash
		originKind = string(gen.ContractCreation)
	} else {
		row, queryErr := queries.QueryFirstFundingOrigin(ctx, chain, numericUint64(referenceNumber), address.Bytes())
		err = queryErr
		candidateBlock, sourceBytes, transactionHashBytes, originKind, blockHashBytes = row.BlockNumber, row.SourceAddress, row.TransactionHash, row.OriginKind, row.BlockHash
		if row.WithdrawalIndex != nil {
			withdrawalIndex = pgtype.Text{String: *row.WithdrawalIndex, Valid: true}
		}
	}

	notFound := errors.Is(err, pgx.ErrNoRows)
	if !notFound && err != nil {
		return gen.AddressOrigin{}, fmt.Errorf("query address origin: %w", err)
	}
	if !notFound {
		coverageEnd, err = strconv.ParseUint(candidateBlock, 10, 64)
		if err != nil || strconv.FormatUint(coverageEnd, 10) != candidateBlock ||
			coverageEnd > referenceNumber {
			return gen.AddressOrigin{}, errors.New("stored address origin block is malformed")
		}
	}

	var complete bool
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(fmt.Sprint(coverageEnd)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).QueryAddressOriginCoverage(ctx, queryValue1, queryValue0)
		if err != nil {
			return err
		}
		if queryRow == nil {
			return errors.New("invalid stored query value")
		}
		complete = *queryRow
		return nil
	}(); err != nil {
		return gen.AddressOrigin{}, fmt.Errorf("check address origin coverage: %w", err)
	}
	if !complete {
		if err := tx.Commit(ctx); err != nil {
			return gen.AddressOrigin{}, fmt.Errorf("commit unavailable address origin snapshot: %w", err)
		}
		return result, nil
	}
	if notFound {
		result.State = gen.AddressOriginStateNotFound
		if err := tx.Commit(ctx); err != nil {
			return gen.AddressOrigin{}, fmt.Errorf("commit empty address origin snapshot: %w", err)
		}
		return result, nil
	}
	result.State = gen.AddressOriginStateFound
	switch gen.AddressOriginKind(originKind) {
	case gen.Funding, gen.ContractCreation:
		if len(sourceBytes) != common.AddressLength || len(transactionHashBytes) != common.HashLength {
			return gen.AddressOrigin{}, errors.New("stored funding origin identity is malformed")
		}
		source := common.BytesToAddress(sourceBytes).Hex()
		transactionHash := common.BytesToHash(transactionHashBytes).Hex()
		result.SourceAddress = &source
		result.TransactionHash = &transactionHash
	case gen.Withdrawal, gen.BlockFeeRecipient:
		if len(blockHashBytes) != common.HashLength {
			return gen.AddressOrigin{}, errors.New("stored block origin identity is malformed")
		}
		blockHash := common.BytesToHash(blockHashBytes).Hex()
		result.Kind = gen.AddressOriginKind(originKind)
		result.BlockHash = &blockHash
		if withdrawalIndex.Valid {
			index, parseErr := parseDecimalUint64(withdrawalIndex.String)
			if parseErr != nil || strconv.FormatUint(index, 10) != withdrawalIndex.String {
				return gen.AddressOrigin{}, errors.New("stored withdrawal origin index is malformed")
			}
			quantity := gen.Quantity(withdrawalIndex.String)
			result.WithdrawalIndex = &quantity
		}
	default:
		return gen.AddressOrigin{}, errors.New("stored address origin kind is invalid")
	}
	blockNumber := gen.Quantity(candidateBlock)
	if originKind == string(gen.Withdrawal) || originKind == string(gen.BlockFeeRecipient) {
		result.BlockNumber = &blockNumber
	}
	if err := tx.Commit(ctx); err != nil {
		return gen.AddressOrigin{}, fmt.Errorf("commit address origin snapshot: %w", err)
	}
	return result, nil
}
