package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/enscontract"
)

type failingENSReader struct{ nameHistoryReaderStub }

func (r failingENSReader) NameDetail(context.Context, string) (enscontract.NameDetail, error) {
	return enscontract.NameDetail{}, r.err
}

func TestENSReadErrorsPreservePublicContract(t *testing.T) {
	t.Parallel()
	const privateDetail = "postgres://fixture:private-password@private-host/database"
	hash := "0x" + strings.Repeat("1", 64)
	routes := []struct{ name, path string }{
		{"batch", "/api/v1/address-names?addresses=0x1111111111111111111111111111111111111111"},
		{"detail", "/api/v1/names/alice.eth"},
		{"history", "/api/v1/names/alice.eth/history"},
		{"events", "/api/v1/names/alice.eth/history/" + hash + "/events?block_hash=" + hash + "&snapshot=fixture"},
	}
	for _, failure := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"internal", errors.New(privateDetail), http.StatusInternalServerError, "query_failed"},
		{"invalid snapshot", fmt.Errorf("%s: %w", privateDetail, enscontract.ErrSnapshotInvalid), http.StatusBadRequest, "invalid_cursor"},
	} {
		for _, route := range routes {
			t.Run(failure.name+"/"+route.name, func(t *testing.T) {
				cfg := config.Default()
				cfg.Features.ENS = true
				reader := failingENSReader{nameHistoryReaderStub{addressNameReaderStub: addressNameReaderStub{err: failure.err}, err: failure.err}}
				handler, err := New(Options{Config: cfg, Reader: fakeReader{}, AddressNames: reader})
				if err != nil {
					t.Fatal(err)
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, route.path, nil))
				var body gen.ErrorResponse
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if response.Code != failure.status || body.Error.Code != failure.code || body.Error.RequestId == "" {
					t.Fatalf("status=%d error=%+v", response.Code, body.Error)
				}
				for _, forbidden := range []string{"postgres://", "private-password", "private-host", `"data":`, `"details":`} {
					if strings.Contains(response.Body.String(), forbidden) {
						t.Fatalf("error response leaks %q: %s", forbidden, response.Body)
					}
				}
			})
		}
	}
}
