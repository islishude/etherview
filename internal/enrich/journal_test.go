package enrich

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDerivedJournalPayloadIsStableAndControlled(t *testing.T) {
	t.Parallel()
	tests := []struct {
		stage     StageID
		relations []string
	}{
		{stage: ProxyStage, relations: []string{"contract_code_observations", "proxy_observations"}},
		{stage: ABIStage, relations: []string{"contract_abis", "abi_decodings"}},
		{stage: TokenStage, relations: []string{"token_events", "token_balance_deltas"}},
		{stage: StatsStage, relations: []string{"block_statistics"}},
		{stage: TraceStage, relations: []string{"normalized_traces"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.stage.String(), func(t *testing.T) {
			t.Parallel()
			first, err := encodeDerivedJournal(test.stage)
			if err != nil {
				t.Fatal(err)
			}
			second, err := encodeDerivedJournal(test.stage)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(first, second) {
				t.Fatalf("journal encoding is not deterministic:\n%s\n%s", first, second)
			}
			var payload derivedJournalPayload
			if err := json.Unmarshal(first, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Schema != derivedJournalSchema || payload.Version != derivedJournalVersion || payload.Stage != test.stage.String() {
				t.Fatalf("payload identity = %+v", payload)
			}
			if payload.Rollback.Operation != "set_canonical" || payload.Rollback.Canonical ||
				payload.Replay.Operation != "set_canonical" || !payload.Replay.Canonical ||
				!slicesEqual(payload.Rollback.Relations, test.relations) || !slicesEqual(payload.Replay.Relations, test.relations) {
				t.Fatalf("payload transitions = %+v", payload)
			}
			for _, forbidden := range []string{"opcode", "raw_trace", "details", "rpc", "calldata"} {
				if bytes.Contains(bytes.ToLower(first), []byte(forbidden)) {
					t.Fatalf("journal contains forbidden untrusted/raw field %q: %s", forbidden, first)
				}
			}
		})
	}
	if _, err := encodeDerivedJournal(StageID{Name: "future", Version: 1}); err == nil {
		t.Fatal("unregistered stage journal unexpectedly succeeded")
	}
	if !strings.Contains(upsertDerivedJournalSQL, "number = $6::numeric") ||
		!strings.Contains(upsertDerivedJournalSQL, "block_hash = $2") ||
		!strings.Contains(upsertDerivedJournalSQL, "canonical = EXCLUDED.canonical") {
		t.Fatalf("journal upsert does not derive and refresh exact canonical identity:\n%s", upsertDerivedJournalSQL)
	}
}

func TestStatsJournalFailureRollsBackStageTransaction(t *testing.T) {
	t.Parallel()
	job := Job{ID: "journal-failure", Stage: StatsStage, ChainID: "1", BlockHash: uintWord(701), BlockNumber: 7}
	raw := []byte(fmt.Sprintf(`{"number":"0x7","hash":%q,"timestamp":"0x64","gasUsed":"0x5208","gasLimit":"0x1c9c380"}`, job.BlockHash.String()))
	journalFailure := errors.New("journal trigger rejected write")
	var derivedWrites, stageWrites, journalWrites, commits, rollbacks atomic.Int64
	backend := &fakeSQLBackend{
		query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
			switch {
			case strings.Contains(query, "FOR KEY SHARE"):
				return &fakeSQLRows{columns: []string{"one"}, values: [][]driver.Value{{int64(1)}}}, nil
			case strings.Contains(query, "GROUP BY block.raw"):
				return &fakeSQLRows{
					columns: []string{"raw", "count", "configured_start", "parent_number", "parent_timestamp", "canonical_parent"},
					values:  [][]driver.Value{{raw, int64(1), "0", "6", "99", true}},
				}, nil
			case strings.Contains(query, "FROM receipts AS receipt"):
				return &fakeSQLRows{columns: []string{"raw"}}, nil
			default:
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
		},
		exec: func(query string, arguments []driver.NamedValue) (driver.Result, error) {
			switch {
			case strings.Contains(query, "INSERT INTO block_statistics"):
				derivedWrites.Add(1)
			case strings.Contains(query, "INSERT INTO block_stage_results"):
				stageWrites.Add(1)
			case strings.Contains(query, "INSERT INTO block_journals"):
				journalWrites.Add(1)
				if arguments[2].Value != StatsStage.String() || arguments[3].Value != derivedJournalSequence {
					t.Errorf("journal arguments = %+v", arguments)
				}
				return nil, journalFailure
			default:
				return nil, fmt.Errorf("unexpected exec: %s", query)
			}
			return driver.RowsAffected(1), nil
		},
		commit: func() error {
			commits.Add(1)
			return nil
		},
		rollback: func() error {
			rollbacks.Add(1)
			return nil
		},
	}
	processor, err := NewPostgresStatsProcessor(openFakeSQLDB(t, backend))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Process(context.Background(), job); !errors.Is(err, journalFailure) {
		t.Fatalf("Process error = %v, want journal failure", err)
	}
	if derivedWrites.Load() != 1 || stageWrites.Load() != 1 || journalWrites.Load() != 1 || commits.Load() != 0 || rollbacks.Load() != 1 {
		t.Fatalf("derived=%d stage=%d journal=%d commits=%d rollbacks=%d",
			derivedWrites.Load(), stageWrites.Load(), journalWrites.Load(), commits.Load(), rollbacks.Load())
	}
}

func slicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
