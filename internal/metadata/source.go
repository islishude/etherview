package metadata

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/islishude/etherview/internal/ethrpc"
)

type NFTStandard string

const (
	NFTStandardERC721  NFTStandard = "erc721"
	NFTStandardERC1155 NFTStandard = "erc1155"
)

type NFTSourceCandidate struct {
	ChainID     string
	Token       ethrpc.Address
	TokenID     string
	BlockNumber uint64
	BlockHash   ethrpc.Hash
	Standard    NFTStandard
}

func (candidate NFTSourceCandidate) validate() error {
	request := NFTRequest{
		ChainID: candidate.ChainID, Token: candidate.Token, TokenID: candidate.TokenID,
		BlockNumber: candidate.BlockNumber, BlockHash: candidate.BlockHash,
		SourceURI: "urn:etherview:source-validation",
	}
	if err := request.Validate(); err != nil {
		return err
	}
	if candidate.Standard != NFTStandardERC721 && candidate.Standard != NFTStandardERC1155 {
		return fmt.Errorf("unsupported NFT metadata standard %q", candidate.Standard)
	}
	return nil
}

type NFTSourceState string

const (
	NFTSourceFound       NFTSourceState = "found"
	NFTSourceUnavailable NFTSourceState = "unavailable"
)

type NFTSourceObservation struct {
	Candidate NFTSourceCandidate
	State     NFTSourceState
	SourceURI string
	ErrorCode string
}

func (observation NFTSourceObservation) validate() error {
	if err := observation.Candidate.validate(); err != nil {
		return err
	}
	switch observation.State {
	case NFTSourceFound:
		request := NFTRequest{
			ChainID: observation.Candidate.ChainID, Token: observation.Candidate.Token,
			TokenID: observation.Candidate.TokenID, BlockNumber: observation.Candidate.BlockNumber,
			BlockHash: observation.Candidate.BlockHash, SourceURI: observation.SourceURI,
		}
		if err := request.Validate(); err != nil {
			return err
		}
		if observation.ErrorCode != "" {
			return errors.New("found NFT metadata source contains an error code")
		}
	case NFTSourceUnavailable:
		if observation.SourceURI != "" {
			return errors.New("unavailable NFT metadata source contains a URI")
		}
		if err := validateErrorCode(observation.ErrorCode); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid NFT metadata source state %q", observation.State)
	}
	return nil
}

type NFTSourceRepository interface {
	NextNFTSource(context.Context) (NFTSourceCandidate, bool, error)
	NFTSourceCanonical(context.Context, NFTSourceCandidate) (bool, error)
	RecordNFTSource(context.Context, NFTSourceObservation) error
	EnqueueNFT(context.Context, NFTRequest) (EnqueueResult, error)
}

type SourceDiscovererOptions struct {
	PollInterval time.Duration
	MaxAttempts  uint32
}

type SourceDiscoverer struct {
	repository NFTSourceRepository
	pool       *ethrpc.Pool
	options    SourceDiscovererOptions
}

func NewSourceDiscoverer(repository NFTSourceRepository, pool *ethrpc.Pool, options SourceDiscovererOptions) (*SourceDiscoverer, error) {
	if repository == nil {
		return nil, errors.New("NFT metadata source discovery requires a repository")
	}
	if pool == nil || len(pool.Names(ethrpc.PurposeState)) == 0 {
		return nil, errors.New("NFT metadata source discovery requires a state RPC endpoint")
	}
	if options.PollInterval <= 0 {
		options.PollInterval = time.Second
	}
	if options.MaxAttempts == 0 {
		options.MaxAttempts = DefaultMaxAttempts
	}
	if options.MaxAttempts > MaximumMaxAttempts {
		return nil, fmt.Errorf("NFT metadata max attempts exceeds %d", MaximumMaxAttempts)
	}
	return &SourceDiscoverer{repository: repository, pool: pool, options: options}, nil
}

func (*SourceDiscoverer) Name() string { return "metadata-source-discovery" }

