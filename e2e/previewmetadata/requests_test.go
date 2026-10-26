//go:build previewmetadatae2e

package previewmetadatae2e

import (
	"context"
	"fmt"

	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/consensus/testfixture"
)

// Compare the public publication with a separate real Lighthouse envelope read
// and the real execution header in both runtime topologies.
func (h *harness) assertExecutionRequests(ctx context.Context, receipt rpcReceipt, root string) {
	h.t.Helper()
	var response gen.BlockRequestsResponse
	waitFor(h.t, ctx, "real execution request publication", func() (bool, string, error) {
		if err := h.getJSON(ctx, h.apiURL+"/api/v1/blocks/"+receipt.BlockHash.Hex()+"/requests", &response); err != nil {
			return false, err.Error(), nil
		}
		return response.Data.State == gen.ConsensusStateComplete, fmt.Sprintf("requests=%s code=%v", response.Data.State, response.Data.Code), nil
	})
	var envelope struct {
		Data *gloas.SignedExecutionPayloadEnvelope `json:"data"`
	}
	if err := h.getJSON(ctx, "https://beacon.etherview.local/eth/v1/beacon/execution_payload_envelopes/"+root, &envelope); err != nil {
		h.t.Fatal(err)
	}
	if envelope.Data == nil || envelope.Data.Message == nil || envelope.Data.Message.ExecutionRequests == nil {
		h.t.Fatal("missing real envelope")
	}
	message := envelope.Data.Message
	if common.Hash(message.BeaconBlockRoot).Hex() != root || common.Hash(message.Payload.BlockHash) != receipt.BlockHash {
		h.t.Fatal("real envelope identity mismatch")
	}
	hash, err := testfixture.RequestsHash(message.ExecutionRequests)
	if err != nil {
		h.t.Fatal(err)
	}
	var header types.Header
	h.rpcCall(ctx, &header, "eth_getBlockByHash", receipt.BlockHash, false)
	if header.Hash() != receipt.BlockHash || header.RequestsHash == nil || *header.RequestsHash != hash || response.Data.RequestsHash == nil || *response.Data.RequestsHash != hash.Hex() || response.Data.BeaconRoot == nil || *response.Data.BeaconRoot != root {
		h.t.Fatal("real request commitment mismatch")
	}
	h.t.Logf("requests verified against real Lighthouse envelope and execution header: block=%s hash=%s", receipt.BlockHash, hash)
}
