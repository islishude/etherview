package etherscan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	calls   int
}

func (b *fakeBackend) Execute(_ context.Context, request Request) (any, error) {
	b.calls++
	b.request = request
	return b.result, b.err
}

type compatibilityActionCase struct {
	module         string
	action         string
	requiredMethod string
	keyed          bool
	values         url.Values
}

func (c compatibilityActionCase) key() string { return c.module + "." + c.action }

// compatibilityActionCases is an explicit public-surface inventory. Keeping
// this separate from supported makes a route addition or removal fail until
// its HTTP and production-backend behavior is deliberately covered.
func compatibilityActionCases() []compatibilityActionCase {
	const guid = "123e4567-e89b-42d3-a456-426614174000"
	return []compatibilityActionCase{
		{module: "account", action: "balance", values: url.Values{"address": {testSender}}},
		{module: "account", action: "balancemulti", values: url.Values{"address": {testSender + "," + testRecipient}}},
		{module: "account", action: "txlist", values: url.Values{"address": {testSender}}},
		{module: "account", action: "txlistinternal", values: url.Values{"txhash": {testHash(7)}}},
		{module: "account", action: "tokentx", values: url.Values{"address": {testSender}}},
		{module: "account", action: "tokennfttx", values: url.Values{"address": {testSender}}},
		{module: "account", action: "token1155tx", values: url.Values{"address": {testSender}}},
		{module: "account", action: "tokenbalance", values: url.Values{"contractaddress": {testContract}, "address": {testSender}}},
		{module: "account", action: "getminedblocks", values: url.Values{"address": {testSender}}},

		{module: "contract", action: "getabi", values: url.Values{"address": {testContract}}},
		{module: "contract", action: "getsourcecode", values: url.Values{"address": {testContract}}},
		{module: "contract", action: "getcontractcreation", values: url.Values{"contractaddresses": {testContract}}},
		{
			module: "contract", action: "verifysourcecode", requiredMethod: http.MethodPost, keyed: true,
			values: url.Values{
				"contractaddress": {testContract}, "sourceCode": {"contract A{}"},
				"codeformat": {"solidity-single-file"}, "contractname": {"A"}, "compilerversion": {"v0.8.30"},
			},
		},
		{module: "contract", action: "checkverifystatus", requiredMethod: http.MethodPost, keyed: true, values: url.Values{"guid": {guid}}},
		{module: "contract", action: "verifyproxycontract", requiredMethod: http.MethodPost, keyed: true, values: url.Values{"address": {testContract}}},
		{module: "contract", action: "checkproxyverification", requiredMethod: http.MethodGet, keyed: true, values: url.Values{"guid": {guid}}},

		{module: "transaction", action: "getstatus", values: url.Values{"txhash": {testHash(7)}}},
		{module: "transaction", action: "gettxreceiptstatus", values: url.Values{"txhash": {testHash(7)}}},
		{module: "logs", action: "getLogs", values: url.Values{"address": {testContract}}},
		{module: "block", action: "getblocknobytime", values: url.Values{"timestamp": {"100"}, "closest": {"before"}}},
		{module: "block", action: "getblockcountdown", values: url.Values{"blockno": {"20"}}},
		{module: "stats", action: "ethsupply", values: url.Values{}},
		{module: "stats", action: "ethprice", values: url.Values{}},
		{module: "stats", action: "tokensupply", values: url.Values{"contractaddress": {testContract}}},
		{module: "token", action: "tokensupply", values: url.Values{"contractaddress": {testContract}}},
		{module: "token", action: "tokenbalance", values: url.Values{"contractaddress": {testContract}, "address": {testSender}}},
		{module: "token", action: "tokeninfo", values: url.Values{"contractaddress": {testContract}}},
		{module: "token", action: "tokenholderlist", values: url.Values{"contractaddress": {testContract}}},
	}
}

