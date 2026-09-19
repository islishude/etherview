package catalog

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	testpgx "github.com/islishude/etherview/internal/testpgx"
)

func TestAddressDelegationsUsesSafeAuthorizationAliasAndReturnsHistory(t *testing.T) {
	authority := "0x" + strings.Repeat("11", 20)
	catalog, backend := openCatalog(t,
		snapshotStep("100", bytesOf(0xaa, 32)),
		catalogQueryStep{
			contains: "FROM eip7702_authorizations AS authz",
			rows: catalogRows(7, []any{
				"100", bytesOf(0xbb, 32), bytesOf(0xcc, 32), "2", "0",
				bytesOf(0x22, 20), nil,
			}),
			check: func(arguments []any) error {
				if len(arguments) != 8 || !testpgx.NumericEquals(arguments[5], "1") ||
					!bytes.Equal(arguments[6].([]byte), bytesOf(0x11, 20)) || arguments[0] != false ||
					!testpgx.NumericEquals(arguments[1], "0") || !testpgx.NumericEquals(arguments[2], "0") || !testpgx.NumericEquals(arguments[3], "0") ||
					arguments[4] != int32(3) {
					return fmt.Errorf("unexpected delegation arguments: %v", arguments)
				}
				return nil
			},
		},
	)

	page, err := catalog.AddressDelegations(context.Background(), AddressDelegationRequest{
		ChainID: "1", Address: authority, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Kind != "delegated" ||
		page.Items[0].Delegate != "0x2222222222222222222222222222222222222222" {
		t.Fatalf("page=%+v", page)
	}
	backend.mu.Lock()
	query := backend.queries[len(backend.queries)-1]
	backend.mu.Unlock()
	if strings.Contains(query, "authorization.") {
		t.Fatalf("delegation query still uses reserved alias: %s", query)
	}
	if !strings.Contains(query, "ORDER BY ordered.block_number DESC, ordered.transaction_index DESC") ||
		!strings.Contains(query, "ordered.authorization_index DESC") {
		t.Fatalf("delegation query does not order by the numeric source columns: %s", query)
	}
	assertCatalogConsumed(t, backend)
}
