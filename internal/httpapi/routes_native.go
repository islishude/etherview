package httpapi

func (h *Handler) registerNativeRoutes() {
	h.mux.HandleFunc("GET /api/v1/genesis/accounts", h.genesisAccounts)
	h.handleBillable("listBlocks", h.blocks)
	h.handleBillable("getBlock", h.block)
	h.handleBillable("listBlockTransactions", h.blockTransactions)
	h.handleBillable("listTransactions", h.transactions)
	h.handleBillable("listUserOperations", h.userOperationList)
	h.handleBillable("getUserOperation", h.userOperationDetail)
	h.handleBillable("getTransaction", h.transaction)
	h.handleBillable("listTransactionUserOperations", h.transactionUserOperations)
	h.handleBillable("listPendingTransactions", h.pendingTransactions)
	h.handleBillable("getAddress", h.address)
	h.handleBillable("listAddressNames", h.addressNamesPage)
	h.handleBillable("getAddressDelegation", h.addressDelegation)
	h.handleBillable("listAddressTransactions", h.addressTransactions)
	h.handleBillable("listAddressUserOperations", h.addressUserOperations)
	h.handleBillable("listAddressWithdrawals", h.addressWithdrawals)
	h.handleBillable("listAddressInternalTransactions", h.addressInternalTransactions)
	h.handleBillable("listAddressERC20Transfers", h.addressERC20Transfers)
	h.handleBillable("listAddressNFTTransfers", h.addressNFTTransfers)
	h.handleBillable("search", h.search)
}
