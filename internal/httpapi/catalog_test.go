package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/config"
)

type fakeCatalog struct {
	tokenPage  catalog.TokenPage
	token      catalog.TokenContract
	tokenErr   error
	nftOwner   catalog.NFTOwnership
	nftBalance catalog.NFTBalancePage
	nftErr     error
	trace      catalog.TransactionTrace
	traceErr   error
	blockStats []catalog.BlockStat
	aggregate  catalog.AggregateStats
	statsErr   error
}

func (fake *fakeCatalog) TokenContract(context.Context, string, string) (catalog.TokenContract, error) {
	return fake.token, fake.tokenErr
}

func (fake *fakeCatalog) TokenContracts(context.Context, catalog.TokenListRequest) (catalog.TokenPage, error) {
	return fake.tokenPage, fake.tokenErr
}

func (*fakeCatalog) TokenEvents(context.Context, catalog.TokenEventRequest) (catalog.TokenEventPage, error) {
	return catalog.TokenEventPage{}, nil
}

func (fake *fakeCatalog) NFTOwner(context.Context, string, string, string) (catalog.NFTOwnership, error) {
	return fake.nftOwner, fake.nftErr
}

func (fake *fakeCatalog) NFTBalances(context.Context, catalog.NFTBalanceRequest) (catalog.NFTBalancePage, error) {
	return fake.nftBalance, fake.nftErr
}

func (fake *fakeCatalog) BlockStats(context.Context, catalog.BlockStatsRequest) ([]catalog.BlockStat, error) {
	return fake.blockStats, fake.statsErr
}

func (fake *fakeCatalog) AggregateStats(context.Context, catalog.AggregateStatsRequest) (catalog.AggregateStats, error) {
	return fake.aggregate, fake.statsErr
}

func (fake *fakeCatalog) TransactionTrace(context.Context, string, string) (catalog.TransactionTrace, error) {
	return fake.trace, fake.traceErr
}

