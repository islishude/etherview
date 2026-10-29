package enssync

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
)

func v2Key(address common.Address, category, id string) string {
	return addressKey(address) + "/" + category + "/" + id
}
func projectionFields(ctx context.Context, q *dbgen.Queries, source int64, kind, key string) (map[string]any, error) {
	row, e := q.GetENSSyncLatestProjection(ctx, source, kind, key)
	if errors.Is(e, pgx.ErrNoRows) {
		return map[string]any{}, nil
	}
	if e != nil {
		return nil, e
	}
	fields := map[string]any{}
	if json.Unmarshal(row.Payload, &fields) != nil {
		return nil, ErrStorage
	}
	return fields, nil
}
func (c *Collector) projectV2(ctx context.Context, q *dbgen.Queries, source int64, d Deployment, log types.Log, revision *int64) error {
	if len(log.Topics) > 0 && log.Topics[0] == crypto.Keccak256Hash([]byte("Upgraded(address)")) {
		if len(log.Topics) != 2 || len(log.Data) != 0 || log.Topics[1] != common.BytesToHash(d.Implementation.Bytes()) || d.Implementation == (common.Address{}) {
			return ErrUnsupportedDeployment
		}
		return nil
	}
	if len(log.Topics) > 0 && log.Topics[0] == crypto.Keccak256Hash([]byte("BeaconUpgraded(address)")) {
		return ErrUnsupportedDeployment
	}
	profile := c.profiles[d.Address]
	event, e := profile.abi.EventByID(log.Topics[0])
	if e != nil {
		return nil
	}
	values, e := unpackEvent(*event, log)
	if e != nil {
		return e
	}
	put := func(kind, key string, fields map[string]any, merge bool) error {
		return saveProjection(ctx, q, source, d.Address, log.BlockHash, projection{Kind: kind, Key: key, Fields: fields, Merge: merge}, revision)
	}
	if event.Name == "Upgraded" {
		if values["implementation"].(common.Address) != d.Implementation {
			return ErrUnsupportedDeployment
		}
		return nil
	}
	if d.Profile == "v2_factory" {
		return nil
	} // Discovery is verified before this transaction.
	if d.Profile == "v2_resolver" {
		return c.projectV2Resolver(ctx, q, source, d, log, *event, values, put)
	}
	if event.Name == "LabelRegistered" || event.Name == "LabelReserved" {
		label := values["label"].(string)
		if len(label) > 1024 || crypto.Keccak256Hash([]byte(label)) != hashValue(values["labelHash"]) {
			return ErrInvalidEvent
		}
	}
	token := decimalValue(values["tokenId"])
	tokenKey := v2Key(d.Address, "token", token)
	mapping, e := projectionFields(ctx, q, source, "lifecycle", tokenKey)
	if e != nil {
		return e
	}
	label, _ := mapping["label_hash"].(string)
	fields := map[string]any{}
	switch event.Name {
	case "LabelRegistered", "LabelReserved":
		name := values["label"].(string)
		hash := hashValue(values["labelHash"])
		if len(name) > 1024 || crypto.Keccak256Hash([]byte(name)) != hash {
			return ErrInvalidEvent
		}
		label = hash.Hex()
		fields = map[string]any{"protocol": "v2", "label_hash": label, "label": name, "token_id": token, "expiry": decimalValue(values["expiry"]), "reserved": event.Name == "LabelReserved", "registered": event.Name == "LabelRegistered", "owner": addressKey(common.Address{}), "subregistry": addressKey(common.Address{}), "resolver": addressKey(common.Address{}), "sender": addressKey(values["sender"].(common.Address))}
		if owner, ok := values["owner"].(common.Address); ok {
			fields["owner"] = addressKey(owner)
			if owner != (common.Address{}) {
				if e = c.v2Candidate(ctx, q, source, log, candidate{"primary", addressKey(owner)}); e != nil {
					return e
				}
			}
		}
		if e = put("lifecycle", tokenKey, map[string]any{"label_hash": label, "token_id": token}, false); e != nil {
			return e
		}
		if e = put("registration", v2Key(d.Address, "label", label), fields, false); e != nil {
			return e
		}
		return q.DirtyENSSyncGraph(ctx, source)
	case "TokenResource":
		if label == "" {
			return ErrCoverageGap
		}
		fields["resource"] = decimalValue(values["resource"])
		if e = put("lifecycle", tokenKey, fields, true); e != nil {
			return e
		}
	case "TokenRegenerated":
		oldID, newID := decimalValue(values["oldTokenId"]), decimalValue(values["newTokenId"])
		previous, e := projectionFields(ctx, q, source, "lifecycle", v2Key(d.Address, "token", oldID))
		if e != nil {
			return e
		}
		label, _ = previous["label_hash"].(string)
		if label == "" {
			return ErrCoverageGap
		}
		previous["token_id"] = newID
		if e = put("lifecycle", v2Key(d.Address, "token", newID), previous, true); e != nil {
			return e
		}
		fields["token_id"] = newID
		fields["resource"] = previous["resource"]
	case "LabelUnregistered":
		fields["registered"] = false
		fields["reserved"] = false
		fields["owner"] = addressKey(common.Address{})
	case "ExpiryUpdated":
		fields["expiry"] = decimalValue(values["newExpiry"])
	case "ResolverUpdated":
		fields["resolver"] = addressKey(values["resolver"].(common.Address))
	case "SubregistryUpdated":
		fields["subregistry"] = addressKey(values["subregistry"].(common.Address))
	case "TransferSingle", "TransferBatch":
		return projectV2Transfers(ctx, q, source, d, event.Name, values, put)
	case "ParentUpdated":
		return put("registry_link", addressKey(d.Address)+"/parent_hint", map[string]any{"parent": addressKey(values["parent"].(common.Address)), "label": values["label"]}, false)
	case "EACRolesChanged":
		return put("control", v2Key(d.Address, "roles", decimalValue(values["resource"]))+"/"+addressKey(values["account"].(common.Address)), map[string]any{"roles": decimalValue(values["newRoleBitmap"])}, false)
	default:
		return nil
	}
	if label == "" {
		return ErrCoverageGap
	}
	if e = put("registration", v2Key(d.Address, "label", label), fields, true); e != nil {
		return e
	}
	return q.DirtyENSSyncGraph(ctx, source)
}

