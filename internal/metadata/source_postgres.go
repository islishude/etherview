package metadata

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/db/gen"
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
	err := repository.db.QueryRowContext(ctx, dbgen.MetadataNextNFTSource, repository.chainID).Scan(
		&addressBytes, &tokenID, &blockNumber, &hashBytes, &standard,
	)
	if errors.Is(err, sql.ErrNoRows) {
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
	err := repository.db.QueryRowContext(ctx, dbgen.MetadataCanonicalObservation, candidate.ChainID, strconv.FormatUint(candidate.BlockNumber, 10), mustHashBytes(candidate.BlockHash)).Scan(&canonical)
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
	var inserted int
	err := repository.db.QueryRowContext(ctx, dbgen.MetadataWriteInsertNFTSource, observation.Candidate.ChainID, address, observation.Candidate.TokenID,
		strconv.FormatUint(observation.Candidate.BlockNumber, 10), hash,
		observation.Candidate.Standard, observation.State, nullableString(observation.SourceURI),
		nullableString(observation.ErrorCode),
	).Scan(&inserted)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("insert NFT metadata source observation: %w", err)
	}
	var (
		storedAddress, storedHash                         []byte
		storedTokenID, storedBlock, storedStandard, state string
		storedURI, storedCode                             sql.NullString
	)
	err = repository.db.QueryRowContext(ctx, dbgen.MetadataExistingNFTSource, observation.Candidate.ChainID, address, observation.Candidate.TokenID, hash).Scan(&storedAddress, &storedTokenID, &storedBlock, &storedHash, &storedStandard, &state, &storedURI, &storedCode)
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

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
