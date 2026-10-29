package enssync

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
)

type dependencyBinder func(string, string) (dbgen.EnsSyncProjection, error)

// Missing keys get an explicit dependency marker at the completely scanned
// target. It is not a record value or a negative resolution. A later event for
// this key advances its version, including a previously absent default link.
func dependencyMarker(ctx context.Context, q *dbgen.Queries, job dbgen.EnsSyncJob, address common.Address, kind, key string, bind dependencyBinder, revision *int64) (dbgen.EnsSyncProjection, error) {
	row, e := bind(kind, key)
	if !errors.Is(e, pgx.ErrNoRows) {
		return row, e
	}
	e = saveProjection(ctx, q, job.SourceID, address, common.BytesToHash(job.TargetHash), projection{Kind: kind, Key: key, Fields: map[string]any{"dependency_only": true}}, revision)
	if e != nil {
		return row, e
	}
	return bind(kind, key)
}
func (w *Worker) bindRecordDependencies(ctx context.Context, q *dbgen.Queries, job dbgen.EnsSyncJob, name string, o *dbgen.PublishENSSyncObservationParams, bind dependencyBinder, revision *int64) error {
	addresses := []common.Address{w.options.Registry, common.BytesToAddress(o.Resolver), common.BytesToAddress(o.ReverseResolver)}
	rows, e := q.ListENSSyncEmitterDeployments(ctx, job.SourceID, [][]byte{addresses[0].Bytes(), addresses[1].Bytes(), addresses[2].Bytes()})
	if e != nil {
		return e
	}
	profiles := map[common.Address]string{}
	for _, row := range rows {
		for _, kind := range []string{"registry", "resolver", "v2_registry", "v2_resolver", "default_reverse"} {
			profile, _ := loadProfile(kind)
			if common.BytesToHash(row.AbiDigest) == profile.digest {
				profiles[common.BytesToAddress(row.Address)] = kind
			}
		}
	}
	mark := func(address common.Address, kind, key string) (dbgen.EnsSyncProjection, error) {
		return dependencyMarker(ctx, q, job, address, kind, key, bind, revision)
	}
	// An empty reverse result may omit its resolver entirely. A newly written
	// reverse record must withdraw that negative before a replacement job runs,
	// otherwise it can continue authorizing an official-to-custom fallback.
	if o.Outcome == "not_found" && len(o.Address) == common.AddressLength {
		for _, deployment := range w.options.Manifest.Deployments {
			switch deployment.Profile {
			case "default_reverse", "reverse", "resolver", "v2_resolver":
			default:
				continue
			}
			_, e = bind("lifecycle", contractRevisionKey(deployment.Address))
			if errors.Is(e, pgx.ErrNoRows) {
				continue // A reviewed future deployment has no collected identity yet.
			}
			if e != nil {
				return e
			}
			if deployment.Profile == "default_reverse" {
				if _, e = mark(deployment.Address, "resolver_record", addressKey(deployment.Address)+"/reverse/"+addressKey(common.BytesToAddress(o.Address))); e != nil {
					return e
				}
			}
		}
	}
	// Registry routing changes affect the name and all descendants, not every
	// unrelated label in this registry. Include ancestors and root wildcard routing.
	if profiles[w.options.Registry] == "registry" {
		suffix := name
		for {
			node := nameHash(suffix)
			if suffix == "" {
				node = common.Hash{}
			}
			for _, field := range []struct{ kind, key string }{{"name_link", "resolver"}, {"control", "owner"}} {
				if _, e = mark(w.options.Registry, field.kind, recordKey(w.options.Registry, node, field.key)); e != nil {
					return e
				}
			}
			if suffix == "" {
				break
			}
			i := strings.IndexByte(suffix, '.')
			if i < 0 {
				suffix = ""
			} else {
				suffix = suffix[i+1:]
			}
		}
	}
	resolver := common.BytesToAddress(o.Resolver)
	switch profiles[resolver] {
	case "resolver":
		node := nameHash(name)
		for _, key := range []string{"version", "addr/" + w.options.Manifest.CoinType} {
			if _, e = mark(resolver, "resolver_record", recordKey(resolver, node, key)); e != nil {
				return e
			}
		}
	case "v2_resolver":
		// Track both the exact link and default. An absent exact link can later
		// override the default without updating the shared record itself.
		link, e := mark(resolver, "name_link", v2Key(resolver, "link", nameHash(name).Hex()))
		if e != nil {
			return e
		}
		fallback, e := mark(resolver, "name_link", v2Key(resolver, "link", (common.Hash{}).Hex()))
		if e != nil {
			return e
		}
		for _, row := range []dbgen.EnsSyncProjection{link, fallback} {
			var fields struct {
				RecordID string `json:"record_id"`
			}
			if json.Unmarshal(row.Payload, &fields) != nil {
				return ErrStorage
			}
			if fields.RecordID == "" || fields.RecordID == "0" {
				continue
			}
			for _, key := range []string{"clear", "addr/" + w.options.Manifest.CoinType} {
				if _, e = mark(resolver, "resolver_record", v2Key(resolver, "record", fields.RecordID)+"/"+key); e != nil {
					return e
				}
			}
		}
	default:
		if _, e = bind("lifecycle", contractRevisionKey(resolver)); e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
	}
	// Reverse bridging and wildcard/nonstandard routing remain conservatively
	// contract-scoped until a specific onchain primary record is established.
	reverse := common.BytesToAddress(o.ReverseResolver)
	if reverse != (common.Address{}) {
		if profiles[reverse] == "default_reverse" {
			_, e = mark(reverse, "resolver_record", addressKey(reverse)+"/reverse/"+addressKey(common.BytesToAddress(o.Address)))
		} else {
			_, e = bind("lifecycle", contractRevisionKey(reverse))
		}
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
	}
	if o.Outcome != "resolved" || len(o.ReverseResolver) > 0 {
		for _, address := range addresses {
			if address == (common.Address{}) {
				continue
			}
			if _, e = bind("lifecycle", contractRevisionKey(address)); e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
		}
	}
	return nil
}
