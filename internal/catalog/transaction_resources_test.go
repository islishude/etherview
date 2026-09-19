package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func transactionResourceIdentityStep(blockHash []byte) catalogQueryStep {
	return catalogQueryStep{
		contains: "FROM transaction_inclusions AS inclusion",
		rows:     catalogRows(4, []any{"100", blockHash, int64(7), true}),
	}
}

func transactionResourceStageStep(state string, generation int64) catalogQueryStep {
	return catalogQueryStep{
		contains: "FROM published_block_stage_results",
		rows:     catalogRows(2, []any{state, generation}),
	}
}

func internalTransactionRow(path string, callType string, created bool, value string) []any {
	to, createdAddress := any(bytesOf(0x22, 20)), any(nil)
	if created {
		to, createdAddress = nil, bytesOf(0x33, 20)
	}
	return []any{
		path, int64(len(strings.Split(path, "."))), callType,
		bytesOf(0x11, 20), to, createdAddress, value,
	}
}

func TestTransactionInternalTransactionsAreFilteredPaginatedAndGenerationBound(t *testing.T) {
	blockHash := bytesOf(0xaa, 32)
	txHash := "0x" + strings.Repeat("bb", 32)
	catalog, backend := openCatalog(t,
		transactionResourceIdentityStep(blockHash),
		transactionResourceStageStep("complete", 3),
		catalogQueryStep{
			contains: "trace.depth > 0",
			rows: catalogRows(7,
				internalTransactionRow("0", "CALL", false, "1"),
				internalTransactionRow("1.0", "CREATE2", true, "2"),
				internalTransactionRow("2", "SELFDESTRUCT", false, "3"),
			),
			check: func(arguments []any) error {
				if len(arguments) != 5 || arguments[4] != int32(3) || arguments[3] != int32(0) {
					return fmt.Errorf("unexpected first-page arguments: %v", arguments)
				}
				return nil
			},
		},
	)
	page, err := catalog.TransactionInternalTransactions(context.Background(), TransactionResourceRequest{
		ChainID: "1", TransactionHash: txHash, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.NextCursor == "" || page.Identity.State != StageComplete ||
		page.Items[0].Value != "1" || page.Items[1].CreatedAddress == nil {
		t.Fatalf("page=%+v", page)
	}
	backend.mu.Lock()
	query := backend.queries[len(backend.queries)-1]
	backend.mu.Unlock()
	for _, fragment := range []string{
		"trace.value > 0", "trace.reverted = false",
		"ORDER BY string_to_array(trace.trace_path, '.')::bigint[]",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("internal transaction query lacks %q: %s", fragment, query)
		}
	}
	assertCatalogConsumed(t, backend)

	stale, staleBackend := openCatalog(t,
		transactionResourceIdentityStep(blockHash),
		transactionResourceStageStep("complete", 4),
	)
	_, err = stale.TransactionInternalTransactions(context.Background(), TransactionResourceRequest{
		ChainID: "1", TransactionHash: txHash, Cursor: page.NextCursor, Limit: 2,
	})
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("stale cursor error=%v", err)
	}
	assertCatalogConsumed(t, staleBackend)
}

func TestTransactionInternalTransactionsPreserveUnavailableAndEmptyStates(t *testing.T) {
	txHash := "0x" + strings.Repeat("bb", 32)
	for _, test := range []struct {
		name  string
		state string
		query bool
	}{
		{name: "unavailable", state: "unavailable"},
		{name: "failed", state: "failed"},
		{name: "complete empty", state: "complete", query: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			steps := []catalogQueryStep{
				transactionResourceIdentityStep(bytesOf(0xaa, 32)),
				transactionResourceStageStep(test.state, 3),
			}
			if test.query {
				steps = append(steps, catalogQueryStep{contains: "FROM normalized_traces AS trace", rows: catalogRows(7)})
			}
			catalog, backend := openCatalog(t, steps...)
			page, err := catalog.TransactionInternalTransactions(context.Background(), TransactionResourceRequest{
				ChainID: "1", TransactionHash: txHash, Limit: 10,
			})
			if err != nil || string(page.Identity.State) != test.state || len(page.Items) != 0 || page.Items == nil {
				t.Fatalf("page=%+v err=%v", page, err)
			}
			assertCatalogConsumed(t, backend)
		})
	}
}
