package enrich

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
)

type ProxyKind string

const (
	ProxyMinimal1167 ProxyKind = "eip1167"
	ProxyEIP1967     ProxyKind = "eip1967"
	ProxyBeacon      ProxyKind = "beacon"
)

var (
	EIP1967ImplementationSlot = mustWord("360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc")
	EIP1967BeaconSlot         = mustWord("a3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50")
	EIP1967AdminSlot          = mustWord("00b53127684a568b3173ae13b9f8a6016e019a3678ee1178d6a717850b5d6103")
)

var (
	minimalProxyPrefix = mustHex("363d3d373d3d3d363d73")
	minimalProxySuffix = mustHex("5af43d82803e903d91602b57fd5bf3")
)

type MinimalProxy struct {
	Implementation Address
	Exact          bool
	TrailingData   []byte
}

// DetectEIP1167 recognizes the canonical 45-byte runtime and clones that append
// immutable arguments. Prefix-only lookalikes are rejected.
func DetectEIP1167(code []byte) (MinimalProxy, bool) {
	minimum := len(minimalProxyPrefix) + len(Address{}) + len(minimalProxySuffix)
	if len(code) < minimum || !bytes.Equal(code[:len(minimalProxyPrefix)], minimalProxyPrefix) {
		return MinimalProxy{}, false
	}
	addressStart := len(minimalProxyPrefix)
	suffixStart := addressStart + len(Address{})
	if !bytes.Equal(code[suffixStart:suffixStart+len(minimalProxySuffix)], minimalProxySuffix) {
		return MinimalProxy{}, false
	}
	var implementation Address
	copy(implementation[:], code[addressStart:suffixStart])
	trailing := append([]byte(nil), code[suffixStart+len(minimalProxySuffix):]...)
	return MinimalProxy{Implementation: implementation, Exact: len(trailing) == 0, TrailingData: trailing}, true
}

type ProxyReference struct {
	Kind       ProxyKind
	Target     Address
	Slot       Word
	Confidence Confidence
}

// ParseEIP1967Storage returns independent implementation and beacon evidence.
// A zero storage word means that evidence is absent.
func ParseEIP1967Storage(implementationWord, beaconWord Word) ([]ProxyReference, error) {
	var references []ProxyReference
	if !implementationWord.IsZero() {
		implementation, err := AddressFromWord(implementationWord)
		if err != nil {
			return nil, fmt.Errorf("implementation slot: %w", err)
		}
		if implementation != (Address{}) {
			references = append(references, ProxyReference{
				Kind:       ProxyEIP1967,
				Target:     implementation,
				Slot:       EIP1967ImplementationSlot,
				Confidence: ConfidenceHigh,
			})
		}
	}
	if !beaconWord.IsZero() {
		beacon, err := AddressFromWord(beaconWord)
		if err != nil {
			return nil, fmt.Errorf("beacon slot: %w", err)
		}
		if beacon != (Address{}) {
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
func ParseBeaconImplementation(data []byte) (Address, error) {
	if len(data) != 32 {
		return Address{}, fmt.Errorf("beacon implementation response is %d bytes; want 32", len(data))
	}
	word, _ := WordFromBytes(data)
	address, err := AddressFromWord(word)
	if err != nil {
		return Address{}, err
	}
	if address == (Address{}) {
		return Address{}, errors.New("beacon returned the zero implementation address")
	}
	return address, nil
}

type ProxyObservation struct {
	BlockNumber uint64
	Proxy       Address
	CodeHash    Word
	Reference   ProxyReference
}

type ProxyVersion struct {
	FromBlock    uint64
	ThroughBlock *uint64
	Proxy        Address
	CodeHash     Word
	Reference    ProxyReference
}

// ApplyProxyObservation updates an append-only block-range timeline. Repeating
// the same observation is idempotent; a changed target closes the prior range.
func ApplyProxyObservation(versions []ProxyVersion, observation ProxyObservation) ([]ProxyVersion, bool, error) {
	if observation.Proxy == (Address{}) || observation.CodeHash.IsZero() || observation.Reference.Target == (Address{}) {
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

func mustWord(value string) Word {
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
