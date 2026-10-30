package enssync

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
)

type v2Registration struct {
	Protocol, Label      string
	LabelHash            string `json:"label_hash"`
	Registered, Reserved bool
	Expiry               string
	Subregistry          string
	TokenID              string `json:"token_id"`
}

// Distinguish resumable batch exhaustion from a single graph exceeding its
// supported depth or fanout. Neither case permits publishing partial paths.
var errGraphBudget = errors.New("ENS graph batch budget exhausted")

func (c *Collector) expandGraph(ctx context.Context, source int64) error {
	if !c.manifest.hasV2() {
		return nil
	}
	err := dbaccess.WithTransaction(ctx, c.repo.db, func(q *dbgen.Queries) error {
		progress, e := q.LockENSSyncSource(ctx, source)
		if e != nil {
			return e
		}
		if len(progress.TargetHash) == 0 {
			return nil
		}
		target, e := q.GetENSSyncSourceBlock(ctx, source, progress.TargetHash)
		if e != nil {
			return e
		}
		work, e := q.GetENSSyncGraphWork(ctx, source)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		rows, e := q.ListENSSyncV2Registrations(ctx, source, work.AfterKey, int32(c.options.MaxAddresses))
		if e != nil {
			return e
		}
		after := work.AfterKey
		complete := len(rows) < c.options.MaxAddresses
		budget := 512
		for _, row := range rows {
			var registration v2Registration
			if json.Unmarshal(row.Payload, &registration) != nil {
				return ErrStorage
			}
			label, valid := normalizedName(registration.Label)
			if !valid || strings.Contains(label, ".") || crypto.Keccak256Hash([]byte(label)).Hex() != registration.LabelHash {
				after = row.Key
				continue
			}
			paths, e := c.registryPaths(ctx, q, source, common.BytesToAddress(row.Deployment), target.BlockTime.Time.Unix(), nil, &budget)
			if errors.Is(e, errGraphBudget) {
				if after == work.AfterKey {
					return ErrCollectionLimit
				}
				// No origins for this row have been written. Commit completed
				// rows and retry this one with a fresh budget on the next step.
				complete = false
				break
			}
			if e != nil {
				return e
			}
			for _, parent := range paths {
				name := label
				if parent != "" {
					name += "." + parent
				}
				if _, valid = normalizedName(name); !valid {
					continue
				}
				identity := row.BlockHash
				origin := common.BytesToHash(identity).Hex() + "/" + strconv.FormatInt(row.Revision, 10) + "/" + name
				if e = candidateOrigin(ctx, q, source, c.manifest.CoinType, candidate{"forward", name}, "event", origin); e != nil {
					return e
				}
			}
			after = row.Key
		}
		if complete {
			after = ""
		}
		return q.AdvanceENSSyncGraphWork(ctx, after, complete, source)
	})
	return collectionError(err)
}
func (c *Collector) registryPaths(ctx context.Context, q *dbgen.Queries, source int64, registry common.Address, at int64, seen []common.Address, budget *int) ([]string, error) {
	if slices.Contains(seen, registry) {
		return nil, nil
	}
	if len(seen) >= 32 {
		return nil, ErrCollectionLimit
	}
	if *budget <= 0 {
		return nil, errGraphBudget
	}
	*budget--
	paths := []string{}
	for _, root := range c.manifest.Roots {
		if root.Address == registry {
			paths = append(paths, root.Name)
		}
	}
	parents, e := q.ListENSSyncV2Parents(ctx, source, addressKey(registry), 65)
	if e != nil {
		return nil, e
	}
	if len(parents) > 64 {
		return nil, ErrCollectionLimit
	}
	for _, parent := range parents {
		var registration v2Registration
		if json.Unmarshal(parent.Payload, &registration) != nil {
			return nil, ErrStorage
		}
		expiry, e := strconv.ParseUint(registration.Expiry, 10, 64)
		if e != nil {
			return nil, ErrStorage
		}
		if !registration.Registered || registration.Reserved || at < 0 || expiry <= uint64(at) {
			continue
		}
		label, valid := normalizedName(registration.Label)
		if !valid || strings.Contains(label, ".") || crypto.Keccak256Hash([]byte(label)).Hex() != registration.LabelHash {
			continue
		}
		ancestors, e := c.registryPaths(ctx, q, source, common.BytesToAddress(parent.Deployment), at, append(seen, registry), budget)
		if e != nil {
			return nil, e
		}
		for _, ancestor := range ancestors {
			name := label
			if ancestor != "" {
				name += "." + ancestor
			}
			if _, valid = normalizedName(name); valid {
				paths = append(paths, name)
			}
		}
		if len(paths) > 64 {
			return nil, ErrCollectionLimit
		}
	}
	return paths, nil
}
