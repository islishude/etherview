package query

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/httpapi"
	"github.com/islishude/etherview/internal/publicquery"
)

func TestSearchInvalidNameIsAClientError(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"a..eth", ".eth", "foo..bar.eth"} {
		t.Run(name, func(t *testing.T) {
			reader := testReader(t, testDatabase(t), Options{ChainID: 1})
			_, _, err := reader.Search(t.Context(), name, "", 20)
			if !errors.Is(err, publicquery.ErrInvalidInput) {
				t.Fatalf("invalid name error=%v, want ErrInvalidInput", err)
			}
			handler, err := httpapi.New(httpapi.Options{Config: config.Default(), Reader: reader})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/search?q="+url.QueryEscape(name), nil))
			if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `"code":"invalid_query"`) {
				t.Fatalf("invalid search status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
