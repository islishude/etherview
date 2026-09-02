// Package erc4337 owns the bounded, versioned EntryPoint wire contract used by
// the canonical UserOperation index.
package erc4337

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/config"
)

const (
	StageName          = "userop"
	StageVersion       = uint32(1)
	maximumEntryPoints = 16
)

type Version string

const (
	Version06 Version = "0.6"
	Version07 Version = "0.7"
	Version08 Version = "0.8"
	Version09 Version = "0.9"
)

func (version Version) Valid() bool {
	switch version {
	case Version06, Version07, Version08, Version09:
		return true
	default:
		return false
	}
}

type EntryPoint struct {
	Address   common.Address
	Version   Version
	FromBlock uint64
	ToBlock   uint64
	HasEnd    bool
}

func (entry EntryPoint) Active(block uint64) bool {
	return block >= entry.FromBlock && (!entry.HasEnd || block <= entry.ToBlock)
}

type Registry struct {
	entries []EntryPoint
	digest  [sha256.Size]byte
}

func NewRegistry(raw config.ERC4337Config) (Registry, error) {
	if len(raw.EntryPoints) > maximumEntryPoints {
		return Registry{}, fmt.Errorf("entry point registry exceeds %d entries", maximumEntryPoints)
	}
	entries := make([]EntryPoint, len(raw.EntryPoints))
	for index, item := range raw.EntryPoints {
		version := Version(item.Version)
		if !common.IsHexAddress(item.Address) || common.HexToAddress(item.Address) == (common.Address{}) {
			return Registry{}, fmt.Errorf("entry point %d has an invalid address", index)
		}
		if !version.Valid() {
			return Registry{}, fmt.Errorf("entry point %d has unsupported version %q", index, version)
		}
		entry := EntryPoint{
			Address: common.HexToAddress(item.Address), Version: version,
			FromBlock: item.FromBlock,
		}
		if item.ToBlock != nil {
			if *item.ToBlock < item.FromBlock {
				return Registry{}, fmt.Errorf("entry point %d has an invalid block range", index)
			}
			entry.ToBlock, entry.HasEnd = *item.ToBlock, true
		}
		entries[index] = entry
	}
	slices.SortFunc(entries, compareEntryPoints)
	for index := 1; index < len(entries); index++ {
		previous, current := entries[index-1], entries[index]
		if previous.Address != current.Address {
			continue
		}
		previousEnd := ^uint64(0)
		if previous.HasEnd {
			previousEnd = previous.ToBlock
		}
		if current.FromBlock <= previousEnd {
			return Registry{}, errors.New("entry point ranges overlap")
		}
	}
	digest, err := digestEntryPoints(entries)
	if err != nil {
		return Registry{}, err
	}
	return Registry{entries: entries, digest: digest}, nil
}

func compareEntryPoints(left, right EntryPoint) int {
	if compared := bytes.Compare(left.Address[:], right.Address[:]); compared != 0 {
		return compared
	}
	if left.FromBlock < right.FromBlock {
		return -1
	}
	if left.FromBlock > right.FromBlock {
		return 1
	}
	if left.Version < right.Version {
		return -1
	}
	if left.Version > right.Version {
		return 1
	}
	return 0
}

func digestEntryPoints(entries []EntryPoint) ([sha256.Size]byte, error) {
	hash := sha256.New()
	if _, err := hash.Write([]byte("etherview:erc4337-entrypoints:v1\x00")); err != nil {
		return [sha256.Size]byte{}, err
	}
	if err := binary.Write(hash, binary.BigEndian, uint16(len(entries))); err != nil {
		return [sha256.Size]byte{}, err
	}
	for _, entry := range entries {
		if _, err := hash.Write(entry.Address[:]); err != nil {
			return [sha256.Size]byte{}, err
		}
		if _, err := hash.Write([]byte{byte(len(entry.Version))}); err != nil {
			return [sha256.Size]byte{}, err
		}
		if _, err := hash.Write([]byte(entry.Version)); err != nil {
			return [sha256.Size]byte{}, err
		}
		if err := binary.Write(hash, binary.BigEndian, entry.FromBlock); err != nil {
			return [sha256.Size]byte{}, err
		}
		if entry.HasEnd {
			if _, err := hash.Write([]byte{1}); err != nil {
				return [sha256.Size]byte{}, err
			}
			if err := binary.Write(hash, binary.BigEndian, entry.ToBlock); err != nil {
				return [sha256.Size]byte{}, err
			}
		} else if _, err := hash.Write([]byte{0}); err != nil {
			return [sha256.Size]byte{}, err
		}
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func (registry Registry) Digest() [sha256.Size]byte { return registry.digest }

func (registry Registry) DigestHex() string { return fmt.Sprintf("%x", registry.digest) }

func (registry Registry) Entries() []EntryPoint { return slices.Clone(registry.entries) }

func (registry Registry) Match(address common.Address, block uint64) (EntryPoint, bool) {
	for _, entry := range registry.entries {
		if entry.Address == address && entry.Active(block) {
			return entry, true
		}
	}
	return EntryPoint{}, false
}

func (registry Registry) StartBlock(chainStart uint64) uint64 {
	start := ^uint64(0)
	for _, entry := range registry.entries {
		if entry.FromBlock < start {
			start = entry.FromBlock
		}
	}
	if start == ^uint64(0) || start < chainStart {
		return chainStart
	}
	return start
}
