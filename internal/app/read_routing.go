package app

import (
	"context"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/etherscan"
	"github.com/islishude/etherview/internal/httpapi"
)

// searchRoutingReader keeps ordinary explorer reads on the configured reader
// while sending search to a writer-backed reader when name resolution can
// persist an observation that the same request must immediately consume.
type searchRoutingReader struct {
	httpapi.Reader
	search httpapi.Reader
}

func (reader searchRoutingReader) Search(
	ctx context.Context,
	value string,
	cursor string,
	limit int,
) ([]gen.SearchResult, string, error) {
	return reader.search.Search(ctx, value, cursor, limit)
}

// replicaAwareEtherscanBackend serves the explicitly inventoried compatibility
// reads from the reader. Everything else stays on the writer so a future action
// cannot silently cross the authority boundary when it is added.
type replicaAwareEtherscanBackend struct {
	reader        etherscan.Backend
	authoritative etherscan.Backend
}

func (backend replicaAwareEtherscanBackend) Execute(
	ctx context.Context,
	request etherscan.Request,
) (any, error) {
	if compatibilityUsesAuthoritativeDatabase(request) {
		return backend.authoritative.Execute(ctx, request)
	}
	return backend.reader.Execute(ctx, request)
}

func compatibilityUsesAuthoritativeDatabase(request etherscan.Request) bool {
	switch request.Module + "." + request.Action {
	case "account.balance",
		"account.balancemulti",
		"account.txlist",
		"account.txlistinternal",
		"account.tokentx",
		"account.tokennfttx",
		"account.token1155tx",
		"account.tokenbalance",
		"account.getminedblocks",
		"transaction.getstatus",
		"transaction.gettxreceiptstatus",
		"logs.getLogs",
		"block.getblocknobytime",
		"block.getblockcountdown",
		"stats.ethsupply",
		"stats.tokensupply",
		"contract.getabi",
		"contract.getsourcecode",
		"contract.getcontractcreation",
		"token.tokensupply",
		"token.tokenbalance",
		"token.tokeninfo",
		"token.tokenholderlist":
		return false
	default:
		return true
	}
}
