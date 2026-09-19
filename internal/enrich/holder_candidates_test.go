package enrich

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/testpgx"
	"github.com/jackc/pgx/v5"
)

func TestHolderCandidatePagesDeduplicateAndKeepSnapshot(t *testing.T) {
	t.Parallel()
	for _, full := range []bool{true, false} {
		t.Run(fmt.Sprint(full), func(t *testing.T) {
			pages := 0
			committed := false
			hash := uintWord(100)
			first, last := testAddress(1), testAddress(2)
			backend := &fakeSQLBackend{
				beginOptions: func(options pgx.TxOptions) {
					if options.IsoLevel != pgx.RepeatableRead || options.AccessMode != pgx.ReadOnly {
						t.Fatalf("candidate pages lost snapshot: %+v", options)
					}
				},
				commit: func() error { committed = true; return nil },
				query: func(statement string, args []any) (pgx.Rows, error) {
					if committed {
						return nil, fmt.Errorf("queried after snapshot was closed")
					}
					name := "HolderTouchedCandidates"
					cursorIndex := 4
					if full {
						name = "HolderCandidates"
						cursorIndex = 3
					}
					if !strings.HasPrefix(statement, "-- name: "+name) {
						return nil, fmt.Errorf("unexpected candidate query")
					}
					pages++
					rows := &testpgx.Rows{ColumnNames: []string{"block_number", "log_index", "sub_index", "block_hash", "from_address", "to_address"}}
					if args[len(args)-1] != int32(512) {
						return nil, fmt.Errorf("unbounded page: %v", args)
					}
					switch pages {
					case 1:
						if args[cursorIndex] != false {
							return nil, fmt.Errorf("first page has cursor")
						}
						for index := 512; index > 0; index-- {
							rows.ValuesList = append(rows.ValuesList, []any{"100", int64(index), int32(0), hash[:], common.Address{}.Bytes(), first[:]})
						}
					case 2:
						if args[cursorIndex] != true || args[cursorIndex+1] != "100" || args[cursorIndex+2] != int64(1) {
							return nil, fmt.Errorf("wrong keyset cursor: %v", args)
						}
						rows.ValuesList = [][]any{{"100", int64(0), int32(0), hash[:], first[:], last[:]}}
					default:
						return nil, fmt.Errorf("unexpected third page")
					}
					return rows, nil
				},
			}
			processor := &PostgresHolderProcessor{db: openFakeSQLDB(t, backend)}
			holders, err := processor.readHolderCandidates(t.Context(), Job{ChainID: "1", BlockNumber: 100, BlockHash: hash}, testAddress(3), full)
			if err != nil {
				t.Fatal(err)
			}
			if pages != 2 || !committed || len(holders) != 2 || holders[0] != first || holders[1] != last {
				t.Fatalf("candidate scan truncated or duplicated: pages=%d committed=%t holders=%v", pages, committed, holders)
			}
		})
	}
}