func TestCompatibilityActionMapMatchesBackendDispatch(t *testing.T) {
	t.Parallel()
	cases := compatibilityActionCases()
	seen := make(map[string]struct{}, len(cases))
	backend := testPostgresBackend(t, fakeDatabase(t), PostgresOptions{ChainID: 1})

	for _, test := range cases {
		key := test.key()
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf("duplicate compatibility action %s", key)
		}
		seen[key] = struct{}{}

		actions, moduleExists := supported[test.module]
		spec, actionExists := actions[test.action]
		if !moduleExists || !actionExists {
			t.Errorf("golden action %s is absent from the handler map", key)
			continue
		}
		if spec.method != test.requiredMethod || spec.keyed != test.keyed {
			t.Errorf("%s handler policy method=%q keyed=%t, want method=%q keyed=%t", key, spec.method, spec.keyed, test.requiredMethod, test.keyed)
		}
		if err := validateValues(test.values, spec); err != nil {
			t.Errorf("%s golden values do not pass handler validation: %v", key, err)
			continue
		}

		_, err := backend.Execute(context.Background(), Request{Module: test.module, Action: test.action, Values: cloneValues(test.values)})
		if errors.Is(err, ErrInvalidParameter) && strings.Contains(err.Error(), "unsupported module/action") {
			t.Errorf("handler action %s has no production backend dispatch: %v", key, err)
		}
	}

	for module, actions := range supported {
		for action := range actions {
			key := module + "." + action
			if _, exists := seen[key]; !exists {
				t.Errorf("handler action %s has no explicit golden case", key)
			}
		}
	}

	_, err := backend.Execute(context.Background(), Request{Module: "unsupported", Action: "unsupported"})
	if !errors.Is(err, ErrInvalidParameter) || !strings.Contains(err.Error(), "unsupported module/action") {
		t.Fatalf("backend unsupported-action probe returned %v", err)
	}
}

func TestCompatibilityActionGoldenEnvelopesAndMethods(t *testing.T) {
	t.Parallel()
	manager := auth.Manager{Repository: auth.NewMemoryRepository(), Pepper: bytes.Repeat([]byte{5}, 32)}
	issued, err := manager.Create(context.Background(), "compatibility-action-goldens", 100, 100)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range compatibilityActionCases() {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			t.Run(test.key()+"/"+method, func(t *testing.T) {
				backend := &fakeBackend{result: test.key()}
				handler := manager.Middleware(false, Handler{ChainID: 1, Backend: backend})
				request := compatibilityActionRequest(method, test)
				if test.keyed {
					request.Header.Set("X-API-Key", issued.Token)
				}
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, request)

				allowed := test.requiredMethod == "" || test.requiredMethod == method
				if !allowed {
					assertCompatibilityGolden(t, recorder, http.StatusOK,
						fmt.Sprintf("{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"action requires %s\"}\n", test.requiredMethod))
					if backend.calls != 0 {
						t.Fatalf("wrong-method request dispatched %#v", backend.request)
					}
					return
				}

				assertCompatibilityGolden(t, recorder, http.StatusOK,
					fmt.Sprintf("{\"status\":\"1\",\"message\":\"OK\",\"result\":%q}\n", test.key()))
				if backend.calls != 1 || backend.request.Module != test.module || backend.request.Action != test.action {
					t.Fatalf("backend calls=%d request=%#v", backend.calls, backend.request)
				}
				wantValues := cloneValues(test.values)
				wantValues.Set("chainid", "1")
				wantValues.Set("module", test.module)
				wantValues.Set("action", test.action)
				if got, want := backend.request.Values.Encode(), wantValues.Encode(); got != want {
					t.Fatalf("backend values=%q, want %q", got, want)
				}
			})
		}
	}
}

