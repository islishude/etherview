package query

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/httpapi"
)

func TestHasAddressDelegationHistoryUsesCanonicalAppliedRowsAtReference(t *testing.T) {
	t.Parallel()
	address := common.HexToAddress("0x1111111111111111111111111111111111111111")
	referenceHash := common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	for _, test := range []struct {
		name       string
		hasHistory bool
	}{
		{name: "delegation including a later clearing", hasHistory: true},
		{name: "no canonical applied history", hasHistory: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := testDatabase(t, queryExpectation{
				contains: "FROM eip7702_authorizations AS authz",
				columns:  columns(2),
				rows:     [][]driver.Value{{true, test.hasHistory}},
				check: func(arguments []driver.NamedValue) error {
					if len(arguments) != 4 || arguments[0].Value != "1" || arguments[1].Value != "12" ||
						common.BytesToHash(arguments[2].Value.([]byte)) != referenceHash ||
						common.BytesToAddress(arguments[3].Value.([]byte)) != address {
						return fmt.Errorf("unexpected delegation history arguments: %v", arguments)
					}
					return nil
				},
			})
			reader, err := NewPostgresReader(db, Options{ChainID: 1})
			if err != nil {
				t.Fatal(err)
			}
			hasHistory, err := reader.HasAddressDelegationHistory(t.Context(), address.Hex(), 12, referenceHash)
			if err != nil {
				t.Fatal(err)
			}
			if hasHistory != test.hasHistory {
				t.Fatalf("hasHistory=%v, want %v", hasHistory, test.hasHistory)
			}
		})
	}

	compact := compactSQL(addressDelegationHistorySQL)
	for _, fragment := range []string{
		"authz.application_status = 'applied'",
		"authz.canonical",
		"authz.block_number <= $2::numeric",
		"canonical.block_hash = authz.block_hash",
	} {
		if !strings.Contains(compact, compactSQL(fragment)) {
			t.Fatalf("delegation history query does not enforce %q: %s", fragment, compact)
		}
	}
}

func TestHasAddressDelegationHistoryRejectsNonCanonicalReference(t *testing.T) {
	t.Parallel()
	db := testDatabase(t, queryExpectation{
		contains: "FROM eip7702_authorizations AS authz",
		columns:  columns(2),
		rows:     [][]driver.Value{{false, true}},
	})
	reader, err := NewPostgresReader(db, Options{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = reader.HasAddressDelegationHistory(t.Context(),
		"0x1111111111111111111111111111111111111111", 12, common.HexToHash("0xaa"),
	)
	if !errors.Is(err, httpapi.ErrNotReady) {
		t.Fatalf("error=%v, want not ready", err)
	}
}

func TestHasAddressDelegationHistoryFailsClosedOnQueryError(t *testing.T) {
	t.Parallel()
	db := testDatabase(t, queryExpectation{
		contains: "FROM eip7702_authorizations AS authz",
		err:      errors.New("database unavailable"),
	})
	reader, err := NewPostgresReader(db, Options{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = reader.HasAddressDelegationHistory(t.Context(),
		"0x1111111111111111111111111111111111111111", 12, common.HexToHash("0xaa"),
	)
	if err == nil || !strings.Contains(err.Error(), "check address delegation history") {
		t.Fatalf("unexpected error: %v", err)
	}
}
