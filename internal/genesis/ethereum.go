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

	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"golang.org/x/crypto/sha3"
)

var (
	emptyTrieRoot = mustHash("56e81f171bcc55a6ff8345e692c0f86" +
		"e5b48e01b996cadc001622fb5e363b421")
	emptyUncleHash = mustHash("1dcc4de8dec75d7aab85b567b6ccd41a" +
		"d312451b948a7413f0a142fd40d49347")
	emptyRequestsHash = mustHash("e3b0c44298fc1c149afbf4c8996fb924" +
		"27ae41e4649b934ca495991b7852b855")
	initialBaseFee = big.NewInt(1_000_000_000)
)

type hash [32]byte
type address [20]byte

func (value hash) Bytes() []byte {
	output := make([]byte, len(value))
	copy(output, value[:])
	return output
}

func (value hash) Hex() string { return "0x" + hex.EncodeToString(value[:]) }

type genesisAccount struct {
	Balance *big.Int
	Nonce   uint64
	Code    []byte
	Storage map[hash]hash
}

type chainConfig struct {
	ChainID        *big.Int
	LondonBlock    *big.Int
	ShanghaiTime   *uint64
	CancunTime     *uint64
	PragueTime     *uint64
	AmsterdamTime  *uint64
	VerkleGenesis  bool
	blockForks     []forkValue
	timestampForks []forkValue
}

type forkValue struct {
	name      string
	block     *big.Int
	timestamp *uint64
	optional  bool
}

type genesisSpec struct {
	Config        *chainConfig
	Nonce         uint64
	Timestamp     uint64
	ExtraData     []byte
	GasLimit      uint64
	Difficulty    *big.Int
	MixHash       hash
	Coinbase      address
	Alloc         map[address]genesisAccount
	Number        uint64
	GasUsed       uint64
	ParentHash    hash
	BaseFee       *big.Int
	ExcessBlobGas *uint64
	BlobGasUsed   *uint64
	SlotNumber    *uint64
}

type genesisBlockData struct {
	root hash
	hash hash
}

func (block *genesisBlockData) Root() hash { return block.root }
func (block *genesisBlockData) Hash() hash { return block.hash }

type genesisJSON struct {
	Config        json.RawMessage            `json:"config"`
	Nonce         json.RawMessage            `json:"nonce"`
	Timestamp     json.RawMessage            `json:"timestamp"`
	ExtraData     json.RawMessage            `json:"extraData"`
	GasLimit      json.RawMessage            `json:"gasLimit"`
	Difficulty    json.RawMessage            `json:"difficulty"`
	MixHash       json.RawMessage            `json:"mixHash"`
	Coinbase      json.RawMessage            `json:"coinbase"`
	Alloc         map[string]json.RawMessage `json:"alloc"`
	Number        json.RawMessage            `json:"number"`
	GasUsed       json.RawMessage            `json:"gasUsed"`
	ParentHash    json.RawMessage            `json:"parentHash"`
	BaseFee       json.RawMessage            `json:"baseFeePerGas"`
	ExcessBlobGas json.RawMessage            `json:"excessBlobGas"`
	BlobGasUsed   json.RawMessage            `json:"blobGasUsed"`
	SlotNumber    json.RawMessage            `json:"slotNumber"`
}

type accountJSON struct {
	Balance json.RawMessage   `json:"balance"`
	Nonce   json.RawMessage   `json:"nonce"`
	Code    json.RawMessage   `json:"code"`
	Storage map[string]string `json:"storage"`
}

