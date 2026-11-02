package facilitator

import (
	"bytes"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"testing"
)

func TestJournalProgressRequiresBroadcastEvidence(t *testing.T) {
	for _, test := range []struct {
		state, broadcast string
		hash             []byte
		want             SettlementProgressStage
	}{
		{"reserved", "unsubmitted", nil, Submitting},
		{"signed", "unknown", bytes.Repeat([]byte{1}, 32), Unknown},
		{"signed", "confirmed", bytes.Repeat([]byte{1}, 32), Confirming},
		{"succeeded", "unknown", bytes.Repeat([]byte{1}, 32), Confirming},
	} {
		got := journalProgress(dbgen.FacilitatorSettlement{State: test.state, BroadcastStatus: test.broadcast, TransactionHash: test.hash})
		if got.Stage != test.want {
			t.Fatalf("%s/%s: %+v", test.state, test.broadcast, got)
		}
	}
}
