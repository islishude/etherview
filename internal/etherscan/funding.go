package etherscan

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/ethereum/go-ethereum/common/hexutil"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

func (b *PostgresBackend) fundedBy(ctx context.Context, values url.Values) (fundedByResult, error) {
	address, addressBytes, err := parseAddressParameter(values.Get("address"), "address")
	if err != nil {
		return fundedByResult{}, err
	}
	if b.state == nil {
		return fundedByResult{}, ErrStateUnavailable
	}
	kind, referenceNumber, referenceHash, err := b.state.AccountKind(ctx, address.Hex())
	if err != nil {
		return fundedByResult{}, ErrStateUnavailable
	}
	switch kind {
	case "eoa", "delegated_eoa":
	case "contract":
		return fundedByResult{}, ErrFundedByEOARequired
	default:
		return fundedByResult{}, ErrStateUnavailable
	}
	referenceBlock, err := storedUint256(referenceNumber, "funding reference block")
	if err != nil {
		return fundedByResult{}, ErrStateUnavailable
	}
	_, referenceHashBytes, err := parseHashParameter(referenceHash, "funding reference block hash")
	if err != nil || strings.ToLower(referenceHash) != referenceHash {
		return fundedByResult{}, ErrStateUnavailable
	}

	tx, err := b.beginEnrichmentSnapshot(ctx)
	if err != nil {
		return fundedByResult{}, err
	}
	defer dbaccess.Rollback(ctx, tx)
	var canonical bool
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(b.chain); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(referenceNumber); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).EtherscanCanonicalReference(ctx, queryValue0, queryValue1, referenceHashBytes)
		if err != nil {
			return err
		}
		canonical = queryRow
		return nil
	}(); err != nil {
		return fundedByResult{}, fmt.Errorf("validate funding state reference: %w", err)
	}
	if !canonical {
		return fundedByResult{}, ErrStateUnavailable
	}
	var result fundedByResult
	var sourceBytes, transactionHashBytes []byte
	var valueHex, valueDecimal pgtype.Text
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(b.chain); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(referenceNumber); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).EtherscanFirstFunding(ctx, queryValue0, queryValue1, addressBytes)
		if err != nil {
			return err
		}
		result.Block = queryRow.BlockNumber
		sourceBytes = queryRow.SourceAddress
		transactionHashBytes = queryRow.TransactionHash
		valueHex = pgtype.Text{String: queryRow.ValueHex, Valid: queryRow.ValueHexPresent}
		valueDecimal = pgtype.Text{String: queryRow.ValueDecimal, Valid: queryRow.ValueDecimalPresent}
		result.TimeStamp = queryRow.BlockTimestamp
		return nil
	}()
	notFound := errors.Is(err, pgx.ErrNoRows)
	if !notFound && err != nil {
		return fundedByResult{}, fmt.Errorf("query first account funding: %w", err)
	}
	coverageEnd := referenceNumber
	if !notFound {
		candidateBlock, parseErr := storedUint256(result.Block, "funding block number")
		if parseErr != nil {
			return fundedByResult{}, parseErr
		}
		if candidateBlock.Cmp(referenceBlock) > 0 {
			return fundedByResult{}, errors.New("stored funding block exceeds the state reference")
		}
		coverageEnd = result.Block
	}
	if _, err := b.requireCanonicalStageRange(
		ctx, tx, traceStage, "0", &coverageEnd, ErrTraceUnavailable,
	); err != nil {
		return fundedByResult{}, err
	}
	if notFound {
		return fundedByResult{}, ErrNotFound
	}
	if _, err := storedUint256(result.TimeStamp, "funding block timestamp"); err != nil {
		return fundedByResult{}, err
	}
	if valueHex.Valid == valueDecimal.Valid {
		return fundedByResult{}, errors.New("stored funding value representation is invalid")
	}
	if valueHex.Valid {
		value, decodeErr := hexutil.DecodeBig(valueHex.String)
		if decodeErr != nil || value.Sign() <= 0 || value.BitLen() > 256 {
			return fundedByResult{}, errors.New("stored direct funding value is invalid")
		}
		result.Value = value.String()
	} else {
		value, decodeErr := storedUint256(valueDecimal.String, "internal funding value")
		if decodeErr != nil || value.Sign() <= 0 {
			return fundedByResult{}, errors.New("stored internal funding value is invalid")
		}
		result.Value = value.String()
	}
	source, err := addressFromBytes(sourceBytes)
	if err != nil || source == address {
		return fundedByResult{}, errors.New("stored funding source is invalid")
	}
	result.FundingAddress, err = checksumAddress(source)
	if err != nil {
		return fundedByResult{}, fmt.Errorf("checksum funding source: %w", err)
	}
	transactionHash, err := hashFromBytes(transactionHashBytes)
	if err != nil {
		return fundedByResult{}, err
	}
	result.FundingTxn = strings.ToLower(transactionHash.Hex())
	if err := tx.Commit(ctx); err != nil {
		return fundedByResult{}, fmt.Errorf("commit funding snapshot: %w", err)
	}
	canonical, err = b.state.IsCanonical(ctx, referenceNumber, referenceHash)
	if err != nil || !canonical {
		return fundedByResult{}, ErrStateUnavailable
	}
	return result, nil
}
