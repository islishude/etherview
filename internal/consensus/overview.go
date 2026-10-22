package consensus

import (
	"context"
	"strconv"
	"time"

	"github.com/islishude/etherview/internal/api/gen"
)

func (c *Client) Overview(ctx context.Context, network Network) (gen.ConsensusOverview, error) {
	h, root, err := c.Header(ctx, "head")
	if err != nil {
		return gen.ConsensusOverview{}, err
	}
	var checkpoints struct {
		ExecutionOptimistic bool `json:"execution_optimistic"`
		Data                struct {
			Justified gen.ConsensusCheckpoint `json:"current_justified"`
			Finalized gen.ConsensusCheckpoint `json:"finalized"`
		}
	}
	if err := c.get(ctx, "/eth/v1/beacon/states/"+h.StateRoot.Hex()+"/finality_checkpoints", &checkpoints); err != nil {
		return gen.ConsensusOverview{}, err
	}
	for _, point := range []gen.ConsensusCheckpoint{checkpoints.Data.Justified, checkpoints.Data.Finalized} {
		if _, err := quantity(point.Epoch); err != nil || !fixedHex(point.Root, 32) {
			return gen.ConsensusOverview{}, failure("beacon_checkpoint")
		}
	}
	var sync struct {
		Data struct {
			Syncing    *bool `json:"is_syncing"`
			Optimistic *bool `json:"is_optimistic"`
		}
	}
	if err := c.get(ctx, "/eth/v1/node/syncing", &sync); err != nil {
		return gen.ConsensusOverview{}, err
	}
	if sync.Data.Syncing == nil || sync.Data.Optimistic == nil {
		return gen.ConsensusOverview{}, failure("beacon_sync_status")
	}
	slot := h.Slot
	slotText := strconv.FormatUint(slot, 10)
	epoch := strconv.FormatUint(slot/network.Slots, 10)
	now := time.Now().UTC()
	optimistic := *sync.Data.Optimistic || checkpoints.ExecutionOptimistic
	return gen.ConsensusOverview{State: gen.ConsensusStateComplete, Slot: &slotText, Epoch: &epoch, Root: &root, Justified: &checkpoints.Data.Justified, Finalized: &checkpoints.Data.Finalized, Syncing: sync.Data.Syncing, ExecutionOptimistic: &optimistic, ObservedAt: &now}, nil
}
