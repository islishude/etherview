package enrich

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"testing"

	dbaccess "github.com/islishude/etherview/internal/db"
	testpgx "github.com/islishude/etherview/internal/testpgx"
	pgx "github.com/jackc/pgx/v5"
	pgconn "github.com/jackc/pgx/v5/pgconn"
	pgtype "github.com/jackc/pgx/v5/pgtype"
)

func TestPostgresTokenProcessorPersistsGuessAndBalancedDeltas(t *testing.T) {
	t.Parallel()
	job := Job{ID: "7", Stage: TokenStage, ChainID: "1", BlockHash: uintWord(700), BlockNumber: 7}
	from, to, contract := testAddress(1), testAddress(2), testAddress(3)
	transactionHash := uintWord(701)
	raw := fmt.Sprintf(`{
		"removed":false,"logIndex":"0x0","transactionIndex":"0x0",
		"transactionHash":%q,"blockHash":%q,"blockNumber":"0x7",
		"address":%q,"data":%q,"topics":[%q,%q,%q]
	}`,
		transactionHash.String(), job.BlockHash.String(), contract.String(),
		"0x"+strings.Repeat("0", 63)+"5", topicTransfer.String(), addressWord(from).String(), addressWord(to).String(),
	)
	var mu sync.Mutex
	queryCount := 0
	var eventConfidence string
	deltas := make(map[string]string)
	stageWritten, journalWritten := false, false
	backend := &fakeSQLBackend{
		query: func(query string, _ []any) (pgx.Rows, error) {
			mu.Lock()
			defer mu.Unlock()
			queryCount++
			switch {
			case strings.Contains(query, "FROM canonical_blocks"):
				return &testpgx.Rows{ColumnNames: []string{"one"}, ValuesList: [][]any{{int64(1)}}}, nil
			case strings.Contains(query, "FROM logs"):
				return &testpgx.Rows{
					ColumnNames: []string{"log_index", "tx_hash", "address", "raw"},
					ValuesList:  [][]any{{int64(0), transactionHash[:], contract[:], []byte(raw)}},
				}, nil
			case strings.Contains(query, "FROM token_contracts"):
				return &testpgx.Rows{ColumnNames: []string{"standard", "confidence"}}, nil
			default:
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
		},
		exec: func(query string, arguments []any) (pgconn.CommandTag, error) {
			mu.Lock()
			defer mu.Unlock()
			switch {
			case strings.Contains(query, "INSERT INTO token_events"):
				eventConfidence = arguments[14].(string)
			case strings.Contains(query, "INSERT INTO token_balance_deltas"):
				owner := hex.EncodeToString(arguments[6].([]byte))
				stored, err := dbaccess.NumericText(arguments[8].(pgtype.Numeric))
				if err != nil {
					return pgconn.CommandTag{}, err
				}
				deltas[owner] = stored.String
			case strings.Contains(query, "INSERT INTO block_stage_results"):
				stageWritten = true
			case strings.Contains(query, "INSERT INTO block_journals"):
				journalWritten = true
			default:
				return pgconn.CommandTag{}, fmt.Errorf("unexpected exec: %s", query)
			}
			return testpgx.Affected(1), nil
		},
	}
	processor, err := NewPostgresTokenProcessor(openFakeSQLDB(t, backend))
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.Process(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != ResultComplete || result.Details["events"] != "1" || queryCount != 3 || !stageWritten || !journalWritten {
		t.Fatalf("result=%+v queries=%d stage=%v journal=%v", result, queryCount, stageWritten, journalWritten)
	}
	if eventConfidence != string(ConfidenceGuess) || deltas[hex.EncodeToString(from[:])] != "-5" || deltas[hex.EncodeToString(to[:])] != "5" {
		t.Fatalf("confidence=%q deltas=%v", eventConfidence, deltas)
	}
}

func TestPostgresTokenProcessorSkipsStaleCanonicalJob(t *testing.T) {
	t.Parallel()
	job := Job{ID: "9", Stage: TokenStage, ChainID: "1", BlockHash: uintWord(900), BlockNumber: 9}
	stageWritten, journalWritten := false, false
	backend := &fakeSQLBackend{
		query: func(query string, _ []any) (pgx.Rows, error) {
			if !strings.Contains(query, "FROM canonical_blocks") {
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
			return &testpgx.Rows{ColumnNames: []string{"one"}}, nil
		},
		exec: func(query string, _ []any) (pgconn.CommandTag, error) {
			switch {
			case strings.Contains(query, "INSERT INTO block_stage_results"):
				stageWritten = true
			case strings.Contains(query, "INSERT INTO block_journals"):
				journalWritten = true
			default:
				return pgconn.CommandTag{}, fmt.Errorf("unexpected exec: %s", query)
			}
			return testpgx.Affected(1), nil
		},
	}
	processor, _ := NewPostgresTokenProcessor(openFakeSQLDB(t, backend))
	result, err := processor.Process(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if result.Details["outcome"] != "stale_canonical_skipped" || !stageWritten || !journalWritten {
		t.Fatalf("result=%+v stage=%v journal=%v", result, stageWritten, journalWritten)
	}
}
