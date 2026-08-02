package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/config"
)

type proxyReaderStub struct {
	detail          gen.ProxyDetails
	upgrades        gen.ProxyUpgradeHistory
	initializations gen.ProxyInitializationHistory
	next            string
	err             error
	address         string
	cursor          string
	limit           int
}

func (reader *proxyReaderStub) Proxy(_ context.Context, address string) (gen.ProxyDetails, error) {
	reader.address = address
	return reader.detail, reader.err
}

func (reader *proxyReaderStub) ProxyUpgrades(
	_ context.Context,
	address, cursor string,
	limit int,
) (gen.ProxyUpgradeHistory, string, error) {
	reader.address, reader.cursor, reader.limit = address, cursor, limit
	return reader.upgrades, reader.next, reader.err
}

func (reader *proxyReaderStub) ProxyInitializations(
	_ context.Context,
	address, cursor string,
	limit int,
) (gen.ProxyInitializationHistory, string, error) {
	reader.address, reader.cursor, reader.limit = address, cursor, limit
	return reader.initializations, reader.next, reader.err
}

func TestProxyRoutesAreAnonymousSnapshotBoundReads(t *testing.T) {
	t.Parallel()
	const address = "0x1111111111111111111111111111111111111111"
	snapshot := gen.CatalogSnapshot{
		ChainId: "1", BlockNumber: "42",
		BlockHash: "0x" + strings.Repeat("2", 64),
	}
	reader := &proxyReaderStub{
		detail: gen.ProxyDetails{
			Address: address, Status: gen.ProxyDetailStatusNotDetected,
			Snapshot: snapshot, Evidence: []gen.ProxyRecognitionEvidence{},
		},
		upgrades: gen.ProxyUpgradeHistory{
			ProxyAddress: address, Snapshot: snapshot,
			Coverage: gen.ProxyHistoryCoverage{State: gen.ProxyHistoryCoverageStateComplete},
			Items:    []gen.ProxyUpgrade{},
		},
		initializations: gen.ProxyInitializationHistory{
			ContractAddress: address, Snapshot: snapshot,
			Coverage: gen.ProxyHistoryCoverage{State: gen.ProxyHistoryCoverageStatePartial},
			Items:    []gen.ProxyInitialization{},
		},
		next: "next-page",
	}
	handler, err := New(Options{Config: config.Default(), Reader: fakeReader{}, ProxyReader: reader})
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet,
		"/api/v1/contracts/"+address+"/proxy", nil))
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"status":"not_detected"`) {
		t.Fatalf("proxy detail status=%d body=%s", response.Code, response.Body.String())
	}
	if reader.address != strings.ToLower(address) {
		t.Fatalf("proxy detail address=%q", reader.address)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet,
		"/api/v1/contracts/"+address+"/proxy/upgrades?cursor=page-one&limit=7", nil))
	if response.Code != http.StatusOK || reader.cursor != "page-one" || reader.limit != 7 ||
		!strings.Contains(response.Body.String(), `"next_cursor":"next-page"`) {
		t.Fatalf("proxy upgrades status=%d cursor=%q limit=%d body=%s",
			response.Code, reader.cursor, reader.limit, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet,
		"/api/v1/contracts/"+address+"/proxy/initializations", nil))
	if response.Code != http.StatusOK || reader.limit != 20 ||
		!strings.Contains(response.Body.String(), `"state":"partial"`) {
		t.Fatalf("proxy initializations status=%d limit=%d body=%s",
			response.Code, reader.limit, response.Body.String())
	}
}

func TestProxyHistoryRejectsStaleCursorAndBoundsInputs(t *testing.T) {
	t.Parallel()
	const address = "0x1111111111111111111111111111111111111111"
	reader := &proxyReaderStub{err: ErrInvalidCursor}
	handler, err := New(Options{Config: config.Default(), Reader: fakeReader{}, ProxyReader: reader})
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet,
		"/api/v1/contracts/"+address+"/proxy/upgrades?cursor=stale", nil))
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), `"code":"invalid_cursor"`) {
		t.Fatalf("stale cursor status=%d body=%s", response.Code, response.Body.String())
	}

	reader.err = errors.New("must not be called")
	for _, path := range []string{
		"/api/v1/contracts/not-an-address/proxy",
		"/api/v1/contracts/" + address + "/proxy?code_hash=ignored",
		"/api/v1/contracts/" + address + "/proxy/upgrades?limit=101",
		"/api/v1/contracts/" + address + "/proxy/upgrades?limit=01",
		"/api/v1/contracts/" + address + "/proxy/upgrades?limit=1&limit=2",
		"/api/v1/contracts/" + address + "/proxy/upgrades?cursor=",
		"/api/v1/contracts/" + address + "/proxy/upgrades?cursor=&cursor=stale",
		"/api/v1/contracts/" + address + "/proxy/upgrades?unknown=value",
		"/api/v1/contracts/" + address + "/proxy/initializations?cursor=" + strings.Repeat("x", maximumOpaqueCursorLength+1),
	} {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid proxy request %s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestProxyRoutesRemainPresentWhenReaderUnavailable(t *testing.T) {
	t.Parallel()
	const address = "0x1111111111111111111111111111111111111111"
	handler, err := New(Options{Config: config.Default(), Reader: fakeReader{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "/upgrades", "/initializations"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet,
			"/api/v1/contracts/"+address+"/proxy"+suffix, nil))
		if response.Code != http.StatusServiceUnavailable ||
			!strings.Contains(response.Body.String(), `"code":"proxy_unavailable"`) {
			t.Fatalf("unavailable %s status=%d body=%s", suffix, response.Code, response.Body.String())
		}
	}
}
