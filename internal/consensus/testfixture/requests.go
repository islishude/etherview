package testfixture

import (
	"encoding/json"
	"fmt"

	"github.com/attestantio/go-eth2-client/spec/electra"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	beacontypes "github.com/ethereum/go-ethereum/beacon/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	dynssz "github.com/pk910/dynamic-ssz"
)

// Requests builds complete authenticated Beacon and payload envelope fixtures.
// RequestsHash uses independent protocol SSZ serialization as the encoding oracle.
func RequestsHash(requests *gloas.ExecutionRequests) (common.Hash, error) {
	groups := make([][]byte, 5)
	for i := range groups {
		groups[i] = []byte{byte(i)}
	}
	appendRecord := func(kind int, value any) error {
		raw, err := dynssz.NewDynSsz(nil, dynssz.WithNoFastSsz()).MarshalSSZ(value)
		if err != nil {
			return err
		}
		groups[kind] = append(groups[kind], raw...)
		return nil
	}
	for _, v := range requests.Deposits {
		if err := appendRecord(0, v); err != nil {
			return common.Hash{}, err
		}
	}
	for _, v := range requests.Withdrawals {
		if err := appendRecord(1, v); err != nil {
			return common.Hash{}, err
		}
	}
	for _, v := range requests.Consolidations {
		if err := appendRecord(2, v); err != nil {
			return common.Hash{}, err
		}
	}
	for _, v := range requests.BuilderDeposits {
		if err := appendRecord(3, v); err != nil {
			return common.Hash{}, err
		}
	}
	for _, v := range requests.BuilderExits {
		if err := appendRecord(4, v); err != nil {
			return common.Hash{}, err
		}
	}
	return types.CalcRequestsHash(groups), nil
}

func Requests(fork string, block *types.Block, header beacontypes.Header, requests *gloas.ExecutionRequests) (beacontypes.Header, map[string]any, map[string]any, error) {
	if requests.Deposits == nil {
		requests.Deposits = []*electra.DepositRequest{}
	}
	if requests.Withdrawals == nil {
		requests.Withdrawals = []*electra.WithdrawalRequest{}
	}
	if requests.Consolidations == nil {
		requests.Consolidations = []*electra.ConsolidationRequest{}
	}
	if requests.BuilderDeposits == nil {
		requests.BuilderDeposits = []*gloas.BuilderDepositRequest{}
	}
	if requests.BuilderExits == nil {
		requests.BuilderExits = []*gloas.BuilderExitRequest{}
	}
	header, bodyEnvelope, err := Block(fork, block, header, []string{}, nil)
	if err != nil {
		return header, nil, nil, err
	}
	message := bodyEnvelope["data"].(map[string]any)["message"].(map[string]any)
	body := message["body"].(map[string]any)
	var typed any
	switch fork {
	case "electra", "fulu":
		body["execution_requests"] = &electra.ExecutionRequests{Deposits: requests.Deposits, Withdrawals: requests.Withdrawals, Consolidations: requests.Consolidations}
		typed = new(electra.BeaconBlockBody)
	case "gloas":
		root, err := dynssz.NewDynSsz(nil, dynssz.WithNoFastHash()).HashTreeRoot(requests)
		if err != nil {
			return header, nil, nil, err
		}
		body["signed_execution_payload_bid"].(map[string]any)["message"].(map[string]any)["execution_requests_root"] = common.Hash(root).Hex()
		typed = new(gloas.BeaconBlockBody)
	default:
		return header, nil, nil, fmt.Errorf("unsupported request fixture fork")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return header, nil, nil, err
	}
	if err = json.Unmarshal(raw, typed); err != nil {
		return header, nil, nil, err
	}
	root, err := dynssz.NewDynSsz(nil, dynssz.WithNoFastHash(), dynssz.WithNoDelegation(), dynssz.WithNoFastSsz()).HashTreeRoot(typed)
	if err != nil {
		return header, nil, nil, err
	}
	header.BodyRoot = common.Hash(root)
	payload := &gloas.ExecutionPayload{BlockHash: phase0.Hash32(block.Hash()), ParentHash: phase0.Hash32(block.ParentHash()), BlockNumber: block.NumberU64(), Timestamp: block.Time(), SlotNumber: header.Slot}
	signed := &gloas.SignedExecutionPayloadEnvelope{Message: &gloas.ExecutionPayloadEnvelope{Payload: payload, ExecutionRequests: requests, BeaconBlockRoot: phase0.Root(header.Hash()), ParentBeaconBlockRoot: phase0.Root(header.ParentRoot)}}
	return header, bodyEnvelope, map[string]any{"version": "gloas", "data": signed}, nil
}

func HeaderResponse(header beacontypes.Header) map[string]any {
	return map[string]any{"data": map[string]any{"root": header.Hash().Hex(), "header": map[string]any{"message": map[string]string{
		"slot": fmt.Sprint(header.Slot), "proposer_index": fmt.Sprint(header.ProposerIndex), "parent_root": header.ParentRoot.Hex(), "state_root": header.StateRoot.Hex(), "body_root": header.BodyRoot.Hex(),
	}}}}
}
