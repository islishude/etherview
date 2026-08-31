// Package proxycontract owns stable proxy identity and bounded Diamond
// vocabulary shared by detection workers and public readers.
package proxycontract

type Kind string
type Pattern string

const (
	Minimal1167 Kind = "eip1167"
	CWIA        Kind = "cwia"
	EIP1967     Kind = "eip1967"
	Beacon      Kind = "beacon"

	PatternClone       Pattern = "clone"
	PatternERC1967     Pattern = "erc1967"
	PatternTransparent Pattern = "transparent"
	PatternUUPS        Pattern = "uups"
	PatternBeacon      Pattern = "beacon"
	PatternUnknown     Pattern = "unknown"
)

const (
	DiamondMaxFacets            = 256
	DiamondMaxSelectorsTotal    = 16_384
	DiamondMaxSelectorsPerFacet = 4_096
	DiamondMaxCrossCheckCalls   = 256
	DiamondMaxBatchConcurrency  = 12
	DiamondMaxHistoryChanges    = 262_144
	DiamondMaxRawReturnBytes    = 2 << 20
)
