//go:build runtimee2e

package runtimee2e

import "github.com/ethereum/go-ethereum/common/hexutil"

type modeResult struct {
	durable durableSnapshot
	api     apiSnapshot
}

type eip7702APISnapshot struct {
	authorityType             string
	hasDelegationHistory      bool
	delegationStatus          string
	delegationHistory         string
	authorizationOutcomes     string
	delegatedResolution       string
	delegatedExecutionAddress string
	clearingResolution        string
	clearingStatus            string
}

type rpcBlock struct {
	Hash   string `json:"hash"`
	Number string `json:"number"`
}

type rpcReceipt struct {
	Status          string `json:"status"`
	ContractAddress string `json:"contractAddress"`
	Logs            []struct {
		Topics   []string       `json:"topics"`
		LogIndex hexutil.Uint64 `json:"logIndex"`
	} `json:"logs"`
}
