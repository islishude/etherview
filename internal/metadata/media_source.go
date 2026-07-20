package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/islishude/etherview/internal/ethrpc"
)

var (
	ErrMediaSourceNotFound    = errors.New("canonical NFT metadata was not found")
	ErrMediaImageNotFound     = errors.New("canonical NFT metadata has no image")
	ErrMediaSourcePending     = errors.New("canonical NFT metadata is pending")
	ErrMediaSourceUnavailable = errors.New("canonical NFT metadata is unavailable")
	ErrMediaSourceUnsafe      = errors.New("canonical NFT metadata is unsafe")
	ErrMediaSourceError       = errors.New("canonical NFT metadata failed")
)

// NFTImageSource selects an image URI from a persisted, canonical NFT
// metadata document. The URI remains server-side and must only be consumed by
// MediaProxy; callers must never accept a replacement URI from an HTTP client.
type NFTImageSource interface {
	NFTImageURI(context.Context, ethrpc.Address, string) (string, error)
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

func (source *PostgresImageSource) NFTImageURI(ctx context.Context, address ethrpc.Address, tokenID string) (string, error) {
	if source == nil || source.db == nil {
		return "", errors.New("select NFT media using nil PostgreSQL source")
	}
	addressBytes, err := address.Bytes()
	if err != nil {
		return "", fmt.Errorf("select NFT media: %w", err)
	}
	if err := validateDecimal(tokenID, 78, "media token ID"); err != nil {
		return "", err
	}

	var (
		state State
		image sql.NullString
	)
	err = source.db.QueryRowContext(ctx, selectCanonicalNFTImageSQL,
		source.chainID, addressBytes, tokenID,
	).Scan(&state, &image)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrMediaSourceNotFound
	}
	if err != nil {
		return "", fmt.Errorf("select canonical NFT media state: %w", err)
	}

	switch state {
	case StatePending:
		return "", ErrMediaSourcePending
	case StateUnavailable:
		return "", ErrMediaSourceUnavailable
	case StateUnsafe:
		return "", ErrMediaSourceUnsafe
	case StateError:
		return "", ErrMediaSourceError
	case StateAvailable:
		if !image.Valid || strings.TrimSpace(image.String) == "" {
			return "", ErrMediaImageNotFound
		}
		uri := strings.TrimSpace(image.String)
		if len(uri) > MaxSourceURIBytes || strings.IndexFunc(uri, unicode.IsControl) >= 0 {
			return "", ErrMediaSourceUnsafe
		}
		return uri, nil
	default:
		return "", fmt.Errorf("select canonical NFT media: unsupported metadata state")
	}
}

const selectCanonicalNFTImageSQL = `
SELECT metadata.state,
       CASE
           WHEN jsonb_typeof(metadata.document -> 'image') = 'string'
           THEN metadata.document ->> 'image'
           ELSE NULL
       END
FROM external_metadata AS metadata
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = metadata.chain_id
 AND canonical.number = metadata.observed_block_number
 AND canonical.block_hash = metadata.observed_block_hash
WHERE metadata.chain_id = $1::numeric
  AND metadata.resource_kind = 'nft'
  AND metadata.token_address = $2
  AND metadata.token_id = $3::numeric
LIMIT 1`
