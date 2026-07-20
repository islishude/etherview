package metadata

import (
	"bytes"
	"crypto/sha256"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
)

func TestNFTRequestValidationAndIdempotency(t *testing.T) {
	t.Parallel()
	request := validNFTRequest(t)
	first, err := request.idempotencyKey()
	if err != nil || len(first) != 64 {
		t.Fatalf("idempotency key = %q, err=%v", first, err)
	}
	duplicate, err := request.idempotencyKey()
	if err != nil || duplicate != first {
		t.Fatalf("duplicate key = %q, err=%v, want %q", duplicate, err, first)
	}
	changed := request
	changed.SourceURI += "?v=2"
	changedKey, err := changed.idempotencyKey()
	if err != nil || changedKey == first {
		t.Fatalf("changed key = %q, err=%v, original=%q", changedKey, err, first)
	}
	maximum := request
	maximum.TokenID = maximumUint256.String()
	if err := maximum.Validate(); err != nil {
		t.Fatalf("maximum uint256 token ID rejected: %v", err)
	}

	invalid := []NFTRequest{
		func() NFTRequest { value := request; value.ChainID = "01"; return value }(),
		func() NFTRequest { value := request; value.TokenID = "-1"; return value }(),
		func() NFTRequest {
			value := request
			value.TokenID = new(big.Int).Add(maximumUint256, big.NewInt(1)).String()
			return value
		}(),
		func() NFTRequest { value := request; value.Token = "0x01"; return value }(),
		func() NFTRequest {
			value := request
			value.SourceURI = "https://user:secret@example.invalid"
			return value
		}(),
		func() NFTRequest {
			value := request
			value.SourceURI = strings.Repeat("x", MaxSourceURIBytes+1)
			return value
		}(),
		func() NFTRequest { value := request; value.MaxAttempts = MaximumMaxAttempts + 1; return value }(),
	}
	for index, value := range invalid {
		if err := value.Validate(); err == nil {
			t.Fatalf("invalid request %d was accepted: %+v", index, value)
		}
	}
}

func TestPayloadRoundTripRejectsCrossIdentityMutation(t *testing.T) {
	t.Parallel()
	request := validNFTRequest(t)
	request.MaxAttempts = 7
	payload, err := encodePayload(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodePayload(payload, 7)
	if err != nil || !sameRequest(decoded, request) {
		t.Fatalf("decoded=%+v err=%v request=%+v", decoded, err, request)
	}
	mutated := bytes.Replace(payload, []byte(request.resourceKey()), []byte("0x0000000000000000000000000000000000000002:42"), 1)
	if _, err := decodePayload(mutated, 7); err == nil || !strings.Contains(err.Error(), "resource key") {
		t.Fatalf("mutated payload error = %v", err)
	}
}

func TestOutcomeAndLeaseBoundaries(t *testing.T) {
	t.Parallel()
	request := validNFTRequest(t)
	lease := Lease{JobID: 1, Token: "lease", Request: request, Attempt: 1, MaxAttempts: 2}
	if err := lease.Validate(); err != nil {
		t.Fatal(err)
	}
	lease.Attempt = 3
	if err := lease.Validate(); err == nil {
		t.Fatal("accepted attempt beyond max")
	}

	document := []byte(`{"name":"NFT"}`)
	available := Outcome{
		State: StateAvailable, ResolvedURI: "https://example.invalid/42.json",
		MediaType: "application/json", Document: document, ContentSize: int64(len(document)),
	}
	available.ContentHash = sha256Bytes(document)
	if err := available.validate(); err != nil {
		t.Fatal(err)
	}
	available.ContentSize++
	if err := available.validate(); err == nil {
		t.Fatal("accepted inconsistent content size")
	}
	if err := terminalOutcome(StateUnsafe, "unsafe_url", "blocked").validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := durationMicroseconds(500*time.Nanosecond, false); err != nil {
		t.Fatal(err)
	}
}

func validNFTRequest(t *testing.T) NFTRequest {
	t.Helper()
	address, err := ethrpc.ParseAddress("0x0000000000000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := ethrpc.ParseHash("0x000000000000000000000000000000000000000000000000000000000000002a")
	if err != nil {
		t.Fatal(err)
	}
	return NFTRequest{
		ChainID: "1", Token: address, TokenID: "42", BlockNumber: 42,
		BlockHash: hash, SourceURI: "https://metadata.example.invalid/42.json",
	}
}

func sha256Bytes(value []byte) [32]byte {
	return sha256.Sum256(value)
}
