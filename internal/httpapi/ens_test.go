package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/islishude/etherview/internal/enscontract"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/config"
	ensresolver "github.com/islishude/etherview/internal/ens"
)

type addressNameReaderStub struct {
	results  []ensresolver.PrimaryResolution
	snapshot string
	err      error
	current  ensresolver.PrimaryResolution
}

func (reader addressNameReaderStub) ResolveCurrentPrimary(context.Context, common.Address) (ensresolver.PrimaryResolution, error) {
	return reader.current, reader.err
}

func (reader addressNameReaderStub) ResolveAddressBatch(
	_ context.Context,
	addresses []common.Address,
	_ string,
) ([]ensresolver.PrimaryResolution, string, error) {
	if reader.err != nil {
		return nil, "", reader.err
	}
	results := append([]ensresolver.PrimaryResolution(nil), reader.results...)
	for index := range results {
		results[index].Address = addresses[index]
	}
	return results, reader.snapshot, nil
}

func TestAddressNamesReturnsOrderedPartialSnapshot(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Features.ENS = true
	first := "0x1111111111111111111111111111111111111111"
	second := "0x2222222222222222222222222222222222222222"
	third := "0x3333333333333333333333333333333333333333"
	handler, err := New(Options{
		Config: cfg, Reader: fakeReader{},
		AddressNames: addressNameReaderStub{
			snapshot: "snapshot-token",
			results: []ensresolver.PrimaryResolution{
				{Outcome: ensresolver.OutcomeResolved, Name: "alice.eth"},
				{Outcome: ensresolver.OutcomeNoRecord},
				{Code: ensresolver.CodeCCIPUnavailable},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/address-names?addresses="+strings.Join([]string{first, second, third}, ","),
		nil,
	)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response gen.AddressNamePageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Snapshot != "snapshot-token" || len(response.Data.Items) != 3 ||
		response.Data.Items[0].State != gen.AddressNameLookupStateResolved ||
		response.Data.Items[0].PrimaryName == nil || response.Data.Items[0].PrimaryName.Name != "alice.eth" ||

		response.Data.Items[1].State != gen.AddressNameLookupStateNotFound ||
		response.Data.Items[2].State != gen.AddressNameLookupStateUnavailable ||
		response.Data.Items[2].Code == nil || *response.Data.Items[2].Code != ensresolver.CodeCCIPUnavailable {
		t.Fatalf("response=%+v", response)
	}
}

func TestAddressNamesRejectsDuplicatesAndMapsSnapshotFailure(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Features.ENS = true
	address := "0x1111111111111111111111111111111111111111"
	for _, test := range []struct {
		name   string
		reader AddressNameReader
		query  string
		status int
		code   string
	}{
		{
			name: "duplicate", reader: addressNameReaderStub{},
			query: "addresses=" + address + "," + address, status: http.StatusBadRequest, code: "duplicate_address",
		},
		{
			name: "expired snapshot", reader: addressNameReaderStub{err: enscontract.ErrSnapshotInvalid},
			query: "addresses=" + address + "&snapshot=expired", status: http.StatusBadRequest, code: "invalid_cursor",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, err := New(Options{Config: cfg, Reader: fakeReader{}, AddressNames: test.reader})
			if err != nil {
				t.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/address-names?"+test.query, nil))
			if recorder.Code != test.status || !strings.Contains(recorder.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestEnabledENSRequiresServiceAndDisabledRouteIsTypedUnavailable(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Features.ENS = true
	if _, err := New(Options{Config: cfg, Reader: fakeReader{}}); err == nil {
		t.Fatal("enabled ENS accepted without a name service")
	}
	cfg.Features.ENS = false
	handler, err := New(Options{Config: cfg, Reader: fakeReader{}})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(
		http.MethodGet,
		"/api/v1/address-names?addresses=0x1111111111111111111111111111111111111111",
		nil,
	))
	if recorder.Code != http.StatusServiceUnavailable ||
		!strings.Contains(recorder.Body.String(), `"code":"capability_unavailable"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestENSResolutionErrorNeverExposesNestedText(t *testing.T) {
	t.Parallel()
	err := &ensresolver.ResolutionError{Code: ensresolver.CodeRPCUnavailable}
	if !errors.Is(err, err) || strings.Contains(err.Error(), "rpc") {
		t.Fatalf("resolution error leaked detail: %v", err)
	}
}

type nameDetailReaderStub struct {
	addressNameReaderStub
	detail enscontract.NameDetail
}

func (r nameDetailReaderStub) NameDetail(_ context.Context, name string) (enscontract.NameDetail, error) {
	if name == "invalid" {
		return enscontract.NameDetail{}, enscontract.ErrInvalidName
	}
	return r.detail, nil
}
func TestNameDetailReturnsPartialAndNotIndexedWithoutResolution(t *testing.T) {
	for _, state := range []string{"indexed", "not_indexed"} {
		t.Run(state, func(t *testing.T) {
			cfg := config.Default()
			cfg.Features.ENS = true
			reader := nameDetailReaderStub{detail: enscontract.NameDetail{Name: "alice.eth", State: state, RegistrationProfile: enscontract.PendingProfile(), ResolvedAddress: enscontract.Field{State: "pending"}}}
			h, e := New(Options{Config: cfg, Reader: fakeReader{}, AddressNames: reader})
			if e != nil {
				t.Fatal(e)
			}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/names/alice.eth", nil))
			if response.Code != http.StatusOK {
				t.Fatal(response.Code, response.Body.String())
			}
			var got gen.NameDetailResponse
			if e = json.Unmarshal(response.Body.Bytes(), &got); e != nil {
				t.Fatal(e)
			}
			if string(got.Data.State) != state || got.Data.Registrant.State != "pending" || got.Data.ResolvedAddress.State != "pending" || got.Data.ProfileObservation != nil {
				t.Fatalf("detail %+v", got.Data)
			}
			if strings.Contains(response.Body.String(), "custom_ens") || strings.Contains(response.Body.String(), "\"source\"") {
				t.Fatal("classification exposed")
			}
			bad := httptest.NewRecorder()
			h.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/api/v1/names/invalid", nil))
			if bad.Code != 400 {
				t.Fatal(bad.Code)
			}
		})
	}
}

type nameHistoryReaderStub struct {
	addressNameReaderStub
	err error
}

func (r nameHistoryReaderStub) NameHistory(_ context.Context, request enscontract.HistoryRequest) (enscontract.HistoryPage, string, error) {
	return enscontract.HistoryPage{Name: request.Name, State: "indexed", Snapshot: "snapshot", Coverage: enscontract.HistoryCoverage{State: "partial", Start: "0", Pending: "1"}, Items: []enscontract.HistoryTransaction{{TransactionHash: "0x" + strings.Repeat("1", 64), BlockHash: "0x" + strings.Repeat("2", 64), BlockNumber: "9007199254740993", Timestamp: "2026-10-30T00:00:00Z", Actions: []string{"registration", "address"}, EventCount: "2"}}}, "next", r.err
}
func (r nameHistoryReaderStub) NameHistoryEvents(_ context.Context, _ enscontract.HistoryEventsRequest) (enscontract.HistoryEventsPage, string, error) {
	return enscontract.HistoryEventsPage{Items: []enscontract.HistoryEvent{}}, "", r.err
}
func TestNameHistoryContractAndBounds(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ENS = true
	h, e := New(Options{Config: cfg, Reader: fakeReader{}, AddressNames: nameHistoryReaderStub{}})
	if e != nil {
		t.Fatal(e)
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/names/alice.eth/history?limit=25", nil))
	var body gen.NameHistoryResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Data.Items) != 1 || body.Data.Items[0].BlockNumber != "9007199254740993" || body.Meta.NextCursor == nil {
		t.Fatalf("%d %s", response.Code, response.Body)
	}
	for _, path := range []string{"/api/v1/names/alice.eth/history?limit=101", "/api/v1/names/alice.eth/history?limit=1&limit=2", "/api/v1/names/alice.eth/history?cursor=" + strings.Repeat("a", 1025), "/api/v1/names/alice.eth/history/0x" + strings.Repeat("1", 64) + "/events"} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != 400 {
			t.Fatalf("bounds %s: %d", path, response.Code)
		}
	}
	h, e = New(Options{Config: cfg, Reader: fakeReader{}, AddressNames: nameHistoryReaderStub{err: enscontract.ErrSnapshotInvalid}})
	if e != nil {
		t.Fatal(e)
	}
	response = httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/names/alice.eth/history", nil))
	if response.Code != 400 || !strings.Contains(response.Body.String(), "invalid_cursor") {
		t.Fatal(response.Code, response.Body)
	}
}