type chainConfigJSON struct {
	ChainID               json.RawMessage `json:"chainId"`
	HomesteadBlock        json.RawMessage `json:"homesteadBlock"`
	DAOForkBlock          json.RawMessage `json:"daoForkBlock"`
	EIP150Block           json.RawMessage `json:"eip150Block"`
	EIP155Block           json.RawMessage `json:"eip155Block"`
	EIP158Block           json.RawMessage `json:"eip158Block"`
	ByzantiumBlock        json.RawMessage `json:"byzantiumBlock"`
	ConstantinopleBlock   json.RawMessage `json:"constantinopleBlock"`
	PetersburgBlock       json.RawMessage `json:"petersburgBlock"`
	IstanbulBlock         json.RawMessage `json:"istanbulBlock"`
	MuirGlacierBlock      json.RawMessage `json:"muirGlacierBlock"`
	BerlinBlock           json.RawMessage `json:"berlinBlock"`
	LondonBlock           json.RawMessage `json:"londonBlock"`
	ArrowGlacierBlock     json.RawMessage `json:"arrowGlacierBlock"`
	GrayGlacierBlock      json.RawMessage `json:"grayGlacierBlock"`
	MergeNetsplitBlock    json.RawMessage `json:"mergeNetsplitBlock"`
	ShanghaiTime          json.RawMessage `json:"shanghaiTime"`
	CancunTime            json.RawMessage `json:"cancunTime"`
	PragueTime            json.RawMessage `json:"pragueTime"`
	OsakaTime             json.RawMessage `json:"osakaTime"`
	VerkleTime            json.RawMessage `json:"verkleTime"`
	BPO1Time              json.RawMessage `json:"bpo1Time"`
	BPO2Time              json.RawMessage `json:"bpo2Time"`
	BPO3Time              json.RawMessage `json:"bpo3Time"`
	BPO4Time              json.RawMessage `json:"bpo4Time"`
	BPO5Time              json.RawMessage `json:"bpo5Time"`
	AmsterdamTime         json.RawMessage `json:"amsterdamTime"`
	EnableVerkleAtGenesis bool            `json:"enableVerkleAtGenesis"`
}

func parseDocument(document []byte, expectedChainID uint64) (*genesisSpec, *genesisBlockData, error) {
	if len(document) == 0 {
		return nil, nil, errors.New("genesis file is empty")
	}
	if err := rejectDuplicateKeys(document); err != nil {
		return nil, nil, err
	}
	var wire genesisJSON
	if err := decodeOneJSON(document, &wire); err != nil {
		return nil, nil, errors.New("decode genesis document")
	}
	if len(wire.Config) == 0 || wire.Alloc == nil ||
		len(wire.GasLimit) == 0 || len(wire.Difficulty) == 0 {
		return nil, nil, errors.New("genesis document is missing config, gasLimit, difficulty, or alloc")
	}
	config, err := parseChainConfig(wire.Config)
	if err != nil {
		return nil, nil, err
	}
	if config.ChainID.Cmp(new(big.Int).SetUint64(expectedChainID)) != 0 {
		return nil, nil, errors.New("genesis document chain ID does not match configured chain")
	}
	if config.VerkleGenesis {
		return nil, nil, errors.New("genesis Verkle allocation is unsupported")
	}
	spec := &genesisSpec{Config: config}
	if spec.GasLimit, err = parseRequiredUint64(wire.GasLimit, "gasLimit"); err != nil {
		return nil, nil, err
	}
	if spec.Difficulty, err = parseRequiredBig(wire.Difficulty, "difficulty", 256); err != nil {
		return nil, nil, err
	}
	if spec.Nonce, err = parseOptionalUint64Value(wire.Nonce, "nonce"); err != nil {
		return nil, nil, err
	}
	if spec.Timestamp, err = parseOptionalUint64Value(wire.Timestamp, "timestamp"); err != nil {
		return nil, nil, err
	}
	if spec.Number, err = parseOptionalUint64Value(wire.Number, "number"); err != nil {
		return nil, nil, err
	}
	if spec.GasUsed, err = parseOptionalUint64Value(wire.GasUsed, "gasUsed"); err != nil {
		return nil, nil, err
	}
	if spec.ExtraData, err = parseOptionalBytes(wire.ExtraData, "extraData"); err != nil {
		return nil, nil, err
	}
	if spec.MixHash, err = parseOptionalHash(wire.MixHash, "mixHash"); err != nil {
		return nil, nil, err
	}
	if spec.ParentHash, err = parseOptionalHash(wire.ParentHash, "parentHash"); err != nil {
		return nil, nil, err
	}
	if spec.Coinbase, err = parseOptionalAddress(wire.Coinbase, "coinbase"); err != nil {
		return nil, nil, err
	}
	if spec.BaseFee, err = parseOptionalBig(wire.BaseFee, "baseFeePerGas", 256); err != nil {
		return nil, nil, err
	}
	if spec.ExcessBlobGas, err = parseOptionalUint64(wire.ExcessBlobGas, "excessBlobGas"); err != nil {
		return nil, nil, err
	}
	if spec.BlobGasUsed, err = parseOptionalUint64(wire.BlobGasUsed, "blobGasUsed"); err != nil {
		return nil, nil, err
	}
	if spec.SlotNumber, err = parseOptionalUint64(wire.SlotNumber, "slotNumber"); err != nil {
		return nil, nil, err
	}
	if spec.Number != 0 || spec.ParentHash != (hash{}) {
		return nil, nil, errors.New("genesis document must describe block zero with a zero parent")
	}
	if len(wire.Alloc) > maximumAccounts {
		return nil, nil, errors.New("genesis allocation exceeds 500000 accounts")
	}
	spec.Alloc, err = parseAllocation(wire.Alloc)
	if err != nil {
		return nil, nil, err
	}
	block, err := genesisBlock(spec)
	if err != nil {
		return nil, nil, err
	}
	return spec, block, nil
}

