package catalog

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestAddressInternalTransactionsAreCanonicalPaginatedAndReorgBound(t *testing.T) {
	subject := "0x" + strings.Repeat("11", 20)
	rows := [][]driver.Value{
		internalActivityRow("100", "2", "0.1", 0x31),
		internalActivityRow("99", "1", "0", 0x32),
		internalActivityRow("98", "0", "0", 0x33),
	}
	catalog, backend := openCatalog(t,
		snapshotStep("100", bytesOf(0xaa, 32)),
		stageStep("complete"),
		catalogQueryStep{
			contains: "FROM candidates",
			rows:     catalogRows(17, rows...),
			check: func(arguments []driver.NamedValue) error {
				if len(arguments) != 10 || arguments[3].Value != false ||
					arguments[4].Value != "0" || arguments[6].Value != "0" ||
					arguments[9].Value != int64(3) {
					return fmt.Errorf("unsafe first-page arguments: %v", arguments)
				}
				return nil
			},
		},
	)
	page, err := catalog.AddressInternalTransactions(context.Background(), AddressActivityRequest{
		ChainID: "1", Address: subject, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.NextCursor == "" || page.Snapshot.BlockNumber != "100" {
		t.Fatalf("page=%+v", page)
	}
	if page.Items[0].TransactionIndex != "2" || len(page.Items[0].Path) != 2 ||
		page.Items[0].CreatedAddress == nil || page.Items[0].Value == nil ||
		*page.Items[0].Value != "115792089237316195423570985008687907853269984665640564039457584007913129639935" {
		t.Fatalf("item=%+v", page.Items[0])
	}
	backend.mu.Lock()
	query := backend.queries[len(backend.queries)-1]
	backend.mu.Unlock()
	if !strings.Contains(query, "UNION") || !strings.Contains(query, "created_address = $3") ||
		!strings.Contains(query, "JOIN canonical_blocks") {
		t.Fatalf("query lacks indexed canonical branches: %s", query)
	}
	assertCatalogConsumed(t, backend)

	reorged, reorgBackend := openCatalog(t,
		catalogQueryStep{contains: "SELECT EXISTS", rows: catalogRows(1, []driver.Value{false})},
	)
	_, err = reorged.AddressInternalTransactions(context.Background(), AddressActivityRequest{
		ChainID: "1", Address: subject, Cursor: page.NextCursor, Limit: 2,
	})
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("reorg cursor error=%v", err)
	}
	assertCatalogConsumed(t, reorgBackend)
}

func TestAddressNFTTransfersMergeERC721AndERC1155(t *testing.T) {
	subject := "0x" + strings.Repeat("11", 20)
	catalog, backend := openCatalog(t,
		snapshotStep("100", bytesOf(0xaa, 32)),
		stageStep("complete"),
		catalogQueryStep{
			contains: "FROM candidates",
			rows: catalogRows(15,
				tokenActivityRow("100", "2", "9", "0", "erc721", "transfer", "42", nil, 0x41),
				tokenActivityRow("100", "2", "8", "1", "erc1155", "mint", "7", "340282366920938463463374607431768211455", 0x42),
			),
			check: func(arguments []driver.NamedValue) error {
				if len(arguments) != 12 || arguments[3].Value != "nft" ||
					arguments[4].Value != false || arguments[11].Value != int64(3) {
					return fmt.Errorf("unexpected NFT arguments: %v", arguments)
				}
				return nil
			},
		},
	)
	page, err := catalog.AddressNFTTransfers(context.Background(), AddressActivityRequest{
		ChainID: "1", Address: subject, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.NextCursor != "" ||
		page.Items[0].Standard != "erc721" || page.Items[1].Standard != "erc1155" ||
		page.Items[1].Amount == nil || *page.Items[1].Amount != "340282366920938463463374607431768211455" {
		t.Fatalf("page=%+v", page)
	}
	backend.mu.Lock()
	query := backend.queries[len(backend.queries)-1]
	backend.mu.Unlock()
	if !strings.Contains(query, "standard IN ('erc721', 'erc1155')") ||
		!strings.Contains(query, "ORDER BY event.block_number DESC") {
		t.Fatalf("NFT query does not merge/sort standards: %s", query)
	}
	assertCatalogConsumed(t, backend)
}

func TestAddressActivityStageFailureIsNotAnEmptyPage(t *testing.T) {
	catalog, backend := openCatalog(t,
		snapshotStep("100", bytesOf(0xaa, 32)),
		stageStep("failed"),
	)
	_, err := catalog.AddressERC20Transfers(context.Background(), AddressActivityRequest{
		ChainID: "1", Address: "0x" + strings.Repeat("11", 20), Limit: 2,
	})
	var unavailable StageUnavailableError
	if !errors.As(err, &unavailable) || unavailable.Stage != StageToken ||
		unavailable.State != StageFailed {
		t.Fatalf("stage error=%T %+v", err, err)
	}
	assertCatalogConsumed(t, backend)
}

func internalActivityRow(
	blockNumber string,
	transactionIndex string,
	path string,
	hashByte byte,
) []driver.Value {
	depth := int64(len(strings.Split(path, ".")))
	return []driver.Value{
		blockNumber, bytesOf(hashByte, 32), "1700000000",
		bytesOf(hashByte+1, 32), transactionIndex, path, depth, "create",
		bytesOf(0x11, 20), nil, bytesOf(0x22, 20),
		"115792089237316195423570985008687907853269984665640564039457584007913129639935",
		"21000", "20000", []byte{0xde, 0xad}, nil, false,
	}
}

func tokenActivityRow(
	blockNumber string,
	transactionIndex string,
	logIndex string,
	subIndex string,
	standard string,
	kind string,
	tokenID string,
	amount any,
	hashByte byte,
) []driver.Value {
	return []driver.Value{
		blockNumber, bytesOf(hashByte, 32), "1700000000",
		bytesOf(hashByte+1, 32), transactionIndex, logIndex, subIndex,
		bytesOf(0x44, 20), standard, kind,
		bytesOf(0x11, 20), bytesOf(0x22, 20), tokenID, amount, "high",
	}
}
