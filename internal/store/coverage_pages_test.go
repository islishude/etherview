package store

import (
	"fmt"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestCoverageRangeScanReadsAllKeysetPages(t *testing.T) {
	first := make([][]any, coverageReadPageSize)
	for index := range first {
		first[index] = []any{fmt.Sprint(index * 2), fmt.Sprint(index * 2)}
	}
	database, _ := refreshDatabase(t,
		refreshStep{kind: "query", contains: "LIMIT $4::integer", columns: 2, rows: first, check: func(args []any) error {
			if args[0] != "1" || args[1] != false || args[2] != "0" || args[3] != int32(coverageReadPageSize) {
				return fmt.Errorf("first page args: %v", args)
			}
			return nil
		}},
		refreshStep{kind: "query", contains: "range_start > $3::text::numeric", columns: 2, rows: [][]any{{"1024", "1024"}}, check: func(args []any) error {
			if args[1] != true || args[2] != "1022" || args[3] != int32(coverageReadPageSize) {
				return fmt.Errorf("next page args: %v", args)
			}
			return nil
		}},
	)
	ranges, err := queryCoverageRangesTx(t.Context(), database, "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 513 || ranges[0].Start != 0 || ranges[512].End != 1024 {
		t.Fatalf("range scan lost a page: count=%d", len(ranges))
	}
}

func TestCanonicalReferenceScanReadsAllKeysetPages(t *testing.T) {
	first := make([][]any, coverageReadPageSize)
	for index := range first {
		first[index] = []any{fmt.Sprint(index), common.HexToHash(fmt.Sprintf("%064x", index+1)).Bytes(), common.Hash{}.Bytes()}
	}
	database, _ := refreshDatabase(t,
		refreshStep{kind: "query", contains: "LIMIT $5::integer", columns: 3, rows: first, check: func(args []any) error {
			if args[0] != "1" || args[1] != "0" || args[2] != false || args[3] != "0" || args[4] != int32(coverageReadPageSize) {
				return fmt.Errorf("first page args: %v", args)
			}
			return nil
		}},
		refreshStep{kind: "query", contains: "cb.number > $4::text::numeric", columns: 3, rows: [][]any{{"512", common.HexToHash("0x201").Bytes(), common.Hash{}.Bytes()}}, check: func(args []any) error {
			if args[2] != true || args[3] != "511" || args[4] != int32(coverageReadPageSize) {
				return fmt.Errorf("next page args: %v", args)
			}
			return nil
		}},
	)
	references, err := queryCanonicalReferencesTx(t.Context(), database, "1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 513 || references[0].Number != 0 || references[512].Number != 512 {
		t.Fatalf("canonical scan lost a page: count=%d", len(references))
	}
}