func parseChainConfig(raw json.RawMessage) (*chainConfig, error) {
	var wire chainConfigJSON
	if err := decodeOneJSON(raw, &wire); err != nil {
		return nil, errors.New("decode genesis chain config")
	}
	chainID, err := parseRequiredBig(wire.ChainID, "chainId", 256)
	if err != nil || chainID.Sign() <= 0 {
		return nil, errors.New("genesis document chain ID is missing")
	}
	config := &chainConfig{ChainID: chainID, VerkleGenesis: wire.EnableVerkleAtGenesis}
	blockInputs := []struct {
		name     string
		raw      json.RawMessage
		optional bool
	}{
		{"homesteadBlock", wire.HomesteadBlock, false},
		{"daoForkBlock", wire.DAOForkBlock, true},
		{"eip150Block", wire.EIP150Block, false},
		{"eip155Block", wire.EIP155Block, false},
		{"eip158Block", wire.EIP158Block, false},
		{"byzantiumBlock", wire.ByzantiumBlock, false},
		{"constantinopleBlock", wire.ConstantinopleBlock, false},
		{"petersburgBlock", wire.PetersburgBlock, false},
		{"istanbulBlock", wire.IstanbulBlock, false},
		{"muirGlacierBlock", wire.MuirGlacierBlock, true},
		{"berlinBlock", wire.BerlinBlock, false},
		{"londonBlock", wire.LondonBlock, false},
		{"arrowGlacierBlock", wire.ArrowGlacierBlock, true},
		{"grayGlacierBlock", wire.GrayGlacierBlock, true},
		{"mergeNetsplitBlock", wire.MergeNetsplitBlock, true},
	}
	for _, input := range blockInputs {
		value, parseErr := parseOptionalBig(input.raw, input.name, 256)
		if parseErr != nil {
			return nil, errors.New("genesis document fork order is invalid")
		}
		config.blockForks = append(config.blockForks, forkValue{
			name: input.name, block: value, optional: input.optional,
		})
		if input.name == "londonBlock" {
			config.LondonBlock = value
		}
	}
	timestampInputs := []struct {
		name     string
		raw      json.RawMessage
		optional bool
	}{
		{"shanghaiTime", wire.ShanghaiTime, false},
		{"cancunTime", wire.CancunTime, true},
		{"pragueTime", wire.PragueTime, true},
		{"osakaTime", wire.OsakaTime, true},
		{"verkleTime", wire.VerkleTime, true},
		{"bpo1Time", wire.BPO1Time, true},
		{"bpo2Time", wire.BPO2Time, true},
		{"bpo3Time", wire.BPO3Time, true},
		{"bpo4Time", wire.BPO4Time, true},
		{"bpo5Time", wire.BPO5Time, true},
		{"amsterdamTime", wire.AmsterdamTime, true},
	}
	for _, input := range timestampInputs {
		value, parseErr := parseOptionalUint64(input.raw, input.name)
		if parseErr != nil {
			return nil, errors.New("genesis document fork order is invalid")
		}
		config.timestampForks = append(config.timestampForks, forkValue{
			name: input.name, timestamp: value, optional: input.optional,
		})
		switch input.name {
		case "shanghaiTime":
			config.ShanghaiTime = value
		case "cancunTime":
			config.CancunTime = value
		case "pragueTime":
			config.PragueTime = value
		case "amsterdamTime":
			config.AmsterdamTime = value
		}
	}
	if err := config.checkForkOrder(); err != nil {
		return nil, errors.New("genesis document fork order is invalid")
	}
	return config, nil
}

