package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode"

	"github.com/islishude/etherview/internal/ethrpc"
)

var (
	ErrMediaSourceNotFound     = errors.New("canonical NFT metadata was not found")
	ErrMediaImageNotFound      = errors.New("canonical NFT metadata has no image")
	ErrMediaSourcePending      = errors.New("canonical NFT metadata is pending")
	ErrMediaSourceUnavailable  = errors.New("canonical NFT metadata is unavailable")
	ErrMediaSourceUnsafe       = errors.New("canonical NFT metadata is unsafe")
	ErrMediaSourceError        = errors.New("canonical NFT metadata failed")
	ErrMediaSourceNoncanonical = errors.New("NFT metadata exists only for a noncanonical block")
)

type NFTImageSelection struct {
	URI         string
	BlockNumber uint64
	BlockHash   ethrpc.Hash
}

// NFTImageSource selects an image URI from a persisted, canonical NFT
// metadata document. The URI remains server-side and must only be consumed by
// MediaProxy; callers must never accept a replacement URI from an HTTP client.
type NFTImageSource interface {
	SelectNFTImage(context.Context, ethrpc.Address, string) (NFTImageSelection, error)
	NFTImageCurrent(context.Context, ethrpc.Address, string, NFTImageSelection) (bool, error)
}

// PostgresImageSource binds media selection to one configured chain. Only a
// document observed from the block that is currently canonical at its height
// is eligible.
type PostgresImageSource struct {
	db      *sql.DB
	chainID string
}

func NewPostgresImageSource(db *sql.DB, chainID string) (*PostgresImageSource, error) {
	if db == nil {
		return nil, errors.New("NFT media source requires a database")
	}
	if err := validateDecimal(chainID, 78, "media source chain ID"); err != nil {
		return nil, err
	}
	return &PostgresImageSource{db: db, chainID: chainID}, nil
}

func (source *PostgresImageSource) SelectNFTImage(ctx context.Context, address ethrpc.Address, tokenID string) (NFTImageSelection, error) {
	if source == nil || source.db == nil {
		return NFTImageSelection{}, errors.New("select NFT media using nil PostgreSQL source")
	}
	addressBytes, err := address.Bytes()
	if err != nil {
		return NFTImageSelection{}, fmt.Errorf("select NFT media: %w", err)
	}
	if err := validateDecimal(tokenID, 78, "media token ID"); err != nil {
		return NFTImageSelection{}, err
	}
	parsedTokenID, _ := new(big.Int).SetString(tokenID, 10)
	if parsedTokenID.Cmp(maximumUint256) > 0 {
		return NFTImageSelection{}, errors.New("media token ID exceeds uint256")
	}

	var (
		state       State
		image       sql.NullString
		blockNumber string
		blockHash   []byte
	)
	err = source.db.QueryRowContext(ctx, selectCanonicalNFTImageSQL,
		source.chainID, addressBytes, tokenID,
	).Scan(&state, &image, &blockNumber, &blockHash)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if queryErr := source.db.QueryRowContext(ctx, anyNFTMetadataSQL,
			source.chainID, addressBytes, tokenID,
		).Scan(&exists); queryErr != nil {
			return NFTImageSelection{}, fmt.Errorf("check historical NFT media state: %w", queryErr)
		}
		if exists {
			return NFTImageSelection{}, ErrMediaSourceNoncanonical
		}
		return NFTImageSelection{}, ErrMediaSourceNotFound
	}
	if err != nil {
		return NFTImageSelection{}, fmt.Errorf("select canonical NFT media state: %w", err)
	}

	switch state {
	case StatePending:
		return NFTImageSelection{}, ErrMediaSourcePending
	case StateUnavailable:
		return NFTImageSelection{}, ErrMediaSourceUnavailable
	case StateUnsafe:
		return NFTImageSelection{}, ErrMediaSourceUnsafe
	case StateError:
		return NFTImageSelection{}, ErrMediaSourceError
	case StateAvailable:
		if !image.Valid || strings.TrimSpace(image.String) == "" {
			return NFTImageSelection{}, ErrMediaImageNotFound
		}
		uri := strings.TrimSpace(image.String)
		if len(uri) > MaxSourceURIBytes || strings.IndexFunc(uri, unicode.IsControl) >= 0 {
			return NFTImageSelection{}, ErrMediaSourceUnsafe
		}
		height, err := strconv.ParseUint(blockNumber, 10, 64)
		if err != nil || strconv.FormatUint(height, 10) != blockNumber {
			return NFTImageSelection{}, errors.New("select canonical NFT media: invalid block number")
		}
		hash, err := ethrpc.ParseHash(ethrpc.DataFromBytes(blockHash).String())
		if err != nil {
			return NFTImageSelection{}, errors.New("select canonical NFT media: invalid block hash")
		}
		return NFTImageSelection{URI: uri, BlockNumber: height, BlockHash: hash}, nil
	default:
		return NFTImageSelection{}, fmt.Errorf("select canonical NFT media: unsupported metadata state")
	}
}

