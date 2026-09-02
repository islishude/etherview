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
		relations = []string{
			"contract_code_observations",
			"proxy_observations",
			"beacon_implementation_observations",
			"uups_implementation_observations",
			"proxy_detection_evidence",
			"proxy_upgrade_events",
			"proxy_initialization_events",
			"diamond_loupe_snapshots",
			"diamond_cut_events",
		}
	case ABIStage:
		relations = []string{
			"contract_abis", "abi_decodings",
			"transaction_effective_execution_identities",
		}
	case TokenStage:
		relations = []string{"token_events", "token_balance_deltas"}
	case StatsStage:
		relations = []string{"block_statistics"}
	case TraceStage:
		// Only the normalized call tree and exact receipt-log attribution are
		// persisted by TraceStage. Opcode and raw traces remain outside this
		// journal contract.
		relations = []string{"normalized_traces", "trace_log_attributions"}
	case StateDiffStage:
		relations = []string{
			"transaction_state_changes",
			"eip7702_authorizations",
			"transaction_execution_code_resolutions",
		}
	case UserOperationStage:
		relations = []string{
			"erc4337_user_operations",
			"erc4337_user_operation_events",
			"erc4337_user_operation_participants",
		}
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