func (config *chainConfig) checkForkOrder() error {
	var last *forkValue
	all := append(append([]forkValue{}, config.blockForks...), config.timestampForks...)
	for index := range all {
		current := &all[index]
		defined := current.block != nil || current.timestamp != nil
		if last != nil {
			lastDefined := last.block != nil || last.timestamp != nil
			if !lastDefined && defined {
				return errors.New("skipped required fork")
			}
			if last.block != nil && current.block != nil && last.block.Cmp(current.block) > 0 {
				return errors.New("decreasing block fork")
			}
			if last.timestamp != nil && current.timestamp != nil &&
				*last.timestamp > *current.timestamp {
				return errors.New("decreasing timestamp fork")
			}
			if last.timestamp != nil && current.block != nil {
				return errors.New("block fork after timestamp fork")
			}
		}
		if !current.optional || defined {
			last = current
		}
	}
	return nil
}

func parseAllocation(raw map[string]json.RawMessage) (map[address]genesisAccount, error) {
	alloc := make(map[address]genesisAccount, len(raw))
	for encodedAddress, accountRaw := range raw {
		accountAddress, err := parseAddressText(encodedAddress)
		if err != nil {
			return nil, errors.New("genesis allocation address is invalid")
		}
		if _, exists := alloc[accountAddress]; exists {
			return nil, errors.New("genesis allocation contains a duplicate address")
		}
		var wire accountJSON
		if err := decodeOneJSON(accountRaw, &wire); err != nil {
			return nil, errors.New("decode genesis account")
		}
		balance, err := parseRequiredBig(wire.Balance, "balance", 256)
		if err != nil {
			return nil, errors.New("genesis account balance is outside uint256")
		}
		nonce, err := parseOptionalUint64Value(wire.Nonce, "nonce")
		if err != nil {
			return nil, errors.New("genesis account nonce is invalid")
		}
		code, err := parseOptionalBytes(wire.Code, "code")
		if err != nil {
			return nil, errors.New("genesis account code is invalid")
		}
		storage, err := parseStorage(wire.Storage)
		if err != nil {
			return nil, err
		}
		alloc[accountAddress] = genesisAccount{
			Balance: balance, Nonce: nonce, Code: code, Storage: storage,
		}
	}
	return alloc, nil
}

func parseStorage(raw map[string]string) (map[hash]hash, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	storage := make(map[hash]hash, len(raw))
	for rawKey, rawValue := range raw {
		key, err := parsePaddedHashText(rawKey)
		if err != nil {
			return nil, errors.New("genesis account storage key is invalid")
		}
		if _, exists := storage[key]; exists {
			return nil, errors.New("genesis account storage contains a duplicate key")
		}
		value, err := parsePaddedHashText(rawValue)
		if err != nil {
			return nil, errors.New("genesis account storage value is invalid")
		}
		storage[key] = value
	}
	return storage, nil
}

