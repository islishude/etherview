package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"strings"
	"time"
	"unicode"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const maximumNFTUpdateRawBytes = 32 << 10

type NFTUpdateKind string

const (
	NFTUpdateERC4906Single NFTUpdateKind = "erc4906_single"
	NFTUpdateERC4906Batch  NFTUpdateKind = "erc4906_batch"
	NFTUpdateERC1155URI    NFTUpdateKind = "erc1155_uri"
)

type NFTUpdateState string

const (
	NFTUpdateAccepted  NFTUpdateState = "accepted"
	NFTUpdateMalformed NFTUpdateState = "malformed"
)

var (
	metadataUpdateTopic      = crypto.Keccak256Hash([]byte("MetadataUpdate(uint256)"))
	batchMetadataUpdateTopic = crypto.Keccak256Hash([]byte("BatchMetadataUpdate(uint256,uint256)"))
	erc1155URITopic          = crypto.Keccak256Hash([]byte("URI(string,uint256)"))
)

type NFTUpdateCandidate struct {
	ChainID         string
	BlockNumber     uint64
	BlockHash       common.Hash
	LogIndex        uint64
	TransactionHash common.Hash
	Token           common.Address
	Standard        NFTStandard
	Raw             []byte
}

func (candidate NFTUpdateCandidate) validate() error {
	if err := validateDecimal(candidate.ChainID, "update chain ID"); err != nil {
		return err
	}
	if candidate.BlockHash == (common.Hash{}) || candidate.TransactionHash == (common.Hash{}) ||
		candidate.Token == (common.Address{}) {
		return errors.New("NFT metadata update identity is incomplete")
	}
	if candidate.LogIndex > math.MaxInt64 {
		return errors.New("NFT metadata update log index exceeds PostgreSQL range")
	}
	if candidate.Standard != NFTStandardERC721 && candidate.Standard != NFTStandardERC1155 {
		return fmt.Errorf("unsupported NFT metadata update standard %q", candidate.Standard)
	}
	if len(candidate.Raw) == 0 || len(candidate.Raw) > maximumNFTUpdateRawBytes {
		return fmt.Errorf("NFT metadata update raw log must contain between 1 and %d bytes", maximumNFTUpdateRawBytes)
	}
	return nil
}

type NFTUpdateObservation struct {
	Candidate   NFTUpdateCandidate
	Kind        NFTUpdateKind
	State       NFTUpdateState
	FromTokenID string
	ToTokenID   string
	ErrorCode   string
}

func (observation NFTUpdateObservation) validate() error {
	if err := observation.Candidate.validate(); err != nil {
		return err
	}
	switch observation.Kind {
	case NFTUpdateERC4906Single, NFTUpdateERC4906Batch, NFTUpdateERC1155URI:
		// The state-specific validation below applies the standard relationship
		// only to accepted events. A recognized topic emitted by the wrong token
		// standard is retained as a malformed exact log fact.
	default:
		return fmt.Errorf("invalid NFT metadata update kind %q", observation.Kind)
	}
	switch observation.State {
	case NFTUpdateAccepted:
		if observation.Kind == NFTUpdateERC1155URI && observation.Candidate.Standard != NFTStandardERC1155 {
			return errors.New("ERC-1155 URI observation requires ERC-1155")
		}
		if observation.Kind != NFTUpdateERC1155URI && observation.Candidate.Standard != NFTStandardERC721 {
			return errors.New("ERC-4906 update observation requires ERC-721")
		}
		if observation.ErrorCode != "" {
			return errors.New("accepted NFT metadata update contains an error code")
		}
		if err := validateDecimal(observation.FromTokenID, "update from token ID"); err != nil {
			return err
		}
		if err := validateDecimal(observation.ToTokenID, "update to token ID"); err != nil {
			return err
		}
		from, _ := new(big.Int).SetString(observation.FromTokenID, 10)
		to, _ := new(big.Int).SetString(observation.ToTokenID, 10)
		if from.Cmp(maximumUint256) > 0 || to.Cmp(maximumUint256) > 0 || from.Cmp(to) > 0 {
			return errors.New("NFT metadata update token range is invalid")
		}
		if observation.Kind != NFTUpdateERC4906Batch && from.Cmp(to) != 0 {
			return errors.New("single NFT metadata update contains a token range")
		}
	case NFTUpdateMalformed:
		if observation.FromTokenID != "" || observation.ToTokenID != "" {
			return errors.New("malformed NFT metadata update contains token IDs")
		}
		if err := validateErrorCode(observation.ErrorCode); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid NFT metadata update state %q", observation.State)
	}
	return nil
}

