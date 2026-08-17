package metadata

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/netpolicy"
)

const (
	maximumDisplayNameRunes        = 512
	maximumDisplayDescriptionRunes = 16_384
	maximumDisplayAttributes       = 100
	maximumDisplayAttributeRunes   = 512
)

var (
	ErrNFTMetadataNotFound     = errors.New("canonical NFT metadata was not found")
	ErrNFTMetadataNoncanonical = errors.New("NFT metadata exists only for a noncanonical block")
)

type NFTMetadataImageState string

const (
	NFTMetadataImageAvailable          NFTMetadataImageState = "available"
	NFTMetadataImageUnavailable        NFTMetadataImageState = "unavailable"
	NFTMetadataImageMissing            NFTMetadataImageState = "missing"
	NFTMetadataImageUnsafe             NFTMetadataImageState = "unsafe"
	NFTMetadataImageUnsupported        NFTMetadataImageState = "unsupported"
	NFTMetadataImageGatewayUnavailable NFTMetadataImageState = "gateway_unavailable"
)

type NFTMetadataObservation struct {
	BlockNumber uint64
	BlockHash   common.Hash
}

type NFTMetadataAttribute struct {
	TraitType   string
	Value       string
	DisplayType string
}

type NFTMetadataImage struct {
	State        NFTMetadataImageState
	URL          string
	SourceScheme string
}

type NFTMetadata struct {
	State                 State
	Observation           NFTMetadataObservation
	Name                  string
	NameTruncated         bool
	Description           string
	DescriptionTruncated  bool
	Attributes            []NFTMetadataAttribute
	OmittedAttributeCount int
	Image                 NFTMetadataImage
}

type NFTMetadataReader interface {
	NFTMetadata(context.Context, common.Address, string) (NFTMetadata, error)
}

// PostgresMetadataReader exposes a bounded display projection of the newest
// exact NFT metadata observation that is still canonical. It never returns the
// metadata source URI, resolved document URI, raw JSON, or image bytes.
type PostgresMetadataReader struct {
	db           *sql.DB
	chainID      string
	linkResolver *Client
}

func NewPostgresMetadataReader(db *sql.DB, chainID, ipfsGateway string) (*PostgresMetadataReader, error) {
	if db == nil {
		return nil, errors.New("NFT metadata reader requires a database")
	}
	if err := validateDecimal(chainID, "display reader chain ID"); err != nil {
		return nil, err
	}
	resolver, err := New(Policy{IPFSGateway: ipfsGateway}, nil)
	if err != nil {
		return nil, fmt.Errorf("configure NFT metadata display links: %w", err)
	}
	return &PostgresMetadataReader{db: db, chainID: chainID, linkResolver: resolver}, nil
}

func (reader *PostgresMetadataReader) NFTMetadata(
	ctx context.Context,
	address common.Address,
	tokenID string,
) (NFTMetadata, error) {
	if reader == nil || reader.db == nil || reader.linkResolver == nil {
		return NFTMetadata{}, errors.New("read NFT metadata using an unconfigured reader")
	}
	if err := validateDecimal(tokenID, "display token ID"); err != nil {
		return NFTMetadata{}, err
	}
	parsedTokenID, _ := new(big.Int).SetString(tokenID, 10)
	if parsedTokenID.Cmp(maximumUint256) > 0 {
		return NFTMetadata{}, errors.New("metadata display token ID exceeds uint256")
	}

	tx, err := reader.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return NFTMetadata{}, fmt.Errorf("begin NFT metadata display snapshot: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var (
		state       State
		document    []byte
		blockNumber string
		blockHash   []byte
	)
	err = tx.QueryRowContext(ctx, selectCanonicalNFTMetadataSQL,
		reader.chainID, address.Bytes(), tokenID,
	).Scan(&state, &document, &blockNumber, &blockHash)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if queryErr := tx.QueryRowContext(ctx, anyNFTMetadataSQL,
			reader.chainID, address.Bytes(), tokenID,
		).Scan(&exists); queryErr != nil {
			return NFTMetadata{}, fmt.Errorf("check historical NFT metadata display state: %w", queryErr)
		}
		if exists {
			return NFTMetadata{}, ErrNFTMetadataNoncanonical
		}
		return NFTMetadata{}, ErrNFTMetadataNotFound
	}
	if err != nil {
		return NFTMetadata{}, fmt.Errorf("select canonical NFT metadata display state: %w", err)
	}

	height, err := strconv.ParseUint(blockNumber, 10, 64)
	if err != nil || strconv.FormatUint(height, 10) != blockNumber {
		return NFTMetadata{}, errors.New("select canonical NFT metadata display: invalid block number")
	}
	if len(blockHash) != common.HashLength {
		return NFTMetadata{}, errors.New("select canonical NFT metadata display: invalid block hash")
	}
	result := NFTMetadata{
		State: state,
		Observation: NFTMetadataObservation{
			BlockNumber: height,
			BlockHash:   common.BytesToHash(blockHash),
		},
		Attributes: make([]NFTMetadataAttribute, 0),
		Image:      NFTMetadataImage{State: NFTMetadataImageUnavailable},
	}
	switch state {
	case StatePending, StateUnavailable, StateUnsafe, StateError:
		if len(document) != 0 {
			return NFTMetadata{}, errors.New("non-available NFT metadata display row contains a document")
		}
	case StateAvailable:
		if len(document) == 0 {
			return NFTMetadata{}, errors.New("available NFT metadata display row has no document")
		}
		projection, projectErr := projectNFTMetadataDocument(document, reader.linkResolver)
		if projectErr != nil {
			return NFTMetadata{}, projectErr
		}
		projection.State = state
		projection.Observation = result.Observation
		result = projection
	default:
		return NFTMetadata{}, errors.New("select canonical NFT metadata display: unsupported state")
	}
	if err := tx.Commit(); err != nil {
		return NFTMetadata{}, fmt.Errorf("commit NFT metadata display snapshot: %w", err)
	}
	return result, nil
}