func genesisBlock(spec *genesisSpec) (*genesisBlockData, error) {
	root, err := accountRoot(spec.Alloc)
	if err != nil {
		return nil, errors.New("compute genesis account trie")
	}
	gasLimit := spec.GasLimit
	if gasLimit == 0 {
		gasLimit = 5_000
	}
	baseFee := spec.BaseFee
	if forkBlockActive(spec.Config.LondonBlock, spec.Number) && baseFee == nil {
		baseFee = new(big.Int).Set(initialBaseFee)
	}
	fields := [][]byte{
		rlpBytes(spec.ParentHash[:]),
		rlpBytes(emptyUncleHash[:]),
		rlpBytes(spec.Coinbase[:]),
		rlpBytes(root[:]),
		rlpBytes(emptyTrieRoot[:]),
		rlpBytes(emptyTrieRoot[:]),
		rlpBytes(make([]byte, 256)),
		rlpBig(spec.Difficulty),
		rlpUint64(spec.Number),
		rlpUint64(gasLimit),
		rlpUint64(spec.GasUsed),
		rlpUint64(spec.Timestamp),
		rlpBytes(spec.ExtraData),
		rlpBytes(spec.MixHash[:]),
		rlpBytes(uint64Bytes(spec.Nonce)),
	}
	if baseFee != nil {
		fields = append(fields, rlpBig(baseFee))
	}
	if forkTimeActive(spec.Config.ShanghaiTime, spec.Timestamp) {
		fields = append(fields, rlpBytes(emptyTrieRoot[:]))
	}
	if forkTimeActive(spec.Config.CancunTime, spec.Timestamp) {
		blobGasUsed := uint64(0)
		if spec.BlobGasUsed != nil {
			blobGasUsed = *spec.BlobGasUsed
		}
		excessBlobGas := uint64(0)
		if spec.ExcessBlobGas != nil {
			excessBlobGas = *spec.ExcessBlobGas
		}
		fields = append(fields,
			rlpUint64(blobGasUsed),
			rlpUint64(excessBlobGas),
			rlpBytes(make([]byte, 32)),
		)
	} else if spec.ExcessBlobGas != nil {
		if baseFee == nil {
			fields = append(fields, rlpBytes(nil))
		}
		if !forkTimeActive(spec.Config.ShanghaiTime, spec.Timestamp) {
			fields = append(fields, rlpBytes(nil))
		}
		fields = append(fields, rlpBytes(nil))
		fields = append(fields, rlpUint64(*spec.ExcessBlobGas))
	}
	if forkTimeActive(spec.Config.PragueTime, spec.Timestamp) {
		fields = append(fields, rlpBytes(emptyRequestsHash[:]))
	}
	if forkTimeActive(spec.Config.AmsterdamTime, spec.Timestamp) {
		slotNumber := uint64(0)
		if spec.SlotNumber != nil {
			slotNumber = *spec.SlotNumber
		}
		fields = append(fields, rlpBytes(emptyUncleHash[:]), rlpUint64(slotNumber))
	}
	header := rlpList(fields...)
	return &genesisBlockData{root: root, hash: keccakHash(header)}, nil
}

func storageRoot(storage map[hash]hash) (hash, error) {
	entries := make([]trieEntry, 0, len(storage))
	for key, value := range storage {
		if value == (hash{}) {
			continue
		}
		entries = append(entries, trieEntry{
			key:   keccakHash(key[:]),
			value: rlpBytes(trimLeadingZeroes(value[:])),
		})
	}
	return trieRoot(entries)
}

