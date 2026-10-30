package enssync

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"math/big"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enscontract"
)

func historyAction(name string) string {
	switch name {
	case "NameRegistered", "LabelRegistered":
		return "registration"
	case "LabelReserved":
		return "reservation"
	case "NameRenewed", "ExpiryUpdated", "ExpiryExtended":
		return "renewal"
	case "LabelUnregistered":
		return "unregistration"
	case "Transfer", "TransferSingle", "TransferBatch":
		return "transfer"
	case "NewOwner":
		return "subname"
	case "NewResolver", "ResolverUpdated":
		return "resolver"
	case "NewTTL":
		return "ttl"
	case "AddrChanged", "AddressChanged", "AddressUpdated":
		return "address"
	case "NameChanged", "NameUpdated", "NameForAddrChanged", "ReverseClaimed":
		return "name"
	case "TextChanged", "TextUpdated":
		return "text"
	case "ContenthashChanged", "ContenthashUpdated":
		return "contenthash"
	case "ABIChanged", "ABIUpdated":
		return "abi"
	case "DNSRecordChanged", "DNSRecordDeleted", "DNSZonehashChanged":
		return "dns"
	case "PubkeyChanged":
		return "pubkey"
	case "InterfaceChanged", "InterfaceUpdated":
		return "interface"
	case "DataChanged", "DataUpdated":
		return "data"
	case "Cleared", "VersionChanged":
		return "clear"
	case "Approval", "Approved", "EACRolesChanged":
		return "permission"
	case "NameWrapped":
		return "wrap"
	case "NameUnwrapped":
		return "unwrap"
	case "FusesSet":
		return "fuses"
	case "NameMigrated":
		return "migration"
	case "SubregistryUpdated", "Linked":
		return "link"
	case "TokenResource", "TokenRegenerated", "ResourceArgument":
		return "resource"
	default:
		return ""
	}
}

func historyValue(value any) any {
	switch v := value.(type) {
	case common.Address:
		return v.Hex()
	case common.Hash:
		return v.Hex()
	case *big.Int:
		return v.String()
	case []byte:
		return "0x" + hex.EncodeToString(v)
	case string, bool:
		return v
	}
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Array && v.Type().Elem().Kind() == reflect.Uint8 {
		b := make([]byte, v.Len())
		for i := range b {
			b[i] = byte(v.Index(i).Uint())
		}
		return "0x" + hex.EncodeToString(b)
	}
	if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
		values := make([]any, v.Len())
		for i := range values {
			values[i] = historyValue(v.Index(i).Interface())
		}
		return values
	}
	return fmt.Sprint(value)
}
func historyFields(event abi.Event, values map[string]any) ([]enscontract.HistoryField, error) {
	result := make([]enscontract.HistoryField, 0, len(event.Inputs))
	for _, arg := range event.Inputs {
		value := historyValue(values[arg.Name])
		text, ok := value.(string)
		if !ok {
			raw, e := json.Marshal(value)
			if e != nil {
				return nil, ErrInvalidEvent
			}
			text = string(raw)
		}
		typ := arg.Type.String()
		if arg.Indexed && (arg.Type.T == abi.StringTy || arg.Type.T == abi.BytesTy) {
			typ = "hash"
		}
		truncated := len(text) > 4096
		if truncated {
			text = text[:4096]
			for !utf8.ValidString(text) {
				text = text[:len(text)-1]
			}
		}
		result = append(result, enscontract.HistoryField{Key: arg.Name, Type: typ, Value: text, Truncated: truncated})
	}
	// Dynamic indexed keys must agree with the disclosed key, where one exists.
	if key, ok := values["key"].(string); ok {
		for _, field := range []string{"indexedKey", "keyHash"} {
			if hash, ok := values[field].(common.Hash); ok && hash != crypto.Keccak256Hash([]byte(key)) {
				return nil, ErrInvalidEvent
			}
		}
	}
	return result, nil
}

