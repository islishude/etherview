package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/deficontract"
)

type defiReaderFixture struct {
	calls     int
	request   deficontract.Request
	err       error
	positions []deficontract.Position
}

func (f *defiReaderFixture) Positions(_ context.Context, r deficontract.Request) (deficontract.Page, error) {
	f.calls++
	f.request = r
	return deficontract.Page{Positions: f.positions, Deployments: []deficontract.DeploymentStatus{}}, f.err
}
func TestDeFiHTTPReadOnlyContractAndErrors(t *testing.T) {
	f := &defiReaderFixture{}
	h, err := New(Options{Config: config.Default(), Reader: fakeReader{}, DeFi: f})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/addresses/0x" + strings.Repeat("11", 20) + "/defi-positions"
	for _, tc := range []struct {
		query  string
		err    error
		status int
		code   string
	}{{"?protocol=uniswap-v2&limit=2", nil, 200, ""}, {"?protocol=unknown", nil, 400, "invalid_protocol"}, {"?cursor=bad", deficontract.ErrCursor, 400, "invalid_cursor"}, {"", errors.New("https://secret.invalid/?key=password"), 503, "query_failed"}} {
		f.err = tc.err
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path+tc.query, nil))
		if r.Code != tc.status || tc.code != "" && !strings.Contains(r.Body.String(), tc.code) || strings.Contains(r.Body.String(), "password") {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	if f.calls != 3 {
		t.Fatal("invalid filter reached reader", f.calls)
	}
}

func TestDeFiHTTPReceiptMetadataMatchesGeneratedContract(t *testing.T) {
	decimals := 18
	receipt := &deficontract.Asset{Kind: "erc20", Address: "0x" + strings.Repeat("22", 20), Amount: "12345000000000000000", Decimals: &decimals, Symbol: "UNI-V2"}
	f := &defiReaderFixture{positions: []deficontract.Position{{ReceiptAsset: receipt}}}
	h, err := New(Options{Config: config.Default(), Reader: fakeReader{}, DeFi: f})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/v1/addresses/0x"+strings.Repeat("11", 20)+"/defi-positions", nil))
	var response gen.DeFiPositionsResponse
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &response) != nil || len(response.Data.Positions) != 1 {
		t.Fatal(r.Code, r.Body.String())
	}
	got := response.Data.Positions[0].ReceiptAsset
	if got == nil || got.Amount != receipt.Amount || got.Address == nil || *got.Address != receipt.Address || got.Decimals == nil || *got.Decimals != decimals || got.Symbol == nil || *got.Symbol != receipt.Symbol {
		t.Fatalf("receipt mismatch: %+v", got)
	}
}