func accountRoot(alloc map[address]genesisAccount) (hash, error) {
	entries := make([]trieEntry, 0, len(alloc))
	for accountAddress, account := range alloc {
		storageHash, err := storageRoot(account.Storage)
		if err != nil {
			return hash{}, err
		}
		codeHash := keccakHash(account.Code)
		encoded := rlpList(
			rlpUint64(account.Nonce),
			rlpBig(account.Balance),
			rlpBytes(storageHash[:]),
			rlpBytes(codeHash[:]),
		)
		entries = append(entries, trieEntry{
			key: keccakHash(accountAddress[:]), value: encoded,
		})
	}
	return trieRoot(entries)
}

type trieEntry struct {
	key   hash
	value []byte
}

type nibbleEntry struct {
	key   []byte
	value []byte
}

func trieRoot(entries []trieEntry) (hash, error) {
	if len(entries) == 0 {
		return emptyTrieRoot, nil
	}
	nibbles := make([]nibbleEntry, 0, len(entries))
	for _, entry := range entries {
		nibbles = append(nibbles, nibbleEntry{
			key: bytesToNibbles(entry.key[:]), value: entry.value,
		})
	}
	sort.Slice(nibbles, func(i, j int) bool {
		return bytes.Compare(nibbles[i].key, nibbles[j].key) < 0
	})
	for index := 1; index < len(nibbles); index++ {
		if bytes.Equal(nibbles[index-1].key, nibbles[index].key) {
			return hash{}, errors.New("trie contains a duplicate key")
		}
	}
	encoded, err := buildTrieNode(nibbles, 0)
	if err != nil {
		return hash{}, err
	}
	return keccakHash(encoded), nil
}

func buildTrieNode(entries []nibbleEntry, depth int) ([]byte, error) {
	if len(entries) == 0 {
		return nil, errors.New("build empty trie node")
	}
	if len(entries) == 1 {
		return rlpList(
			rlpBytes(compactPath(entries[0].key[depth:], true)),
			rlpBytes(entries[0].value),
		), nil
	}
	shared := commonNibblePrefix(entries, depth)
	if shared > 0 {
		child, err := buildTrieNode(entries, depth+shared)
		if err != nil {
			return nil, err
		}
		return rlpList(
			rlpBytes(compactPath(entries[0].key[depth:depth+shared], false)),
			trieReference(child),
		), nil
	}
	children := make([][]byte, 17)
	for index := range children {
		children[index] = rlpBytes(nil)
	}
	start := 0
	for start < len(entries) {
		if depth == len(entries[start].key) {
			children[16] = rlpBytes(entries[start].value)
			start++
			continue
		}
		nibble := entries[start].key[depth]
		end := start + 1
		for end < len(entries) && depth < len(entries[end].key) &&
			entries[end].key[depth] == nibble {
			end++
		}
		child, err := buildTrieNode(entries[start:end], depth+1)
		if err != nil {
			return nil, err
		}
		children[int(nibble)] = trieReference(child)
		start = end
	}
	return rlpList(children...), nil
}

func commonNibblePrefix(entries []nibbleEntry, depth int) int {
	limit := len(entries[0].key)
	for _, entry := range entries[1:] {
		if len(entry.key) < limit {
			limit = len(entry.key)
		}
	}
	length := 0
	for depth+length < limit {
		nibble := entries[0].key[depth+length]
		for _, entry := range entries[1:] {
			if entry.key[depth+length] != nibble {
				return length
			}
		}
		length++
	}
	return length
}

func trieReference(encoded []byte) []byte {
	if len(encoded) < 32 {
		return encoded
	}
	digest := keccakHash(encoded)
	return rlpBytes(digest[:])
}

func compactPath(nibbles []byte, leaf bool) []byte {
	flag := byte(0)
	if leaf {
		flag = 2
	}
	odd := len(nibbles)%2 == 1
	output := make([]byte, 1+len(nibbles)/2)
	index := 0
	if odd {
		output[0] = (flag+1)<<4 | nibbles[0]
		index = 1
	} else {
		output[0] = flag << 4
	}
	for ; index < len(nibbles); index += 2 {
		output[1+index/2] = nibbles[index]<<4 | nibbles[index+1]
	}
	return output
}