func (source *PostgresImageSource) NFTImageCurrent(
	ctx context.Context,
	address ethrpc.Address,
	tokenID string,
	selection NFTImageSelection,
) (bool, error) {
	if source == nil || source.db == nil {
		return false, errors.New("validate NFT media using nil PostgreSQL source")
	}
	addressBytes, err := address.Bytes()
	if err != nil {
		return false, fmt.Errorf("validate NFT media: %w", err)
	}
	if err := validateDecimal(tokenID, 78, "media token ID"); err != nil {
		return false, err
	}
	if strings.TrimSpace(selection.URI) == "" || len(selection.URI) > MaxSourceURIBytes {
		return false, errors.New("validate NFT media: invalid selection")
	}
	var current bool
	err = source.db.QueryRowContext(ctx, currentNFTImageSQL,
		source.chainID, addressBytes, tokenID, strconv.FormatUint(selection.BlockNumber, 10),
		mustHashBytes(selection.BlockHash), selection.URI,
	).Scan(&current)
	if err != nil {
		return false, fmt.Errorf("validate canonical NFT media selection: %w", err)
	}
	return current, nil
}

const selectCanonicalNFTImageSQL = `
SELECT metadata.state,
       CASE
           WHEN jsonb_typeof(metadata.document -> 'image') = 'string'
           THEN metadata.document ->> 'image'
           ELSE NULL
       END,
       metadata.observed_block_number::text,
       metadata.observed_block_hash
FROM external_metadata AS metadata
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = metadata.chain_id
 AND canonical.number = metadata.observed_block_number
 AND canonical.block_hash = metadata.observed_block_hash
WHERE metadata.chain_id = $1::numeric
  AND metadata.resource_kind = 'nft'
  AND metadata.token_address = $2
  AND metadata.token_id = $3::numeric
ORDER BY metadata.observed_block_number DESC, metadata.observed_block_hash
LIMIT 1`

const anyNFTMetadataSQL = `
SELECT EXISTS (
    SELECT 1 FROM external_metadata
    WHERE chain_id = $1::numeric AND resource_kind = 'nft'
      AND token_address = $2 AND token_id = $3::numeric
)`

const currentNFTImageSQL = `
SELECT EXISTS (
    SELECT 1
    FROM external_metadata AS metadata
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = metadata.chain_id
     AND canonical.number = metadata.observed_block_number
     AND canonical.block_hash = metadata.observed_block_hash
    WHERE metadata.chain_id = $1::numeric
      AND metadata.resource_kind = 'nft'
      AND metadata.token_address = $2
      AND metadata.token_id = $3::numeric
      AND metadata.observed_block_number = $4::numeric
      AND metadata.observed_block_hash = $5
      AND metadata.state = 'available'
      AND jsonb_typeof(metadata.document -> 'image') = 'string'
      AND btrim(metadata.document ->> 'image') = $6
      AND NOT EXISTS (
          SELECT 1
          FROM external_metadata AS newer
          JOIN canonical_blocks AS newer_canonical
            ON newer_canonical.chain_id = newer.chain_id
           AND newer_canonical.number = newer.observed_block_number
           AND newer_canonical.block_hash = newer.observed_block_hash
          WHERE newer.chain_id = metadata.chain_id
            AND newer.resource_kind = 'nft'
            AND newer.token_address = metadata.token_address
            AND newer.token_id = metadata.token_id
            AND newer.observed_block_number > metadata.observed_block_number
      )
)`
