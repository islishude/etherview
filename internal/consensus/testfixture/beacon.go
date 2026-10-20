// Package testfixture builds complete Beacon REST blocks with authenticated roots.
package testfixture

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/attestantio/go-eth2-client/spec/bellatrix"
	"github.com/attestantio/go-eth2-client/spec/capella"
	"github.com/attestantio/go-eth2-client/spec/deneb"
	"github.com/attestantio/go-eth2-client/spec/electra"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	beacontypes "github.com/ethereum/go-ethereum/beacon/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	dynssz "github.com/pk910/dynamic-ssz"
)

// Block binds a synthetic execution block to a complete fork-specific Beacon body.
func Block(fork string, execution *types.Block, header beacontypes.Header, commitments []string, spec map[string]any) (beacontypes.Header, map[string]any, error) {
	zero := func(n int) string { return "0x" + strings.Repeat("00", n) }
	syncBytes := uint64(64)
	if n, ok := spec["SYNC_COMMITTEE_SIZE"].(uint64); ok {
		syncBytes = n / 8
	}
	body := map[string]any{
		"randao_reveal": zero(96), "eth1_data": map[string]any{"deposit_root": zero(32), "deposit_count": "0", "block_hash": zero(32)},
		"graffiti": zero(32), "proposer_slashings": []any{}, "attester_slashings": []any{}, "attestations": []any{}, "deposits": []any{}, "voluntary_exits": []any{},
		"sync_aggregate": map[string]any{"sync_committee_bits": zero(int(syncBytes)), "sync_committee_signature": zero(96)},
	}
	payload := map[string]any{"parent_hash": execution.ParentHash().Hex(), "fee_recipient": zero(20), "state_root": zero(32), "receipts_root": zero(32), "logs_bloom": zero(256), "prev_randao": zero(32), "block_number": strconv.FormatUint(execution.NumberU64(), 10), "gas_limit": "0", "gas_used": "0", "timestamp": strconv.FormatUint(execution.Time(), 10), "extra_data": "0x", "base_fee_per_gas": "0", "block_hash": execution.Hash().Hex(), "transactions": []any{}}
	body["execution_payload"] = payload
	if fork != "bellatrix" {
		payload["withdrawals"] = []any{}
		body["bls_to_execution_changes"] = []any{}
	}
	if fork != "bellatrix" && fork != "capella" {
		payload["blob_gas_used"] = "0"
		payload["excess_blob_gas"] = "0"
		body["blob_kzg_commitments"] = commitments
	}
	requests := map[string]any{"deposits": []any{}, "withdrawals": []any{}, "consolidations": []any{}}
	if fork == "electra" || fork == "fulu" {
		body["execution_requests"] = requests
	}
	if fork == "gloas" {
		delete(body, "execution_payload")
		delete(body, "blob_kzg_commitments")
		body["signed_execution_payload_bid"] = map[string]any{"signature": zero(96), "message": map[string]any{"parent_block_hash": execution.ParentHash().Hex(), "parent_block_root": header.ParentRoot.Hex(), "block_hash": execution.Hash().Hex(), "prev_randao": zero(32), "fee_recipient": zero(20), "gas_limit": "0", "builder_index": "0", "slot": strconv.FormatUint(header.Slot, 10), "value": "0", "execution_payment": "0", "blob_kzg_commitments": commitments, "execution_requests_root": zero(32)}}
		body["payload_attestations"] = []any{}
		requests["builder_deposits"], requests["builder_exits"] = []any{}, []any{}
		body["parent_execution_requests"] = requests
	}
	var typed any
	switch fork {
	case "bellatrix":
		typed = new(bellatrix.BeaconBlockBody)
	case "capella":
		typed = new(capella.BeaconBlockBody)
	case "deneb":
		typed = new(deneb.BeaconBlockBody)
	case "electra", "fulu":
		typed = new(electra.BeaconBlockBody)
	case "gloas":
		typed = new(gloas.BeaconBlockBody)
	default:
		return header, nil, fmt.Errorf("unsupported fixture fork %s", fork)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return header, nil, err
	}
	if err := json.Unmarshal(raw, typed); err != nil {
		return header, nil, err
	}
	root, err := dynssz.NewDynSsz(spec, dynssz.WithNoFastHash(), dynssz.WithNoDelegation(), dynssz.WithNoFastSsz()).HashTreeRoot(typed)
	if err != nil {
		return header, nil, err
	}
	header.BodyRoot = common.Hash(root)
	message := map[string]any{"slot": strconv.FormatUint(header.Slot, 10), "proposer_index": strconv.FormatUint(header.ProposerIndex, 10), "parent_root": header.ParentRoot.Hex(), "state_root": header.StateRoot.Hex(), "body": body}
	return header, map[string]any{"version": fork, "data": map[string]any{"message": message}}, nil
}
