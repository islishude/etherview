package metadata

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"

	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/ethereum/go-ethereum/common"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

var ErrExactNFTSourceConflict = errors.New("exact NFT metadata source observation conflicts with persisted block fact")

func (repository *PostgresRepository) NextNFTSource(ctx context.Context) (NFTSourceCandidate, bool, error) {
	if repository == nil || repository.db == nil {
		return NFTSourceCandidate{}, false, errors.New("select NFT metadata source using nil PostgreSQL repository")
	}
	var (
		addressBytes, hashBytes        []byte
		tokenID, blockNumber, standard string
	)
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(repository.chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(repository.db).MetadataNextNFTSource(ctx, queryValue0)
		if err != nil {
			return err
		}
		addressBytes = queryRow.TokenAddress
		tokenID = queryRow.PendingTokenID
		blockNumber = queryRow.PendingBlockNumber
		hashBytes = queryRow.BlockHash
		standard = queryRow.Standard
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		return NFTSourceCandidate{}, false, nil
	}
	if err != nil {
		return NFTSourceCandidate{}, false, fmt.Errorf("select NFT metadata source candidate: %w", err)
	}
	if len(addressBytes) != common.AddressLength {
		return NFTSourceCandidate{}, false, errors.New("decode NFT metadata source address")
	}
	address := common.BytesToAddress(addressBytes)
	if len(hashBytes) != common.HashLength {
		return NFTSourceCandidate{}, false, errors.New("decode NFT metadata source block hash")
	}
	hash := common.BytesToHash(hashBytes)
	height, err := parseSourceBlockNumber(blockNumber)
	if err != nil {
		return NFTSourceCandidate{}, false, err
	}
	candidate := NFTSourceCandidate{
		ChainID: repository.chainID, Token: address, TokenID: tokenID,
		BlockNumber: height, BlockHash: hash, Standard: NFTStandard(standard),
	}
	if err := candidate.validate(); err != nil {
		return NFTSourceCandidate{}, false, fmt.Errorf("decode NFT metadata source candidate: %w", err)
	}
	return candidate, true, nil
}

func (repository *PostgresRepository) NFTSourceCanonical(ctx context.Context, candidate NFTSourceCandidate) (bool, error) {
	if repository == nil || repository.db == nil {
		return false, errors.New("check NFT metadata source using nil PostgreSQL repository")
	}
	if err := candidate.validate(); err != nil {
		return false, err
	}
	if candidate.ChainID != repository.chainID {
		return false, errors.New("NFT metadata source chain differs from repository chain")
	}
	var canonical bool
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(candidate.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(candidate.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(repository.db).MetadataCanonicalObservation(ctx, queryValue0, queryValue1, mustHashBytes(candidate.BlockHash))
		if err != nil {
			return err
		}
		canonical = queryRow
		return nil
	}()
	if err != nil {
		return false, fmt.Errorf("check NFT metadata source canonicality: %w", err)
	}
	return canonical, nil
}

func (repository *PostgresRepository) RecordNFTSource(ctx context.Context, observation NFTSourceObservation) error {
	if repository == nil || repository.db == nil {
		return errors.New("record NFT metadata source using nil PostgreSQL repository")
	}
	if err := observation.validate(); err != nil {
		return err
	}
	if observation.Candidate.ChainID != repository.chainID {
		return errors.New("NFT metadata source chain differs from repository chain")
	}
	address := observation.Candidate.Token.Bytes()
	hash := mustHashBytes(observation.Candidate.BlockHash)

	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(observation.Candidate.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(observation.Candidate.TokenID); err != nil {
			return err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(strconv.FormatUint(observation.Candidate.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(repository.db).MetadataWriteInsertNFTSource(ctx, dbgen.MetadataWriteInsertNFTSourceParams{ChainID: queryValue0, TokenAddress: address, TokenID: queryValue1, BlockNumber: queryValue2, BlockHash: hash, Standard: string(observation.Candidate.Standard), State: string(observation.State), SourceUri: nullableString(observation.SourceURI), ErrorCode: nullableString(observation.ErrorCode)})
		if err != nil {
			return err
		}
		_ = int(queryRow)
		return nil
	}()
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("insert NFT metadata source observation: %w", err)
	}
	var (
		storedAddress, storedHash                         []byte
		storedTokenID, storedBlock, storedStandard, state string
		storedURI, storedCode                             pgtype.Text
	)
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(observation.Candidate.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(observation.Candidate.TokenID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(repository.db).MetadataExistingNFTSource(ctx, dbgen.MetadataExistingNFTSourceParams{ChainID: queryValue0, TokenAddress: address, TokenID: queryValue1, BlockHash: hash})
		if err != nil {
			return err
		}
		storedAddress = queryRow.TokenAddress
		storedTokenID = queryRow.TokenID
		storedBlock = queryRow.BlockNumber
		storedHash = queryRow.BlockHash
		storedStandard = queryRow.Standard
		state = queryRow.State
		var resultValue6 pgtype.Text
		if queryRow.SourceUri != nil {
			resultValue6 = pgtype.Text{String: *queryRow.SourceUri, Valid: true}
		}
		storedURI = resultValue6
		var resultValue8 pgtype.Text
		if queryRow.ErrorCode != nil {
			resultValue8 = pgtype.Text{String: *queryRow.ErrorCode, Valid: true}
		}
		storedCode = resultValue8
		return nil
	}()
	if err != nil {
		return fmt.Errorf("read existing NFT metadata source observation: %w", err)
	}
	if !bytes.Equal(storedAddress, address) || storedTokenID != observation.Candidate.TokenID ||
		storedBlock != strconv.FormatUint(observation.Candidate.BlockNumber, 10) || !bytes.Equal(storedHash, hash) ||
		storedStandard != string(observation.Candidate.Standard) || state != string(observation.State) ||
		storedURI.String != observation.SourceURI || storedURI.Valid != (observation.SourceURI != "") ||
		storedCode.String != observation.ErrorCode || storedCode.Valid != (observation.ErrorCode != "") {
		return ErrExactNFTSourceConflict
	}
	return nil
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	return new(value)
}