type NFTUpdateRepository interface {
	NextNFTUpdate(context.Context) (NFTUpdateCandidate, bool, error)
	RecordNFTUpdate(context.Context, NFTUpdateObservation) (bool, error)
}

type UpdateDiscovererOptions struct {
	PollInterval time.Duration
	Logger       *slog.Logger
}

type UpdateDiscoverer struct {
	repository NFTUpdateRepository
	options    UpdateDiscovererOptions
	logger     *slog.Logger
}

func NewUpdateDiscoverer(repository NFTUpdateRepository, options UpdateDiscovererOptions) (*UpdateDiscoverer, error) {
	if repository == nil {
		return nil, errors.New("NFT metadata update discovery requires a repository")
	}
	if options.PollInterval <= 0 {
		options.PollInterval = time.Second
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &UpdateDiscoverer{repository: repository, options: options, logger: options.Logger}, nil
}

func (*UpdateDiscoverer) Name() string { return "metadata-update-discovery" }

func (discoverer *UpdateDiscoverer) Run(ctx context.Context) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
		processed, err := discoverer.ProcessOnce(ctx)
		if err != nil {
			return err
		}
		if processed {
			timer.Reset(0)
		} else {
			timer.Reset(discoverer.options.PollInterval)
		}
	}
}

func (discoverer *UpdateDiscoverer) ProcessOnce(ctx context.Context) (bool, error) {
	startedAt := time.Now()
	candidate, found, err := discoverer.repository.NextNFTUpdate(ctx)
	if err != nil || !found {
		return false, err
	}
	observation, err := parseNFTUpdate(candidate)
	if err != nil {
		return true, err
	}
	canonical, err := discoverer.repository.RecordNFTUpdate(ctx, observation)
	if err != nil {
		return true, err
	}
	result, code := string(observation.State), observation.ErrorCode
	if !canonical {
		result, code = "stale", "update_block_noncanonical"
	}
	discoverer.logUpdate(ctx, observation, result, code, startedAt)
	return true, nil
}

