package enssync

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strings"
	"unicode/utf8"

	"github.com/adraffy/go-ens-normalize/ensip15"
	goens "github.com/ensdomains/go-ens/v4"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

type projection struct {
	Kind, Key  string
	Fields     map[string]any
	Merge      bool
	RecordNode common.Hash
}
type candidate struct{ Direction, Key string }
type decodedEvent struct {
	Projections []projection
	Candidates  []candidate
	Nodes       []common.Hash
}

func normalizedName(value string) (string, bool) {
	if len(value) == 0 || len(value) > 255 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n\t /\\") {
		return "", false
	}
	normalized, err := ensip15.Shared().Normalize(value)
	if err != nil || normalized != value || len(normalized)+2 > 255 {
		return "", false
	}
	return normalized, true
}
func nameHash(name string) common.Hash {
	hash, err := goens.NameHash(name)
	if err != nil {
		return common.Hash{}
	}
	return common.Hash(hash)
}
func nodeFor(parent common.Hash, label common.Hash) common.Hash {
	return crypto.Keccak256Hash(parent.Bytes(), label.Bytes())
}
func addressKey(address common.Address) string { return "0x" + hex.EncodeToString(address.Bytes()) }
func recordKey(address common.Address, node common.Hash, field string) string {
	return addressKey(address) + "/" + node.Hex() + "/" + field
}
func addAddress(event *decodedEvent, address common.Address) {
	if address != (common.Address{}) {
		event.Candidates = append(event.Candidates, candidate{"primary", addressKey(address)})
	}
}
func addName(event *decodedEvent, name string) {
	if normalized, ok := normalizedName(name); ok {
		event.Candidates = append(event.Candidates, candidate{"forward", normalized})
		event.Projections = append(event.Projections, projection{Kind: "preimage", Key: nameHash(name).Hex(), Fields: map[string]any{"name": name}})
	}
}
func hashValue(value any) common.Hash {
	switch v := value.(type) {
	case [32]byte:
		return common.Hash(v)
	case *big.Int:
		return common.BigToHash(v)
	}
	return common.Hash{}
}
func decimalValue(value any) string {
	switch v := value.(type) {
	case *big.Int:
		return v.String()
	case uint64:
		return new(big.Int).SetUint64(v).String()
	case uint32:
		return new(big.Int).SetUint64(uint64(v)).String()
	}
	return "0"
}

