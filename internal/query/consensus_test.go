package query

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/config"
	"testing"
)

func TestConsensusSlotPrecedenceAndDisabledFallback(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			var expectations []queryExpectation
			if enabled {
				expectations = []queryExpectation{{contains: "FROM consensus_observations", columns: columns(2), rows: [][]any{{common.Hash{1}.Bytes(), "9007199254740993"}, {common.Hash{2}.Bytes(), "41"}}}}
			}
			db := testDatabase(t, expectations...)
			cfg := config.DefaultConsensusConfig()
			cfg.Enabled = enabled
			r := testReader(t, db, Options{ChainID: 1, Consensus: cfg})
			execution := gen.Execution
			original := "40"
			blocks := []gen.Block{{Hash: common.Hash{1}.Hex()}, {Hash: common.Hash{2}.Hex(), SlotNumber: &original, SlotNumberSource: &execution}, {Hash: common.Hash{3}.Hex(), SlotNumber: &original, SlotNumberSource: &execution}, {Hash: common.Hash{4}.Hex()}}
			if err := r.consensusSlots(t.Context(), db, blocks); err != nil {
				t.Fatal(err)
			}
			if enabled {
				if blocks[0].SlotNumber == nil || *blocks[0].SlotNumber != "9007199254740993" || *blocks[0].SlotNumberSource != gen.Consensus || *blocks[1].SlotNumber != "41" {
					t.Fatalf("precedence=%+v", blocks)
				}
			} else if blocks[0].SlotNumber != nil || *blocks[1].SlotNumber != "40" {
				t.Fatalf("disabled=%+v", blocks)
			}
			if *blocks[2].SlotNumber != "40" || *blocks[2].SlotNumberSource != gen.Execution || blocks[3].SlotNumber != nil || blocks[3].SlotNumberSource != nil {
				t.Fatalf("fallback=%+v", blocks)
			}
		})
	}
}
