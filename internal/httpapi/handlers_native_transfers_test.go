package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/publicquery"
)

type nativeTransferReaderStub struct {
	err     error
	request publicquery.NativeTransferRequest
}

func (reader *nativeTransferReaderStub) NativeTransfers(_ context.Context, request publicquery.NativeTransferRequest) (publicquery.NativeTransferPage, error) {
	reader.request = request
	return publicquery.NativeTransferPage{Applicable: true, CoverageStart: 12, CoverageEnd: 12, NextCursor: "opaque-next", Items: []gen.NativeTransfer{{Amount: "9007199254740993"}}}, reader.err
}
func TestNativeTransferRoutesUseDedicatedReaderAndStableErrors(t *testing.T) {
	reader := &nativeTransferReaderStub{}
	handler, err := New(Options{Config: config.Default(), Reader: fakeReader{}, NativeTransfers: reader})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/transactions/0x" + strings.Repeat("11", 32) + "/native-transfers", "/api/v1/addresses/0x" + strings.Repeat("22", 20) + "/native-transfers"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path+"?limit=1&cursor=opaque", nil))
		if recorder.Code != 200 {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
		}
		var payload gen.NativeTransferListResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if !payload.Applicable || len(payload.Data) != 1 || payload.Data[0].Amount != "9007199254740993" || payload.Meta.NextCursor == nil || *payload.Meta.NextCursor != "opaque-next" || reader.request.Cursor != "opaque" || reader.request.Limit != 1 {
			t.Fatalf("payload=%+v request=%+v", payload, reader.request)
		}
	}
	reader.err = publicquery.NewCapabilityUnavailableError("native_transfers", "pending", "coverage_incomplete")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/addresses/0x"+strings.Repeat("22", 20)+"/native-transfers", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("pending status=%d body=%s", recorder.Code, recorder.Body)
	}
}
