package catalog

import (
	"bytes"
	"context"
	"database/sql/driver"
	"fmt"
	"strings"
	"testing"
)

func TestAddressDelegationsUsesSafeAuthorizationAliasAndReturnsHistory(t *testing.T) {
	authority := "0x" + strings.Repeat("11", 20)
	catalog, backend := openCatalog(t,
		snapshotStep("100", bytesOf(0xaa, 32)),
		catalogQueryStep{
			contains: "FROM eip7702_authorizations AS authz",
			rows: catalogRows(7, []driver.Value{
				"100", bytesOf(0xbb, 32), bytesOf(0xcc, 32), "2", "0",
				bytesOf(0x22, 20), nil,
			}),
			check: func(arguments []driver.NamedValue) error {
				if len(arguments) != 8 || arguments[0].Value != "1" ||
					!bytes.Equal(arguments[1].Value.([]byte), bytesOf(0x11, 20)) || arguments[3].Value != false ||
					arguments[4].Value != "0" || arguments[5].Value != "0" || arguments[6].Value != "0" ||
					arguments[7].Value != int64(3) {
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
	assertCatalogConsumed(t, backend)
}
