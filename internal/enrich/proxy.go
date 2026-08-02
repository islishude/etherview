package enrich

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
)

type ProxyKind string

type ProxyPattern string

const (
	ProxyMinimal1167 ProxyKind = "eip1167"
	ProxyEIP1967     ProxyKind = "eip1967"
	ProxyBeacon      ProxyKind = "beacon"

	ProxyPatternClone       ProxyPattern = "clone"
	ProxyPatternERC1967     ProxyPattern = "erc1967"
	ProxyPatternTransparent ProxyPattern = "transparent"
	ProxyPatternUUPS        ProxyPattern = "uups"
	ProxyPatternBeacon      ProxyPattern = "beacon"
	ProxyPatternUnknown     ProxyPattern = "unknown"
)

const (
	OpenZeppelin561Standard = "5.6.1"
	MaxCloneImmutableArgs   = 0x5fd3
)

var (
	EIP1967ImplementationSlot = mustWord("360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc")
	EIP1967BeaconSlot         = mustWord("a3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50")
	EIP1967AdminSlot          = mustWord("b53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103")
)

var (
	minimalProxyPrefix = mustHex("363d3d373d3d3d363d73")
	minimalProxySuffix = mustHex("5af43d82803e903d91602b57fd5bf3")
	ozCloneInitPrefix  = mustHex("3d81600a3d39f3")
)

type MinimalProxy struct {
	Implementation        common.Address
	Exact                 bool
	OpenZeppelinImmutable bool
	ImmutableArgsTooLarge bool
	TrailingData          []byte
}

// DetectEIP1167 parses the actual deployed runtime. A trailing payload is only
// a clone-with-immutable-args candidate: the bytes alone do not authenticate
// that OpenZeppelin creation code returned them.
func DetectEIP1167(code []byte) (MinimalProxy, bool) {
	minimum := len(minimalProxyPrefix) + common.AddressLength + len(minimalProxySuffix)
	if len(code) < minimum {
		return MinimalProxy{}, false
	}
	implementation, ok := parseMinimalProxyRuntime(code[:minimum])
	if !ok {
		return MinimalProxy{}, false
	}
	if len(code)-minimum > MaxCloneImmutableArgs {
		return MinimalProxy{
			Implementation: implementation, ImmutableArgsTooLarge: true,
		}, true
	}
	return MinimalProxy{
		Implementation: implementation, Exact: len(code) == minimum,
		TrailingData: append([]byte(nil), code[minimum:]...),
	}, true
}

// AuthenticateOpenZeppelinImmutableClone checks the exact OpenZeppelin 5.6.1
// initcode header and proves that CREATE returned the observed runtime. This is
// the evidence needed to promote trailing runtime bytes to immutable args.
func AuthenticateOpenZeppelinImmutableClone(initcode, runtime []byte) bool {
	const creationHeaderBytes = 10
	if len(runtime) <= len(minimalProxyPrefix)+common.AddressLength+len(minimalProxySuffix) ||
		len(initcode) != creationHeaderBytes+len(runtime) || initcode[0] != 0x61 ||
		!bytes.Equal(initcode[3:creationHeaderBytes], ozCloneInitPrefix) ||
		!bytes.Equal(initcode[creationHeaderBytes:], runtime) {
		return false
	}
	return int(binary.BigEndian.Uint16(initcode[1:3])) == len(runtime)
}

func parseMinimalProxyRuntime(code []byte) (common.Address, bool) {
	minimum := len(minimalProxyPrefix) + common.AddressLength + len(minimalProxySuffix)
	if len(code) != minimum || !bytes.Equal(code[:len(minimalProxyPrefix)], minimalProxyPrefix) {
		return common.Address{}, false
	}
	addressStart := len(minimalProxyPrefix)
	suffixStart := addressStart + common.AddressLength
	if !bytes.Equal(code[suffixStart:], minimalProxySuffix) {
		return common.Address{}, false
	}
	return common.BytesToAddress(code[addressStart:suffixStart]), true
}

type ProxyReference struct {
	Kind       ProxyKind
	Target     common.Address
	Slot       common.Hash
	Confidence Confidence
}

// ParseEIP1967Storage returns independent implementation and beacon evidence.
// A zero storage word means that evidence is absent.
func ParseEIP1967Storage(implementationWord, beaconWord common.Hash) ([]ProxyReference, error) {
	var references []ProxyReference
	if implementationWord != (common.Hash{}) {
		implementation, err := AddressFromWord(implementationWord)
		if err != nil {
			return nil, fmt.Errorf("implementation slot: %w", err)
		}
		if implementation != (common.Address{}) {
			references = append(references, ProxyReference{
				Kind:       ProxyEIP1967,
				Target:     implementation,
				Slot:       EIP1967ImplementationSlot,
				Confidence: ConfidenceHigh,
			})
		}
	}
	if beaconWord != (common.Hash{}) {
		beacon, err := AddressFromWord(beaconWord)
		if err != nil {
			return nil, fmt.Errorf("beacon slot: %w", err)
		}
		if beacon != (common.Address{}) {
			references = append(references, ProxyReference{
				Kind:       ProxyBeacon,
				Target:     beacon,
				Slot:       EIP1967BeaconSlot,
				Confidence: ConfidenceHigh,
			})
		}
	}
	return references, nil
}

