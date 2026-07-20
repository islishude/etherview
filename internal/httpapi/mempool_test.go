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
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/mempool"
)

type fakeMempool struct {
	page mempool.Page
	err  error
}

func (reader fakeMempool) Pending(context.Context, string, int) (mempool.Page, error) {
	return reader.page, reader.err
}

func mempoolHandler(t *testing.T, reader mempool.Reader, enabled bool) http.Handler {
	t.Helper()
	cfg := config.Default()
	cfg.Chain.ID = 1
	cfg.Features.Mempool = enabled
	handler, err := New(Options{
		Config: cfg, Reader: fakeReader{}, Mempool: reader,
		RequestID: func() string { return "pending-request" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestPendingTransactionsUseSnapshotEnvelopeAndStringQuantities(t *testing.T) {
	t.Parallel()
	now := time.Unix(100, 0).UTC()
	to := "0x0000000000000000000000000000000000000002"
	gasPrice := "100000000000000000000"
	reader := fakeMempool{page: mempool.Page{
		Items: []mempool.Transaction{{
			Hash: "0x" + strings.Repeat("11", 32), From: "0x0000000000000000000000000000000000000001", To: &to,
			Nonce: "7", Value: "999999999999999999999", Gas: "21000", GasPrice: &gasPrice,
			Input: "0x", FirstSeenAt: now, LastSeenAt: now.Add(time.Second), ExpiresAt: now.Add(time.Minute), Endpoint: "pending-rpc",
		}},
		NextCursor: "opaque-next",
		Snapshot:   mempool.SnapshotInfo{ID: 9, Endpoint: "pending-rpc", ObservedAt: now.Add(time.Second), ExpiresAt: now.Add(time.Minute), TransactionCount: 1},
	}}
	recorder := httptest.NewRecorder()
	mempoolHandler(t, reader, true).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/pending?limit=1", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response gen.PendingTransactionListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].Value != "999999999999999999999" || response.Data[0].GasPrice == nil || *response.Data[0].GasPrice != gasPrice {
		t.Fatalf("pending data=%+v", response.Data)
	}
	if response.Meta.SnapshotId != "9" || response.Meta.TransactionCount != "1" || response.Meta.NextCursor == nil || *response.Meta.NextCursor != "opaque-next" {
		t.Fatalf("pending meta=%+v", response.Meta)
	}
}

func TestPendingUnavailableIsExplicitForDisabledAndFailedCapability(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		reader  mempool.Reader
		enabled bool
		reason  string
	}{
		{reader: nil, enabled: false, reason: "feature_disabled"},
		{reader: fakeMempool{err: mempool.CapabilityError{State: mempool.StateFailed, Code: "rpc_request_failed", LastAttemptAt: time.Unix(5, 0)}}, enabled: true, reason: "rpc_request_failed"},
	} {
		recorder := httptest.NewRecorder()
		mempoolHandler(t, test.reader, test.enabled).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/pending", nil))
		if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), `"code":"mempool_unavailable"`) || !strings.Contains(recorder.Body.String(), test.reason) {
			t.Fatalf("enabled=%v status=%d body=%s", test.enabled, recorder.Code, recorder.Body.String())
		}
	}
}

func TestPendingCursorAndLimitFailuresAreTyped(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		path   string
		reader mempool.Reader
	}{
		{path: "/api/v1/pending?limit=101", reader: fakeMempool{}},
		{path: "/api/v1/pending?cursor=bad", reader: fakeMempool{err: mempool.ErrInvalidCursor}},
	} {
		recorder := httptest.NewRecorder()
		mempoolHandler(t, test.reader, true).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "invalid_") {
			t.Fatalf("path=%s status=%d body=%s", test.path, recorder.Code, recorder.Body.String())
		}
	}
}
