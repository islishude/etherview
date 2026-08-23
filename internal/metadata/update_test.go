package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type fakeNFTUpdateRepository struct {
	candidate NFTUpdateCandidate
	found     bool
	nextErr   error
	recordErr error
	canonical bool
	recorded  []NFTUpdateObservation
}

func (repository *fakeNFTUpdateRepository) NextNFTUpdate(context.Context) (NFTUpdateCandidate, bool, error) {
	return repository.candidate, repository.found, repository.nextErr
}

func (repository *fakeNFTUpdateRepository) RecordNFTUpdate(_ context.Context, observation NFTUpdateObservation) (bool, error) {
	repository.recorded = append(repository.recorded, observation)
	return repository.canonical, repository.recordErr
}

func TestParseNFTMetadataUpdateStandards(t *testing.T) {
	t.Parallel()
	maximum := new(big.Int).Set(maximumUint256)
	tests := []struct {
		name     string
		standard NFTStandard
		topics   []common.Hash
		data     []byte
		kind     NFTUpdateKind
		from     string
		to       string
	}{
		{name: "ERC-4906 single", standard: NFTStandardERC721, topics: []common.Hash{metadataUpdateTopic}, data: maximum.FillBytes(make([]byte, 32)), kind: NFTUpdateERC4906Single, from: maximum.String(), to: maximum.String()},
		{name: "ERC-4906 batch", standard: NFTStandardERC721, topics: []common.Hash{batchMetadataUpdateTopic}, data: append(uint256Bytes(7), uint256Bytes(99)...), kind: NFTUpdateERC4906Batch, from: "7", to: "99"},
		{name: "ERC-1155 URI", standard: NFTStandardERC1155, topics: []common.Hash{erc1155URITopic, common.BigToHash(big.NewInt(42))}, data: encodeSourceString("ipfs://bafybeigdyrzt1234567890/{id}.json"), kind: NFTUpdateERC1155URI, from: "42", to: "42"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := nftUpdateCandidate(t, test.standard, test.topics, test.data)
			got, err := parseNFTUpdate(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != NFTUpdateAccepted || got.Kind != test.kind || got.FromTokenID != test.from || got.ToTokenID != test.to || got.ErrorCode != "" {
				t.Fatalf("observation=%+v", got)
			}
		})
	}
}

func TestParseNFTMetadataUpdateRejectsMalformedLayouts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		standard NFTStandard
		topics   []common.Hash
		data     []byte
		code     string
	}{
		{name: "single topics", standard: NFTStandardERC721, topics: []common.Hash{metadataUpdateTopic, {}}, data: uint256Bytes(1), code: "topic_count_invalid"},
		{name: "single data", standard: NFTStandardERC721, topics: []common.Hash{metadataUpdateTopic}, data: make([]byte, 31), code: "data_length_invalid"},
		{name: "batch topics", standard: NFTStandardERC721, topics: []common.Hash{batchMetadataUpdateTopic, {}}, data: append(uint256Bytes(1), uint256Bytes(2)...), code: "topic_count_invalid"},
		{name: "batch data", standard: NFTStandardERC721, topics: []common.Hash{batchMetadataUpdateTopic}, data: make([]byte, 63), code: "data_length_invalid"},
		{name: "batch reverse", standard: NFTStandardERC721, topics: []common.Hash{batchMetadataUpdateTopic}, data: append(uint256Bytes(3), uint256Bytes(2)...), code: "token_range_invalid"},
		{name: "URI topics", standard: NFTStandardERC1155, topics: []common.Hash{erc1155URITopic}, data: encodeSourceString("https://metadata.example/42.json"), code: "topic_count_invalid"},
		{name: "URI ABI", standard: NFTStandardERC1155, topics: []common.Hash{erc1155URITopic, {}}, data: []byte{1, 2}, code: "uri_payload_invalid"},
		{name: "URI too long", standard: NFTStandardERC1155, topics: []common.Hash{erc1155URITopic, {}}, data: encodeSourceString(strings.Repeat("x", MaxSourceURIBytes+1)), code: "uri_payload_invalid"},
		{name: "URI control", standard: NFTStandardERC1155, topics: []common.Hash{erc1155URITopic, {}}, data: encodeSourceString("https://metadata.example/a\n.json"), code: "uri_payload_invalid"},
		{name: "ERC-4906 standard", standard: NFTStandardERC1155, topics: []common.Hash{metadataUpdateTopic}, data: uint256Bytes(1), code: "standard_mismatch"},
		{name: "URI standard", standard: NFTStandardERC721, topics: []common.Hash{erc1155URITopic, {}}, data: encodeSourceString("https://metadata.example/1.json"), code: "standard_mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseNFTUpdate(nftUpdateCandidate(t, test.standard, test.topics, test.data))
			if err != nil {
				t.Fatal(err)
			}
			if got.State != NFTUpdateMalformed || got.ErrorCode != test.code || got.FromTokenID != "" || got.ToTokenID != "" {
				t.Fatalf("observation=%+v", got)
			}
		})
	}
}

