package httpapi

import (
	"context"
	"encoding/json"
	"errors"
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
				{Outcome: ensresolver.OutcomeResolved, Name: "alice.eth", Source: ensresolver.SourceOfficial},
				{Outcome: ensresolver.OutcomeNoRecord, Source: ensresolver.SourceCustom},
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
		response.Data.Items[0].PrimaryName.Source != gen.PrimaryNameSourceEns ||
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
			name: "expired snapshot", reader: addressNameReaderStub{err: ensresolver.ErrSnapshotInvalid},
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