func decodeEvent(m Manifest, d Deployment, profile contractProfile, log types.Log) (decodedEvent, error) {
	var result decodedEvent
	if len(log.Topics) == 0 {
		return result, nil
	}
	for _, sig := range []string{"Upgraded(address)", "BeaconUpgraded(address)"} {
		if log.Topics[0] == crypto.Keccak256Hash([]byte(sig)) {
			return result, ErrUnsupportedDeployment
		}
	}
	event, err := profile.abi.EventByID(log.Topics[0])
	if err != nil {
		return result, nil
	}
	values, err := unpackEvent(*event, log)
	if err != nil {
		return result, err
	}
	node := hashValue(values["node"])
	put := func(kind, key string, fields map[string]any, merge bool) {
		result.Projections = append(result.Projections, projection{Kind: kind, Key: key, Fields: fields, Merge: merge})
	}
	switch d.Profile {
	case "registry":
		switch event.Name {
		case "NewOwner":
			label := hashValue(values["label"])
			child := nodeFor(node, label)
			owner := values["owner"].(common.Address)
			put("registry_link", child.Hex(), map[string]any{"parent": node.Hex(), "label": label.Hex(), "owner": addressKey(owner)}, false)
			put("control", recordKey(d.Address, child, "owner"), map[string]any{"owner": addressKey(owner)}, false)
			result.Nodes = append(result.Nodes, child)
		case "Transfer":
			put("control", recordKey(d.Address, node, "owner"), map[string]any{"owner": addressKey(values["owner"].(common.Address))}, false)
		case "NewResolver":
			put("name_link", recordKey(d.Address, node, "resolver"), map[string]any{"resolver": addressKey(values["resolver"].(common.Address))}, false)
		case "NewTTL":
			put("control", recordKey(d.Address, node, "ttl"), map[string]any{"ttl": decimalValue(values["ttl"])}, false)
		}
	case "registrar":
		id := values["id"]
		if id == nil {
			id = values["tokenId"]
		}
		label := hashValue(id)
		node = nodeFor(nameHash(m.ParentName), label)
		fields := map[string]any{"node": node.Hex(), "label": label.Hex()}
		if owner, ok := values["owner"].(common.Address); ok {
			fields["owner"] = addressKey(owner)
			addAddress(&result, owner)
		}
		if to, ok := values["to"].(common.Address); ok {
			fields["owner"] = addressKey(to)
			addAddress(&result, to)
		}
		if expires, ok := values["expires"]; ok {
			fields["expires"] = decimalValue(expires)
		}
		put("registration", recordKey(d.Address, node, "registration"), fields, true)
	case "controller_legacy", "controller_v3", "controller":
		labelName, ok := values["name"].(string)
		if !ok {
			labelName, _ = values["label"].(string)
		}
		label := hashValue(values["label"])
		if label == (common.Hash{}) {
			label = hashValue(values["labelhash"])
		}
		if len(labelName) > 1024 || crypto.Keccak256Hash([]byte(labelName)) != label {
			return result, ErrInvalidEvent
		}
		node = nodeFor(nameHash(m.ParentName), label)
		fields := map[string]any{"node": node.Hex(), "label": label.Hex(), "expires": decimalValue(values["expires"])}
		if owner, ok := values["owner"].(common.Address); ok {
			fields["owner"] = addressKey(owner)
			addAddress(&result, owner)
		}
		put("registration", recordKey(d.Address, node, "registration"), fields, true)
		if _, ok := normalizedName(labelName); ok && !strings.Contains(labelName, ".") {
			addName(&result, labelName+"."+m.ParentName)
		}
	case "resolver":
		switch event.Name {
		case "VersionChanged":
			put("resolver_record", recordKey(d.Address, node, "version"), map[string]any{"version": decimalValue(values["newVersion"])}, false)
		case "AddrChanged", "AddressChanged":
			coin := "60"
			var addr []byte
			if event.Name == "AddrChanged" {
				addr = values["a"].(common.Address).Bytes()
			} else {
				coin = decimalValue(values["coinType"])
				addr = values["newAddress"].([]byte)
			}
			if len(addr) > 512 {
				return result, ErrCollectionLimit
			}
			result.Projections = append(result.Projections, projection{Kind: "resolver_record", Key: recordKey(d.Address, node, "addr/"+coin), Fields: map[string]any{"coin_type": coin, "address": "0x" + hex.EncodeToString(addr), "deleted": len(addr) == 0 || bytes.Equal(addr, make([]byte, 20))}, RecordNode: node})
			if coin == m.CoinType && len(addr) == 20 {
				addAddress(&result, common.BytesToAddress(addr))
			}
		case "NameChanged":
			name := values["name"].(string)
			if len(name) > 1024 {
				return result, ErrCollectionLimit
			}
			result.Projections = append(result.Projections, projection{Kind: "resolver_record", Key: recordKey(d.Address, node, "name"), Fields: map[string]any{"name": name, "deleted": name == ""}, RecordNode: node})
			addName(&result, name)
		}
	case "reverse":
		if event.Name == "ReverseClaimed" {
			address := values["addr"].(common.Address)
			put("name_link", recordKey(d.Address, node, "reverse"), map[string]any{"address": addressKey(address)}, false)
			addAddress(&result, address)
		}
		if event.Name == "DefaultResolverChanged" {
			put("name_link", addressKey(d.Address)+"/default", map[string]any{"resolver": addressKey(values["resolver"].(common.Address))}, false)
		}
	case "default_reverse":
		address := values["addr"].(common.Address)
		name := values["name"].(string)
		if len(name) > 1024 {
			return result, ErrCollectionLimit
		}
		put("resolver_record", addressKey(d.Address)+"/reverse/"+addressKey(address), map[string]any{"name": name, "address": addressKey(address), "deleted": name == ""}, false)
		addAddress(&result, address)
		addName(&result, name)
	case "wrapper":
		return decodeWrapper(result, d, log, event.Name, values)
	}
	if node != (common.Hash{}) {
		result.Nodes = append(result.Nodes, node)
	}
	return result, nil
}

