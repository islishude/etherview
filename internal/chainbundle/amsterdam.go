package chainbundle

import "github.com/ethereum/go-ethereum/core/types"

// ValidateBlockGas authenticates the relationship available without re-executing
// the EVM. Amsterdam headers report max(execution, state) gas while receipts
// charge the combined net amount. The two dimensions are not exposed by RPC.
func ValidateBlockGas(header *types.Header, chargedGas uint64) error {
	if header == nil || (header.SlotNumber == nil) != (header.BlockAccessListHash == nil) {
		return validation("header", "incomplete Amsterdam fields")
	}
	if header.GasUsed > header.GasLimit {
		return validation("block.gasUsed", "exceeds block gas limit")
	}
	if header.SlotNumber == nil && chargedGas != header.GasUsed ||
		header.SlotNumber != nil && chargedGas > header.GasUsed && chargedGas-header.GasUsed > header.GasUsed {
		return validation("block.gasUsed", "does not match receipt gas accounting")
	}
	return nil
}