func testCatalogHandler(t *testing.T, catalogReader catalog.Reader) http.Handler {
	t.Helper()
	cfg := config.Default()
	cfg.Chain.ID = 11155111
	handler, err := New(Options{
		Config: cfg, Reader: fakeReader{}, Catalog: catalogReader,
		RequestID: func() string { return "catalog-request" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestTokenCatalogResponseUsesStableSnapshotAndStringQuantities(t *testing.T) {
	t.Parallel()
	hash := "0x" + strings.Repeat("01", 32)
	address := "0x" + strings.Repeat("11", 20)
	name, totalSupply := "Example", "1000000000000000000000000000000"
	fake := &fakeCatalog{tokenPage: catalog.TokenPage{
		Items: []catalog.TokenContract{{
			ChainID: "11155111", Address: address, CodeHash: hash,
			Standard: "erc20", Confidence: "probed", Name: &name, TotalSupply: &totalSupply,
			MetadataState: "complete", ObservedBlockNumber: "12", ObservedBlockHash: hash,
			UpdatedAt: time.Unix(5, 0).UTC(),
		}},
		NextCursor: "next-token", Snapshot: catalog.Snapshot{
			ChainID: "11155111", BlockNumber: "12", BlockHash: hash,
		},
	}}
	recorder := httptest.NewRecorder()
	testCatalogHandler(t, fake).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/tokens?limit=1", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response gen.TokenListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].TotalSupply == nil || *response.Data[0].TotalSupply != totalSupply {
		t.Fatalf("data=%+v", response.Data)
	}
	if response.Meta.NextCursor == nil || *response.Meta.NextCursor != "next-token" || response.Meta.CoverageEnd == nil || *response.Meta.CoverageEnd != "12" {
		t.Fatalf("meta=%+v", response.Meta)
	}
}

func TestCatalogStageUnavailableIsExplicitAndSanitized(t *testing.T) {
	t.Parallel()
	address := "0x" + strings.Repeat("22", 20)
	hash := "0x" + strings.Repeat("33", 32)
	fake := &fakeCatalog{tokenErr: catalog.StageUnavailableError{
		Stage: catalog.StageToken, State: catalog.StageUnavailable,
		BlockNumber: "99", BlockHash: hash,
	}}
	recorder := httptest.NewRecorder()
	testCatalogHandler(t, fake).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/tokens/"+address, nil))
	body := recorder.Body.String()
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(body, `"code":"stage_unavailable"`) || !strings.Contains(body, `"stage":"token"`) || !strings.Contains(body, `"block_number":"99"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, body)
	}
}

func TestNFTResponsesExposeOnlyExactStateConfidence(t *testing.T) {
	t.Parallel()
	address := "0x" + strings.Repeat("22", 20)
	owner := "0x" + strings.Repeat("33", 20)
	hash := "0x" + strings.Repeat("44", 32)
	fake := &fakeCatalog{
		nftOwner: catalog.NFTOwnership{
			ChainID: "11155111", TokenAddress: address, TokenID: "42", Owner: owner,
			Balance: "1", Confidence: catalog.NFTStateConfidenceRPCExact,
			Snapshot: catalog.Snapshot{ChainID: "11155111", BlockNumber: "12", BlockHash: hash},
		},
		nftBalance: catalog.NFTBalancePage{
			Items: []catalog.NFTBalance{{
				ChainID: "11155111", Owner: owner, TokenAddress: address, TokenID: "42",
				Balance: "1", Confidence: catalog.NFTStateConfidenceRPCExact,
			}},
			Snapshot: catalog.Snapshot{ChainID: "11155111", BlockNumber: "12", BlockHash: hash},
		},
	}
	handler := testCatalogHandler(t, fake)

	ownerRecorder := httptest.NewRecorder()
	handler.ServeHTTP(ownerRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/nfts/"+address+"/42", nil))
	if ownerRecorder.Code != http.StatusOK {
		t.Fatalf("owner status=%d body=%s", ownerRecorder.Code, ownerRecorder.Body.String())
	}
	var ownerResponse gen.NFTOwnershipResponse
	if err := json.Unmarshal(ownerRecorder.Body.Bytes(), &ownerResponse); err != nil {
		t.Fatal(err)
	}
	if ownerResponse.Data.Confidence != gen.RpcExact || ownerResponse.Data.Snapshot.BlockHash != hash {
		t.Fatalf("owner response=%+v", ownerResponse.Data)
	}

	balanceRecorder := httptest.NewRecorder()
	handler.ServeHTTP(balanceRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/addresses/"+owner+"/nfts", nil))
	if balanceRecorder.Code != http.StatusOK {
		t.Fatalf("balance status=%d body=%s", balanceRecorder.Code, balanceRecorder.Body.String())
	}
	var balanceResponse gen.NFTBalanceListResponse
	if err := json.Unmarshal(balanceRecorder.Body.Bytes(), &balanceResponse); err != nil {
		t.Fatal(err)
	}
	if len(balanceResponse.Data) != 1 || balanceResponse.Data[0].Confidence != gen.RpcExact {
		t.Fatalf("balance response=%+v", balanceResponse.Data)
	}
}

func TestTransactionTraceDistinguishesStageAbsenceFromNoInternalCalls(t *testing.T) {
	t.Parallel()
	hash := "0x" + strings.Repeat("55", 32)
	blockHash := "0x" + strings.Repeat("66", 32)
	for _, state := range []catalog.StageState{catalog.StageMissing, catalog.StageUnavailable, catalog.StageFailed} {
		state := state
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()
			fake := &fakeCatalog{traceErr: catalog.StageUnavailableError{
				Stage: catalog.StageTrace, State: state, BlockNumber: "12", BlockHash: blockHash,
			}}
			recorder := httptest.NewRecorder()
			testCatalogHandler(t, fake).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/transactions/"+hash+"/trace", nil))
			body := recorder.Body.String()
			if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(body, `"code":"stage_unavailable"`) || !strings.Contains(body, `"state":"`+string(state)+`"`) {
				t.Fatalf("status=%d body=%s", recorder.Code, body)
			}
		})
	}

	fake := &fakeCatalog{trace: catalog.TransactionTrace{
		ChainID: "11155111", BlockNumber: "12", BlockHash: blockHash,
		TransactionHash: hash, TransactionIndex: "0", State: catalog.StageComplete,
		Frames: []catalog.TraceFrame{{Path: []uint32{}, ParentPath: []uint32{}, Depth: 0, CallType: "CALL"}},
	}}
	recorder := httptest.NewRecorder()
	testCatalogHandler(t, fake).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/transactions/"+hash+"/trace", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response gen.TransactionTraceResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.State != "complete" || len(response.Data.Frames) != 1 || response.Data.Frames[0].Depth != 0 {
		t.Fatalf("data=%+v", response.Data)
	}
}

func TestStatsV2ResponsesExposeBlobTokenAndNullableIntervalSemantics(t *testing.T) {
	t.Parallel()
	hash := "0x" + strings.Repeat("77", 32)
	interval, tps, blobPrice := "12", "0.25", "3"
	fake := &fakeCatalog{
		blockStats: []catalog.BlockStat{{
			ChainID: "11155111", BlockNumber: "12", BlockHash: hash,
			TransactionCount: "3", GasUsed: "21000", GasLimit: "30000000",
			BlockTimestamp: "1700000012", BlockIntervalSeconds: &interval,
			TransactionsPerSecond: &tps, BlobBaseFeePerGas: &blobPrice,
			BlobBurnedWei: ptr("393216"), TokenEventCount: "4",
			TokenTransferCount: "2", NFTTransferCount: "1", ComputedAt: time.Unix(20, 0).UTC(),
		}},
		aggregate: catalog.AggregateStats{
			ChainID: "11155111", FromBlock: "0", ToBlock: "0",
			Snapshot:   catalog.Snapshot{ChainID: "11155111", BlockNumber: "12", BlockHash: hash},
			BlockCount: "1", TransactionCount: "1", GasUsed: "21000", BurnedWei: "0",
			BlobBurnedWei: "0", TokenEventCount: "0", TokenTransferCount: "0", NFTTransferCount: "0",
			CoreComplete: true, StatsComplete: true, TokenComplete: true,
		},
	}
	handler := testCatalogHandler(t, fake)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/stats/blocks?from_block=12&to_block=12", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var blocks gen.BlockStatListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &blocks); err != nil {
		t.Fatal(err)
	}
	if len(blocks.Data) != 1 || blocks.Data[0].TransactionsPerSecond == nil || *blocks.Data[0].TransactionsPerSecond != "0.25" || blocks.Data[0].NftTransferCount != "1" {
		t.Fatalf("blocks=%+v", blocks.Data)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/stats/summary?from_block=0&to_block=0", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var aggregate gen.AggregateStatsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &aggregate); err != nil {
		t.Fatal(err)
	}
	if aggregate.Data.AverageTps != nil || !aggregate.Data.Completeness.Core || !aggregate.Data.Completeness.Stats || !aggregate.Data.Completeness.Token {
		t.Fatalf("aggregate=%+v", aggregate.Data)
	}
}

func ptr(value string) *string { return &value }

func TestCatalogRoutesRejectMalformedIdentifiersBeforeQuery(t *testing.T) {
	t.Parallel()
	handler := testCatalogHandler(t, &fakeCatalog{})
	address := "0x" + strings.Repeat("44", 20)
	for _, path := range []string{
		"/api/v1/tokens/0x12",
		"/api/v1/nfts/" + address + "/01",
		"/api/v1/stats/blocks?from_block=1&to_block=",
		"/api/v1/transactions/0x12/trace",
		"/api/v1/tokens?cursor=" + strings.Repeat("x", 1025),
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestCanonicalQuantityEnforcesUint256AndCanonicalDecimal(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"0", "1", "115792089237316195423570985008687907853269984665640564039457584007913129639935"} {
		if !canonicalQuantity(value) {
			t.Fatalf("rejected valid quantity %s", value)
		}
	}
	for _, value := range []string{"", "01", "-1", "0x1", "115792089237316195423570985008687907853269984665640564039457584007913129639936"} {
		if canonicalQuantity(value) {
			t.Fatalf("accepted invalid quantity %s", value)
		}
	}
}
