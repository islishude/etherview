// Package verify implements reproducible, asynchronous contract verification
// primitives. Runtime services must execute public compilers in a hard sandbox.
package verify

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"strings"

	"golang.org/x/crypto/sha3"
)

type Language string

const (
	LanguageSolidity Language = "solidity"
	LanguageVyper    Language = "vyper"
)

var versionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]{0,127}$`)

var (
	ErrSandboxRequired = errors.New("public verification requires hard compiler isolation")
	ErrConsentRequired = errors.New("sourcify submission requires explicit consent")
)

type Request struct {
	ChainID            uint64          `json:"chain_id"`
	Address            string          `json:"address"`
	CodeHash           string          `json:"code_hash"`
	AtBlockHash        string          `json:"at_block_hash"`
	Language           Language        `json:"language"`
	CompilerVersion    string          `json:"compiler_version"`
	ContractIdentifier string          `json:"contract_identifier"`
	StandardJSON       json.RawMessage `json:"standard_json"`
	CreationBytecode   string          `json:"creation_bytecode"`
	RuntimeBytecode    string          `json:"runtime_bytecode"`
	ConstructorArgs    string          `json:"constructor_arguments,omitempty"`
	LicenseType        string          `json:"license_type,omitempty"`
	SubmitToSourcify   bool            `json:"submit_to_sourcify"`
}

func (r Request) Validate(maxInputBytes int) error {
	var errs []error
	if r.ChainID == 0 {
		errs = append(errs, errors.New("chain ID is required"))
	}
	if !fixedHex(r.Address, 20) {
		errs = append(errs, errors.New("address must be 20 bytes"))
	}
	var codeHash []byte
	if !fixedHex(r.CodeHash, 32) {
		errs = append(errs, errors.New("code hash must be 32 bytes"))
	} else {
		codeHash, _ = decodeBytecode(r.CodeHash)
	}
	if !fixedHex(r.AtBlockHash, 32) {
		errs = append(errs, errors.New("block hash must be 32 bytes"))
	}
	if r.Language != LanguageSolidity && r.Language != LanguageVyper {
		errs = append(errs, errors.New("language must be solidity or vyper"))
	}
	if !versionPattern.MatchString(r.CompilerVersion) {
		errs = append(errs, errors.New("compiler version is invalid"))
	}
	separator := strings.LastIndex(r.ContractIdentifier, ":")
	if len(r.ContractIdentifier) > 512 || separator <= 0 || separator == len(r.ContractIdentifier)-1 {
		errs = append(errs, errors.New("contract identifier must be source:name"))
	}
	if maxInputBytes <= 0 {
		maxInputBytes = 5 << 20
	}
	if _, _, err := validateStandardJSON(
		r.StandardJSON,
		r.Language,
		r.CompilerVersion,
		r.ContractIdentifier,
		maxInputBytes,
	); err != nil {
		errs = append(errs, err)
	}
	if len(r.CreationBytecode) > maxInputBytes {
		errs = append(errs, errors.New("creation bytecode exceeds the input limit"))
	} else if _, err := decodeBytecode(r.CreationBytecode); err != nil {
		errs = append(errs, errors.New("creation bytecode must be hexadecimal without unresolved links"))
	}
	if len(r.RuntimeBytecode) > maxInputBytes {
		errs = append(errs, errors.New("runtime bytecode exceeds the input limit"))
	} else if runtimeBytecode, err := decodeBytecode(r.RuntimeBytecode); err != nil {
		errs = append(errs, errors.New("runtime bytecode must be hexadecimal without unresolved links"))
	} else if len(runtimeBytecode) == 0 {
		errs = append(errs, errors.New("runtime bytecode must not be empty"))
	} else if len(codeHash) == 32 {
		if !bytes.Equal(keccak256Bytes(runtimeBytecode), codeHash) {
			errs = append(errs, errors.New("code hash must equal the keccak256 runtime bytecode hash"))
		}
	}
	if len(r.ConstructorArgs) > maxInputBytes {
		errs = append(errs, errors.New("constructor arguments exceed the input limit"))
	} else if _, err := decodeBytecode(r.ConstructorArgs); err != nil {
		errs = append(errs, errors.New("constructor arguments must be hexadecimal"))
	}
	if r.LicenseType != "" {
		license, ok := new(big.Int).SetString(r.LicenseType, 10)
		if !ok || license.Sign() <= 0 || license.Cmp(big.NewInt(14)) > 0 || license.String() != r.LicenseType {
			errs = append(errs, errors.New("license type must be a canonical integer between 1 and 14"))
		}
	}
	return errors.Join(errs...)
}

func keccak256Bytes(value []byte) []byte {
	hasher := sha3.NewLegacyKeccak256()
	_, _ = hasher.Write(value)
	return hasher.Sum(nil)
}

type MatchKind string

const (
	MatchExact        MatchKind = "exact"
	MatchMetadataOnly MatchKind = "metadata_only"
	MatchMismatch     MatchKind = "mismatch"
)

type MatchResult struct {
	Creation MatchKind `json:"creation"`
	Runtime  MatchKind `json:"runtime"`
}

func validMatchKind(kind MatchKind) bool {
	return kind == MatchExact || kind == MatchMetadataOnly || kind == MatchMismatch
}

func validMatchResult(result MatchResult) bool {
	return validMatchKind(result.Creation) && validMatchKind(result.Runtime)
}

func jsonObject(value json.RawMessage) bool {
	value = bytes.TrimSpace(value)
	return len(value) >= 2 && value[0] == '{' && value[len(value)-1] == '}' && json.Valid(value)
}

func jsonArray(value json.RawMessage) bool {
	value = bytes.TrimSpace(value)
	return len(value) >= 2 && value[0] == '[' && value[len(value)-1] == ']' && json.Valid(value)
}

func fixedHex(value string, bytes int) bool {
	if len(value) != 2+bytes*2 || !strings.HasPrefix(value, "0x") {
		return false
	}
	for _, char := range value[2:] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') && (char < 'A' || char > 'F') {
			return false
		}
	}
	return true
}
