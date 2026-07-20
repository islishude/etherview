package verify

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type Artifact struct {
	CreationBytecode string
	RuntimeBytecode  string
	ABI              json.RawMessage
	Metadata         string
}

// ExtractArtifact reads the common Solidity/Vyper Standard JSON output shape.
func ExtractArtifact(output json.RawMessage, identifier string) (Artifact, error) {
	separator := strings.LastIndex(identifier, ":")
	if separator <= 0 || separator == len(identifier)-1 {
		return Artifact{}, errors.New("contract identifier must be source:name")
	}
	source, name := identifier[:separator], identifier[separator+1:]
	var document struct {
		Contracts map[string]map[string]struct {
			ABI      json.RawMessage `json:"abi"`
			Metadata string          `json:"metadata"`
			EVM      struct {
				Bytecode struct {
					Object string `json:"object"`
				} `json:"bytecode"`
				DeployedBytecode struct {
					Object string `json:"object"`
				} `json:"deployedBytecode"`
			} `json:"evm"`
		} `json:"contracts"`
		Errors []struct {
			Severity         string `json:"severity"`
			FormattedMessage string `json:"formattedMessage"`
			Message          string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(output, &document); err != nil {
		return Artifact{}, fmt.Errorf("decode compiler output: %w", err)
	}
	for _, diagnostic := range document.Errors {
		if strings.EqualFold(diagnostic.Severity, "error") {
			message := diagnostic.FormattedMessage
			if message == "" {
				message = diagnostic.Message
			}
			return Artifact{}, fmt.Errorf("compiler error: %s", strings.TrimSpace(message))
		}
	}
	contracts, ok := document.Contracts[source]
	if !ok {
		return Artifact{}, fmt.Errorf("compiler output has no source %q", source)
	}
	contract, ok := contracts[name]
	if !ok {
		return Artifact{}, fmt.Errorf("compiler output has no contract %q in %q", name, source)
	}
	return Artifact{
		CreationBytecode: prefixHex(contract.EVM.Bytecode.Object),
		RuntimeBytecode:  prefixHex(contract.EVM.DeployedBytecode.Object),
		ABI:              contract.ABI,
		Metadata:         contract.Metadata,
	}, nil
}

func MatchBytecode(onchain, compiled string) (MatchKind, error) {
	onchainBytes, err := decodeBytecode(onchain)
	if err != nil {
		return MatchMismatch, fmt.Errorf("decode on-chain bytecode: %w", err)
	}
	compiledBytes, err := decodeBytecode(compiled)
	if err != nil {
		return MatchMismatch, fmt.Errorf("decode compiled bytecode: %w", err)
	}
	if string(onchainBytes) == string(compiledBytes) {
		return MatchExact, nil
	}
	onchainCore, onchainMetadata := stripCBORMetadata(onchainBytes)
	compiledCore, compiledMetadata := stripCBORMetadata(compiledBytes)
	if onchainMetadata && compiledMetadata && string(onchainCore) == string(compiledCore) {
		return MatchMetadataOnly, nil
	}
	return MatchMismatch, nil
}

func MatchArtifact(request Request, artifact Artifact) (MatchResult, error) {
	creation, err := MatchBytecode(request.CreationBytecode, artifact.CreationBytecode)
	if err != nil {
		return MatchResult{}, err
	}
	runtime, err := MatchBytecode(request.RuntimeBytecode, artifact.RuntimeBytecode)
	if err != nil {
		return MatchResult{}, err
	}
	return MatchResult{Creation: creation, Runtime: runtime}, nil
}

func stripCBORMetadata(bytecode []byte) ([]byte, bool) {
	if len(bytecode) < 4 {
		return bytecode, false
	}
	metadataLength := int(binary.BigEndian.Uint16(bytecode[len(bytecode)-2:]))
	start := len(bytecode) - 2 - metadataLength
	if metadataLength < 2 || start < 0 {
		return bytecode, false
	}
	// Solidity/Vyper metadata is a CBOR map; major type 5 occupies 0xa0-0xbf.
	if bytecode[start]&0xe0 != 0xa0 {
		return bytecode, false
	}
	return bytecode[:start], true
}

func decodeBytecode(value string) ([]byte, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "0x")
	if len(value)%2 != 0 {
		return nil, errors.New("hex bytecode has odd length")
	}
	if strings.ContainsAny(value, "_$") {
		return nil, errors.New("bytecode contains unresolved link placeholders")
	}
	return hex.DecodeString(value)
}

func prefixHex(value string) string {
	if strings.HasPrefix(value, "0x") {
		return value
	}
	return "0x" + value
}
