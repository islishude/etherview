package facilitator

import (
	"bytes"
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

type cursorJournal struct {
	settlementJournal
	cursor []byte
	writes int
}

func (j *cursorJournal) FacilitatorScanCursor(_ context.Context, _ string, cursor []byte) (int64, error) {
	j.cursor = bytes.Clone(cursor)
	j.writes++
	return 1, nil
}

func TestEvidenceScanBoundsInterruptionAndReorg(t *testing.T) {
	in := proofRequest(t, "eip3009")
	proof, err := proofFor(in)
	if err != nil {
		t.Fatal(err)
	}
	head := uint64(5000)
	branch := byte(0)
	unavailable := false
	var ranges [][2]uint64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     json.RawMessage   `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("invalid RPC")
			return
		}
		result := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "eth_getBlockByNumber":
			var number string
			_ = json.Unmarshal(request.Params[0], &number)
			height := head
			if number != "latest" {
				var e error
				height, e = hexutil.DecodeUint64(number)
				if e != nil {
					t.Error(e)
				}
			}
			result["result"] = &types.Header{Number: new(big.Int).SetUint64(height), Difficulty: big.NewInt(0), GasLimit: 30000000, Extra: []byte{branch}}
		case "eth_getLogs":
			var query struct {
				From string `json:"fromBlock"`
				To   string `json:"toBlock"`
			}
			_ = json.Unmarshal(request.Params[0], &query)
			from, _ := hexutil.DecodeUint64(query.From)
			to, _ := hexutil.DecodeUint64(query.To)
			ranges = append(ranges, [2]uint64{from, to})
			if to < from || to-from >= 2048 {
				t.Error("unbounded scan", from, to)
			}
			if unavailable || to-from >= 1024 {
				result["error"] = map[string]any{"code": -32005, "message": "range unavailable"}
			} else {
				result["result"] = []types.Log{}
			}
		default:
			t.Error("unexpected RPC", request.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()
	client, err := dialRPC(t.Context(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	journal := &cursorJournal{}
	service := &Service{queries: journal}
	ch := &chainRuntime{adapter: &chainClient{client: client, config: ChainConfig{Confirmations: 1}}}
	row := dbgen.FacilitatorSettlement{AuthorizationID: "scan"}
	if e, err := service.scanExternal(t.Context(), ch, row, in, proof); err != nil || e != nil {
		t.Fatal(e, err)
	}
	var cursor scanCursor
	_ = json.Unmarshal(journal.cursor, &cursor)
	if cursor.Back != 3976 || cursor.Forward != 5001 || len(ranges) != 2 {
		t.Fatal("oversized page skipped coverage", cursor, ranges)
	}
	row.ScanCursor = bytes.Clone(journal.cursor)
	unavailable = true
	if _, err := service.scanExternal(t.Context(), ch, row, in, proof); err == nil || journal.writes != 3 {
		t.Fatal("failed page did not retain scheduling checkpoint", err, journal.writes)
	}
	var interrupted scanCursor
	_ = json.Unmarshal(journal.cursor, &interrupted)
	if interrupted.Back != cursor.Back || interrupted.Forward != cursor.Forward {
		t.Fatal("failed page advanced coverage", cursor, interrupted)
	}
	unavailable = false
	head = 5010
	ranges = nil
	if _, err := service.scanExternal(t.Context(), ch, row, in, proof); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(journal.cursor, &cursor)
	if cursor.Back != 3976 || cursor.Forward != 5011 || len(ranges) != 1 || ranges[0] != [2]uint64{5001, 5010} {
		t.Fatal("forward gap", cursor, ranges)
	}
	// An unavailable historical page preserves coverage but yields the next
	// attempt to forward work. Otherwise a dense single block could starve it.
	row.ScanCursor = bytes.Clone(journal.cursor)
	unavailable = true
	head += 10
	if _, err := service.scanExternal(t.Context(), ch, row, in, proof); err == nil {
		t.Fatal("unavailable history accepted")
	}
	_ = json.Unmarshal(journal.cursor, &interrupted)
	if interrupted.Back != cursor.Back || interrupted.Forward != cursor.Forward || interrupted.BackNext {
		t.Fatal("unavailable history lost coverage or next direction", cursor, interrupted)
	}
	row.ScanCursor = bytes.Clone(journal.cursor)
	unavailable = false
	head += 10
	if _, err := service.scanExternal(t.Context(), ch, row, in, proof); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(journal.cursor, &cursor)
	if cursor.Back != interrupted.Back || cursor.Forward != head+1 {
		t.Fatal("unavailable history starved forward scan", cursor)
	}
	// Persisted direction survives a service restart while every round sees new
	// confirmed blocks. Both history and the forward cursor must make progress.
	for round := range 4 {
		row.ScanCursor = bytes.Clone(journal.cursor)
		before := cursor
		head += 10
		ranges = nil
		restarted := &Service{queries: journal}
		if _, err := restarted.scanExternal(t.Context(), ch, row, in, proof); err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(journal.cursor, &cursor)
		if round%2 == 0 {
			if cursor.Back >= before.Back || cursor.Forward != before.Forward || ranges[0][1] != uint64(before.Back) {
				t.Fatal("continuous head growth starved history", before, cursor, ranges)
			}
		} else if cursor.Forward <= before.Forward || cursor.Back != before.Back || ranges[0][0] != before.Forward {
			t.Fatal("historical scan starved forward progress", before, cursor, ranges)
		}
	}
	row.ScanCursor = bytes.Clone(journal.cursor)
	branch = 1
	ranges = nil
	if _, err := service.scanExternal(t.Context(), ch, row, in, proof); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(journal.cursor, &cursor)
	if cursor.Back != int64(head)-1024 || len(ranges) != 2 || ranges[0][1] != head {
		t.Fatal("reorg did not rewind coverage", cursor, ranges)
	}
}
