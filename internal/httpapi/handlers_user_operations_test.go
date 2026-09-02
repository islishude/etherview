package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/publicquery"
)

type fakeUserOperationReader struct {
	page   publicquery.UserOperationPage
	detail gen.UserOperationDetail
	err    error
	calls  []string
}

func (reader *fakeUserOperationReader) UserOperations(context.Context, string, int) (publicquery.UserOperationPage, error) {
	reader.calls = append(reader.calls, "global")
	return reader.page, reader.err
}

func (reader *fakeUserOperationReader) UserOperation(context.Context, string) (gen.UserOperationDetail, error) {
	reader.calls = append(reader.calls, "detail")
	return reader.detail, reader.err
}

func (reader *fakeUserOperationReader) TransactionUserOperations(context.Context, string, string, int) (publicquery.UserOperationPage, error) {
	reader.calls = append(reader.calls, "transaction")
	return reader.page, reader.err
}

func (reader *fakeUserOperationReader) AddressUserOperations(context.Context, string, string, int) (publicquery.UserOperationPage, error) {
	reader.calls = append(reader.calls, "address")
	return reader.page, reader.err
}

func TestUserOperationHandlersExposeAllGeneratedResourcesAndCoverage(t *testing.T) {
	t.Parallel()
	hash := "0x" + repeatHex("12", 32)
	address := "0x" + repeatHex("34", 20)
	summary := userOperationHandlerSummary(hash, address)
	reader := &fakeUserOperationReader{
		page: publicquery.UserOperationPage{
			Items: []gen.UserOperationSummary{summary}, NextCursor: "next-userop",
			CoverageStart: 10, CoverageEnd: 20,
		},
		detail: gen.UserOperationDetail{
			Hash: summary.Hash, EntryPoint: summary.EntryPoint, EntryPointVersion: summary.EntryPointVersion,
			Sender: summary.Sender, Nonce: summary.Nonce, NonceKey: summary.NonceKey,
			NonceSequence: summary.NonceSequence, Success: summary.Success,
			ActualGasCost: summary.ActualGasCost, ActualGasUsed: summary.ActualGasUsed,
			TransactionHash: summary.TransactionHash, TransactionIndex: summary.TransactionIndex,
			OperationIndex: summary.OperationIndex, BlockNumber: summary.BlockNumber,
			BlockHash: summary.BlockHash, BlockTimestamp: summary.BlockTimestamp,
			Canonical: true, Finality: summary.Finality, Bundler: summary.Bundler,
			Beneficiary: summary.Beneficiary, InitKind: summary.InitKind,
			Request: gen.UserOperationRequest{
				CallGasLimit: "1", VerificationGasLimit: "2", PreVerificationGas: "3",
				MaxFeePerGas: "4", MaxPriorityFeePerGas: "5", InitCode: "0x",
				FactoryData: "0x", CallData: "0x", PaymasterAndData: "0x",
				PaymasterData: "0x", PaymasterSignature: "0x", Signature: "0x",
				AggregatedSignature: "0x",
			},
			Events: []gen.UserOperationProtocolEvent{},
		},
	}
	handler, err := New(Options{Config: config.Default(), Reader: fakeReader{}, UserOperations: reader})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/v1/user-operations?limit=1",
		"/api/v1/transactions/" + hash + "/user-operations?limit=1",
		"/api/v1/addresses/" + address + "/user-operations?limit=1",
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
		var response gen.UserOperationListResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Data) != 1 || response.Meta.CoverageStart == nil ||
			*response.Meta.CoverageStart != "10" || response.Meta.CoverageEnd == nil ||
			*response.Meta.CoverageEnd != "20" || response.Meta.NextCursor == nil ||
			*response.Meta.NextCursor != "next-userop" {
			t.Fatalf("%s response=%+v", path, response)
		}
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/user-operations/"+hash, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got, want := reader.calls, []string{"global", "transaction", "address", "detail"}; len(got) != len(want) {
		t.Fatalf("calls=%v", got)
	} else {
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("calls=%v", got)
			}
		}
	}
}

func TestUserOperationHandlersFailExplicitlyWhenReaderIsUnavailable(t *testing.T) {
	t.Parallel()
	handler, err := New(Options{Config: config.Default(), Reader: fakeReader{}})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/user-operations", nil))
	if recorder.Code != http.StatusServiceUnavailable || !json.Valid(recorder.Body.Bytes()) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func userOperationHandlerSummary(hash, address string) gen.UserOperationSummary {
	return gen.UserOperationSummary{
		Hash: hash, EntryPoint: address, EntryPointVersion: gen.N09,
		Sender: address, Nonce: "1", NonceKey: "0", NonceSequence: "1",
		Success: true, ActualGasCost: "10", ActualGasUsed: "20",
		TransactionHash: "0x" + repeatHex("56", 32), TransactionIndex: 0,
		OperationIndex: 0, EventLogIndex: 3, BlockNumber: "12", BlockHash: "0x" + repeatHex("78", 32),
		BlockTimestamp: time.Unix(1_700_000_000, 0).UTC(), Canonical: true,
		Finality: gen.FinalitySafe, Bundler: address, Beneficiary: address,
		InitKind: gen.UserOperationInitKindNone,
	}
}

func repeatHex(value string, count int) string {
	result := ""
	for range count {
		result += value
	}
	return result
}