func projectNFTMetadataDocument(document []byte, resolver *Client) (NFTMetadata, error) {
	if err := validateDocument(document); err != nil {
		return NFTMetadata{}, fmt.Errorf("validate stored NFT metadata display document: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return NFTMetadata{}, fmt.Errorf("decode stored NFT metadata display document: %w", err)
	}
	result := NFTMetadata{
		Attributes: make([]NFTMetadataAttribute, 0),
		Image:      NFTMetadataImage{State: NFTMetadataImageMissing},
	}
	if name, ok := root["name"].(string); ok {
		result.Name, result.NameTruncated = truncateRunes(name, maximumDisplayNameRunes)
	}
	if description, ok := root["description"].(string); ok {
		result.Description, result.DescriptionTruncated = truncateRunes(description, maximumDisplayDescriptionRunes)
	}
	if attributes, ok := root["attributes"].([]any); ok {
		for _, raw := range attributes {
			attribute, valid := projectNFTMetadataAttribute(raw)
			if !valid || len(result.Attributes) >= maximumDisplayAttributes {
				result.OmittedAttributeCount++
				continue
			}
			result.Attributes = append(result.Attributes, attribute)
		}
	}
	if image, ok := root["image"].(string); ok {
		result.Image = projectNFTMetadataImage(image, resolver)
	}
	return result, nil
}

func projectNFTMetadataAttribute(raw any) (NFTMetadataAttribute, bool) {
	object, ok := raw.(map[string]any)
	if !ok {
		return NFTMetadataAttribute{}, false
	}
	traitType, ok := object["trait_type"].(string)
	if !ok || strings.TrimSpace(traitType) == "" || exceedsRunes(traitType, maximumDisplayAttributeRunes) {
		return NFTMetadataAttribute{}, false
	}
	rawValue, exists := object["value"]
	if !exists {
		return NFTMetadataAttribute{}, false
	}
	value, ok := displayScalar(rawValue)
	if !ok || exceedsRunes(value, maximumDisplayAttributeRunes) {
		return NFTMetadataAttribute{}, false
	}
	attribute := NFTMetadataAttribute{TraitType: traitType, Value: value}
	if rawDisplayType, exists := object["display_type"]; exists && rawDisplayType != nil {
		displayType, valid := rawDisplayType.(string)
		if !valid || exceedsRunes(displayType, maximumDisplayAttributeRunes) {
			return NFTMetadataAttribute{}, false
		}
		attribute.DisplayType = displayType
	}
	return attribute, true
}

func displayScalar(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case json.Number:
		return typed.String(), true
	case bool:
		return strconv.FormatBool(typed), true
	case nil:
		return "null", true
	default:
		return "", false
	}
}

func projectNFTMetadataImage(raw string, resolver *Client) NFTMetadataImage {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return NFTMetadataImage{State: NFTMetadataImageMissing}
	}
	if len(trimmed) > MaxSourceURIBytes || strings.IndexFunc(trimmed, unicode.IsControl) >= 0 {
		return NFTMetadataImage{State: NFTMetadataImageUnsafe}
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.User != nil || parsed.Fragment != "" {
		return NFTMetadataImage{State: NFTMetadataImageUnsafe}
	}
	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "ipfs":
		if resolver == nil || resolver.policy.IPFSGateway == "" {
			return NFTMetadataImage{State: NFTMetadataImageGatewayUnavailable}
		}
	case "https":
		// Supported below.
	default:
		return NFTMetadataImage{State: NFTMetadataImageUnsupported}
	}
	if resolver == nil {
		return NFTMetadataImage{State: NFTMetadataImageUnsafe}
	}
	target, err := resolver.resolveTarget(trimmed)
	if err != nil || unsafeBrowserNavigationHost(target.URL) {
		return NFTMetadataImage{State: NFTMetadataImageUnsafe}
	}
	return NFTMetadataImage{
		State: NFTMetadataImageAvailable, URL: target.URL.String(), SourceScheme: scheme,
	}
}

func unsafeBrowserNavigationHost(target *url.URL) bool {
	if target == nil {
		return true
	}
	host := strings.TrimSuffix(strings.ToLower(target.Hostname()), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.Contains(host, "%") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return !netpolicy.PublicIP(ip)
	}
	return false
}

func truncateRunes(value string, maximum int) (string, bool) {
	if maximum <= 0 {
		return "", value != ""
	}
	runes := []rune(value)
	if len(runes) <= maximum {
		return value, false
	}
	return string(runes[:maximum]), true
}

func exceedsRunes(value string, maximum int) bool {
	return len([]rune(value)) > maximum
}

const selectCanonicalNFTMetadataSQL = `
SELECT metadata.state,
       metadata.document,
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
