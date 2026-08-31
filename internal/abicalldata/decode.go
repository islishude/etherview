// Package abicalldata owns exact read-side calldata verification for one
// normalized verified ABI entry.
package abicalldata

import (
	"bytes"
	"encoding/json"
	"strings"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
)

func DecodeVerifiedFunction(entry json.RawMessage, calldata []byte) (string, bool) {
	if len(entry) == 0 || len(calldata) < 4 {
		return "", false
	}
	document := make([]byte, 0, len(entry)+2)
	document = append(document, '[')
	document = append(document, entry...)
	document = append(document, ']')
	parsed, err := gethabi.JSON(strings.NewReader(string(document)))
	if err != nil || len(parsed.Methods) != 1 {
		return "", false
	}
	method, err := parsed.MethodById(calldata[:4])
	if err != nil {
		return "", false
	}
	values, err := method.Inputs.Unpack(calldata[4:])
	if err != nil {
		return "", false
	}
	reencoded, err := method.Inputs.Pack(values...)
	if err != nil || !bytes.Equal(reencoded, calldata[4:]) {
		return "", false
	}
	return method.Sig, true
}
