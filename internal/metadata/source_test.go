package metadata

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/ethrpc"
)

type sourceRPC struct {
	call     map[string]any
	selector rpc.BlockNumberOrHash
	result   []byte
	err      error
}

func (service *sourceRPC) Call(_ context.Context, call map[string]any, selector rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	service.call = call
	service.selector = selector
	if service.err != nil {
		return nil, service.err
	}
	return hexutil.Bytes(service.result), nil
}

func newSourceRPCClient(t *testing.T, service *sourceRPC) *rpc.Client {
	t.Helper()
	server := rpc.NewServer()
	if err := server.RegisterName("eth", service); err != nil {
		t.Fatal(err)
	}
	client := rpc.DialInProc(server)
	t.Cleanup(func() {
		client.Close()
		server.Stop()
	})
	return client
}

type fakeSourceRepository struct {
	candidate    NFTSourceCandidate
	found        bool
	canonical    bool
	observations []NFTSourceObservation
	requests     []NFTRequest
}

func (repository *fakeSourceRepository) NextNFTSource(context.Context) (NFTSourceCandidate, bool, error) {
	return repository.candidate, repository.found, nil
}

func (repository *fakeSourceRepository) NFTSourceCanonical(context.Context, NFTSourceCandidate) (bool, error) {
	return repository.canonical, nil
}

func (repository *fakeSourceRepository) RecordNFTSource(_ context.Context, observation NFTSourceObservation) error {
	repository.observations = append(repository.observations, observation)
	return nil
}

func (repository *fakeSourceRepository) EnqueueNFT(_ context.Context, request NFTRequest) (EnqueueResult, error) {
	repository.requests = append(repository.requests, request)
	return EnqueueResult{JobID: 1, Created: true}, nil
}

func TestSourceDiscovererUsesOneExactStateCallAndEnqueuesURI(t *testing.T) {
	t.Parallel()
	candidate := sourceCandidate(t, NFTStandardERC721)
	repository := &fakeSourceRepository{candidate: candidate, found: true, canonical: true}
	service := &sourceRPC{result: encodeSourceString("ipfs://bafybeigdyrzt1234567890/42.json")}
	discoverer := newTestSourceDiscoverer(t, repository, newSourceRPCClient(t, service))
	processed, err := discoverer.ProcessOnce(t.Context())
	if err != nil || !processed {
		t.Fatalf("processed=%t err=%v", processed, err)
	}
	if len(repository.requests) != 1 || repository.requests[0].SourceURI != "ipfs://bafybeigdyrzt1234567890/42.json" {
		t.Fatalf("enqueued requests = %+v", repository.requests)
	}
	if len(repository.observations) != 1 || repository.observations[0].State != NFTSourceFound {
		t.Fatalf("source observations = %+v", repository.observations)
	}
	if service.call["to"] != candidate.Token.Hex() || !strings.HasPrefix(service.call["data"].(string), "0xc87b56dd") {
		t.Fatalf("RPC call payload = %#v", service.call)
	}
	wantSelector := rpc.BlockNumberOrHashWithHash(candidate.BlockHash, true)
	if !reflect.DeepEqual(service.selector, wantSelector) {
		t.Fatalf("RPC selector = %#v, want %#v", service.selector, wantSelector)
	}
}

func TestSourceDiscovererExpandsERC1155IDAndPersistsPermanentGaps(t *testing.T) {
	t.Parallel()
	t.Run("template", func(t *testing.T) {
		candidate := sourceCandidate(t, NFTStandardERC1155)
		repository := &fakeSourceRepository{candidate: candidate, found: true, canonical: true}
		service := &sourceRPC{result: encodeSourceString("https://example.invalid/{id}.json")}
		processed, err := newTestSourceDiscoverer(t, repository, newSourceRPCClient(t, service)).ProcessOnce(t.Context())
		if err != nil || !processed {
			t.Fatalf("processed=%t err=%v", processed, err)
		}
		want := "https://example.invalid/" + strings.Repeat("0", 62) + "2a.json"
		if len(repository.requests) != 1 || repository.requests[0].SourceURI != want {
			t.Fatalf("source URI = %q, want %q", repository.requests[0].SourceURI, want)
		}
	})

	t.Run("revert", func(t *testing.T) {
		repository := &fakeSourceRepository{candidate: sourceCandidate(t, NFTStandardERC721), found: true, canonical: true}
		service := &sourceRPC{err: &sourceRPCError{code: 3, message: "execution reverted with secret"}}
		processed, err := newTestSourceDiscoverer(t, repository, newSourceRPCClient(t, service)).ProcessOnce(t.Context())
		if err != nil || !processed {
			t.Fatalf("processed=%t err=%v", processed, err)
		}
		if len(repository.requests) != 0 || len(repository.observations) != 1 ||
			repository.observations[0].State != NFTSourceUnavailable || repository.observations[0].ErrorCode != "token_uri_unavailable" {
			t.Fatalf("requests=%+v observations=%+v", repository.requests, repository.observations)
		}
	})

	t.Run("transient", func(t *testing.T) {
		repository := &fakeSourceRepository{candidate: sourceCandidate(t, NFTStandardERC721), found: true, canonical: true}
		service := &sourceRPC{err: errors.New("secret transport failure")}
		processed, err := newTestSourceDiscoverer(t, repository, newSourceRPCClient(t, service)).ProcessOnce(t.Context())
		if err != nil || processed || len(repository.observations) != 0 || len(repository.requests) != 0 {
			t.Fatalf("processed=%t err=%v requests=%+v observations=%+v", processed, err, repository.requests, repository.observations)
		}
	})
}

func TestSourceDiscovererDropsCandidateThatReorgsDuringExactCall(t *testing.T) {
	t.Parallel()
	candidate := sourceCandidate(t, NFTStandardERC721)
	repository := &fakeSourceRepository{candidate: candidate, found: true, canonical: false}
	service := &sourceRPC{result: encodeSourceString("https://metadata.example/42-v2.json")}
	processed, err := newTestSourceDiscoverer(t, repository, newSourceRPCClient(t, service)).ProcessOnce(t.Context())
	if err != nil || !processed {
		t.Fatalf("processed=%t err=%v", processed, err)
	}
	if service.call == nil || len(repository.requests) != 0 || len(repository.observations) != 0 {
		t.Fatalf("RPC=%#v requests=%+v observations=%+v", service.call, repository.requests, repository.observations)
	}
}

type sourceRPCError struct {
	code    int
	message string
}

func (err *sourceRPCError) Error() string  { return err.message }
func (err *sourceRPCError) ErrorCode() int { return err.code }

func newTestSourceDiscoverer(t *testing.T, repository NFTSourceRepository, client *rpc.Client) *SourceDiscoverer {
	t.Helper()
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "state", Client: client, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	discoverer, err := NewSourceDiscoverer(repository, pool, SourceDiscovererOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return discoverer
}

func sourceCandidate(t *testing.T, standard NFTStandard) NFTSourceCandidate {
	t.Helper()
	return NFTSourceCandidate{
		ChainID: "1", Token: common.HexToAddress("0x1111111111111111111111111111111111111111"),
		TokenID: "42", BlockNumber: 7, BlockHash: common.HexToHash("0x" + strings.Repeat("22", 32)),
		Standard: standard,
	}
}

func encodeSourceString(value string) []byte {
	length := len(value)
	padded := (length + 31) / 32 * 32
	result := make([]byte, 64+padded)
	result[31] = 32
	binary.BigEndian.PutUint64(result[56:64], uint64(length))
	copy(result[64:], value)
	return result
}
