package verify

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"sort"
	"strings"

	"github.com/fxamacker/cbor/v2"
)

// Vyper 0.4.3: [integrity, runtime size, data sizes, immutable size, version]
// followed by a big-endian length INCLUDING the two length bytes.
func vyperFooter(code []byte) (start int, runtimeSize, immutableSize uint64, ok bool) {
	if len(code) < 3 {
		return
	}
	size := int(binary.BigEndian.Uint16(code[len(code)-2:]))
	if size < 3 || size > maxCompilerFooterBytes || size > len(code) {
		return
	}
	start = len(code) - size
	raw := code[start : len(code)-2]
	var tuple []cbor.RawMessage
	if !validCompleteCBOR(raw) || matcherCBORMode.Unmarshal(raw, &tuple) != nil || len(tuple) != 5 {
		return 0, 0, 0, false
	}
	var integrity []byte
	var sections []uint64
	var version map[string][]uint64
	if matcherCBORMode.Unmarshal(tuple[0], &integrity) != nil || len(integrity) != 32 ||
		matcherCBORMode.Unmarshal(tuple[1], &runtimeSize) != nil || runtimeSize == 0 ||
		matcherCBORMode.Unmarshal(tuple[2], &sections) != nil || len(sections) > 1024 ||
		matcherCBORMode.Unmarshal(tuple[3], &immutableSize) != nil ||
		matcherCBORMode.Unmarshal(tuple[4], &version) != nil || len(version) != 1 {
		return 0, 0, 0, false
	}
	var dataSize uint64
	for _, size := range sections {
		if size > runtimeSize-dataSize {
			return 0, 0, 0, false
		}
		dataSize += size
	}
	v := version["vyper"]
	if len(v) != 3 || v[0] != 0 || v[1] != 4 || v[2] != 3 {
		return 0, 0, 0, false
	}
	if runtimeSize > maxMatcherBytecodeBytes || immutableSize > maxMatcherBytecodeBytes {
		return 0, 0, 0, false
	}
	return start, runtimeSize, immutableSize, true
}

func vyperContractDocuments(output json.RawMessage) (map[string]json.RawMessage, error) {
	doc, err := decodeRawJSONObject(output)
	if err != nil {
		return nil, errCompilerOutputMalformed
	}
	if err := validateCompilerDiagnostics(doc["errors"]); err != nil {
		if errors.Is(err, errCompilerOutputDiagnostic) {
			return nil, CompilationFailure{Message: "compiler reported an error"}
		}
		return nil, err
	}
	contracts, err := decodeRawJSONObject(doc["contracts"])
	if err != nil || len(contracts) != 1 {
		return nil, errCompilerOutputMalformed
	}
	result := map[string]json.RawMessage{}
	for file, raw := range contracts {
		if !validGeasSourcePath(file) || !strings.HasSuffix(file, ".vy") {
			return nil, errCompilerOutputMalformed
		}
		name := strings.TrimSuffix(path.Base(file), ".vy")
		if name == "" || len(name) > 256 {
			return nil, errCompilerOutputMalformed
		}
		candidates, err := decodeRawJSONObject(raw)
		if err != nil || len(candidates) != 1 || len(candidates[name]) == 0 {
			return nil, errCompilerOutputMalformed
		}
		result[file+"\x00"+name] = candidates[name]
	}
	return result, nil
}

func extractVyperCandidates(firstOutput, secondOutput json.RawMessage, version string) ([]CandidateArtifact, error) {
	if version != VyperCompilerVersion {
		return nil, errCompilerOutputMalformed
	}
	for _, raw := range []json.RawMessage{firstOutput, secondOutput} {
		var identity struct {
			Compiler string `json:"compiler"`
		}
		if json.Unmarshal(raw, &identity) != nil || identity.Compiler != "vyper-0.4.3" {
			return nil, errCompilerOutputMalformed
		}
	}
	first, err := vyperContractDocuments(firstOutput)
	if err != nil {
		return nil, err
	}
	second, err := vyperContractDocuments(secondOutput)
	if err != nil {
		return nil, err
	}
	names := sortedKeys(first)
	if !equalStrings(names, sortedKeys(second)) || len(names) != 1 {
		return nil, errCompilerOutputMalformed
	}
	var result []CandidateArtifact
	for _, name := range names {
		a, _, err := parseCandidateContract(first[name], LanguageVyper, false)
		if err != nil {
			return nil, err
		}
		b, _, err := parseCandidateContract(second[name], LanguageVyper, false)
		if err != nil {
			return nil, err
		}
		if len(a.creationLinks)+len(a.runtimeLinks)+len(a.runtimeImmutables)+len(b.creationLinks)+len(b.runtimeLinks)+len(b.runtimeImmutables) != 0 {
			return nil, errCompilerOutputMalformed
		}
		var da, db map[string]json.RawMessage
		if json.Unmarshal(first[name], &da) != nil || json.Unmarshal(second[name], &db) != nil {
			return nil, errCompilerOutputMalformed
		}
		if !equalJSONValue(da["abi"], db["abi"]) || !equalJSONValue(da["layout"], db["layout"]) || !jsonObject(da["metadata"]) || !equalJSONValue(da["metadata"], db["metadata"]) {
			return nil, errCompilerOutputMalformed
		}
		if len(a.creationBytes) != len(b.creationBytes) || !bytes.Equal(a.runtimeBytes, b.runtimeBytes) {
			return nil, errors.New("vyper compilation changed code shape")
		}
		immutableSize, refs, err := vyperImmutableLayout(da["layout"], len(a.runtimeBytes))
		if err != nil {
			return nil, err
		}
		start, rs, is, has := vyperFooter(a.creationBytes)
		other, brs, bis, bhas := vyperFooter(b.creationBytes)
		if has != bhas {
			return nil, errCompilerOutputMalformed
		}
		a.creationAuxdata = map[string]AuxdataValue{}
		if has {
			if !stableVyperFooter(a.creationBytes[start:], b.creationBytes[other:]) || start != other || rs != brs || is != bis || rs != uint64(len(a.runtimeBytes)) || is != immutableSize || !bytes.Equal(a.creationBytes[:start], b.creationBytes[:other]) {
				return nil, errCompilerOutputMalformed
			}
			a.creationAuxdata["1"] = AuxdataValue{Offset: uint64(start), Value: "0x" + hex.EncodeToString(a.creationBytes[start:])}
		} else if !bytes.Equal(a.creationBytes, b.creationBytes) {
			return nil, errCompilerOutputMalformed
		}
		a.runtimeBytes = append(a.runtimeBytes, make([]byte, int(immutableSize))...)
		a.RuntimeBytecode = "0x" + hex.EncodeToString(a.runtimeBytes)
		a.runtimeImmutables = refs
		a.RuntimeCodeArtifacts = mergeArtifactField(a.RuntimeCodeArtifacts, "immutableReferences", refs)
		a.CompilationArtifacts = mergeArtifactField(a.CompilationArtifacts, "layout", json.RawMessage(da["layout"]))
		a.CompilationArtifacts = mergeArtifactField(a.CompilationArtifacts, "metadata", json.RawMessage(da["metadata"]))
		a.CreationCodeArtifacts = mergeAuxdataArtifact(a.CreationCodeArtifacts, a.creationAuxdata)
		parts := strings.SplitN(name, "\x00", 2)
		a.FileName, a.ContractName = parts[0], parts[1]
		a.Language = LanguageVyper
		a.CompilerVersion = version
		result = append(result, a)
	}
	return result, nil
}

