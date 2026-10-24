package enrich

import "github.com/islishude/etherview/internal/stagecontract"

var (
	RequestsStage       = stagecontract.Requests
	ConsensusStage      = stagecontract.Consensus
	BlobStage           = stagecontract.Blob
	NativeTransferStage = stagecontract.NativeTransfer
	ProxyStage          = stagecontract.Proxy
	ABIStage            = stagecontract.ABI
	TokenStage          = stagecontract.Token
	TraceStage          = stagecontract.Trace
	StateDiffStage      = stagecontract.StateDiff
	// stats@4 adds receipt-authenticated execution fees, priority fees, failed
	// transactions, and successful top-level creations.
	StatsStage = stagecontract.Stats
)
