package verify

import (
	"encoding/binary"

	"github.com/fxamacker/cbor/v2"
)

const maxCompilerFooterBytes = 8 << 10

type decodedFooter struct {
	Start   int
	Payload []byte
	Raw     []byte
}

var matcherCBORMode = newMatcherCBORMode()

func newMatcherCBORMode() cbor.DecMode {
	mode, err := (cbor.DecOptions{
		DupMapKey:        cbor.DupMapKeyEnforcedAPF,
		MaxNestedLevels:  8,
		MaxArrayElements: 1024,
		MaxMapPairs:      64,
		IndefLength:      cbor.IndefLengthForbidden,
		TagsMd:           cbor.TagsForbidden,
		IntDec:           cbor.IntDecConvertNone,
		UTF8:             cbor.UTF8RejectInvalid,
	}).DecMode()
	if err != nil {
		panic("construct bounded compiler metadata decoder")
	}
	return mode
}

func decodeExclusiveMapFooter(bytecode []byte) (decodedFooter, bool) {
	if len(bytecode) < 3 {
		return decodedFooter{}, false
	}
	payloadLength := int(binary.BigEndian.Uint16(bytecode[len(bytecode)-2:]))
	if payloadLength == 0 || payloadLength > maxCompilerFooterBytes || payloadLength+2 > len(bytecode) {
		return decodedFooter{}, false
	}
	start := len(bytecode) - payloadLength - 2
	payload := bytecode[start : len(bytecode)-2]
	if !validCompleteCBOR(payload) {
		return decodedFooter{}, false
	}
	var value map[string]cbor.RawMessage
	if err := matcherCBORMode.Unmarshal(payload, &value); err != nil || len(value) == 0 {
		return decodedFooter{}, false
	}
	return decodedFooter{
		Start:   start,
		Payload: append([]byte(nil), payload...),
		Raw:     append([]byte(nil), bytecode[start:]...),
	}, true
}

func decodeInclusiveArrayFooter(bytecode []byte) (decodedFooter, []cbor.RawMessage, bool) {
	if len(bytecode) < 3 {
		return decodedFooter{}, nil, false
	}
	totalLength := int(binary.BigEndian.Uint16(bytecode[len(bytecode)-2:]))
	if totalLength < 3 || totalLength > maxCompilerFooterBytes || totalLength > len(bytecode) {
		return decodedFooter{}, nil, false
	}
	start := len(bytecode) - totalLength
	payload := bytecode[start : len(bytecode)-2]
	if !validCompleteCBOR(payload) {
		return decodedFooter{}, nil, false
	}
	var value []cbor.RawMessage
	if err := matcherCBORMode.Unmarshal(payload, &value); err != nil || len(value) == 0 {
		return decodedFooter{}, nil, false
	}
	return decodedFooter{
		Start:   start,
		Payload: append([]byte(nil), payload...),
		Raw:     append([]byte(nil), bytecode[start:]...),
	}, value, true
}

// Decoding into RawMessage intentionally leaves descendants opaque. Decode a
// second time into a complete tree so the bounded mode enforces duplicate-key,
// tag, nesting, and collection rules at every depth of compiler metadata.
func validCompleteCBOR(payload []byte) bool {
	var value any
	return matcherCBORMode.Unmarshal(payload, &value) == nil
}
