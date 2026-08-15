package enrich

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
)

// VerifiedFunctionSelector is the bounded, canonical function-only projection
// persisted for a successful verification result. ABIEntry is deliberately a
// singleton function document entry so readers never load a complete ABI.
type VerifiedFunctionSelector struct {
	Selector  [4]byte
	Name      string
	Signature string
	ABIEntry  json.RawMessage
}

// VerifiedFunctionCalldataMatches accepts one normalized function entry only
// when geth can decode the entire argument payload and packing those values
// produces the exact original bytes. This rejects selector collisions whose
// permissive shape merely consumes a prefix of the calldata.
func VerifiedFunctionCalldataMatches(entry json.RawMessage, calldata []byte) bool {
	_, ok := DecodeVerifiedFunctionCalldata(entry, calldata)
	return ok
}

// DecodeVerifiedFunctionCalldata applies the same exact round-trip check as
// VerifiedFunctionCalldataMatches and returns the signature derived from the
// normalized singleton ABI entry. Readers therefore do not have to trust a
// denormalized signature column independently from its ABI payload.
func DecodeVerifiedFunctionCalldata(entry json.RawMessage, calldata []byte) (string, bool) {
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

// NormalizeVerifiedFunctionSelectors validates an entire verified ABI with the
// same parser and limits as abi@4, then projects only selector-bearing
// functions. Duplicate canonical signatures are collapsed deterministically.
func NormalizeVerifiedFunctionSelectors(data []byte) ([]VerifiedFunctionSelector, error) {
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return []VerifiedFunctionSelector{}, nil
	}
	entries, err := parseABIEntries(data, ABISourceVerified, DefaultDecodeLimits())
	if err != nil {
		return nil, err
	}
	selectors := make([]VerifiedFunctionSelector, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.kind != ABIKindFunction || entry.selectorless != "" {
			continue
		}
		key := string(entry.selector[:]) + "\x00" + entry.signature
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		outputs, err := json.Marshal(entry.outputs)
		if err != nil {
			return nil, err
		}
		if !entry.outputsKnown {
			outputs = []byte("null")
		}
		document, err := json.Marshal(abiEntryJSON{
			Type: "function", Name: entry.name, Inputs: entry.inputs, Outputs: outputs,
		})
		if err != nil {
			return nil, err
		}
		selectors = append(selectors, VerifiedFunctionSelector{
			Selector: entry.selector, Name: entry.name, Signature: entry.signature, ABIEntry: document,
		})
	}
	sort.Slice(selectors, func(left, right int) bool {
		if comparison := bytes.Compare(selectors[left].Selector[:], selectors[right].Selector[:]); comparison != 0 {
			return comparison < 0
		}
		return selectors[left].Signature < selectors[right].Signature
	})
	return selectors, nil
}
