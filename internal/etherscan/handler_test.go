package etherscan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/auth"
)

type fakeBackend struct {
	request Request
	result  any
	err     error
}

func (b *fakeBackend) Execute(_ context.Context, request Request) (any, error) {
	b.request = request
	return b.result, b.err
}

func TestDispatchesSupportedAction(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{result: "123"}
	handler := Handler{ChainID: 1, Backend: backend}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v2/api?chainid=1&module=account&action=balance&address=0x0000000000000000000000000000000000000001", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || backend.request.Action != "balance" {
		t.Fatalf("status=%d request=%#v body=%s", recorder.Code, backend.request, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response["status"] != "1" || response["result"] != "123" {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}

func TestDispatchesOfficialTokenSupplyAndBalanceModules(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		"/v2/api?chainid=1&module=stats&action=tokensupply&contractaddress=0x0000000000000000000000000000000000000001",
		"/v2/api?chainid=1&module=account&action=tokenbalance&contractaddress=0x0000000000000000000000000000000000000001&address=0x0000000000000000000000000000000000000002&tag=latest",
	} {
		backend := &fakeBackend{result: "123"}
		recorder := httptest.NewRecorder()
		Handler{ChainID: 1, Backend: backend}.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK || backend.request.Action == "" || !strings.Contains(recorder.Body.String(), `"result":"123"`) {
			t.Fatalf("path=%s status=%d request=%#v body=%s", path, recorder.Code, backend.request, recorder.Body.String())
		}
	}
}

func TestDispatchesInternalTransactionsByHashOrBlockRange(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		"/v2/api?chainid=1&module=account&action=txlistinternal&txhash=0x" + strings.Repeat("1", 64),
		"/v2/api?chainid=1&module=account&action=txlistinternal&startblock=10&endblock=20",
	} {
		backend := &fakeBackend{result: []any{}}
		recorder := httptest.NewRecorder()
		Handler{ChainID: 1, Backend: backend}.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK || backend.request.Action != "txlistinternal" || !strings.Contains(recorder.Body.String(), `"status":"1"`) {
			t.Fatalf("path=%s status=%d request=%#v body=%s", path, recorder.Code, backend.request, recorder.Body.String())
		}
	}
}

func TestRejectsWrongChainUnknownAndInvalidPagination(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		"/v2/api?chainid=2&module=stats&action=ethsupply",
		"/v2/api?chainid=1&module=proxy&action=eth_blockNumber",
		"/v2/api?chainid=1&module=account&action=txlist&address=0x0000000000000000000000000000000000000001&offset=1001",
	} {
		recorder := httptest.NewRecorder()
		Handler{ChainID: 1, Backend: &fakeBackend{}}.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"status":"0"`) {
			t.Fatalf("path=%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestVerificationRequiresPostAndBodyBound(t *testing.T) {
	t.Parallel()
	query := "chainid=1&module=contract&action=verifysourcecode&contractaddress=0x0000000000000000000000000000000000000001&sourceCode=x&codeformat=solidity-standard-json-input&contractname=A.sol:A&compilerversion=v1"
	handler := Handler{ChainID: 1, Backend: &fakeBackend{}, MaxBody: 8}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v2/api?"+query, nil))
	if !strings.Contains(recorder.Body.String(), "requires POST") {
		t.Fatalf("body=%s", recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v2/api", strings.NewReader(query+strings.Repeat("x", 20)))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestVerificationMethodMatrixAndAPIKeyBoundary(t *testing.T) {
	t.Parallel()
	manager := auth.Manager{Repository: auth.NewMemoryRepository(), Pepper: bytes.Repeat([]byte{7}, 32)}
	issued, err := manager.Create(context.Background(), "verification-test", 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	const guid = "123e4567-e89b-42d3-a456-426614174000"
	tests := []struct {
		name   string
		method string
		values string
	}{
		{name: "submit source post", method: http.MethodPost, values: "module=contract&action=verifysourcecode&contractaddress=" + testContract + "&sourceCode=contract+A%7B%7D&codeformat=solidity-single-file&contractname=A&compilerversion=v0.8.30"},
		{name: "source status post", method: http.MethodPost, values: "module=contract&action=checkverifystatus&guid=" + guid},
		{name: "submit proxy post", method: http.MethodPost, values: "module=contract&action=verifyproxycontract&address=" + testContract},
		{name: "proxy status get", method: http.MethodGet, values: "module=contract&action=checkproxyverification&guid=" + guid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &fakeBackend{result: "ok"}
			handler := manager.Middleware(false, Handler{ChainID: 1, Backend: backend})
			request := verificationHandlerRequest(test.method, test.values)
			request.Header.Set("X-API-Key", issued.Token)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if backend.request.Action == "" || recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"result":"ok"`) {
				t.Fatalf("method=%s request=%#v status=%d body=%s", test.method, backend.request, recorder.Code, recorder.Body.String())
			}

			wrongMethod := http.MethodPost
			if test.method == http.MethodPost {
				wrongMethod = http.MethodGet
			}
			backend = &fakeBackend{result: "should-not-dispatch"}
			handler = manager.Middleware(false, Handler{ChainID: 1, Backend: backend})
			request = verificationHandlerRequest(wrongMethod, test.values)
			request.Header.Set("X-API-Key", issued.Token)
			recorder = httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if backend.request.Action != "" || !strings.Contains(recorder.Body.String(), "action requires "+test.method) {
				t.Fatalf("wrong method=%s request=%#v body=%s", wrongMethod, backend.request, recorder.Body.String())
			}
		})
	}

	backend := &fakeBackend{result: "should-not-dispatch"}
	handler := manager.Middleware(false, Handler{ChainID: 1, Backend: backend})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, verificationHandlerRequest(http.MethodPost, "module=contract&action=checkverifystatus&guid="+guid))
	if backend.request.Action != "" || !strings.Contains(recorder.Body.String(), "API Key required") {
		t.Fatalf("request=%#v body=%s", backend.request, recorder.Body.String())
	}
}