func TestCompatibilityKeyedActionsGoldenRejection(t *testing.T) {
	t.Parallel()
	for _, test := range compatibilityActionCases() {
		if !test.keyed {
			continue
		}
		test := test
		t.Run(test.key(), func(t *testing.T) {
			backend := &fakeBackend{result: "must not dispatch"}
			recorder := httptest.NewRecorder()
			Handler{ChainID: 1, Backend: backend}.ServeHTTP(recorder, compatibilityActionRequest(test.requiredMethod, test))
			assertCompatibilityGolden(t, recorder, http.StatusOK, "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"API Key required\"}\n")
			if backend.calls != 0 {
				t.Fatalf("unauthenticated request dispatched %#v", backend.request)
			}
		})
	}
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

func TestCompatibilityBoundaryGoldenFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		chainID          uint64
		backendAvailable bool
		method           string
		values           url.Values
		rawBody          string
		maxBody          int64
		wantStatus       int
		wantBody         string
		wantAllow        string
	}{
		{
			name: "backend unavailable", chainID: 1, method: http.MethodGet,
			values:     url.Values{"chainid": {"1"}, "module": {"stats"}, "action": {"ethsupply"}},
			wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"compatibility backend is unavailable\"}\n",
		},
		{
			name: "chain unconfigured", backendAvailable: true, method: http.MethodGet,
			values:     url.Values{"chainid": {"1"}, "module": {"stats"}, "action": {"ethsupply"}},
			wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"compatibility backend is unavailable\"}\n",
		},
		{
			name: "unsupported HTTP method", chainID: 1, backendAvailable: true, method: http.MethodPut,
			values:     url.Values{"chainid": {"1"}, "module": {"stats"}, "action": {"ethsupply"}},
			wantStatus: http.StatusMethodNotAllowed, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"unsupported HTTP method\"}\n",
			wantAllow: "GET, POST",
		},
		{
			name: "malformed form", chainID: 1, backendAvailable: true, method: http.MethodPost, rawBody: "%zz", maxBody: 1024,
			wantStatus: http.StatusBadRequest, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"invalid form or request too large\"}\n",
		},
		{
			name: "oversized form", chainID: 1, backendAvailable: true, method: http.MethodPost,
			rawBody: "chainid=1&module=stats&action=ethsupply", maxBody: 8,
			wantStatus: http.StatusBadRequest, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"invalid form or request too large\"}\n",
		},
		{
			name: "missing chain", chainID: 1, backendAvailable: true, method: http.MethodGet,
			values:     url.Values{"module": {"stats"}, "action": {"ethsupply"}},
			wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"missing or unsupported chainid\"}\n",
		},
		{
			name: "unsupported chain", chainID: 1, backendAvailable: true, method: http.MethodGet,
			values:     url.Values{"chainid": {"2"}, "module": {"stats"}, "action": {"ethsupply"}},
			wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"missing or unsupported chainid\"}\n",
		},
		{
			name: "unsupported module", chainID: 1, backendAvailable: true, method: http.MethodGet,
			values:     url.Values{"chainid": {"1"}, "module": {"proxy"}, "action": {"eth_blockNumber"}},
			wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"unsupported module\"}\n",
		},
		{
			name: "unsupported action", chainID: 1, backendAvailable: true, method: http.MethodGet,
			values:     url.Values{"chainid": {"1"}, "module": {"stats"}, "action": {"dailyavgblocksize"}},
			wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"unsupported action\"}\n",
		},
		{
			name: "invalid action parameter", chainID: 1, backendAvailable: true, method: http.MethodGet,
			values: url.Values{
				"chainid": {"1"}, "module": {"account"}, "action": {"txlist"},
				"address": {testSender}, "offset": {"1001"},
			},
			wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"offset must be between 1 and 1000\"}\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &fakeBackend{result: "must not dispatch"}
			var configured Backend
			if test.backendAvailable {
				configured = backend
			}
			handler := Handler{ChainID: test.chainID, Backend: configured, MaxBody: test.maxBody}
			var request *http.Request
			if test.rawBody != "" {
				request = httptest.NewRequest(test.method, "/v2/api", strings.NewReader(test.rawBody))
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			} else {
				request = compatibilityValuesRequest(test.method, test.values)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			assertCompatibilityGolden(t, recorder, test.wantStatus, test.wantBody)
			if got := recorder.Header().Get("Allow"); got != test.wantAllow {
				t.Fatalf("Allow=%q, want %q", got, test.wantAllow)
			}
			if backend.calls != 0 {
				t.Fatalf("invalid boundary request dispatched %#v", backend.request)
			}
		})
	}
}

