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
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(repository.chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(repository.db).MetadataNextNFTUpdateLog(ctx, dbgen.MetadataNextNFTUpdateLogParams{ChainID: queryValue0, Topic0: metadataUpdateTopic[:], Topic02: batchMetadataUpdateTopic[:], Topic03: erc1155URITopic[:]})
		if err != nil {
			return err
		}
		blockNumber = queryRow.LogBlockNumber
		blockHash = queryRow.BlockHash
		logIndex = queryRow.LogIndex
		transactionHash = queryRow.TxHash
		addressBytes = queryRow.Address
		raw = queryRow.Raw
		standard = queryRow.Standard
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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
	var fromTokenID *string
	var toTokenID *string
	var errorCode *string
	if observation.State == NFTUpdateAccepted {
		fromTokenID, toTokenID = new(observation.FromTokenID), new(observation.ToTokenID)
	} else {
		errorCode = new(observation.ErrorCode)
	}

	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(observation.Candidate.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(observation.Candidate.BlockNumber, 10)); err != nil {
			return err
		}
		var queryValue2 pgtype.Numeric
		if fromTokenID != nil {
			if err := queryValue2.Scan(*fromTokenID); err != nil {
				return err
			}
		}
		var queryValue3 pgtype.Numeric
		if toTokenID != nil {
			if err := queryValue3.Scan(*toTokenID); err != nil {
				return err
			}
		}
		queryRow, err := dbgen.New(repository.db).MetadataWriteInsertNFTUpdateObservation(ctx, dbgen.MetadataWriteInsertNFTUpdateObservationParams{ChainID: queryValue0, BlockNumber: queryValue1, BlockHash: mustHashBytes(observation.Candidate.BlockHash), LogIndex: int64(observation.Candidate.LogIndex), TokenAddress: observation.Candidate.Token.Bytes(), Standard: string(observation.Candidate.Standard), EventKind: string(observation.Kind), State: string(observation.State), FromTokenID: queryValue2, ToTokenID: queryValue3, ErrorCode: errorCode})
		if err != nil {
			return err
		}
		_ = int(queryRow)
		return nil
	}()
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("insert NFT metadata update observation: %w", err)
	}
	var (
		storedBlock, storedStandard, storedKind, storedState string
		storedFrom, storedTo                                 string
		storedHash, storedAddress                            []byte
		storedLogIndex                                       int64
		storedCode                                           pgtype.Text
	)
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(observation.Candidate.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(observation.Candidate.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(repository.db).MetadataExistingNFTUpdateObservation(ctx, dbgen.MetadataExistingNFTUpdateObservationParams{ChainID: queryValue0, BlockNumber: queryValue1, BlockHash: mustHashBytes(observation.Candidate.BlockHash), LogIndex: int64(observation.Candidate.LogIndex)})
		if err != nil {
			return err
		}
		storedBlock = queryRow.BlockNumber
		storedHash = queryRow.BlockHash
		storedLogIndex = queryRow.LogIndex
		storedAddress = queryRow.TokenAddress
		storedStandard = queryRow.Standard
		storedKind = queryRow.EventKind
		storedState = queryRow.State
		storedFrom = queryRow.FromTokenID
		storedTo = queryRow.ToTokenID
		var resultValue9 pgtype.Text
		if queryRow.ErrorCode != nil {
			resultValue9 = pgtype.Text{String: *queryRow.ErrorCode, Valid: true}
		}
		storedCode = resultValue9
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(observation.Candidate.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(observation.Candidate.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(repository.db).MetadataCanonicalObservation(ctx, queryValue0, queryValue1, mustHashBytes(observation.Candidate.BlockHash))
		if err != nil {
			return err
		}
		canonical = queryRow
		return nil
	}(); err != nil {
		return false, fmt.Errorf("recheck existing NFT metadata update canonicality: %w", err)
	}
	return canonical, nil
}
