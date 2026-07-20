package enrich

import (
	"encoding/json"
	"fmt"
)

const (
	derivedJournalSchema   = "etherview.derived-canonicality"
	derivedJournalVersion  = 1
	derivedJournalSequence = int64(1)
)

// derivedJournalPayload is deliberately a small, controlled description of
// how canonicality changes for block-local normalized output. It is not an
// executable command and never contains RPC, log, trace, or result-detail
// input.
type derivedJournalPayload struct {
	Schema   string                   `json:"schema"`
	Version  int                      `json:"version"`
	Stage    string                   `json:"stage"`
	Rollback derivedJournalTransition `json:"rollback"`
	Replay   derivedJournalTransition `json:"replay"`
}

type derivedJournalTransition struct {
	Operation string   `json:"operation"`
	Canonical bool     `json:"canonical"`
	Relations []string `json:"relations"`
}

func encodeDerivedJournal(stage StageID) ([]byte, error) {
	var relations []string
	switch stage {
	case ProxyStage:
		relations = []string{"contract_code_observations", "proxy_observations"}
	case ABIStage:
		relations = []string{"contract_abis", "abi_decodings"}
	case TokenStage:
		relations = []string{"token_events", "token_balance_deltas"}
	case StatsStage:
		relations = []string{"block_statistics"}
	case TraceStage:
		// Only the normalized call tree is persisted by TraceStage. Opcode and
		// raw traces are intentionally outside this journal contract.
		relations = []string{"normalized_traces"}
	default:
		return nil, fmt.Errorf("stage %s has no derived journal contract", stage)
	}
	payload := derivedJournalPayload{
		Schema:  derivedJournalSchema,
		Version: derivedJournalVersion,
		Stage:   stage.String(),
		Rollback: derivedJournalTransition{
			Operation: "set_canonical", Canonical: false, Relations: relations,
		},
		Replay: derivedJournalTransition{
			Operation: "set_canonical", Canonical: true, Relations: relations,
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode derived journal: %w", err)
	}
	return encoded, nil
}

const upsertDerivedJournalSQL = `
INSERT INTO block_journals AS current (
    chain_id, block_hash, stage, sequence, payload, canonical
)
SELECT $1::numeric, $2, $3, $4::numeric, $5::jsonb,
       EXISTS (
           SELECT 1
           FROM canonical_blocks
           WHERE chain_id = $1::numeric
             AND number = $6::numeric
             AND block_hash = $2
       )
ON CONFLICT (chain_id, block_hash, stage, sequence) DO UPDATE SET
    payload = EXCLUDED.payload,
    canonical = EXCLUDED.canonical
WHERE current.durable_job_id IS NULL
  AND current.job_generation IS NULL`