func TestCompatibilityBackendErrorGoldenEnvelopes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
		wrap       bool
	}{
		{name: "not found", err: ErrNotFound, wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"No records found\",\"result\":[]}\n", wrap: true},
		{name: "core", err: ErrCoreUnavailable, wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"core coverage unavailable\"}\n", wrap: true},
		{name: "trace", err: ErrTraceUnavailable, wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"trace capability unavailable\"}\n", wrap: true},
		{name: "price", err: ErrPriceUnavailable, wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"price capability unavailable\"}\n", wrap: true},
		{name: "state", err: ErrStateUnavailable, wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"state capability unavailable\"}\n", wrap: true},
		{name: "status", err: ErrStatusUnavailable, wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"receipt status unavailable\"}\n", wrap: true},
		{name: "estimate", err: ErrEstimateUnavailable, wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"block time estimate unavailable\"}\n", wrap: true},
		{name: "passed block", err: ErrBlockAlreadyPassed, wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"Error! Block number already pass\"}\n", wrap: true},
		{name: "supply", err: ErrSupplyUnavailable, wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"supply capability unavailable\"}\n", wrap: true},
		{name: "token", err: ErrTokenUnavailable, wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"token index capability unavailable\"}\n", wrap: true},
		{name: "uncle", err: ErrUncleUnavailable, wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"uncle index capability unavailable\"}\n", wrap: true},
		{name: "proxy verification", err: ErrProxyVerificationUnavailable, wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"proxy verification workflow unavailable\"}\n", wrap: true},
		{name: "verification target", err: ErrVerificationTargetUnavailable, wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"verification target state unavailable\"}\n", wrap: true},
		{name: "verification", err: ErrVerificationUnavailable, wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"verification workflow unavailable\"}\n", wrap: true},
		{name: "verification job missing", err: ErrVerificationJobNotFound, wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"Unable to locate verification request\"}\n", wrap: true},
		{name: "verification failed", err: ErrVerificationFailed, wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"Fail - Unable to verify\"}\n", wrap: true},
		{name: "unverified contract", err: ErrContractUnverified, wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"Contract source code not verified\"}\n", wrap: true},
		{name: "invalid parameter", err: invalidParameter("bad topic"), wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"invalid parameter: bad topic\"}\n"},
		{name: "pending", err: ErrPending, wantStatus: http.StatusOK, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"Pending in queue\"}\n", wrap: true},
		{name: "internal", err: errors.New("database credential detail"), wantStatus: http.StatusInternalServerError, wantBody: "{\"status\":\"0\",\"message\":\"NOTOK\",\"result\":\"query failed\"}\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			backendErr := test.err
			if test.wrap {
				backendErr = fmt.Errorf("sensitive nested backend detail: %w", backendErr)
			}
			backend := &fakeBackend{err: backendErr}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/v2/api?chainid=1&module=stats&action=ethsupply", nil)
			Handler{ChainID: 1, Backend: backend}.ServeHTTP(recorder, request)
			assertCompatibilityGolden(t, recorder, test.wantStatus, test.wantBody)
			if backend.calls != 1 {
				t.Fatalf("backend calls=%d, want 1", backend.calls)
			}
		})
	}
}

func compatibilityActionRequest(method string, action compatibilityActionCase) *http.Request {
	values := cloneValues(action.values)
	values.Set("chainid", "1")
	values.Set("module", action.module)
	values.Set("action", action.action)
	return compatibilityValuesRequest(method, values)
}

func compatibilityValuesRequest(method string, values url.Values) *http.Request {
	encoded := values.Encode()
	if method == http.MethodGet {
		return httptest.NewRequest(method, "/v2/api?"+encoded, nil)
	}
	request := httptest.NewRequest(method, "/v2/api", strings.NewReader(encoded))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}

func assertCompatibilityGolden(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantBody string) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("status=%d, want %d; body=%s", recorder.Code, wantStatus, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type=%q", got)
	}
	if got := recorder.Body.String(); got != wantBody {
		t.Fatalf("body mismatch\n got: %q\nwant: %q", got, wantBody)
	}
}
