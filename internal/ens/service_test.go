package ens

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/metadata"
)

type canonicalStub struct{}

func (canonicalStub) Tip(context.Context) (BlockRef, error)               { return BlockRef{}, nil }
func (canonicalStub) IsCanonical(context.Context, BlockRef) (bool, error) { return true, nil }

func TestCustomOnlyServiceRequiresNoOfficialPoolAndSeparatesPolicy(t *testing.T) {
	client := rpc.DialInProc(rpc.NewServer())
	defer client.Close()
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{Name: "local", Client: client, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true}}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	options := ServiceOptions{Mode: "custom_only", ChainID: 48815, Repository: &Repository{}, Resolver: testResolver(t, &gatewayStub{}), CustomPool: pool, Canonical: canonicalStub{}, Custom: &CustomOptions{Registry: testForwardResolver, UniversalResolver: testUniversalResolver, CoinType: big.NewInt(2147532463)}, ResolutionFreshness: time.Minute, SnapshotTTL: time.Hour, FailureTTL: time.Second, RequestTimeout: time.Second, MaxBatchAddresses: 10, MaxConcurrency: 1}
	service, err := NewService(options)
	if err != nil {
		t.Fatal(err)
	}
	if service.officialPool != nil {
		t.Fatal("custom-only created an official pool")
	}
	if _, _, _, err = service.source(Generation{}, SourceOfficial); err == nil {
		t.Fatal("absent official source accepted")
	}
	options.OfficialPool = pool
	if _, err = NewService(options); err == nil {
		t.Fatal("custom-only accepted an official pool")
	}
	options.Mode = "official_first"
	options.OfficialGateways = []string{"https://ccip.example"}
	official, err := NewService(options)
	if err != nil {
		t.Fatal(err)
	}
	if service.policyKey == official.policyKey {
		t.Fatal("modes share cache policy")
	}
	options.Mode = "custom_only"
	options.OfficialPool = nil
	options.Custom = nil
	if _, err = NewService(options); err == nil {
		t.Fatal("custom-only accepted no custom source")
	}
}

func TestGenerationRequiresWholeSource(t *testing.T) {
	repository := &Repository{}
	now := time.Now().UTC()
	candidate := GenerationCandidate{PolicyKey: "sha256:fixture", CoinType: big.NewInt(60), CreatedAt: now, FreshUntil: now.Add(time.Minute), RetainUntil: now.Add(time.Hour)}
	if _, err := repository.generationParams(candidate); err == nil {
		t.Fatal("source-less generation accepted")
	}
	candidate.CustomEndpoint = "local"
	candidate.CustomCoinType = big.NewInt(60)
	candidate.CustomBlock = &BlockRef{Hash: common.HexToHash("0x01")}
	params, err := repository.generationParams(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if params.OfficialEndpoint != nil || params.OfficialBlockNumber.Valid || len(params.OfficialBlockHash) > 0 {
		t.Fatal("fabricated official source")
	}
	candidate.OfficialEndpoint = "mainnet"
	if _, err = repository.generationParams(candidate); err == nil {
		t.Fatal("partial official source accepted")
	}
}

func TestEmptyGatewayAllowlistDeniesAllRequests(t *testing.T) {
	client, err := metadata.New(metadata.Policy{Timeout: time.Second, MaxBytes: 1024}, nil)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := NewHTTPGateway(client, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = gateway.Request(t.Context(), "https://ccip.example", common.Address{}, nil); err == nil {
		t.Fatal("empty allowlist permitted network request")
	}
}
