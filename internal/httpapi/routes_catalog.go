package httpapi

func (h *Handler) registerCatalogRoutes() {
	if h.catalog == nil {
		return
	}
	h.handleBillable("getTransactionCalldata", h.transactionCalldata)
	h.handleBillable("getTransactionFailure", h.transactionFailure)
	h.handleBillable("getTransactionTrace", h.transactionTrace)
	h.handleBillable("listTransactionInternalTransactions", h.transactionInternalTransactions)
	h.handleBillable("listTransactionTokenTransfers", h.transactionTokenTransfers)
	h.handleBillable("listTransactionLogs", h.transactionLogs)
	h.handleBillable("listTransactionStateChanges", h.transactionStateChanges)
	h.handleBillable("listTransactionAuthorizations", h.transactionAuthorizations)
	h.handleBillable("listAddressDelegations", h.addressDelegations)
	h.handleBillable("listAddressNFTBalances", h.nftBalances)
	h.handleBillable("listAddressERC20Balances", h.erc20Balances)
	h.handleBillable("listTokens", h.tokens)
	h.handleBillable("getToken", h.token)
	h.handleBillable("listTokenTransfers", h.tokenTransfers)
	h.handleBillable("listTokenHolders", h.tokenHolders)
	h.handleBillable("getTokenHolderCount", h.tokenHolderCount)
	h.handleBillable("getNFTOwner", h.nftOwner)
	h.handleBillable("getBlockStats", h.blockStats)
	h.handleBillable("getAggregateStats", h.aggregateStats)
}
