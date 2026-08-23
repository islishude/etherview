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

var ErrExactNFTUpdateConflict = errors.New("exact NFT metadata update observation conflicts with persisted log fact")

func (repository *PostgresRepository) NextNFTUpdate(ctx context.Context) (NFTUpdateCandidate, bool, error) {
	if repository == nil || repository.db == nil {
		return NFTUpdateCandidate{}, false, errors.New("select NFT metadata update using nil PostgreSQL repository")
	}
	var (
		blockNumber, standard                         string
		blockHash, transactionHash, addressBytes, raw []byte
		logIndex                                      int64
	)
	err := repository.db.QueryRowContext(ctx, dbgen.MetadataNextNFTUpdateLog,
		repository.chainID, metadataUpdateTopic[:], batchMetadataUpdateTopic[:], erc1155URITopic[:],
	).Scan(&blockNumber, &blockHash, &logIndex, &transactionHash, &addressBytes, &raw, &standard)
	if errors.Is(err, sql.ErrNoRows) {
		return NFTUpdateCandidate{}, false, nil
	}
	if err != nil {
		return NFTUpdateCandidate{}, false, fmt.Errorf("select NFT metadata update candidate: %w", err)
	}
	if logIndex < 0 || len(blockHash) != common.HashLength || len(transactionHash) != common.HashLength ||
		len(addressBytes) != common.AddressLength {
		return NFTUpdateCandidate{}, false, errors.New("decode NFT metadata update identity")
	}
	height, err := parseSourceBlockNumber(blockNumber)
	if err != nil {
		return NFTUpdateCandidate{}, false, err
	}
	candidate := NFTUpdateCandidate{
		ChainID: repository.chainID, BlockNumber: height, BlockHash: common.BytesToHash(blockHash),
		LogIndex: uint64(logIndex), TransactionHash: common.BytesToHash(transactionHash),
		Token: common.BytesToAddress(addressBytes), Standard: NFTStandard(standard), Raw: append([]byte(nil), raw...),
	}
	if err := candidate.validate(); err != nil {
		return NFTUpdateCandidate{}, false, fmt.Errorf("decode NFT metadata update candidate: %w", err)
	}
	return candidate, true, nil
}

func (repository *PostgresRepository) RecordNFTUpdate(ctx context.Context, observation NFTUpdateObservation) (bool, error) {
	if repository == nil || repository.db == nil {
		return false, errors.New("record NFT metadata update using nil PostgreSQL repository")
	}
	if err := observation.validate(); err != nil {
		return false, err
	}
	if observation.Candidate.ChainID != repository.chainID {
		return false, errors.New("NFT metadata update chain differs from repository chain")
	}
	var fromTokenID, toTokenID, errorCode any
	if observation.State == NFTUpdateAccepted {
		fromTokenID, toTokenID = observation.FromTokenID, observation.ToTokenID
	} else {
		errorCode = observation.ErrorCode
	}
	var inserted int
	err := repository.db.QueryRowContext(ctx, dbgen.MetadataWriteInsertNFTUpdateObservation,
		observation.Candidate.ChainID,
		strconv.FormatUint(observation.Candidate.BlockNumber, 10),
		mustHashBytes(observation.Candidate.BlockHash),
		int64(observation.Candidate.LogIndex),
		observation.Candidate.Token.Bytes(),
		observation.Candidate.Standard,
		observation.Kind,
		observation.State,
		fromTokenID,
		toTokenID,
		errorCode,
	).Scan(&inserted)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("insert NFT metadata update observation: %w", err)
	}
	var (
		storedBlock, storedStandard, storedKind, storedState string
		storedFrom, storedTo                                 string
		storedHash, storedAddress                            []byte
		storedLogIndex                                       int64
		storedCode                                           sql.NullString
	)
	err = repository.db.QueryRowContext(ctx, dbgen.MetadataExistingNFTUpdateObservation,
		observation.Candidate.ChainID,
		strconv.FormatUint(observation.Candidate.BlockNumber, 10),
		mustHashBytes(observation.Candidate.BlockHash),
		int64(observation.Candidate.LogIndex),
	).Scan(
		&storedBlock, &storedHash, &storedLogIndex, &storedAddress,
		&storedStandard, &storedKind, &storedState, &storedFrom, &storedTo, &storedCode,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read existing NFT metadata update observation: %w", err)
	}
	if storedBlock != strconv.FormatUint(observation.Candidate.BlockNumber, 10) ||
		!bytes.Equal(storedHash, mustHashBytes(observation.Candidate.BlockHash)) ||
		storedLogIndex != int64(observation.Candidate.LogIndex) ||
		!bytes.Equal(storedAddress, observation.Candidate.Token.Bytes()) ||
		storedStandard != string(observation.Candidate.Standard) || storedKind != string(observation.Kind) ||
		storedState != string(observation.State) || storedFrom != observation.FromTokenID ||
		storedTo != observation.ToTokenID || storedCode.String != observation.ErrorCode ||
		storedCode.Valid != (observation.ErrorCode != "") {
		return false, ErrExactNFTUpdateConflict
	}
	var canonical bool
	if err := repository.db.QueryRowContext(ctx, dbgen.MetadataCanonicalObservation,
		observation.Candidate.ChainID,
		strconv.FormatUint(observation.Candidate.BlockNumber, 10),
		mustHashBytes(observation.Candidate.BlockHash),
	).Scan(&canonical); err != nil {
		return false, fmt.Errorf("recheck existing NFT metadata update canonicality: %w", err)
	}
	return canonical, nil
}