func decodeWrapper(result decodedEvent, d Deployment, _ types.Log, event string, v map[string]any) (decodedEvent, error) {
	node := hashValue(v["node"])
	fields := map[string]any{}
	switch event {
	case "NameWrapped":
		name, err := decodeDNSName(v["name"].([]byte))
		if err != nil || nameHash(name) != node {
			return result, ErrInvalidEvent
		}
		fields["owner"] = addressKey(v["owner"].(common.Address))
		fields["fuses"] = decimalValue(v["fuses"])
		fields["expiry"] = decimalValue(v["expiry"])
		fields["wrapped"] = true
		addName(&result, name)
	case "NameUnwrapped":
		fields["owner"] = addressKey(v["owner"].(common.Address))
		fields["wrapped"] = false
	case "FusesSet":
		fields["fuses"] = decimalValue(v["fuses"])
	case "ExpiryExtended":
		fields["expiry"] = decimalValue(v["expiry"])
	case "TransferSingle", "TransferBatch":
		ids := []*big.Int{}
		amounts := []*big.Int{}
		if event == "TransferSingle" {
			ids = append(ids, v["id"].(*big.Int))
			amounts = append(amounts, v["value"].(*big.Int))
		} else {
			ids = v["ids"].([]*big.Int)
			amounts = v["values"].([]*big.Int)
		}
		if len(ids) != len(amounts) || len(ids) > 256 {
			return result, ErrCollectionLimit
		}
		to := v["to"].(common.Address)
		addAddress(&result, to)
		for i, id := range ids {
			if amounts[i].Cmp(big.NewInt(1)) != 0 {
				return result, ErrInvalidEvent
			}
			n := common.BigToHash(id)
			result.Nodes = append(result.Nodes, n)
			result.Projections = append(result.Projections, projection{Kind: "control", Key: recordKey(d.Address, n, "wrapper"), Fields: map[string]any{"owner": addressKey(to)}, Merge: true})
		}
		return result, nil
	}
	result.Nodes = append(result.Nodes, node)
	result.Projections = append(result.Projections, projection{Kind: "control", Key: recordKey(d.Address, node, "wrapper"), Fields: fields, Merge: true})
	return result, nil
}

func decodeDNSName(data []byte) (string, error) {
	if len(data) < 2 || len(data) > 255 {
		return "", ErrInvalidEvent
	}
	parts := []string{}
	for offset := 0; offset < len(data); {
		length := int(data[offset])
		offset++
		if length == 0 {
			if offset != len(data) {
				return "", ErrInvalidEvent
			}
			name := strings.Join(parts, ".")
			if _, ok := normalizedName(name); !ok {
				return "", ErrInvalidEvent
			}
			return name, nil
		}
		if offset+length > len(data) {
			return "", ErrInvalidEvent
		}
		parts = append(parts, string(data[offset:offset+length]))
		offset += length
	}
	return "", ErrInvalidEvent
}

func projectionJSON(fields map[string]any) ([]byte, error) {
	raw, err := json.Marshal(fields)
	if err != nil || len(raw) > 65536 {
		return nil, ErrCollectionLimit
	}
	return raw, nil
}

func unpackEvent(event abi.Event, log types.Log) (map[string]any, error) {
	values := map[string]any{}
	indexed := abi.Arguments{}
	for _, arg := range event.Inputs {
		if arg.Indexed {
			indexed = append(indexed, arg)
		}
	}
	if len(log.Topics) != len(indexed)+1 || len(log.Data) > 65536 {
		return nil, ErrInvalidEvent
	}
	plain := event.Inputs.NonIndexed()
	unpacked, err := plain.Unpack(log.Data)
	if err != nil {
		return nil, ErrInvalidEvent
	}
	encoded, err := plain.Pack(unpacked...)
	if err != nil || !bytes.Equal(encoded, log.Data) {
		return nil, ErrInvalidEvent
	}
	if err = plain.UnpackIntoMap(values, log.Data); err != nil {
		return nil, ErrInvalidEvent
	}
	if err = abi.ParseTopicsIntoMap(values, indexed, log.Topics[1:]); err != nil {
		return nil, ErrInvalidEvent
	}
	for i, arg := range indexed {
		b, e := (abi.Arguments{arg}).Pack(values[arg.Name])
		if e != nil || !bytes.Equal(b, log.Topics[i+1].Bytes()) {
			return nil, ErrInvalidEvent
		}
	}
	return values, nil
}
