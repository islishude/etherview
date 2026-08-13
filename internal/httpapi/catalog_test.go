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
	tokenPage    catalog.TokenPage
	token        catalog.TokenContract
	tokenErr     error
	nftOwner     catalog.NFTOwnership
	nftBalance   catalog.NFTBalancePage
	erc20Balance catalog.ERC20BalancePage
	nftErr       error
	trace        catalog.TransactionTrace
	traceErr     error
	calldata     catalog.TransactionCalldata
	calldataErr  error
	txTokens     catalog.TransactionTokenEventPage
	txInternal   catalog.TransactionInternalTransactionPage
	txLogs       catalog.TransactionLogPage
	txState      catalog.TransactionStateChangePage
	txRequest    catalog.TransactionResourceRequest
	blockStats   []catalog.BlockStat
	aggregate    catalog.AggregateStats
	statsErr     error
	internal     catalog.AddressInternalTransactionPage
	erc20        catalog.AddressTokenTransferPage
	nftEvents    catalog.AddressTokenTransferPage
	addressReq   catalog.AddressActivityRequest
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

func (fake *fakeCatalog) ERC20Balances(context.Context, catalog.ERC20BalanceRequest) (catalog.ERC20BalancePage, error) {
	return fake.erc20Balance, fake.nftErr
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

func (fake *fakeCatalog) TransactionCalldata(context.Context, string, string) (catalog.TransactionCalldata, error) {
	return fake.calldata, fake.calldataErr
}

func (fake *fakeCatalog) TransactionTokenEvents(_ context.Context, request catalog.TransactionResourceRequest) (catalog.TransactionTokenEventPage, error) {
	fake.txRequest = request
	return fake.txTokens, nil
}

func (fake *fakeCatalog) TransactionInternalTransactions(_ context.Context, request catalog.TransactionResourceRequest) (catalog.TransactionInternalTransactionPage, error) {
	fake.txRequest = request
	return fake.txInternal, nil
}

func (fake *fakeCatalog) TransactionLogs(_ context.Context, request catalog.TransactionResourceRequest) (catalog.TransactionLogPage, error) {
	fake.txRequest = request
	return fake.txLogs, nil
}

func (fake *fakeCatalog) TransactionStateChanges(_ context.Context, request catalog.TransactionResourceRequest) (catalog.TransactionStateChangePage, error) {
	fake.txRequest = request
	return fake.txState, nil
}

func (fake *fakeCatalog) AddressInternalTransactions(_ context.Context, request catalog.AddressActivityRequest) (catalog.AddressInternalTransactionPage, error) {
	fake.addressReq = request
	return fake.internal, nil
}

func (fake *fakeCatalog) AddressERC20Transfers(_ context.Context, request catalog.AddressActivityRequest) (catalog.AddressTokenTransferPage, error) {
	fake.addressReq = request
	return fake.erc20, nil
}

func (fake *fakeCatalog) AddressNFTTransfers(_ context.Context, request catalog.AddressActivityRequest) (catalog.AddressTokenTransferPage, error) {
	fake.addressReq = request
	return fake.nftEvents, nil
}

type fakeAddressActivityReader struct {
	items            []gen.Transaction
	next             string
	withdrawals      []gen.AddressWithdrawal
	withdrawalsNext  string
	address          string
	cursor           string
	limit            int
	withdrawalCalled bool
}

func (fake *fakeAddressActivityReader) AddressWithdrawals(
	_ context.Context,
	address string,
	cursor string,
	limit int,
) ([]gen.AddressWithdrawal, string, error) {
	fake.address, fake.cursor, fake.limit = address, cursor, limit
	fake.withdrawalCalled = true
	return fake.withdrawals, fake.withdrawalsNext, nil
}

func (fake *fakeAddressActivityReader) AddressTransactions(
	_ context.Context,
	address string,
	cursor string,
	limit int,
) ([]gen.Transaction, string, error) {
	fake.address, fake.cursor, fake.limit = address, cursor, limit
	return fake.items, fake.next, nil
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

func testAddressActivityHandler(
	t *testing.T,
	activity AddressActivityReader,
	catalogReader catalog.Reader,
) http.Handler {
	t.Helper()
	cfg := config.Default()
	cfg.Chain.ID = 11155111
	handler, err := New(Options{
		Config: cfg, Reader: fakeReader{}, AddressActivities: activity, Catalog: catalogReader,
		RequestID: func() string { return "address-activity-request" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestAddressActivityHTTPResponsesUseGeneratedEnvelopes(t *testing.T) {
	t.Parallel()
	address := "0x" + strings.Repeat("11", 20)
	other := "0x" + strings.Repeat("22", 20)
	hash := "0x" + strings.Repeat("33", 32)
	value := "115792089237316195423570985008687907853269984665640564039457584007913129639935"
	ordinary := &fakeAddressActivityReader{
		items: []gen.Transaction{{
			Hash: hash, From: address, To: &other, Nonce: "1", Value: value,
			Gas: "21000", Input: "0x", Canonical: true, Finality: gen.FinalityLatest,
			Completeness: gen.Completeness{
				Core: gen.StageStateComplete, Trace: gen.StageStateUnavailable,
				Metadata: gen.StageStateUnavailable, State: gen.StageStateUnavailable,
			},
		}},
		next: "next-transactions",
		withdrawals: []gen.AddressWithdrawal{{
			Index: "10", ValidatorIndex: "110", Address: address, Amount: "3200000000",
			BlockNumber: "12", BlockHash: hash, BlockTimestamp: time.Unix(1_700_000_000, 0).UTC(),
		}},
		withdrawalsNext: "next-withdrawals",
	}
	fake := &fakeCatalog{
		internal: catalog.AddressInternalTransactionPage{
			Items: []catalog.AddressInternalTransaction{{
				BlockNumber: "12", BlockHash: hash, BlockTimestamp: time.Unix(1_700_000_000, 0),
				TransactionHash: hash, TransactionIndex: "1", Path: []uint32{0, 1},
				Depth: 2, CallType: "create", From: &address, CreatedAddress: &other,
				Value: &value,
			}},
			NextCursor: "next-internal",
			Snapshot:   catalog.Snapshot{ChainID: "11155111", BlockNumber: "12", BlockHash: hash},
		},
		erc20: catalog.AddressTokenTransferPage{
			Items: []catalog.AddressTokenTransfer{{
				BlockNumber: "12", BlockHash: hash, BlockTimestamp: time.Unix(1_700_000_000, 0),
				TransactionHash: hash, TransactionIndex: "1", LogIndex: "2", SubIndex: "0",
				TokenAddress: other, Standard: "erc20", Kind: "transfer",
				From: &address, To: &other, Amount: &value, Confidence: "high",
			}},
			Snapshot: catalog.Snapshot{ChainID: "11155111", BlockNumber: "12", BlockHash: hash},
		},
		nftEvents: catalog.AddressTokenTransferPage{
			Items: []catalog.AddressTokenTransfer{{
				BlockNumber: "12", BlockHash: hash, BlockTimestamp: time.Unix(1_700_000_000, 0),
				TransactionHash: hash, TransactionIndex: "1", LogIndex: "3", SubIndex: "1",
				TokenAddress: other, Standard: "erc1155", Kind: "mint",
				To: &address, TokenID: &value, Amount: &value, Confidence: "verified",
			}},
			Snapshot: catalog.Snapshot{ChainID: "11155111", BlockNumber: "12", BlockHash: hash},
		},
	}
	handler := testAddressActivityHandler(t, ordinary, fake)

	transactionRecorder := httptest.NewRecorder()
	handler.ServeHTTP(transactionRecorder, httptest.NewRequest(
		http.MethodGet,
		"/api/v1/addresses/"+address+"/transactions?cursor=opaque&limit=1",
		nil,
	))
	if transactionRecorder.Code != http.StatusOK {
		t.Fatalf("transaction status=%d body=%s", transactionRecorder.Code, transactionRecorder.Body.String())
	}
	var transactions gen.TransactionListResponse
	if err := json.Unmarshal(transactionRecorder.Body.Bytes(), &transactions); err != nil {
		t.Fatal(err)
	}
	if len(transactions.Data) != 1 || transactions.Data[0].Value != value ||
		transactions.Meta.NextCursor == nil || *transactions.Meta.NextCursor != ordinary.next ||
		ordinary.address != address || ordinary.cursor != "opaque" || ordinary.limit != 1 {
		t.Fatalf("transactions=%+v reader=%+v", transactions, ordinary)
	}

	withdrawalRecorder := httptest.NewRecorder()
	handler.ServeHTTP(withdrawalRecorder, httptest.NewRequest(
		http.MethodGet,
		"/api/v1/addresses/"+address+"/withdrawals?cursor=withdrawal-page&limit=1",
		nil,
	))
	if withdrawalRecorder.Code != http.StatusOK {
		t.Fatalf("withdrawal status=%d body=%s", withdrawalRecorder.Code, withdrawalRecorder.Body.String())
	}
	var withdrawals gen.AddressWithdrawalListResponse
	if err := json.Unmarshal(withdrawalRecorder.Body.Bytes(), &withdrawals); err != nil {
		t.Fatal(err)
	}
	if len(withdrawals.Data) != 1 || withdrawals.Data[0].Index != "10" ||
		withdrawals.Meta.NextCursor == nil || *withdrawals.Meta.NextCursor != ordinary.withdrawalsNext ||
		!ordinary.withdrawalCalled || ordinary.address != address || ordinary.cursor != "withdrawal-page" || ordinary.limit != 1 {
		t.Fatalf("withdrawals=%+v reader=%+v", withdrawals, ordinary)
	}

	for _, test := range []struct {
		path     string
		standard gen.AddressTokenTransferStandard
	}{
		{"/internal-transactions", ""},
		{"/erc20-transfers", gen.AddressTokenTransferStandardErc20},
		{"/nft-transfers", gen.AddressTokenTransferStandardErc1155},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(
			http.MethodGet, "/api/v1/addresses/"+address+test.path+"?limit=1", nil,
		))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", test.path, recorder.Code, recorder.Body.String())
		}
		if test.standard == "" {
			var response gen.AddressInternalTransactionListResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Data) != 1 || response.Data[0].Depth != 2 ||
				response.Data[0].Value == nil || *response.Data[0].Value != value {
				t.Fatalf("internal response=%+v", response)
			}
		} else {
			var response gen.AddressTokenTransferListResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Data) != 1 || response.Data[0].Standard != test.standard {
				t.Fatalf("%s response=%+v", test.path, response)
			}
		}
	}
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

func TestTransactionTraceEmptyExecutionUsesNotApplicableArrayContract(t *testing.T) {
	t.Parallel()
	hash := "0x" + strings.Repeat("55", 32)
	blockHash := "0x" + strings.Repeat("66", 32)
	address := "0x" + strings.Repeat("77", 20)
	fake := &fakeCatalog{trace: catalog.TransactionTrace{
		ChainID: "11155111", BlockNumber: "12", BlockHash: blockHash,
		TransactionHash: hash, TransactionIndex: "0", State: catalog.StageComplete,
		Frames: []catalog.TraceFrame{{
			Path: []uint32{}, ParentPath: []uint32{}, Depth: 0, CallType: "CALL",
			Execution: &catalog.TraceExecution{ContextAddress: address, Resolution: "empty"},
			Decoding: &catalog.TraceCallDecoding{
				Kind: "function", Status: "not_applicable", Inputs: []catalog.ABIValue{},
				OutputStatus: "not_applicable", Outputs: []catalog.ABIValue{}, Candidates: []string{},
				Warning: "call execution code is empty",
			},
		}},
	}}
	recorder := httptest.NewRecorder()
	testCatalogHandler(t, fake).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/transactions/"+hash+"/trace", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"status":"not_applicable"`) ||
		!strings.Contains(recorder.Body.String(), `"candidates":[]`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response gen.TransactionTraceResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	decoding := response.Data.Frames[0].Decoding
	if decoding == nil || decoding.Status != gen.TraceCallDecodingStatusNotApplicable || decoding.Candidates == nil {
		t.Fatalf("decoding=%+v", decoding)
	}
}

func TestTransactionCalldataExposesExactDelegatedExecution(t *testing.T) {
	t.Parallel()
	hash := "0x" + strings.Repeat("55", 32)
	blockHash := "0x" + strings.Repeat("66", 32)
	authority := "0x" + strings.Repeat("77", 20)
	delegate := "0x" + strings.Repeat("88", 20)
	codeHash := "0x" + strings.Repeat("99", 32)
	fake := &fakeCatalog{calldata: catalog.TransactionCalldata{
		Identity: catalog.TransactionResourceIdentity{
			ChainID: "11155111", BlockNumber: "12", BlockHash: blockHash,
			TransactionHash: hash, TransactionIndex: "3", State: catalog.StageComplete,
		},
		Input: "0x55241077",
		Execution: catalog.TraceExecution{
			ContextAddress: authority, Address: delegate, CodeHash: codeHash, Resolution: "eip7702_delegate",
		},
		Decoding: catalog.TransactionCalldataDecoding{
			Status: "decoded", FunctionName: "setValue", Signature: "setValue(uint256)",
			Inputs: []catalog.ABIValue{{Name: "value", Type: "uint256", Value: "42"}}, Candidates: []string{},
			ABISource:  &catalog.ABISource{Kind: "exact_address", Address: delegate, CodeHash: codeHash},
			Confidence: "verified",
		},
	}}
	recorder := httptest.NewRecorder()
	testCatalogHandler(t, fake).ServeHTTP(recorder, httptest.NewRequest(
		http.MethodGet, "/api/v1/transactions/"+hash+"/calldata", nil,
	))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response gen.TransactionCalldataResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Execution.Resolution != "eip7702_delegate" || response.Data.Execution.Address == nil ||
		*response.Data.Execution.Address != delegate || response.Data.Decoding.Signature == nil ||
		*response.Data.Decoding.Signature != "setValue(uint256)" || len(response.Data.Decoding.Inputs) != 1 {
		t.Fatalf("data=%+v", response.Data)
	}
}

func TestTransactionCalldataRejectsContractCreation(t *testing.T) {
	t.Parallel()
	hash := "0x" + strings.Repeat("55", 32)
	fake := &fakeCatalog{calldataErr: catalog.ErrNotApplicable}
	recorder := httptest.NewRecorder()
	testCatalogHandler(t, fake).ServeHTTP(recorder, httptest.NewRequest(
		http.MethodGet, "/api/v1/transactions/"+hash+"/calldata", nil,
	))
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), `"code":"calldata_not_applicable"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestTransactionSubresourcesExposeInclusionIdentityAndPagination(t *testing.T) {
	t.Parallel()
	hash := "0x" + strings.Repeat("77", 32)
	blockHash := "0x" + strings.Repeat("88", 32)
	address := "0x" + strings.Repeat("99", 20)
	storageKey := "0x" + strings.Repeat("aa", 32)
	before, after := "1", "2"
	decimals := uint8(6)
	identity := catalog.TransactionResourceIdentity{
		ChainID: "11155111", BlockNumber: "12", BlockHash: blockHash,
		TransactionHash: hash, TransactionIndex: "3", State: catalog.StageComplete,
	}
	tests := []struct {
		name       string
		path       string
		configure  func(*fakeCatalog)
		assertBody func(*testing.T, []byte)
	}{
		{
			name: "internal transactions", path: "/internal-transactions?limit=1&cursor=opaque",
			configure: func(fake *fakeCatalog) {
				fake.txInternal = catalog.TransactionInternalTransactionPage{
					Identity: identity, NextCursor: "internal-next",
					Items: []catalog.TransactionInternalTransaction{{
						Path: []uint32{0, 1}, Depth: 2, CallType: "CALL",
						From: address, To: &address, Value: after,
					}},
				}
			},
			assertBody: func(t *testing.T, body []byte) {
				t.Helper()
				var response gen.TransactionInternalTransactionResponse
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatal(err)
				}
				if response.Data.BlockHash != blockHash || len(response.Data.Items) != 1 ||
					response.Data.Items[0].Value != after || response.Data.Items[0].Depth != 2 ||
					response.Meta.NextCursor == nil || *response.Meta.NextCursor != "internal-next" {
					t.Fatalf("response=%+v", response)
				}
			},
		},
		{
			name: "token transfers", path: "/token-transfers?limit=1&cursor=opaque",
			configure: func(fake *fakeCatalog) {
				fake.txTokens = catalog.TransactionTokenEventPage{
					Identity: identity, NextCursor: "token-next",
					Items: []catalog.TokenEvent{{
						ChainID: "11155111", BlockNumber: "12", BlockHash: blockHash,
						TransactionHash: hash, LogIndex: "4", SubIndex: "0",
						TokenAddress: address, Standard: "erc20", Kind: "transfer",
						Amount: &after, Decimals: &decimals, Confidence: "event",
					}},
				}
			},
			assertBody: func(t *testing.T, body []byte) {
				t.Helper()
				var response gen.TransactionTokenTransferResponse
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatal(err)
				}
				if response.Data.BlockHash != blockHash || len(response.Data.Items) != 1 ||
					response.Data.Items[0].Decimals == nil || *response.Data.Items[0].Decimals != 6 ||
					response.Meta.NextCursor == nil || *response.Meta.NextCursor != "token-next" {
					t.Fatalf("response=%+v", response)
				}
			},
		},
		{
			name: "logs", path: "/logs?limit=1&cursor=opaque",
			configure: func(fake *fakeCatalog) {
				fake.txLogs = catalog.TransactionLogPage{
					Identity: identity, NextCursor: "log-next",
					Items: []catalog.TransactionLog{{
						Address: address, LogIndex: "4", Topics: []string{blockHash}, Data: "0x1234",
						Decoding: catalog.TransactionLogDecoding{
							Status: "decoded", EventName: "Changed", Signature: "Changed(uint256)",
							Confidence: "verified", Candidates: []string{"Changed(uint256)"},
							Arguments: []catalog.TransactionLogArgument{{Name: "value", Type: "uint256", Value: "42"}},
							ABISource: &catalog.TransactionLogABISource{Kind: "exact_address", Address: address, CodeHash: blockHash},
						},
					}},
				}
			},
			assertBody: func(t *testing.T, body []byte) {
				t.Helper()
				var response gen.TransactionLogResponse
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatal(err)
				}
				if response.Data.TransactionHash != hash || len(response.Data.Items) != 1 ||
					response.Data.Items[0].LogIndex != "4" ||
					response.Data.Items[0].Decoding.Signature == nil ||
					*response.Data.Items[0].Decoding.Signature != "Changed(uint256)" ||
					response.Data.Items[0].Decoding.AbiSource == nil ||
					response.Data.Items[0].Decoding.AbiSource.Kind != "exact_address" {
					t.Fatalf("response=%+v", response)
				}
			},
		},
		{
			name: "state changes", path: "/state-changes?limit=1&cursor=opaque",
			configure: func(fake *fakeCatalog) {
				fake.txState = catalog.TransactionStateChangePage{
					Identity: identity, NextCursor: "state-next",
					Items: []catalog.TransactionStateChange{{
						Address: address, Kind: "storage", StorageKey: &storageKey,
						Before: &before, After: &after,
					}},
				}
			},
			assertBody: func(t *testing.T, body []byte) {
				t.Helper()
				var response gen.TransactionStateChangeResponse
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatal(err)
				}
				if response.Data.State != gen.TransactionStateChangesStateComplete ||
					len(response.Data.Items) != 1 || response.Data.Items[0].StorageKey == nil {
					t.Fatalf("response=%+v", response)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeCatalog{}
			test.configure(fake)
			recorder := httptest.NewRecorder()
			testCatalogHandler(t, fake).ServeHTTP(recorder, httptest.NewRequest(
				http.MethodGet, "/api/v1/transactions/"+hash+test.path, nil,
			))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if fake.txRequest.TransactionHash != hash || fake.txRequest.Cursor != "opaque" ||
				fake.txRequest.Limit != 1 {
				t.Fatalf("request=%+v", fake.txRequest)
			}
			test.assertBody(t, recorder.Body.Bytes())
		})
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
			BlobBurnedWei: new("393216"), TokenEventCount: "4",
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