func (c *Collector) recordHistory(ctx context.Context, q *dbgen.Queries, source int64, d Deployment, log types.Log, at int64) error {
	if len(log.Topics) == 0 {
		return nil
	}
	profile := c.profiles[d.Address]
	event, e := profile.abi.EventByID(log.Topics[0])
	if e != nil {
		return nil
	}
	action := historyAction(event.RawName)
	if action == "" {
		return nil
	}
	values, e := unpackEvent(*event, log)
	if e != nil {
		return e
	}
	fields, e := historyFields(*event, values)
	if e != nil {
		return e
	}
	raw, e := json.Marshal(fields)
	if e != nil || len(raw) > 65536 {
		return ErrCollectionLimit
	}
	if e = q.PutENSHistoryEvent(ctx, dbgen.PutENSHistoryEventParams{SourceID: source, BlockHash: log.BlockHash.Bytes(), LogIndex: int64(log.Index), BlockNumber: numeric(log.BlockNumber), EventName: event.RawName, Action: action, Fields: raw}); e != nil {
		return e
	}
	nodes, pending, e := c.historySubjects(ctx, q, source, d, log, at, event.RawName, values)
	if e != nil {
		return e
	}
	for _, node := range nodes {
		if node == (common.Hash{}) {
			continue
		}
		if _, e = q.PutENSHistorySubject(ctx, dbgen.PutENSHistorySubjectParams{SourceID: source, BlockHash: log.BlockHash.Bytes(), LogIndex: int64(log.Index), Node: node.Bytes()}); e != nil {
			return e
		}
	}
	if pending {
		return q.PutENSHistoryPending(ctx, source, log.BlockHash.Bytes(), int64(log.Index))
	}
	return q.CompleteENSHistoryPending(ctx, source, log.BlockHash.Bytes(), int64(log.Index))
}

func (c *Collector) historySubjects(ctx context.Context, q *dbgen.Queries, source int64, d Deployment, log types.Log, at int64, event string, v map[string]any) ([]common.Hash, bool, error) {
	if !strings.HasPrefix(d.Profile, "v2_") {
		decoded, e := decodeEvent(c.manifest, d, c.profiles[d.Address], log)
		if e != nil {
			return nil, false, e
		}
		if event == "NewOwner" {
			return []common.Hash{nodeFor(hashValue(v["node"]), hashValue(v["label"]))}, false, nil
		}
		return decoded.Nodes, false, nil
	}
	h := historyPosition{c: c, q: q, source: source, number: log.BlockNumber, index: int64(log.Index), time: at, budget: 512}
	if d.Profile == "v2_resolver" {
		if event == "Linked" {
			return []common.Hash{hashValue(v["node"])}, false, nil
		}
		record, ok := v["recordId"]
		if !ok {
			return nil, false, nil
		}
		if decimalValue(record) == "0" {
			return nil, false, nil
		} // Global permissions are not name operations.
		rows, e := h.states(ctx, "name_link", addressKey(d.Address)+"/link/", "record_id", decimalValue(record))
		if e != nil {
			return nil, false, e
		}
		nodes := []common.Hash{}
		for _, row := range rows {
			var value struct{ Deleted bool }
			if json.Unmarshal(row.Payload, &value) != nil {
				return nil, false, ErrStorage
			}
			if !value.Deleted {
				nodes = append(nodes, common.HexToHash(strings.TrimPrefix(row.Key, addressKey(d.Address)+"/link/")))
			}
		}
		// A record before any link is not an operation on a name. Linking itself is retained.
		return nodes, false, nil
	}
	if d.Profile != "v2_registry" {
		return nil, false, nil
	}
	labels := []string{}
	if label := hashValue(v["labelHash"]); label != (common.Hash{}) {
		labels = append(labels, label.Hex())
	}
	ids := []string{}
	for _, key := range []string{"tokenId", "id", "oldTokenId", "newTokenId"} {
		if value, ok := v[key]; ok {
			ids = append(ids, decimalValue(value))
		}
	}
	if values, ok := v["ids"].([]*big.Int); ok {
		if len(values) > 256 {
			return nil, false, ErrCollectionLimit
		}
		for _, id := range values {
			ids = append(ids, id.String())
		}
	}
	if event == "EACRolesChanged" {
		resource := decimalValue(v["resource"])
		if resource == "0" {
			return nil, false, nil
		}
		rows, e := h.states(ctx, "lifecycle", addressKey(d.Address)+"/token/", "resource", resource)
		if e != nil {
			return nil, false, e
		}
		for _, row := range rows {
			var f struct {
				Label string `json:"label_hash"`
			}
			if json.Unmarshal(row.Payload, &f) != nil {
				return nil, false, ErrStorage
			}
			if f.Label != "" {
				labels = append(labels, f.Label)
			}
		}
	}
	pending := false
	for _, id := range ids {
		f, e := h.state(ctx, "lifecycle", v2Key(d.Address, "token", id))
		if e != nil {
			return nil, false, e
		}
		label, _ := f["label_hash"].(string)
		// Mint Transfer logs may precede LabelRegistered in the same transaction.
		// Only that exact mint can use its registration proof, never a later owner/link.
		if label == "" && (event == "TransferSingle" || event == "TransferBatch") && v["from"] == (common.Address{}) {
			raw, err := q.ReadENSHistoryMintLabel(ctx, dbgen.ReadENSHistoryMintLabelParams{SourceID: source, BlockHash: log.BlockHash.Bytes(), LogIndex: int64(log.Index), Key: v2Key(d.Address, "token", id)})
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, false, err
			}
			if err == nil {
				var proof struct {
					Label string `json:"label_hash"`
				}
				if json.Unmarshal(raw, &proof) != nil {
					return nil, false, ErrStorage
				}
				label = proof.Label
			}
		}
		if label == "" {
			pending = true
		} else {
			labels = append(labels, label)
		}
	}
	nodes := []common.Hash{}
	// All tokens in this event share the same registry paths. Resolve the graph
	// once so a full ERC-1155 batch does not repeatedly consume the graph budget.
	if len(labels) > 0 {
		paths, err := h.paths(ctx, d.Address, nil)
		if err != nil {
			return nil, false, err
		}
		slices.Sort(labels)
		for _, label := range slices.Compact(labels) {
			for _, path := range paths {
				nodes = append(nodes, nodeFor(nameHash(path), common.HexToHash(label)))
			}
		}
	}
	slices.SortFunc(nodes, func(a, b common.Hash) int { return strings.Compare(a.Hex(), b.Hex()) })
	return slices.Compact(nodes), pending, nil
}

