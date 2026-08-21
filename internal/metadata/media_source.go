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

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/db/gen"
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
	BlockHash   common.Hash
}

// NFTImageSource selects an image URI from a persisted, canonical NFT
// metadata document. The URI remains server-side and must only be consumed by
// MediaProxy; callers must never accept a replacement URI from an HTTP client.
type NFTImageSource interface {
	SelectNFTImage(context.Context, common.Address, string) (NFTImageSelection, error)
	NFTImageCurrent(context.Context, common.Address, string, NFTImageSelection) (bool, error)
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
	if err := validateDecimal(chainID, "media source chain ID"); err != nil {
		return nil, err
	}
	return &PostgresImageSource{db: db, chainID: chainID}, nil
}

func (source *PostgresImageSource) SelectNFTImage(ctx context.Context, address common.Address, tokenID string) (NFTImageSelection, error) {
	if source == nil || source.db == nil {
		return NFTImageSelection{}, errors.New("select NFT media using nil PostgreSQL source")
	}
	addressBytes := address.Bytes()
	if err := validateDecimal(tokenID, "media token ID"); err != nil {
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
	err := source.db.QueryRowContext(ctx, dbgen.MetadataSelectCanonicalNFTImage, source.chainID, addressBytes, tokenID).Scan(&state, &image, &blockNumber, &blockHash)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if queryErr := source.db.QueryRowContext(ctx, dbgen.MetadataAnyNFTMetadata, source.chainID, addressBytes, tokenID).Scan(&exists); queryErr != nil {
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
		if len(blockHash) != common.HashLength {
			return NFTImageSelection{}, errors.New("select canonical NFT media: invalid block hash")
		}
		hash := common.BytesToHash(blockHash)
		return NFTImageSelection{URI: uri, BlockNumber: height, BlockHash: hash}, nil
	default:
		return NFTImageSelection{}, fmt.Errorf("select canonical NFT media: unsupported metadata state")
	}
}

func (source *PostgresImageSource) NFTImageCurrent(
	ctx context.Context,
	address common.Address,
	tokenID string,
	selection NFTImageSelection,
) (bool, error) {
	if source == nil || source.db == nil {
		return false, errors.New("validate NFT media using nil PostgreSQL source")
	}
	addressBytes := address.Bytes()
	if err := validateDecimal(tokenID, "media token ID"); err != nil {
		return false, err
	}
	if strings.TrimSpace(selection.URI) == "" || len(selection.URI) > MaxSourceURIBytes {
		return false, errors.New("validate NFT media: invalid selection")
	}
	var current bool
	err := source.db.QueryRowContext(ctx, dbgen.MetadataCurrentNFTImage, source.chainID, addressBytes, tokenID, strconv.FormatUint(selection.BlockNumber, 10),
		mustHashBytes(selection.BlockHash), selection.URI,
	).Scan(&current)
	if err != nil {
		return false, fmt.Errorf("validate canonical NFT media selection: %w", err)
	}
	return current, nil
}
