//go:build runtimee2e

package runtimee2e

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// PreviewMetadataNFT.sol is the repository's reviewed Solidity 0.8.30,
// optimizer-runs=200, Prague, metadata-disabled artifact. It mints token 1 to
// the deployer and supplies the ERC-721 ownerOf/ERC-165 surface needed by the
// exact compatibility holding regression.
const runtimeEtherscanNFTCreationBytecode = "0x60a0604052348015600e575f5ffd5b50336080819052604051600191905f907fddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef908290a46080516103e16100625f395f8181610165015261021e01526103e15ff3fe608060405234801561000f575f5ffd5b506004361061004a575f3560e01c806301ffc9a71461004e5780636352211e14610076578063c87b56dd146100a1578063efcf5f33146100c1575b5f5ffd5b61006161005c3660046102bb565b6100cb565b60405190151581526020015b60405180910390f35b6100896100843660046102e9565b61011c565b6040516001600160a01b03909116815260200161006d565b6100b46100af3660046102e9565b610189565b60405161006d9190610300565b6100c9610213565b005b5f6301ffc9a760e01b6001600160e01b0319831614806100fb57506380ac58cd60e01b6001600160e01b03198316145b806101165750632483248360e11b6001600160e01b03198316145b92915050565b5f816001146101625760405162461bcd60e51b815260206004820152600d60248201526c36b4b9b9b4b733903a37b5b2b760991b60448201526064015b60405180910390fd5b507f0000000000000000000000000000000000000000000000000000000000000000919050565b6060816001146101cb5760405162461bcd60e51b815260206004820152600d60248201526c36b4b9b9b4b733903a37b5b2b760991b6044820152606401610159565b5f5460ff16156101f4576040518060800160405280605b8152602001610386605b913992915050565b6040518060800160405280605081526020016103366050913992915050565b336001600160a01b037f000000000000000000000000000000000000000000000000000000000000000016146102775760405162461bcd60e51b81526020600482015260096024820152683737ba1037bbb732b960b91b6044820152606401610159565b5f805460ff191660019081179091556040519081527ff8e1a15aba9398e019f0b49df1a4fde98ee17ae345cb5f6b5e2c27f5033e8ce79060200160405180910390a1565b5f602082840312156102cb575f5ffd5b81356001600160e01b0319811681146102e2575f5ffd5b9392505050565b5f602082840312156102f9575f5ffd5b5035919050565b602081525f82518060208401528060208501604085015e5f604082850101526040601f19601f8301168401019150509291505056fe697066733a2f2f62616679626569626e736f7566723272656e717a73683334376e727835347763756274356c676b656976657a363378766976706c6677687470796d2f6d657461646174612e6a736f6e697066733a2f2f62616679626569626e736f7566723272656e717a73683334376e727835347763756274356c676b656976657a363378766976706c6677687470796d2f6d657461646174612e6a736f6e3f7265766973696f6e3d32"

func (h *harness) sendEtherscanNFTDeployment(ctx context.Context) string {
	return h.sendTransaction(ctx, map[string]any{
		"from": h.fixture.accounts[0], "data": runtimeEtherscanNFTCreationBytecode,
		"gas": "0x7a120", "gasPrice": "0x3b9aca00",
	})
}

func (h *harness) captureInitialContractDeployments(ctx context.Context, delegateAHash, delegateBHash string) {
	for hash, target := range map[string]*string{
		delegateAHash: &h.fixture.delegateA,
		delegateBHash: &h.fixture.delegateB,
	} {
		receipt := h.waitReceipt(ctx, hash)
		if receipt.Status != "0x1" || !common.IsHexAddress(receipt.ContractAddress) {
			h.t.Fatalf("delegate deployment receipt %s = %#v", hash, receipt)
		}
		*target = common.HexToAddress(receipt.ContractAddress).Hex()
		var code string
		h.rpcCall(ctx, &code, "eth_getCode", *target, "latest")
		if !strings.EqualFold(code, noCBORRuntimeBytecode) {
			h.t.Fatalf("delegate runtime code %s = %s", *target, code)
		}
	}
}

func (h *harness) captureEtherscanNFTDeployment(ctx context.Context) {
	h.t.Helper()
	nftReceipt := h.waitReceipt(ctx, h.fixture.nftCreationHash)
	if nftReceipt.Status != "0x1" || !common.IsHexAddress(nftReceipt.ContractAddress) {
		h.t.Fatalf("Etherscan NFT deployment receipt = %#v", nftReceipt)
	}
	h.fixture.nftAddress = common.HexToAddress(nftReceipt.ContractAddress).Hex()
}

func (h *harness) captureEtherscanNFTHoldings(ctx context.Context) string {
	h.t.Helper()
	holdingsRaw := h.etherscanResult(ctx, url.Values{
		"module": {"account"}, "action": {"addresstokennftbalance"},
		"address": {h.fixture.accounts[0]},
	})
	var holdings []struct {
		TokenAddress  string `json:"TokenAddress"`
		TokenQuantity string `json:"TokenQuantity"`
	}
	if err := json.Unmarshal(holdingsRaw, &holdings); err != nil {
		h.t.Fatal(err)
	}
	found := false
	for _, holding := range holdings {
		if strings.EqualFold(holding.TokenAddress, h.fixture.nftAddress) && holding.TokenQuantity == "1" {
			found = true
			break
		}
	}
	if !found {
		h.t.Fatalf("Etherscan ERC-721 holdings omitted %s: %s", h.fixture.nftAddress, holdingsRaw)
	}

	inventoryRaw := h.etherscanResult(ctx, url.Values{
		"module": {"account"}, "action": {"addresstokennftinventory"},
		"address": {h.fixture.accounts[0]}, "contractaddress": {h.fixture.nftAddress},
	})
	var inventory []struct {
		TokenAddress string `json:"TokenAddress"`
		TokenID      string `json:"TokenId"`
	}
	if err := json.Unmarshal(inventoryRaw, &inventory); err != nil {
		h.t.Fatal(err)
	}
	if len(inventory) != 1 || !strings.EqualFold(inventory[0].TokenAddress, h.fixture.nftAddress) || inventory[0].TokenID != "1" {
		h.t.Fatalf("Etherscan ERC-721 inventory=%s", inventoryRaw)
	}
	return strings.ToLower(h.fixture.nftAddress) + ":1"
}