func bytesToNibbles(input []byte) []byte {
	output := make([]byte, len(input)*2)
	for index, value := range input {
		output[index*2] = value >> 4
		output[index*2+1] = value & 0x0f
	}
	return output
}

func rlpList(items ...[]byte) []byte {
	length := 0
	for _, item := range items {
		length += len(item)
	}
	prefix := rlpLengthPrefix(length, 0xc0)
	output := make([]byte, 0, len(prefix)+length)
	output = append(output, prefix...)
	for _, item := range items {
		output = append(output, item...)
	}
	return output
}

func rlpBytes(value []byte) []byte {
	if len(value) == 1 && value[0] < 0x80 {
		return append([]byte(nil), value...)
	}
	prefix := rlpLengthPrefix(len(value), 0x80)
	return append(prefix, value...)
}

func rlpLengthPrefix(length int, shortBase byte) []byte {
	if length <= 55 {
		return []byte{shortBase + byte(length)}
	}
	encodedLength := intBytes(length)
	return append([]byte{shortBase + 55 + byte(len(encodedLength))}, encodedLength...)
}

func rlpBig(value *big.Int) []byte {
	if value == nil || value.Sign() == 0 {
		return rlpBytes(nil)
	}
	return rlpBytes(value.Bytes())
}

func rlpUint64(value uint64) []byte {
	if value == 0 {
		return rlpBytes(nil)
	}
	return rlpBytes(trimLeadingZeroes(uint64Bytes(value)))
}

func uint64Bytes(value uint64) []byte {
	output := make([]byte, 8)
	for index := 7; index >= 0; index-- {
		output[index] = byte(value)
		value >>= 8
	}
	return output
}

func intBytes(value int) []byte {
	var output [8]byte
	index := len(output)
	for value > 0 {
		index--
		output[index] = byte(value)
		value >>= 8
	}
	return append([]byte(nil), output[index:]...)
}

func trimLeadingZeroes(value []byte) []byte {
	index := 0
	for index < len(value) && value[index] == 0 {
		index++
	}
	return value[index:]
}

func keccakHash(parts ...[]byte) hash {
	hasher := sha3.NewLegacyKeccak256()
	for _, part := range parts {
		_, _ = hasher.Write(part)
	}
	var output hash
	hasher.Sum(output[:0])
	return output
}

