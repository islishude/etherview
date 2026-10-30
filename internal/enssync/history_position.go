package enssync

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
)

// All graph and resource reads are bounded by the event's block/log position.
// Later links, renewals or token regeneration cannot rewrite an earlier operation.
type historyPosition struct {
	c      *Collector
	q      *dbgen.Queries
	source int64
	number uint64
	index  int64
	time   int64
	budget int
}

func (h *historyPosition) state(ctx context.Context, kind, key string) (map[string]any, error) {
	raw, e := h.q.ReadENSHistoryState(ctx, dbgen.ReadENSHistoryStateParams{SourceID: h.source, Kind: kind, Key: key, AtNumber: numeric(h.number), AtLog: h.index})
	if errors.Is(e, pgx.ErrNoRows) {
		return map[string]any{}, nil
	}
	if e != nil {
		return nil, e
	}
	result := map[string]any{}
	if json.Unmarshal(raw, &result) != nil {
		return nil, ErrStorage
	}
	return result, nil
}
func (h *historyPosition) states(ctx context.Context, kind, prefix, field, value string) ([]dbgen.ListENSHistoryStatesRow, error) {
	if h.budget <= 0 {
		return nil, ErrCollectionLimit
	}
	h.budget--
	rows, e := h.q.ListENSHistoryStates(ctx, dbgen.ListENSHistoryStatesParams{SourceID: h.source, Kind: kind, KeyPrefix: prefix, Field: field, Value: value, AtNumber: numeric(h.number), AtLog: h.index, RowLimit: 65})
	if len(rows) > 64 {
		return nil, ErrCollectionLimit
	}
	return rows, e
}
func (h *historyPosition) paths(ctx context.Context, registry common.Address, seen []common.Address) ([]string, error) {
	if slices.Contains(seen, registry) {
		return nil, nil
	}
	if len(seen) >= 32 || h.budget <= 0 {
		return nil, ErrCollectionLimit
	}
	h.budget--
	paths := []string{}
	for _, root := range h.c.manifest.Roots {
		if root.Address == registry {
			paths = append(paths, root.Name)
		}
	}
	rows, e := h.states(ctx, "registration", "", "subregistry", addressKey(registry))
	if e != nil {
		return nil, e
	}
	for _, row := range rows {
		var value v2Registration
		if json.Unmarshal(row.Payload, &value) != nil {
			return nil, ErrStorage
		}
		expiry, e := strconv.ParseUint(value.Expiry, 10, 64)
		if e != nil {
			return nil, ErrStorage
		}
		if !value.Registered || value.Reserved || h.time < 0 || expiry <= uint64(h.time) {
			continue
		}
		parts := strings.Split(row.Key, "/")
		if len(parts) != 3 || parts[1] != "label" {
			continue
		}
		parent := common.HexToAddress(parts[0])
		ancestors, e := h.paths(ctx, parent, append(seen, registry))
		if e != nil {
			return nil, e
		}
		label, valid := normalizedName(value.Label)
		if !valid || strings.Contains(label, ".") {
			continue
		}
		for _, ancestor := range ancestors {
			name := label
			if ancestor != "" {
				name += "." + ancestor
			}
			if _, valid := normalizedName(name); valid {
				paths = append(paths, name)
			}
		}
		if len(paths) > 64 {
			return nil, ErrCollectionLimit
		}
	}
	return paths, nil
}
