package query

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/db/gen"
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
	query := dbgen.QueryFirstFundingOrigin
	if accountType == gen.AddressSummaryTypeContract {
		kind = gen.ContractCreation
		query = dbgen.QueryFirstContractOrigin
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

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return gen.AddressOrigin{}, fmt.Errorf("begin address origin snapshot: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var canonical bool
	if err := tx.QueryRowContext(ctx, dbgen.QueryAddressOriginReference, r.chainID, fmt.Sprint(referenceNumber), referenceHash.Bytes()).Scan(&canonical); err != nil {
		return gen.AddressOrigin{}, fmt.Errorf("validate address origin reference: %w", err)
	}
	if !canonical {
		return gen.AddressOrigin{}, fmt.Errorf("%w: address state reference is no longer canonical", publicquery.ErrNotReady)
	}

	var genesis bool
	if err := tx.QueryRowContext(ctx, dbgen.QueryGenesisAddressOrigin, r.chainID, address.Bytes()).Scan(&genesis); err != nil {
		return gen.AddressOrigin{}, fmt.Errorf("check genesis address origin: %w", err)
	}
	if genesis {
		result.State = gen.AddressOriginStateGenesis
		if err := tx.Commit(); err != nil {
			return gen.AddressOrigin{}, fmt.Errorf("commit genesis address origin snapshot: %w", err)
		}
		return result, nil
	}

	coverageEnd := referenceNumber
	var candidateBlock string
	var sourceBytes, transactionHashBytes, blockHashBytes []byte
	var originKind string
	var withdrawalIndex sql.NullString
	if accountType == gen.AddressSummaryTypeContract {
		err = tx.QueryRowContext(ctx, query,
			r.chainID, fmt.Sprint(referenceNumber), address.Bytes(),
		).Scan(&candidateBlock, &sourceBytes, &transactionHashBytes)
		originKind = string(gen.ContractCreation)
	} else {
		err = tx.QueryRowContext(ctx, query,
			r.chainID, fmt.Sprint(referenceNumber), address.Bytes(),
		).Scan(&candidateBlock, &sourceBytes, &transactionHashBytes, &originKind, &blockHashBytes, &withdrawalIndex)
	}
	notFound := errors.Is(err, sql.ErrNoRows)
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
	if err := tx.QueryRowContext(ctx, dbgen.QueryAddressOriginCoverage, r.chainID, fmt.Sprint(coverageEnd)).Scan(&complete); err != nil {
		return gen.AddressOrigin{}, fmt.Errorf("check address origin coverage: %w", err)
	}
	if !complete {
		if err := tx.Commit(); err != nil {
			return gen.AddressOrigin{}, fmt.Errorf("commit unavailable address origin snapshot: %w", err)
		}
		return result, nil
	}
	if notFound {
		result.State = gen.AddressOriginStateNotFound
		if err := tx.Commit(); err != nil {
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
	if err := tx.Commit(); err != nil {
		return gen.AddressOrigin{}, fmt.Errorf("commit address origin snapshot: %w", err)
	}
	return result, nil
}
