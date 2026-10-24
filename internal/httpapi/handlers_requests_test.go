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
)

type requestReaderStub struct {
	fakeReader
	calls          int
	filter, cursor string
	limit          int
}

func (r *requestReaderStub) BlockRequests(_ context.Context, id, filter, cursor string, limit int) (gen.BlockRequests, string, error) {
	r.calls++
	r.filter = filter
	r.cursor = cursor
	r.limit = limit
	return gen.BlockRequests{BlockHash: "0x" + strings.Repeat("11", 32), State: gen.ConsensusStateComplete, Items: []gen.ExecutionRequest{}}, "next", nil
}
func TestRequestsHTTPValidationAndPagination(t *testing.T) {
	reader := &requestReaderStub{}
	handler, err := New(Options{Config: config.Default(), Reader: &fakeReader{}, RequestsReader: reader})
	if err != nil {
		t.Fatal(err)
	}
	request := func(query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/blocks/1/requests"+query, nil))
		return w
	}
	response := request("")
	if response.Code != 200 || reader.limit != 50 {
		t.Fatalf("default=%d %s", response.Code, response.Body.String())
	}
	response = request("?request_type=0x04&cursor=opaque&limit=100")
	if response.Code != 200 || reader.filter != "0x04" || reader.cursor != "opaque" || reader.limit != 100 {
		t.Fatalf("filter=%d %s", response.Code, response.Body.String())
	}
	var envelope gen.BlockRequestsResponse
	if json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope.Meta.NextCursor == nil || *envelope.Meta.NextCursor != "next" {
		t.Fatal("missing next cursor")
	}
	calls := reader.calls
	for _, query := range []string{"?request_type=0x05", "?request_type=0x00&request_type=0x01", "?limit=0", "?limit=101", "?limit=01", "?cursor=", "?unknown=1"} {
		if response := request(query); response.Code < 400 || reader.calls != calls {
			t.Fatalf("accepted %s: %d", query, response.Code)
		}
	}
}
