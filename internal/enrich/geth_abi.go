package enrich

import (
	"fmt"
	"strings"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
)

var stateProbeABI = mustGethABI(`[
	{"type":"function","name":"supportsInterface","inputs":[{"name":"interfaceId","type":"bytes4"}],"outputs":[{"name":"","type":"bool"}]},
	{"type":"function","name":"name","inputs":[],"outputs":[{"name":"","type":"string"}]},
	{"type":"function","name":"symbol","inputs":[],"outputs":[{"name":"","type":"string"}]},
	{"type":"function","name":"decimals","inputs":[],"outputs":[{"name":"","type":"uint8"}]},
	{"type":"function","name":"totalSupply","inputs":[],"outputs":[{"name":"","type":"uint256"}]},
	{"type":"function","name":"implementation","inputs":[],"outputs":[{"name":"","type":"address"}]},
	{"type":"function","name":"proxiableUUID","inputs":[],"outputs":[{"name":"","type":"bytes32"}]},
	{"type":"function","name":"UPGRADE_INTERFACE_VERSION","inputs":[],"outputs":[{"name":"","type":"string"}]}
]`)

func mustGethABI(definition string) gethabi.ABI {
	parsed, err := gethabi.JSON(strings.NewReader(definition))
	if err != nil {
		panic(fmt.Errorf("parse built-in state probe ABI: %w", err))
	}
	return parsed
}

func packStateProbe(method string, arguments ...any) ([]byte, error) {
	input, err := stateProbeABI.Pack(method, arguments...)
	if err != nil {
		return nil, fmt.Errorf("pack %s state probe: %w", method, err)
	}
	return input, nil
}