func stableVyperFooter(first, second []byte) bool {
	var a, b []cbor.RawMessage
	if len(first) < 3 || len(second) < 3 || matcherCBORMode.Unmarshal(first[:len(first)-2], &a) != nil || matcherCBORMode.Unmarshal(second[:len(second)-2], &b) != nil || len(a) != 5 || len(b) != 5 {
		return false
	}
	for i := 1; i < 5; i++ {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func equalJSONValue(a, b json.RawMessage) bool {
	var first, second any
	if json.Unmarshal(a, &first) != nil || json.Unmarshal(b, &second) != nil {
		return false
	}
	x, _ := json.Marshal(first)
	y, _ := json.Marshal(second)
	return bytes.Equal(x, y)
}

func vyperImmutableLayout(raw json.RawMessage, runtimeBytes int) (uint64, map[string][]bytecodeRange, error) {
	var layout struct {
		Code json.RawMessage `json:"code_layout"`
	}
	if !jsonObject(raw) || json.Unmarshal(raw, &layout) != nil {
		return 0, nil, errCompilerOutputMalformed
	}
	refs := map[string][]bytecodeRange{}
	var ranges []bytecodeRange
	if len(layout.Code) > 0 {
		if err := walkVyperLayout(layout.Code, "", 0, uint64(runtimeBytes), refs, &ranges); err != nil {
			return 0, nil, err
		}
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Start < ranges[j].Start })
	var size uint64
	for _, span := range ranges {
		if span.Start != size {
			return 0, nil, errCompilerOutputMalformed
		}
		size += span.Length
	}
	if size+uint64(runtimeBytes) > maxMatcherBytecodeBytes {
		return 0, nil, errCompilerOutputMalformed
	}
	return size, refs, nil
}

func walkVyperLayout(raw json.RawMessage, prefix string, depth int, runtimeSize uint64, refs map[string][]bytecodeRange, ranges *[]bytecodeRange) error {
	if depth > 32 || len(refs) > 4096 {
		return errCompilerOutputMalformed
	}
	entries, err := decodeRawJSONObject(raw)
	if err != nil || len(entries) > 4096 {
		return errCompilerOutputMalformed
	}
	for name, value := range entries {
		if name == "" || strings.Contains(name, ".") {
			return errCompilerOutputMalformed
		}
		id := name
		if prefix != "" {
			id = prefix + "." + name
		}
		if len(id) > 512 {
			return errCompilerOutputMalformed
		}
		fields, err := decodeRawJSONObject(value)
		if err != nil {
			return errCompilerOutputMalformed
		}
		var typeName string
		if json.Unmarshal(fields["type"], &typeName) != nil {
			if err := walkVyperLayout(value, id, depth+1, runtimeSize, refs, ranges); err != nil {
				return err
			}
			continue
		}
		var item struct {
			Offset *uint64 `json:"offset"`
			Length uint64  `json:"length"`
			Type   string  `json:"type"`
		}
		if len(fields) != 3 || json.Unmarshal(value, &item) != nil || item.Offset == nil || item.Type == "" || item.Length == 0 || item.Length%32 != 0 {
			return errCompilerOutputMalformed
		}
		offset := *item.Offset
		if offset%32 != 0 || offset > maxMatcherBytecodeBytes || item.Length > maxMatcherBytecodeBytes-offset {
			return errCompilerOutputMalformed
		}
		*ranges = append(*ranges, bytecodeRange{Start: offset, Length: item.Length})
		refs[id] = []bytecodeRange{{Start: runtimeSize + offset, Length: item.Length}}
		if len(refs) > 4096 {
			return errCompilerOutputMalformed
		}
	}
	return nil
}