func (c *Collector) repairHistory(ctx context.Context, source int64) error {
	return dbaccess.WithTransaction(ctx, c.repo.db, func(q *dbgen.Queries) error {
		if _, e := c.lock(ctx, q, source); e != nil {
			return e
		}
		rows, e := q.ListENSHistoryPending(ctx, source, int32(c.options.MaxAddresses))
		if e != nil {
			return e
		}
		for _, row := range rows {
			var log types.Log
			if json.Unmarshal(row.Raw, &log) != nil {
				return ErrStorage
			}
			d, ok, err := c.historyDeployment(ctx, q, source, log)
			if err != nil {
				return err
			}
			if !ok {
				if err = q.RetryENSHistoryPending(ctx, source, row.BlockHash, row.LogIndex); err != nil {
					return err
				}
				continue
			}
			if e = c.recordHistory(ctx, q, source, d, log, row.BlockTime.Time.Unix()); e != nil {
				return e
			}
			if e = q.RetryENSHistoryPending(ctx, source, row.BlockHash, row.LogIndex); e != nil {
				return e
			}
		}
		if len(rows) > 0 {
			return q.EmitENSHistoryRefresh(ctx, source)
		}
		return nil
	})
}

// Restore only collector-authenticated instance identities when retrying an old
// event after restart; today's block need not contain another instance log.
func (c *Collector) historyDeployment(ctx context.Context, q *dbgen.Queries, source int64, log types.Log) (Deployment, bool, error) {
	if d, ok := c.deploymentAt(log.Address, log.BlockNumber); ok {
		return d, true, nil
	}
	rows, err := q.ListENSSyncEmitterDeployments(ctx, source, [][]byte{log.Address.Bytes()})
	if err != nil {
		return Deployment{}, false, err
	}
	for _, row := range rows {
		number, err := uintNumber(row.Number)
		if err != nil {
			return Deployment{}, false, err
		}
		if number > log.BlockNumber {
			continue
		}
		for _, name := range []string{"v2_registry", "v2_resolver"} {
			profile, err := loadProfile(name)
			if err != nil {
				return Deployment{}, false, err
			}
			if !bytes.Equal(profile.digest[:], row.AbiDigest) {
				continue
			}
			d := Deployment{Address: log.Address, Profile: name, Number: number, Hash: common.BytesToHash(row.BlockHash), CodeHash: common.BytesToHash(row.CodeHash), Implementation: common.BytesToAddress(row.ImplementationAddress), ImplementationCodeHash: common.BytesToHash(row.ImplementationCodeHash)}
			c.manifest.Deployments = append(c.manifest.Deployments, d)
			c.profiles[d.Address] = profile
			return d, true, nil
		}
	}
	return Deployment{}, false, nil
}
