package genesis

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"sort"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie"
)

func parseDocument(document []byte, expectedChainID uint64) (*core.Genesis, *types.Block, error) {
	if len(document) == 0 {
		return nil, nil, errors.New("genesis file is empty")
	}
	if err := rejectDuplicateKeys(document); err != nil {
		return nil, nil, err
	}
	if err := preflightGenesisAllocation(document); err != nil {
		return nil, nil, err
	}
	var spec core.Genesis
	if err := decodeOneJSON(document, &spec); err != nil {
		return nil, nil, errors.New("decode genesis document")
	}
	if spec.Config == nil || spec.Alloc == nil {
		return nil, nil, errors.New("genesis document is missing config or alloc")
	}
	if expectedChainID == 0 {
		return nil, nil, errors.New("configured chain ID must be positive")
	}
	if spec.Config.ChainID == nil || spec.Config.ChainID.Sign() <= 0 {
		return nil, nil, errors.New("genesis document chain ID is missing")
	}
	if !isUint256(spec.Config.ChainID) {
		return nil, nil, errors.New("genesis document chain ID is outside uint256")
	}
	if spec.Config.ChainID.Cmp(new(big.Int).SetUint64(expectedChainID)) != 0 {
		return nil, nil, errors.New("genesis document chain ID does not match configured chain")
	}
	if err := validateGenesisUint256(&spec); err != nil {
		return nil, nil, err
	}
	if err := spec.Config.CheckConfigForkOrder(); err != nil {
		return nil, nil, errors.New("genesis document fork order is invalid")
	}
	if spec.Number != 0 || spec.ParentHash != (common.Hash{}) {
		return nil, nil, errors.New("genesis document must describe block zero with a zero parent")
	}
	if len(spec.Alloc) > maximumAccounts {
		return nil, nil, errors.New("genesis allocation exceeds 500000 accounts")
	}
	return &spec, spec.ToBlock(), nil
}

func validateGenesisUint256(spec *core.Genesis) error {
	if !isUint256(spec.Difficulty) {
		return errors.New("genesis difficulty is outside uint256")
	}
	if spec.BaseFee != nil && !isUint256(spec.BaseFee) {
		return errors.New("genesis base fee is outside uint256")
	}
	configValues := []*big.Int{
		spec.Config.HomesteadBlock,
		spec.Config.DAOForkBlock,
		spec.Config.EIP150Block,
		spec.Config.EIP155Block,
		spec.Config.EIP158Block,
		spec.Config.ByzantiumBlock,
		spec.Config.ConstantinopleBlock,
		spec.Config.PetersburgBlock,
		spec.Config.IstanbulBlock,
		spec.Config.MuirGlacierBlock,
		spec.Config.BerlinBlock,
		spec.Config.LondonBlock,
		spec.Config.ArrowGlacierBlock,
		spec.Config.GrayGlacierBlock,
		spec.Config.MergeNetsplitBlock,
		spec.Config.TerminalTotalDifficulty,
	}
	for _, value := range configValues {
		if value != nil && !isUint256(value) {
			return errors.New("genesis chain config contains a value outside uint256")
		}
	}
	for _, account := range spec.Alloc {
		if !isUint256(account.Balance) {
			return errors.New("genesis account balance is outside uint256")
		}
	}
	return nil
}

func isUint256(value *big.Int) bool {
	return value != nil && value.Sign() >= 0 && value.BitLen() <= 256
}

func preflightGenesisAllocation(document []byte) error {
	var root map[string]json.RawMessage
	if err := decodeOneJSON(document, &root); err != nil {
		return errors.New("decode genesis document")
	}
	allocRaw, exists, err := rawJSONField(root, "alloc")
	if err != nil {
		return err
	}
	if !exists || bytes.Equal(allocRaw, []byte("null")) {
		return nil
	}
	var alloc map[string]json.RawMessage
	if err := decodeOneJSON(allocRaw, &alloc); err != nil {
		return errors.New("decode genesis allocation")
	}
	if len(alloc) > maximumAccounts {
		return errors.New("genesis allocation exceeds 500000 accounts")
	}
	seen := make(map[common.Address]struct{}, len(alloc))
	for encodedAddress, accountRaw := range alloc {
		var unprefixed common.UnprefixedAddress
		if err := unprefixed.UnmarshalText([]byte(encodedAddress)); err != nil {
			return errors.New("genesis allocation address is invalid")
		}
		accountAddress := common.Address(unprefixed)
		if _, duplicate := seen[accountAddress]; duplicate {
			return errors.New("genesis allocation contains a duplicate address")
		}
		seen[accountAddress] = struct{}{}
		if err := preflightGenesisStorage(accountRaw); err != nil {
			return err
		}
	}
	return nil
}