func TestVerificationPOSTAuthenticatesAPIKeyFromFormAndPreservesPayload(t *testing.T) {
	t.Parallel()
	manager := auth.Manager{
		Repository: auth.NewMemoryRepository(), Pepper: bytes.Repeat([]byte{9}, 32),
		MaxCompatibilityFormBodyBytes: 1 << 20,
	}
	issued, err := manager.Create(context.Background(), "form-verification", 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{result: "guid"}
	handler := manager.Middleware(false, Handler{ChainID: 1, Backend: backend, MaxBody: 1 << 20})
	source := "contract A { string constant value = 'body-preserved'; }"
	values := "module=contract&action=verifysourcecode&contractaddress=" + testContract +
		"&sourceCode=" + url.QueryEscape(source) +
		"&codeformat=solidity-single-file&contractname=A&compilerversion=v0.8.30&apikey=" + url.QueryEscape(issued.Token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, verificationHandlerRequest(http.MethodPost, values))
	if recorder.Code != http.StatusOK || backend.request.Action != "verifysourcecode" || backend.request.Values.Get("sourceCode") != source ||
		backend.request.Values.Get("apikey") != "" || !strings.Contains(recorder.Body.String(), `"result":"guid"`) {
		t.Fatalf("request=%#v status=%d body=%s", backend.request, recorder.Code, recorder.Body.String())
	}
}

func verificationHandlerRequest(method, values string) *http.Request {
	values = "chainid=1&" + values
	if method == http.MethodGet {
		return httptest.NewRequest(method, "/v2/api?"+values, nil)
	}
	request := httptest.NewRequest(method, "/v2/api", strings.NewReader(values))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}

func TestCapabilityErrorsAreExplicit(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		err  error
		text string
	}{
		{ErrTraceUnavailable, "trace capability unavailable"},
		{ErrPriceUnavailable, "price capability unavailable"},
		{ErrStateUnavailable, "state capability unavailable"},
		{ErrStatusUnavailable, "receipt status unavailable"},
		{ErrEstimateUnavailable, "block time estimate unavailable"},
		{ErrBlockAlreadyPassed, "Error! Block number already pass"},
		{ErrSupplyUnavailable, "supply capability unavailable"},
		{ErrTokenUnavailable, "token index capability unavailable"},
		{ErrUncleUnavailable, "uncle index capability unavailable"},
		{ErrVerificationUnavailable, "verification workflow unavailable"},
		{ErrContractUnverified, "Contract source code not verified"},
		{invalidParameter("bad topic"), "invalid parameter: bad topic"},
		{ErrPending, "Pending in queue"},
		{errors.New("db"), "query failed"},
	} {
		backend := &fakeBackend{err: test.err}
		handler := Handler{ChainID: 1, Backend: backend}
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v2/api?chainid=1&module=stats&action=ethsupply", nil)
		handler.ServeHTTP(recorder, request)
		if !strings.Contains(recorder.Body.String(), test.text) {
			t.Fatalf("err=%v body=%s", test.err, recorder.Body.String())
		}
	}
}