// ParseBeaconImplementation decodes the standard implementation() return.
func ParseBeaconImplementation(data []byte) (common.Address, error) {
	if len(data) != 32 {
		return common.Address{}, fmt.Errorf("beacon implementation response is %d bytes; want 32", len(data))
	}
	word, _ := WordFromBytes(data)
	address, err := AddressFromWord(word)
	if err != nil {
		return common.Address{}, err
	}
	if address == (common.Address{}) {
		return common.Address{}, errors.New("beacon returned the zero implementation address")
	}
	return address, nil
}

// ParseUUPSProxiableUUID requires the exact ERC-1822 response used by
// OpenZeppelin's UUPSUpgradeable implementation. Extra or truncated bytes are
// rejected rather than being treated as a compatible implementation.
func ParseUUPSProxiableUUID(data []byte) error {
	if len(data) != common.HashLength {
		return fmt.Errorf("UUPS proxiableUUID response is %d bytes; want 32", len(data))
	}
	word, _ := WordFromBytes(data)
	if word != EIP1967ImplementationSlot {
		return errors.New("UUPS proxiableUUID returned a different storage slot")
	}
	return nil
}

// ParseUUPSInterfaceVersion strictly decodes the no-argument Solidity string
// return. OpenZeppelin 5.x advertises the 5.0.0 upgrade interface.
func ParseUUPSInterfaceVersion(data []byte) error {
	if len(data) != 96 {
		return fmt.Errorf("UUPS interface version response is %d bytes; want 96", len(data))
	}
	offset, _ := WordFromBytes(data[:32])
	length, _ := WordFromBytes(data[32:64])
	var canonicalOffset common.Hash
	canonicalOffset[31] = 32
	if offset != canonicalOffset {
		return errors.New("UUPS interface version has a non-canonical offset")
	}
	var canonicalLength common.Hash
	canonicalLength[31] = 5
	if length != canonicalLength || string(data[64:69]) != "5.0.0" {
		return errors.New("UUPS interface version is not 5.0.0")
	}
	for _, value := range data[69:] {
		if value != 0 {
			return errors.New("UUPS interface version has non-zero padding")
		}
	}
	return nil
}

type ProxyObservation struct {
	BlockNumber uint64
	Proxy       common.Address
	CodeHash    common.Hash
	Reference   ProxyReference
}

type ProxyVersion struct {
	FromBlock    uint64
	ThroughBlock *uint64
	Proxy        common.Address
	CodeHash     common.Hash
	Reference    ProxyReference
}

// ApplyProxyObservation updates an append-only block-range timeline. Repeating
// the same observation is idempotent; a changed target closes the prior range.
func ApplyProxyObservation(versions []ProxyVersion, observation ProxyObservation) ([]ProxyVersion, bool, error) {
	if observation.Proxy == (common.Address{}) || observation.CodeHash == (common.Hash{}) || observation.Reference.Target == (common.Address{}) {
		return nil, false, errors.New("proxy observation is missing proxy, code hash, or target")
	}
	if observation.Reference.Kind == "" {
		return nil, false, errors.New("proxy observation kind is empty")
	}
	updated := append([]ProxyVersion(nil), versions...)
	if len(updated) == 0 {
		return append(updated, ProxyVersion{
			FromBlock: observation.BlockNumber,
			Proxy:     observation.Proxy,
			CodeHash:  observation.CodeHash,
			Reference: observation.Reference,
		}), true, nil
	}
	last := &updated[len(updated)-1]
	if last.ThroughBlock != nil {
		return nil, false, errors.New("proxy timeline has no open version")
	}
	if last.Proxy != observation.Proxy {
		return nil, false, errors.New("proxy observation address differs from timeline")
	}
	if observation.BlockNumber < last.FromBlock {
		return nil, false, errors.New("proxy observation predates current version")
	}
	same := last.CodeHash == observation.CodeHash && last.Reference.Kind == observation.Reference.Kind && last.Reference.Target == observation.Reference.Target
	if same {
		return updated, false, nil
	}
	if observation.BlockNumber == last.FromBlock {
		return nil, false, errors.New("conflicting proxy observations at the same block")
	}
	through := observation.BlockNumber - 1
	last.ThroughBlock = &through
	updated = append(updated, ProxyVersion{
		FromBlock: observation.BlockNumber,
		Proxy:     observation.Proxy,
		CodeHash:  observation.CodeHash,
		Reference: observation.Reference,
	})
	return updated, true, nil
}

func mustWord(value string) common.Hash {
	decoded := mustHex(value)
	word, err := WordFromBytes(decoded)
	if err != nil {
		panic(err)
	}
	return word
}

func mustHex(value string) []byte {
	decoded, err := hex.DecodeString(value)
	if err != nil {
		panic(err)
	}
	return decoded
}