func stateRootFromBlock(raw []byte) (enrich.Word, error) {
	var wire struct {
		StateRoot string `json:"stateRoot"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil || wire.StateRoot == "" {
		return enrich.Word{}, errors.New("stored block zero has invalid state root")
	}
	root, err := ethrpc.ParseHash(wire.StateRoot)
	if err != nil {
		return enrich.Word{}, errors.New("stored block zero has invalid state root")
	}
	decoded, err := root.Bytes()
	if err != nil {
		return enrich.Word{}, errors.New("stored block zero has invalid state root")
	}
	word, err := enrich.WordFromBytes(decoded)
	if err != nil {
		return enrich.Word{}, errors.New("stored block zero has invalid state root")
	}
	return word, nil
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

func parseRequiredBig(raw json.RawMessage, name string, maximumBits int) (*big.Int, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, errors.New(name + " is missing")
	}
	text, err := rawNumberText(raw)
	if err != nil {
		return nil, errors.New(name + " is invalid")
	}
	base := 10
	if strings.HasPrefix(text, "0x") || strings.HasPrefix(text, "0X") {
		base = 16
		text = text[2:]
	}
	if text == "" || strings.HasPrefix(text, "-") {
		return nil, errors.New(name + " is invalid")
	}
	value, ok := new(big.Int).SetString(text, base)
	if !ok || value.Sign() < 0 || value.BitLen() > maximumBits {
		return nil, errors.New(name + " is invalid")
	}
	return value, nil
}

func parseOptionalBig(raw json.RawMessage, name string, maximumBits int) (*big.Int, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	return parseRequiredBig(raw, name, maximumBits)
}

func parseRequiredUint64(raw json.RawMessage, name string) (uint64, error) {
	value, err := parseRequiredBig(raw, name, 64)
	if err != nil {
		return 0, err
	}
	return value.Uint64(), nil
}

func parseOptionalUint64(raw json.RawMessage, name string) (*uint64, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	value, err := parseRequiredUint64(raw, name)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func parseOptionalUint64Value(raw json.RawMessage, name string) (uint64, error) {
	value, err := parseOptionalUint64(raw, name)
	if err != nil || value == nil {
		return 0, err
	}
	return *value, nil
}

func rawNumberText(raw json.RawMessage) (string, error) {
	if len(raw) > 0 && raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", err
		}
		return text, nil
	}
	text := string(raw)
	for _, character := range text {
		if (character < '0' || character > '9') && character != '-' {
			return "", errors.New("not an integer")
		}
	}
	return text, nil
}

func parseOptionalBytes(raw json.RawMessage, name string) ([]byte, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil, errors.New(name + " is invalid")
	}
	return decodeHex(text, -1)
}

func parseOptionalHash(raw json.RawMessage, name string) (hash, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return hash{}, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return hash{}, errors.New(name + " is invalid")
	}
	decoded, err := decodeHex(text, 32)
	if err != nil {
		return hash{}, errors.New(name + " is invalid")
	}
	var output hash
	copy(output[:], decoded)
	return output, nil
}

func parseOptionalAddress(raw json.RawMessage, name string) (address, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return address{}, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return address{}, errors.New(name + " is invalid")
	}
	output, err := parseAddressText(text)
	if err != nil {
		return address{}, errors.New(name + " is invalid")
	}
	return output, nil
}

func parseAddressText(text string) (address, error) {
	if !strings.HasPrefix(text, "0x") && !strings.HasPrefix(text, "0X") {
		text = "0x" + text
	}
	decoded, err := decodeHex(text, 20)
	if err != nil {
		return address{}, err
	}
	var output address
	copy(output[:], decoded)
	return output, nil
}

func parsePaddedHashText(text string) (hash, error) {
	text = strings.TrimPrefix(strings.TrimPrefix(text, "0x"), "0X")
	if len(text) > 64 {
		return hash{}, errors.New("hash is too long")
	}
	if len(text)%2 == 1 {
		text = "0" + text
	}
	decoded, err := hex.DecodeString(text)
	if err != nil {
		return hash{}, err
	}
	var output hash
	copy(output[len(output)-len(decoded):], decoded)
	return output, nil
}

func decodeHex(text string, exactBytes int) ([]byte, error) {
	if !strings.HasPrefix(text, "0x") && !strings.HasPrefix(text, "0X") {
		return nil, errors.New("hex value has no prefix")
	}
	text = text[2:]
	if len(text)%2 != 0 {
		return nil, errors.New("hex value has odd length")
	}
	decoded, err := hex.DecodeString(text)
	if err != nil {
		return nil, err
	}
	if exactBytes >= 0 && len(decoded) != exactBytes {
		return nil, errors.New("hex value has wrong length")
	}
	return decoded, nil
}

func forkBlockActive(fork *big.Int, number uint64) bool {
	return fork != nil && fork.Cmp(new(big.Int).SetUint64(number)) <= 0
}

func forkTimeActive(fork *uint64, timestamp uint64) bool {
	return fork != nil && *fork <= timestamp
}

func mustHash(text string) hash {
	decoded, err := hex.DecodeString(text)
	if err != nil || len(decoded) != 32 {
		panic("invalid hard-coded hash")
	}
	var output hash
	copy(output[:], decoded)
	return output
}
