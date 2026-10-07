//go:build previewmetadatae2e

package previewmetadatae2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/api/gen"
)

func (h *harness) assertAmsterdam(ctx context.Context, recipient string) {
	destination := common.HexToAddress(recipient)
	var hash common.Hash
	h.rpcCall(ctx, &hash, "eth_sendTransaction", map[string]any{"from": h.developer, "to": destination, "value": "0x20000000000001", "gas": "0x2dc6c0", "gasPrice": "0x3b9aca00"})
	receipt := h.waitReceipt(ctx, "Amsterdam native transfer", hash)
	if uint64(receipt.Status) != 1 {
		h.t.Fatalf("native transfer failed: %+v", receipt)
	}
	var header *types.Header
	h.rpcCall(ctx, &header, "eth_getBlockByHash", receipt.BlockHash, false)
	if header == nil || header.SlotNumber == nil || header.BlockAccessListHash == nil {
		h.t.Fatal("Preview did not produce an Amsterdam header")
	}
	for _, path := range []string{"/transactions/" + hash.Hex() + "/native-transfers", "/addresses/" + destination.Hex() + "/native-transfers"} {
		waitFor(h.t, ctx, "Amsterdam indexed native transfer", func() (bool, string, error) {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.apiURL+"/api/v1"+path, nil)
			if err != nil {
				return false, "", err
			}
			response, err := h.http.Do(request)
			if err != nil {
				return false, "", err
			}
			defer response.Body.Close() //nolint:errcheck
			if response.StatusCode == http.StatusServiceUnavailable || response.StatusCode == http.StatusNotFound {
				return false, fmt.Sprintf("status %d", response.StatusCode), nil
			}
			if response.StatusCode != http.StatusOK {
				return false, "", fmt.Errorf("native API status %d", response.StatusCode)
			}
			var payload gen.NativeTransferListResponse
			if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
				return false, "", err
			}
			if !payload.Applicable || len(payload.Data) != 1 {
				return false, "native transfer pending", nil
			}
			item := payload.Data[0]
			if item.Amount != "9007199254740993" || item.TransactionHash != strings.ToLower(hash.Hex()) || item.BlockHash != strings.ToLower(receipt.BlockHash.Hex()) || !strings.EqualFold(item.From, h.developer.Hex()) || !strings.EqualFold(item.To, destination.Hex()) {
				return false, "", fmt.Errorf("unexpected native transfer %+v", item)
			}
			return true, "", nil
		})
	}
}

// Switch only this disposable project to the production all-role builder, then
// mine new data to prove it uses the same processor and public contracts.
func (h *harness) assertAmsterdamMonolith(ctx context.Context) {
	if _, err := h.project.Run(ctx, "stop", "sync", "enrich", "trace", "metadata", "maintenance"); err != nil {
		h.t.Fatal(err)
	}
	override := filepath.Join(h.artifacts, "amsterdam-monolith.yaml")
	config := `services:
  api:
    command: ["serve", "--roles=all", "--config=/etc/etherview/config.yaml"]
    environment:
      ETHERVIEW_ROLES: all
      ETHERVIEW_METADATA_UNSAFE_ALLOW_PRIVATE_NETWORKS: "false"
      SSL_CERT_FILE: /run/ipfs-ca/rootCA.pem
    volumes:
      - ` + filepath.Join(h.root, ".local", "preview-tls", "rootCA.pem") + `:/run/ipfs-ca/rootCA.pem:ro
`
	if err := os.WriteFile(override, []byte(config), 0600); err != nil {
		h.t.Fatal(err)
	}
	h.project.Files = append(h.project.Files, override)
	if _, err := h.project.Run(ctx, "up", "-d", "--no-build", "--no-deps", "--wait", "--wait-timeout", "180", "api"); err != nil {
		h.t.Fatal(err)
	}
	binding, err := h.project.Port(ctx, "api", 8080)
	if err != nil {
		h.t.Fatal(err)
	}
	h.apiURL = "https://" + binding
	h.assertAmsterdam(ctx, "0x1235")
}
