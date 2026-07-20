package metadata

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/ethrpc"
)

type sourceCallerFunc func(context.Context, string, []any, any) error

func (caller sourceCallerFunc) Call(ctx context.Context, method string, params []any, result any) error {
	return caller(ctx, method, params, result)
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
	caller := sourceCallerFunc(func(_ context.Context, method string, params []any, result any) error {
		if method != "eth_call" || len(params) != 2 {
			t.Fatalf("RPC call = %s %#v", method, params)
		}
		call, ok := params[0].(map[string]any)
		if !ok || call["to"] != candidate.Token.String() || !strings.HasPrefix(call["data"].(string), "0xc87b56dd") {
			t.Fatalf("RPC call payload = %#v", params[0])
		}
		selector, ok := params[1].(map[string]any)
		if !ok || selector["blockHash"] != candidate.BlockHash.String() || selector["requireCanonical"] != true {
			t.Fatalf("RPC selector = %#v", params[1])
		}
		*(result.(*ethrpc.Data)) = ethrpc.DataFromBytes(encodeSourceString("ipfs://bafybeigdyrzt1234567890/42.json"))
		return nil
	})
	discoverer := newTestSourceDiscoverer(t, repository, caller)
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
}

func TestSourceDiscovererExpandsERC1155IDAndPersistsPermanentGaps(t *testing.T) {
	t.Parallel()
	t.Run("template", func(t *testing.T) {
		candidate := sourceCandidate(t, NFTStandardERC1155)
		repository := &fakeSourceRepository{candidate: candidate, found: true, canonical: true}
		caller := sourceCallerFunc(func(_ context.Context, _ string, _ []any, result any) error {
			*(result.(*ethrpc.Data)) = ethrpc.DataFromBytes(encodeSourceString("https://example.invalid/{id}.json"))
			return nil
		})
		processed, err := newTestSourceDiscoverer(t, repository, caller).ProcessOnce(t.Context())
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
		caller := sourceCallerFunc(func(context.Context, string, []any, any) error {
			return &ethrpc.RPCError{Code: 3, Message: "execution reverted with secret"}
		})
		processed, err := newTestSourceDiscoverer(t, repository, caller).ProcessOnce(t.Context())
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
		caller := sourceCallerFunc(func(context.Context, string, []any, any) error { return errors.New("secret transport failure") })
		processed, err := newTestSourceDiscoverer(t, repository, caller).ProcessOnce(t.Context())
		if err != nil || processed || len(repository.observations) != 0 || len(repository.requests) != 0 {
			t.Fatalf("processed=%t err=%v requests=%+v observations=%+v", processed, err, repository.requests, repository.observations)
		}
	})
}

func newTestSourceDiscoverer(t *testing.T, repository NFTSourceRepository, caller ethrpc.Caller) *SourceDiscoverer {
	t.Helper()
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "state", Client: caller, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
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
	address, err := ethrpc.ParseAddress("0x1111111111111111111111111111111111111111")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := ethrpc.ParseHash("0x" + strings.Repeat("22", 32))
	if err != nil {
		t.Fatal(err)
	}
	return NFTSourceCandidate{
		ChainID: "1", Token: address, TokenID: "42", BlockNumber: 7, BlockHash: hash, Standard: standard,
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