type v2Put func(kind, key string, fields map[string]any, merge bool) error

func projectV2Transfers(ctx context.Context, q *dbgen.Queries, source int64, d Deployment, event string, v map[string]any, put v2Put) error {
	ids, amounts := []*big.Int{}, []*big.Int{}
	if event == "TransferSingle" {
		ids = append(ids, v["id"].(*big.Int))
		amounts = append(amounts, v["value"].(*big.Int))
	} else {
		ids = v["ids"].([]*big.Int)
		amounts = v["values"].([]*big.Int)
	}
	if len(ids) != len(amounts) || len(ids) > 256 {
		return ErrCollectionLimit
	}
	owner := addressKey(v["to"].(common.Address))
	for i, id := range ids {
		if amounts[i].Cmp(big.NewInt(1)) != 0 {
			return ErrInvalidEvent
		}
		key := v2Key(d.Address, "token", id.String())
		mapping, e := projectionFields(ctx, q, source, "lifecycle", key)
		if e != nil {
			return e
		}
		if e = put("control", key, map[string]any{"owner": owner}, false); e != nil {
			return e
		}
		label, _ := mapping["label_hash"].(string)
		if label == "" {
			continue
		} // Regeneration mint can precede the mapping event.
		row, e := projectionFields(ctx, q, source, "registration", v2Key(d.Address, "label", label))
		if e != nil {
			return e
		}
		if row["token_id"] != id.String() {
			continue
		}
		// Regeneration emits a burn plus mint; it does not unregister the name.
		if v["to"].(common.Address) != (common.Address{}) {
			if e = put("registration", v2Key(d.Address, "label", label), map[string]any{"owner": owner}, true); e != nil {
				return e
			}
		}
	}
	return nil
}
