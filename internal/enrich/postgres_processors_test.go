package enrich

import (
	"context"
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"testing"
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
		query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
			mu.Lock()
			defer mu.Unlock()
			queryCount++
			switch {
			case strings.Contains(query, "FROM canonical_blocks"):
				return &fakeSQLRows{columns: []string{"one"}, values: [][]driver.Value{{int64(1)}}}, nil
			case strings.Contains(query, "FROM logs"):
				return &fakeSQLRows{
					columns: []string{"log_index", "tx_hash", "address", "raw"},
					values:  [][]driver.Value{{int64(0), transactionHash[:], contract[:], []byte(raw)}},
				}, nil
			case strings.Contains(query, "FROM token_contracts"):
				return &fakeSQLRows{columns: []string{"standard", "confidence"}}, nil
			default:
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
		},
		exec: func(query string, arguments []driver.NamedValue) (driver.Result, error) {
			mu.Lock()
			defer mu.Unlock()
			switch {
			case strings.Contains(query, "INSERT INTO token_events"):
				eventConfidence = arguments[14].Value.(string)
			case strings.Contains(query, "INSERT INTO token_balance_deltas"):
				owner := hex.EncodeToString(arguments[6].Value.([]byte))
				deltas[owner] = arguments[8].Value.(string)
			case strings.Contains(query, "INSERT INTO block_stage_results"):
				stageWritten = true
			case strings.Contains(query, "INSERT INTO block_journals"):
				journalWritten = true
			default:
				return nil, fmt.Errorf("unexpected exec: %s", query)
			}
			return driver.RowsAffected(1), nil
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
		query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
			if !strings.Contains(query, "FROM canonical_blocks") {
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
			return &fakeSQLRows{columns: []string{"one"}}, nil
		},
		exec: func(query string, _ []driver.NamedValue) (driver.Result, error) {
			switch {
			case strings.Contains(query, "INSERT INTO block_stage_results"):
				stageWritten = true
			case strings.Contains(query, "INSERT INTO block_journals"):
				journalWritten = true
			default:
				return nil, fmt.Errorf("unexpected exec: %s", query)
			}
			return driver.RowsAffected(1), nil
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