func TestParseNFTMetadataUpdateRejectsStoredIdentityMismatch(t *testing.T) {
	t.Parallel()
	candidate := nftUpdateCandidate(t, NFTStandardERC721, []common.Hash{metadataUpdateTopic}, uint256Bytes(1))
	candidate.TransactionHash = common.HexToHash("0x99")
	if _, err := parseNFTUpdate(candidate); err == nil || !strings.Contains(err.Error(), "identity is inconsistent") {
		t.Fatalf("identity error=%v", err)
	}
}

func TestUpdateDiscovererPersistsAcceptedMalformedAndStaleFacts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		candidate NFTUpdateCandidate
		canonical bool
		state     NFTUpdateState
		code      string
	}{
		{name: "accepted", candidate: nftUpdateCandidate(t, NFTStandardERC721, []common.Hash{metadataUpdateTopic}, uint256Bytes(42)), canonical: true, state: NFTUpdateAccepted},
		{name: "malformed", candidate: nftUpdateCandidate(t, NFTStandardERC721, []common.Hash{batchMetadataUpdateTopic}, uint256Bytes(42)), canonical: true, state: NFTUpdateMalformed, code: "data_length_invalid"},
		{name: "stale", candidate: nftUpdateCandidate(t, NFTStandardERC1155, []common.Hash{erc1155URITopic, common.BigToHash(big.NewInt(7))}, encodeSourceString("https://metadata.example/7.json")), canonical: false, state: NFTUpdateAccepted},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeNFTUpdateRepository{candidate: test.candidate, found: true, canonical: test.canonical}
			discoverer, err := NewUpdateDiscoverer(repository, UpdateDiscovererOptions{})
			if err != nil {
				t.Fatal(err)
			}
			processed, err := discoverer.ProcessOnce(t.Context())
			if err != nil || !processed || len(repository.recorded) != 1 {
				t.Fatalf("processed=%t recorded=%d err=%v", processed, len(repository.recorded), err)
			}
			if got := repository.recorded[0]; got.State != test.state || got.ErrorCode != test.code {
				t.Fatalf("observation=%+v", got)
			}
		})
	}
}

func TestUpdateDiscovererPropagatesRepositoryFailures(t *testing.T) {
	t.Parallel()
	want := errors.New("database unavailable")
	discoverer, err := NewUpdateDiscoverer(&fakeNFTUpdateRepository{nextErr: want}, UpdateDiscovererOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := discoverer.ProcessOnce(t.Context()); processed || !errors.Is(err, want) {
		t.Fatalf("processed=%t err=%v", processed, err)
	}
}

func nftUpdateCandidate(t *testing.T, standard NFTStandard, topics []common.Hash, data []byte) NFTUpdateCandidate {
	t.Helper()
	blockHash := common.HexToHash("0x1234")
	txHash := common.HexToHash("0x5678")
	token := common.HexToAddress("0x1111111111111111111111111111111111111111")
	wire := types.Log{
		Address: token, Topics: topics, Data: data,
		BlockNumber: 12, BlockHash: blockHash, TxHash: txHash, Index: 7,
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	return NFTUpdateCandidate{
		ChainID: "1", BlockNumber: 12, BlockHash: blockHash, LogIndex: 7,
		TransactionHash: txHash, Token: token, Standard: standard, Raw: raw,
	}
}

func uint256Bytes(value int64) []byte {
	return new(big.Int).SetInt64(value).FillBytes(make([]byte, 32))
}
