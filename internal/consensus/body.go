package consensus

import (
	"encoding/json"

	"github.com/attestantio/go-eth2-client/spec/bellatrix"
	"github.com/attestantio/go-eth2-client/spec/capella"
	"github.com/attestantio/go-eth2-client/spec/deneb"
	"github.com/attestantio/go-eth2-client/spec/electra"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/ethereum/go-ethereum/common"
	dynssz "github.com/pk910/dynamic-ssz"
)

// Only SSZ shape parameters are passed to the decoder. Fork schedules and
// unrelated spec entries do not become part of the immutable network identity.
var bodySpecKeys = []string{
	"MAX_PROPOSER_SLASHINGS", "MAX_ATTESTER_SLASHINGS", "MAX_ATTESTATIONS",
	"MAX_DEPOSITS", "MAX_VOLUNTARY_EXITS", "SYNC_COMMITTEE_SIZE",
	"MAX_VALIDATORS_PER_COMMITTEE", "MAX_COMMITTEES_PER_SLOT",
	"DEPOSIT_CONTRACT_TREE_DEPTH", "MAX_TRANSACTIONS_PER_PAYLOAD",
	"MAX_BYTES_PER_TRANSACTION", "MAX_EXTRA_DATA_BYTES", "MAX_WITHDRAWALS_PER_PAYLOAD",
	"MAX_BLS_TO_EXECUTION_CHANGES", "MAX_BLOB_COMMITMENTS_PER_BLOCK",
	"MAX_ATTESTER_SLASHINGS_ELECTRA", "MAX_ATTESTATIONS_ELECTRA",
	"MAX_DEPOSIT_REQUESTS_PER_PAYLOAD", "MAX_WITHDRAWAL_REQUESTS_PER_PAYLOAD",
	"MAX_CONSOLIDATION_REQUESTS_PER_PAYLOAD", "PTC_SIZE",
}

func bodySpec(values map[string]json.RawMessage) (map[string]any, error) {
	spec := make(map[string]any)
	for _, key := range bodySpecKeys {
		raw, exists := values[key]
		if !exists {
			continue // Standard mainnet limits are declared by the protocol types.
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return nil, failure("beacon_spec")
		}
		n, err := quantity(value)
		if err != nil || n == 0 || n > 1<<30 {
			return nil, failure("beacon_spec")
		}
		spec[key] = n
	}
	return spec, nil
}

func verifyBody(raw json.RawMessage, fork string, expected common.Hash, spec map[string]any) (err error) {
	// Keep malformed upstream structures inside the optional capability boundary,
	// including a panic in a third-party protocol decoder.
	defer func() {
		if recover() != nil {
			err = failure("beacon_body_invalid")
		}
	}()
	var body any
	switch fork {
	case "bellatrix":
		body = new(bellatrix.BeaconBlockBody)
	case "capella":
		body = new(capella.BeaconBlockBody)
	case "deneb":
		body = new(deneb.BeaconBlockBody)
	case "electra", "fulu":
		body = new(electra.BeaconBlockBody)
	case "gloas":
		body = new(gloas.BeaconBlockBody)
	default:
		return failure("unsupported_fork")
	}
	if json.Unmarshal(raw, body) != nil {
		return failure("beacon_body_invalid")
	}
	root, err := dynssz.NewDynSsz(spec, dynssz.WithNoFastHash()).HashTreeRoot(body)
	if err != nil {
		return failure("beacon_body_invalid")
	}
	if common.Hash(root) != expected {
		return failure("beacon_block_identity")
	}
	return nil
}