func parseNFTUpdate(candidate NFTUpdateCandidate) (NFTUpdateObservation, error) {
	if err := candidate.validate(); err != nil {
		return NFTUpdateObservation{}, err
	}
	var wire types.Log
	if err := json.Unmarshal(candidate.Raw, &wire); err != nil {
		return NFTUpdateObservation{}, fmt.Errorf("decode NFT metadata update log: %w", err)
	}
	if uint64(wire.Index) != candidate.LogIndex || wire.TxHash != candidate.TransactionHash ||
		wire.BlockHash != candidate.BlockHash || wire.BlockNumber != candidate.BlockNumber ||
		wire.Address != candidate.Token || wire.Removed {
		return NFTUpdateObservation{}, errors.New("stored NFT metadata update log identity is inconsistent")
	}
	if len(wire.Topics) == 0 {
		return NFTUpdateObservation{}, errors.New("stored NFT metadata update log has no topic")
	}
	observation := NFTUpdateObservation{Candidate: candidate}
	switch wire.Topics[0] {
	case metadataUpdateTopic:
		observation.Kind = NFTUpdateERC4906Single
		if candidate.Standard != NFTStandardERC721 {
			return malformedNFTUpdate(observation, "standard_mismatch"), nil
		}
		if len(wire.Topics) != 1 {
			return malformedNFTUpdate(observation, "topic_count_invalid"), nil
		}
		if len(wire.Data) != common.HashLength {
			return malformedNFTUpdate(observation, "data_length_invalid"), nil
		}
		id := new(big.Int).SetBytes(wire.Data).String()
		observation.State, observation.FromTokenID, observation.ToTokenID = NFTUpdateAccepted, id, id
	case batchMetadataUpdateTopic:
		observation.Kind = NFTUpdateERC4906Batch
		if candidate.Standard != NFTStandardERC721 {
			return malformedNFTUpdate(observation, "standard_mismatch"), nil
		}
		if len(wire.Topics) != 1 {
			return malformedNFTUpdate(observation, "topic_count_invalid"), nil
		}
		if len(wire.Data) != 2*common.HashLength {
			return malformedNFTUpdate(observation, "data_length_invalid"), nil
		}
		from, to := new(big.Int).SetBytes(wire.Data[:32]), new(big.Int).SetBytes(wire.Data[32:])
		if from.Cmp(to) > 0 {
			return malformedNFTUpdate(observation, "token_range_invalid"), nil
		}
		observation.State = NFTUpdateAccepted
		observation.FromTokenID, observation.ToTokenID = from.String(), to.String()
	case erc1155URITopic:
		observation.Kind = NFTUpdateERC1155URI
		if candidate.Standard != NFTStandardERC1155 {
			return malformedNFTUpdate(observation, "standard_mismatch"), nil
		}
		if len(wire.Topics) != 2 {
			return malformedNFTUpdate(observation, "topic_count_invalid"), nil
		}
		uri, valid := decodeSourceString(wire.Data, MaxSourceURIBytes)
		if !valid || strings.TrimSpace(uri) == "" || strings.IndexFunc(uri, unicode.IsControl) >= 0 {
			return malformedNFTUpdate(observation, "uri_payload_invalid"), nil
		}
		id := new(big.Int).SetBytes(wire.Topics[1][:]).String()
		observation.State, observation.FromTokenID, observation.ToTokenID = NFTUpdateAccepted, id, id
	default:
		return NFTUpdateObservation{}, errors.New("repository returned an unsupported NFT metadata update topic")
	}
	if err := observation.validate(); err != nil {
		return NFTUpdateObservation{}, err
	}
	return observation, nil
}

func malformedNFTUpdate(observation NFTUpdateObservation, code string) NFTUpdateObservation {
	observation.State = NFTUpdateMalformed
	observation.FromTokenID, observation.ToTokenID, observation.ErrorCode = "", "", code
	return observation
}

func (discoverer *UpdateDiscoverer) logUpdate(
	ctx context.Context,
	observation NFTUpdateObservation,
	result, code string,
	startedAt time.Time,
) {
	if discoverer == nil || discoverer.logger == nil {
		return
	}
	discoverer.logger.LogAttrs(ctx, slog.LevelInfo, "metadata update observation transitioned",
		slog.String("event", "metadata_update_observation_transitioned"),
		slog.String("component", discoverer.Name()),
		slog.Group("nft",
			slog.String("contract", strings.ToLower(observation.Candidate.Token.Hex())),
			slog.String("standard", string(observation.Candidate.Standard)),
			slog.String("from_id", observation.FromTokenID),
			slog.String("to_id", observation.ToTokenID),
		),
		slog.Group("block",
			slog.String("number", fmt.Sprint(observation.Candidate.BlockNumber)),
			slog.String("hash", strings.ToLower(observation.Candidate.BlockHash.Hex())),
			slog.Uint64("log_index", observation.Candidate.LogIndex),
		),
		slog.Group("transition", slog.String("result", result), slog.String("code", code)),
		slog.String("kind", string(observation.Kind)),
		slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
	)
}
