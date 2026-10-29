//go:build previewmetadatae2e

package previewmetadatae2e

import (
	"context"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// Public Hardhat development key. Never fund it on an external network.
const previewSigningKey = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

const previewSlotDurationMillis = "3000"

func (h *harness) assertSlotDuration(ctx context.Context) {
	h.t.Helper()
	var spec struct {
		Data struct {
			SlotDurationMS string `json:"SLOT_DURATION_MS"`
		}
	}
	if err := h.getJSON(ctx, "https://beacon.etherview.localhost/eth/v1/config/spec", &spec); err != nil {
		h.t.Fatal(err)
	}
	if spec.Data.SlotDurationMS != previewSlotDurationMillis {
		h.t.Fatalf("Preview requires %sms slots, got %q", previewSlotDurationMillis, spec.Data.SlotDurationMS)
	}
}

func (h *harness) prepareNetwork(ctx context.Context) {
	h.t.Helper()
	if err := h.project.PreviewCompilerCache(ctx, "init"); err != nil {
		h.t.Fatal(err)
	}
	command := exec.CommandContext(ctx, "node", "--input-type=module", "-e", `import {prepareNetwork} from "./.github/scripts/preview-network.mjs"; prepareNetwork(process.argv[1]);`, h.project.Env["PREVIEW_NETWORK_DIR"])
	command.Dir = h.root
	command.Env = append(os.Environ(), "COMPOSE_PROJECT_NAME="+h.project.Name, "ETHERVIEW_GENESIS_FILE=", "GETH_GENESIS_FILE=")
	output, err := command.CombinedOutput()
	if writeErr := os.WriteFile(filepath.Join(h.artifacts, "network-init.log"), output, 0600); writeErr != nil {
		h.t.Fatal(writeErr)
	}
	if err != nil {
		h.t.Fatalf("prepare Preview network: %v\n%s", err, output)
	}
}

func (h *harness) initializeArchive(ctx context.Context) {
	h.t.Helper()
	if _, err := h.project.Run(ctx, "run", "--rm", "archive-init"); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) sendTransaction(ctx context.Context, to *common.Address, data []byte, value *big.Int) common.Hash {
	h.t.Helper()
	key, err := crypto.HexToECDSA(previewSigningKey)
	if err != nil {
		h.t.Fatal(err)
	}
	h.developer = crypto.PubkeyToAddress(key.PublicKey)
	var nonce hexutil.Uint64
	h.rpcCall(ctx, &nonce, "eth_getTransactionCount", h.developer, "pending")
	transaction := types.NewTx(&types.DynamicFeeTx{ChainID: big.NewInt(48815), Nonce: uint64(nonce), GasTipCap: big.NewInt(1_000_000_000), GasFeeCap: big.NewInt(2_000_000_000), Gas: 3_000_000, To: to, Value: value, Data: data})
	signed, err := types.SignTx(transaction, types.LatestSignerForChainID(big.NewInt(48815)), key)
	if err != nil {
		h.t.Fatal(err)
	}
	encoded, err := signed.MarshalBinary()
	if err != nil {
		h.t.Fatal(err)
	}
	var hash common.Hash
	h.rpcCall(ctx, &hash, "eth_sendRawTransaction", hexutil.Bytes(encoded))
	if hash != signed.Hash() {
		h.t.Fatal("RPC returned a different signed transaction hash")
	}
	return hash
}
