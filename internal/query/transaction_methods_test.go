package query

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enrich"
)

func TestTransactionMethodProjectionUsesPublishedTransactionScopedEffectiveIdentity(t *testing.T) {
	t.Parallel()
	for _, fragment := range []string{
		"transaction_effective_execution_identities AS effective",
		"effective.transaction_hash = inclusion.tx_hash",
		"effective.transaction_index = inclusion.tx_index",
		"effective.context_address",
		"stage_version = 4",
		"NOT EXISTS (",
		"published_block_stage_results AS published_abi",
	} {
		if !strings.Contains(dbgen.QueryListTransactionsWithMethod, fragment) {
			t.Fatalf("transaction Method projection missing %q", fragment)
		}
	}
}

func TestDecodeTransactionSelectorRequiresUniqueExactCalldataMatch(t *testing.T) {
	t.Parallel()
	indexed, err := enrich.NormalizeVerifiedFunctionSelectors([]byte(`[
      {"type":"function","name":"burn","inputs":[{"name":"value","type":"uint256"}]},
      {"type":"function","name":"collate_propagate_storage","inputs":[{"name":"value","type":"bytes16"}]}
    ]`))
	if err != nil || len(indexed) != 2 {
		t.Fatalf("indexed=%+v error=%v", indexed, err)
	}
	calldata := append(indexed[0].Selector[:], make([]byte, 32)...)
	record := transactionRecord{
		Model: gen.Transaction{Input: "0x" + hex.EncodeToString(calldata)},
	}
	candidates := make([]transactionSelectorCandidate, 0, len(indexed))
	for _, entry := range indexed {
		candidates = append(candidates, transactionSelectorCandidate{
			sourceCodeHash: common.HexToHash("0x5678"), abiEntry: entry.ABIEntry,
			signature: entry.Signature, priority: 1,
		})
	}
	if decoded, ambiguous := decodeTransactionSelector(&record, candidates, 0); decoded.Valid || !ambiguous {
		t.Fatalf("colliding exact candidates decoded as %q", decoded.String)
	}
	if decoded, ambiguous := decodeTransactionSelector(&record, candidates[:1], 0); !decoded.Valid || ambiguous ||
		decoded.String != indexed[0].Signature {
		t.Fatalf("unique exact candidate = %+v", decoded)
	}
	record.Model.Input += hex.EncodeToString(make([]byte, 32))
	if decoded, ambiguous := decodeTransactionSelector(&record, candidates[:1], 0); decoded.Valid || ambiguous {
		t.Fatalf("candidate accepted trailing calldata as %q", decoded.String)
	}
}
