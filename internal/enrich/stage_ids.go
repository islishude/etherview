package enrich

import "github.com/islishude/etherview/internal/stagecontract"

var (
	ProxyStage     = stagecontract.Proxy
	ABIStage       = stagecontract.ABI
	TokenStage     = stagecontract.Token
	TraceStage     = stagecontract.Trace
	StateDiffStage = stagecontract.StateDiff
	// stats@3 adds receipt-authenticated execution fees, priority fees, failed
	// transactions, and successful top-level creations.
	StatsStage = stagecontract.Stats
)
