package consensus

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/core/types"
)

// Evidence retains bounded root-pinned wire data independently of processor publication.
type Evidence struct {
	Blobs    []BlobReference   `json:"blobs,omitempty"`
	Network  Network           `json:"network"`
	Spec     map[string]uint64 `json:"spec,omitempty"`
	Header   json.RawMessage   `json:"header"`
	Root     string            `json:"root,omitempty"`
	Block    json.RawMessage   `json:"block,omitempty"`
	Envelope json.RawMessage   `json:"envelope,omitempty"`
}

func (c *Client) FetchBlock(ctx context.Context, network Network, block *types.Block, requests bool) (Evidence, error) {
	slot, err := executionSlot(network, block)
	if err != nil {
		return Evidence{}, err
	}
	var wireHeader json.RawMessage
	if err := c.get(ctx, "/eth/v1/beacon/headers/"+strconv.FormatUint(slot, 10), &wireHeader); err != nil {
		return Evidence{}, err
	}
	_, root, err := decodeHeader(wireHeader)
	if err != nil {
		return Evidence{}, err
	}
	input := Evidence{Network: network, Header: wireHeader, Root: root, Spec: map[string]uint64{}}
	for k, v := range network.BodySpec {
		n, ok := v.(uint64)
		if !ok {
			return Evidence{}, failure("beacon_spec")
		}
		input.Spec[k] = n
	}
	if err = c.get(ctx, "/eth/v2/beacon/blocks/"+root, &input.Block); err != nil {
		return Evidence{}, err
	}
	if requests {
		var fork struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(input.Block, &fork) != nil {
			return Evidence{}, failure("beacon_malformed")
		}
		if fork.Version == "gloas" {
			if err = c.get(ctx, "/eth/v1/beacon/execution_payload_envelopes/"+root, &input.Envelope); err != nil {
				return Evidence{}, err
			}
		}
	}
	return input, nil
}

func (e Evidence) network() Network {
	network := e.Network
	network.BodySpec = make(map[string]any, len(e.Spec))
	for k, v := range e.Spec {
		network.BodySpec[k] = v
	}
	return network
}

func (e Evidence) Observation(block *types.Block) (Observation, error) {
	network := e.network()
	slot, err := executionSlot(network, block)
	if err != nil {
		return Observation{}, err
	}
	header, root, err := decodeHeader(e.Header)
	if err != nil || root != e.Root || slot != header.Slot {
		return Observation{}, failure("beacon_header_identity")
	}
	observed, err := decodeBlock(e.Block, header, e.Root, network)
	if err != nil {
		return Observation{}, err
	}
	if !strings.EqualFold(observed.ExecutionHash, block.Hash().Hex()) || !strings.EqualFold(observed.ExecutionParent, block.ParentHash().Hex()) {
		return Observation{}, &Error{Code: "execution_identity_mismatch", Retry: true}
	}
	observed.SlotConflict = block.SlotNumber() != nil && *block.SlotNumber() != observed.Slot
	return observed, nil
}