func (discoverer *SourceDiscoverer) Run(ctx context.Context) error {
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

func (discoverer *SourceDiscoverer) ProcessOnce(ctx context.Context) (bool, error) {
	candidate, found, err := discoverer.repository.NextNFTSource(ctx)
	if err != nil || !found {
		return false, err
	}
	if err := candidate.validate(); err != nil {
		return true, fmt.Errorf("metadata source repository returned invalid candidate: %w", err)
	}
	endpoint, err := discoverer.pool.Acquire(ethrpc.PurposeState)
	if err != nil {
		return false, nil
	}
	sourceURI, unavailableCode, retry := discoverNFTSource(ctx, endpoint.Client, candidate)
	if retry {
		discoverer.pool.ReportFailure(endpoint.Name)
		return false, nil
	}
	discoverer.pool.ReportSuccess(endpoint.Name)
	canonical, err := discoverer.repository.NFTSourceCanonical(ctx, candidate)
	if err != nil {
		return true, err
	}
	if !canonical {
		return true, nil
	}
	if unavailableCode != "" {
		return true, discoverer.repository.RecordNFTSource(ctx, NFTSourceObservation{
			Candidate: candidate, State: NFTSourceUnavailable, ErrorCode: unavailableCode,
		})
	}
	request := NFTRequest{
		ChainID: candidate.ChainID, Token: candidate.Token, TokenID: candidate.TokenID,
		BlockNumber: candidate.BlockNumber, BlockHash: candidate.BlockHash,
		SourceURI: sourceURI, MaxAttempts: discoverer.options.MaxAttempts,
	}
	if _, err := discoverer.repository.EnqueueNFT(ctx, request); err != nil {
		return true, err
	}
	if err := discoverer.repository.RecordNFTSource(ctx, NFTSourceObservation{
		Candidate: candidate, State: NFTSourceFound, SourceURI: sourceURI,
	}); err != nil {
		return true, err
	}
	return true, nil
}

var (
	erc721TokenURISelector = []byte{0xc8, 0x7b, 0x56, 0xdd}
	erc1155URISelector     = []byte{0x0e, 0x89, 0x34, 0x1c}
)

func discoverNFTSource(ctx context.Context, caller ethrpc.Caller, candidate NFTSourceCandidate) (string, string, bool) {
	tokenID, _ := new(big.Int).SetString(candidate.TokenID, 10)
	input := make([]byte, 36)
	if candidate.Standard == NFTStandardERC1155 {
		copy(input, erc1155URISelector)
	} else {
		copy(input, erc721TokenURISelector)
	}
	tokenID.FillBytes(input[4:])
	call := map[string]any{"to": candidate.Token.String(), "data": ethrpc.DataFromBytes(input).String()}
	selector := map[string]any{"blockHash": candidate.BlockHash.String(), "requireCanonical": true}
	var encoded ethrpc.Data
	if err := caller.Call(ctx, "eth_call", []any{call, selector}, &encoded); err != nil {
		if sourceExecutionReverted(err) {
			return "", "token_uri_unavailable", false
		}
		if sourceExactStateUnavailable(err) {
			return "", "exact_state_unavailable", false
		}
		return "", "", true
	}
	output, err := encoded.Bytes()
	if err != nil {
		return "", "token_uri_invalid", false
	}
	sourceURI, ok := decodeSourceString(output, MaxSourceURIBytes)
	if !ok || strings.TrimSpace(sourceURI) == "" {
		return "", "token_uri_invalid", false
	}
	if candidate.Standard == NFTStandardERC1155 {
		replacement := fmt.Sprintf("%064x", tokenID)
		sourceURI = strings.ReplaceAll(sourceURI, "{id}", replacement)
	}
	request := NFTRequest{
		ChainID: candidate.ChainID, Token: candidate.Token, TokenID: candidate.TokenID,
		BlockNumber: candidate.BlockNumber, BlockHash: candidate.BlockHash, SourceURI: sourceURI,
	}
	if err := request.Validate(); err != nil {
		return "", "token_uri_invalid", false
	}
	return sourceURI, "", false
}

func decodeSourceString(output []byte, maximum int) (string, bool) {
	if len(output) < 64 || maximum <= 0 {
		return "", false
	}
	for _, value := range output[:24] {
		if value != 0 {
			return "", false
		}
	}
	if binary.BigEndian.Uint64(output[24:32]) != 32 {
		return "", false
	}
	for _, value := range output[32:56] {
		if value != 0 {
			return "", false
		}
	}
	length := binary.BigEndian.Uint64(output[56:64])
	if length == 0 || length > uint64(maximum) || length > uint64(len(output)-64) {
		return "", false
	}
	padded := (length + 31) / 32 * 32
	if padded > uint64(len(output)-64) || len(output) != 64+int(padded) {
		return "", false
	}
	value := output[64 : 64+int(length)]
	if !utf8.Valid(value) {
		return "", false
	}
	for _, padding := range output[64+int(length):] {
		if padding != 0 {
			return "", false
		}
	}
	return string(value), true
}

func sourceExecutionReverted(err error) bool {
	var rpcError *ethrpc.RPCError
	if !errors.As(err, &rpcError) {
		return false
	}
	message := strings.ToLower(rpcError.Message)
	return rpcError.Code == 3 || strings.Contains(message, "execution reverted") || strings.Contains(message, "revert")
}

func sourceExactStateUnavailable(err error) bool {
	if ethrpc.IsMethodNotFound(err) {
		return true
	}
	var rpcError *ethrpc.RPCError
	if !errors.As(err, &rpcError) {
		return false
	}
	message := strings.ToLower(rpcError.Message)
	return rpcError.Code == -32602 || strings.Contains(message, "eip-1898") ||
		strings.Contains(message, "block hash") || strings.Contains(message, "missing trie") ||
		strings.Contains(message, "historical state") || strings.Contains(message, "pruned") ||
		strings.Contains(message, "header not found") || strings.Contains(message, "state is not available")
}

func parseSourceBlockNumber(value string) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || strconv.FormatUint(parsed, 10) != value {
		return 0, errors.New("NFT metadata candidate block number is not a canonical uint64")
	}
	return parsed, nil
}