func preflightGenesisStorage(accountRaw json.RawMessage) error {
	var account map[string]json.RawMessage
	if err := decodeOneJSON(accountRaw, &account); err != nil {
		return errors.New("decode genesis account")
	}
	storageRaw, exists, err := rawJSONField(account, "storage")
	if err != nil {
		return err
	}
	if !exists || bytes.Equal(storageRaw, []byte("null")) {
		return nil
	}
	var storage map[string]json.RawMessage
	if err := decodeOneJSON(storageRaw, &storage); err != nil {
		return errors.New("decode genesis account storage")
	}
	seen := make(map[common.Hash]struct{}, len(storage))
	for encodedKey := range storage {
		key, err := normalizeStorageKey(encodedKey)
		if err != nil {
			return errors.New("genesis account storage key is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return errors.New("genesis account storage contains a duplicate key")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func rawJSONField(
	object map[string]json.RawMessage,
	expected string,
) (json.RawMessage, bool, error) {
	var (
		value json.RawMessage
		found bool
	)
	for name, candidate := range object {
		if !strings.EqualFold(name, expected) {
			continue
		}
		if found {
			return nil, false, errors.New("genesis JSON contains duplicate semantic fields")
		}
		value = candidate
		found = true
	}
	return value, found, nil
}

func normalizeStorageKey(text string) (common.Hash, error) {
	text = strings.TrimPrefix(strings.TrimPrefix(text, "0x"), "0X")
	if len(text) > common.HashLength*2 {
		return common.Hash{}, errors.New("storage key is too long")
	}
	if len(text)%2 == 1 {
		text = "0" + text
	}
	decoded, err := hex.DecodeString(text)
	if err != nil {
		return common.Hash{}, err
	}
	return common.BytesToHash(decoded), nil
}

type storageTrieEntry struct {
	key   common.Hash
	value common.Hash
}

func storageRoot(storage map[common.Hash]common.Hash) (common.Hash, error) {
	entries := make([]storageTrieEntry, 0, len(storage))
	for key, value := range storage {
		if value == (common.Hash{}) {
			continue
		}
		entries = append(entries, storageTrieEntry{
			key:   crypto.Keccak256Hash(key[:]),
			value: value,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return bytes.Compare(entries[i].key[:], entries[j].key[:]) < 0
	})
	hasher := trie.NewStackTrie(nil)
	for _, entry := range entries {
		encoded, err := rlp.EncodeToBytes(common.TrimLeftZeroes(entry.value[:]))
		if err != nil {
			return common.Hash{}, err
		}
		if err := hasher.Update(entry.key[:], encoded); err != nil {
			return common.Hash{}, err
		}
	}
	return hasher.Hash(), nil
}

func stateRootFromBlock(raw []byte) (common.Hash, error) {
	var wire struct {
		StateRoot string `json:"stateRoot"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil || wire.StateRoot == "" {
		return common.Hash{}, errors.New("stored block zero has invalid state root")
	}
	root, err := parseHashText(wire.StateRoot)
	if err != nil {
		return common.Hash{}, errors.New("stored block zero has invalid state root")
	}
	return root, nil
}

func rejectDuplicateKeys(document []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	var walk func(int) error
	nodes := 0
	walk = func(depth int) error {
		if depth > 128 {
			return errors.New("genesis JSON nesting exceeds 128")
		}
		nodes++
		if nodes > 2_000_000 {
			return errors.New("genesis JSON is too complex")
		}
		token, err := decoder.Token()
		if err != nil {
			return errors.New("decode genesis JSON")
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return errors.New("decode genesis JSON object")
				}
				name, ok := key.(string)
				if !ok {
					return errors.New("decode genesis JSON object key")
				}
				if _, exists := seen[name]; exists {
					return errors.New("genesis JSON contains a duplicate key")
				}
				seen[name] = struct{}{}
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errors.New("decode genesis JSON delimiter")
		}
		if _, err := decoder.Token(); err != nil {
			return errors.New("decode genesis JSON closing delimiter")
		}
		return nil
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("genesis JSON has trailing input")
	}
	return nil
}

func decodeOneJSON(document []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func parseHashText(text string) (common.Hash, error) {
	var hash common.Hash
	if err := hash.UnmarshalText([]byte(text)); err != nil {
		return common.Hash{}, err
	}
	return hash, nil
}
