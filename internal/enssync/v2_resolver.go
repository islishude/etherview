package enssync

import (
	"context"
	"encoding/hex"
	"strconv"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	dbgen "github.com/islishude/etherview/internal/db/gen"
)

func v2Origin(log types.Log) string {
	return log.BlockHash.Hex() + "/" + strconv.FormatUint(uint64(log.Index), 10)
}
func (c *Collector) v2Candidate(ctx context.Context, q *dbgen.Queries, source int64, log types.Log, value candidate) error {
	return candidateOrigin(ctx, q, source, c.manifest.CoinType, value, "event", v2Origin(log))
}
func (c *Collector) projectV2Resolver(ctx context.Context, q *dbgen.Queries, source int64, d Deployment, log types.Log, event abi.Event, v map[string]any, put v2Put) error {
	record := decimalValue(v["recordId"])
	key := v2Key(d.Address, "record", record)
	switch event.Name {
	case "Linked":
		node := hashValue(v["node"])
		wire := v["name"].([]byte)
		name := ""
		if len(wire) != 1 || wire[0] != 0 {
			var e error
			name, e = decodeDNSName(wire)
			if e != nil {
				return e
			}
		}
		if (name == "" && node != (common.Hash{})) || (name != "" && nameHash(name) != node) {
			return ErrInvalidEvent
		}
		if name != "" {
			if e := put("preimage", node.Hex(), map[string]any{"name": name}, false); e != nil {
				return e
			}
			if e := c.v2Candidate(ctx, q, source, log, candidate{"forward", name}); e != nil {
				return e
			}
		}
		return put("name_link", v2Key(d.Address, "link", node.Hex()), map[string]any{"record_id": record, "name": name, "deleted": record == "0"}, false)
	case "AddressUpdated":
		coin := decimalValue(v["coinType"])
		value := v["addressBytes"].([]byte)
		if len(value) > 512 {
			return ErrCollectionLimit
		}
		if coin == c.manifest.CoinType && len(value) == 20 && common.BytesToAddress(value) != (common.Address{}) {
			if e := c.v2Candidate(ctx, q, source, log, candidate{"primary", addressKey(common.BytesToAddress(value))}); e != nil {
				return e
			}
		}
		return put("resolver_record", key+"/addr/"+coin, map[string]any{"record_id": record, "coin_type": coin, "address": "0x" + hex.EncodeToString(value), "deleted": len(value) == 0}, false)
	case "NameUpdated":
		name := v["primaryName"].(string)
		if len(name) > 1024 {
			return ErrCollectionLimit
		}
		if _, ok := normalizedName(name); ok {
			if e := c.v2Candidate(ctx, q, source, log, candidate{"forward", name}); e != nil {
				return e
			}
		}
		return put("resolver_record", key+"/name", map[string]any{"record_id": record, "name": name, "deleted": name == ""}, false)
	case "Cleared":
		return put("resolver_record", key+"/clear", map[string]any{"block": log.BlockHash.Hex(), "log_index": strconv.FormatUint(uint64(log.Index), 10)}, false)
	case "EACRolesChanged":
		return put("control", v2Key(d.Address, "roles", decimalValue(v["resource"]))+"/"+addressKey(v["account"].(common.Address)), map[string]any{"roles": decimalValue(v["newRoleBitmap"])}, false)
	default:
		return nil
	}
}
