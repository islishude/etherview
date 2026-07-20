package metadata

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/islishude/etherview/internal/ethrpc"
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
	err := repository.db.QueryRowContext(ctx, nextNFTSourceSQL, repository.chainID).Scan(
		&addressBytes, &tokenID, &blockNumber, &hashBytes, &standard,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return NFTSourceCandidate{}, false, nil
	}
	if err != nil {
		return NFTSourceCandidate{}, false, fmt.Errorf("select NFT metadata source candidate: %w", err)
	}
	address, err := ethrpc.ParseAddress(ethrpc.DataFromBytes(addressBytes).String())
	if err != nil {
		return NFTSourceCandidate{}, false, errors.New("decode NFT metadata source address")
	}
	hash, err := ethrpc.ParseHash(ethrpc.DataFromBytes(hashBytes).String())
	if err != nil {
		return NFTSourceCandidate{}, false, errors.New("decode NFT metadata source block hash")
	}
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
	err := repository.db.QueryRowContext(ctx, canonicalObservationSQL,
		candidate.ChainID, strconv.FormatUint(candidate.BlockNumber, 10), mustHashBytes(candidate.BlockHash),
	).Scan(&canonical)
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
	address, _ := observation.Candidate.Token.Bytes()
	hash := mustHashBytes(observation.Candidate.BlockHash)
	var inserted int
	err := repository.db.QueryRowContext(ctx, insertNFTSourceSQL,
		observation.Candidate.ChainID, address, observation.Candidate.TokenID,
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
	err = repository.db.QueryRowContext(ctx, existingNFTSourceSQL,
		observation.Candidate.ChainID, address, observation.Candidate.TokenID, hash,
	).Scan(&storedAddress, &storedTokenID, &storedBlock, &storedHash, &storedStandard, &state, &storedURI, &storedCode)
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

const nextNFTSourceSQL = `
SELECT event.token_address, event.token_id::text, event.block_number::text,
       event.block_hash, event.standard
FROM token_events AS event
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = event.chain_id
 AND canonical.number = event.block_number
 AND canonical.block_hash = event.block_hash
WHERE event.chain_id = $1::numeric
  AND event.token_id IS NOT NULL
  AND event.standard IN ('erc721', 'erc1155')
  AND NOT EXISTS (
      SELECT 1
      FROM external_metadata AS metadata
      WHERE metadata.chain_id = event.chain_id
        AND metadata.resource_kind = 'nft'
        AND metadata.token_address = event.token_address
        AND metadata.token_id = event.token_id
        AND metadata.observed_block_hash = event.block_hash
  )
  AND NOT EXISTS (
      SELECT 1
      FROM nft_metadata_source_observations AS source
      WHERE source.chain_id = event.chain_id
        AND source.token_address = event.token_address
        AND source.token_id = event.token_id
        AND source.block_hash = event.block_hash
  )
ORDER BY event.block_number, event.log_index, event.sub_index,
         event.token_address, event.token_id
LIMIT 1`

const insertNFTSourceSQL = `
INSERT INTO nft_metadata_source_observations (
    chain_id, token_address, token_id, block_number, block_hash,
    standard, state, source_uri, error_code
) VALUES (
    $1::numeric, $2, $3::numeric, $4::numeric, $5,
    $6, $7, $8, $9
)
ON CONFLICT DO NOTHING
RETURNING 1`

const existingNFTSourceSQL = `
SELECT token_address, token_id::text, block_number::text, block_hash,
       standard, state, source_uri, error_code
FROM nft_metadata_source_observations
WHERE chain_id = $1::numeric AND token_address = $2
  AND token_id = $3::numeric AND block_hash = $4`
