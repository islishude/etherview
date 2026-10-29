package enssync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/jackc/pgx/v5"
)

func collectionError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{ErrFenceLost, ErrInvalidEvent, ErrCollectionLimit, ErrUnsupportedDeployment, ErrSourceIdentity, ErrSourceIntegrity, ErrCoverageGap, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, known) {
			return known
		}
	}
	return ErrStorage
}
func (c *Collector) lock(ctx context.Context, q *dbgen.Queries, source int64) (dbgen.EnsSyncProgress, error) {
	if c.manifest.Namespace == enscontract.Custom {
		if err := q.StoreLegacyLockChainStatement1(ctx, &c.manifest.SourceChainID); err != nil {
			return dbgen.EnsSyncProgress{}, err
		}
	}
	return q.LockENSSyncSource(ctx, source)
}
func (c *Collector) discoverAddresses(ctx context.Context, source int64) error {
	err := dbaccess.WithTransaction(ctx, c.repo.db, func(q *dbgen.Queries) error {
		if _, e := q.LockENSSyncSource(ctx, source); e != nil {
			return e
		}
		cursor, e := q.GetENSSyncAddressCursor(ctx, source)
		if e != nil {
			return e
		}
		chain, _ := unsignedNumeric(c.manifest.TargetChainID)
		rows, e := q.ListENSSyncKnownAddresses(ctx, int32(c.options.MaxAddresses), chain, cursor.AfterAddress)
		if e != nil {
			return e
		}
		after := []byte{}
		var cycle int64 = 1
		for _, row := range rows {
			key := addressKey(common.BytesToAddress(row.Address))
			if e = candidateOrigin(ctx, q, source, c.manifest.CoinType, candidate{"primary", key}, row.Kind, key); e != nil {
				return e
			}
			after = row.Address
		}
		if len(rows) == c.options.MaxAddresses {
			cycle = 0
		} else {
			after = []byte{}
		}
		return q.AdvanceENSSyncAddressCursor(ctx, after, cycle, source)
	})
	return collectionError(err)
}
func candidateOrigin(ctx context.Context, q *dbgen.Queries, source int64, coin string, value candidate, kind, identity string) error {
	coinType, _ := unsignedNumeric(coin)
	row, e := q.EnsureENSSyncDiscoveredCandidate(ctx, dbgen.EnsureENSSyncDiscoveredCandidateParams{SourceID: source, Direction: value.Direction, LookupKey: value.Key, CoinType: coinType})
	if e != nil {
		return e
	}
	n, e := q.InsertENSSyncDiscoveryOrigin(ctx, dbgen.InsertENSSyncDiscoveryOriginParams{SourceID: source, CandidateID: row.ID, Kind: kind, Identity: identity})
	if e != nil {
		return e
	}
	_, e = q.AdvanceENSSyncCandidateDiscovery(ctx, source, row.ID, n != 0)
	return e
}
func (c *Collector) rewind(ctx context.Context, source int64, expected dbgen.EnsSyncProgress) (bool, error) {
	changed := false
	err := dbaccess.WithTransaction(ctx, c.repo.db, func(q *dbgen.Queries) error {
		current, e := c.lock(ctx, q, source)
		if e != nil {
			return e
		}
		if current.Revision != expected.Revision || !bytes.Equal(current.ScannedHash, expected.ScannedHash) {
			return ErrFenceLost
		}
		chain, _ := unsignedNumeric(c.manifest.SourceChainID)
		n, e := q.DetachENSSyncUnscannedBlocks(ctx, source, chain, int32(c.options.MaxBlocks))
		if e != nil {
			return e
		}
		changed = n > 0
		if !current.ScannedNumber.Valid {
			return nil
		}
		rows, e := q.ListENSSyncRewindBlocks(ctx, dbgen.ListENSSyncRewindBlocksParams{SourceID: source, ChainID: chain, ThroughNumber: current.ScannedNumber, RowLimit: int32(c.options.MaxBlocks + 1)})
		if e != nil {
			return e
		}
		rewind := dbgen.RewindENSSyncProgressParams{SourceID: source, Revision: current.Revision}
		invalid := 0
		for i, row := range rows {
			if i == c.options.MaxBlocks || (row.CurrentCanonical && row.CoreCanonicalAt.Valid && row.CurrentAttachment.Valid && row.CoreCanonicalAt.Time.Equal(row.CurrentAttachment.Time)) {
				rewind.ScannedNumber = row.Number
				rewind.ScannedHash = row.Hash
				break
			}
			if _, e = q.DetachENSSyncBlock(ctx, source, row.Hash); e != nil {
				return e
			}
			invalid++
		}
		if invalid == 0 {
			return nil
		}
		changed = true
		n, e = q.RewindENSSyncProgress(ctx, rewind)
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrFenceLost
		}
		return nil
	})
	return changed, collectionError(err)
}
func putHeader(ctx context.Context, q *dbgen.Queries, source int64, h *types.Header) error {
	if h.Time > 253402300799 {
		return ErrSourceIdentity
	}
	// A reattached hash keeps its immutable identity and retained orphan facts.
	if _, e := q.ReattachENSSyncBlock(ctx, source, h.Hash().Bytes()); e != nil {
		return e
	}
	n, e := q.PutENSSyncBlock(ctx, dbgen.PutENSSyncBlockParams{SourceID: source, Number: numeric(h.Number.Uint64()), Hash: h.Hash().Bytes(), ParentHash: h.ParentHash.Bytes(), BlockTime: stamp(time.Unix(int64(h.Time), 0)), Canonical: true})
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrSourceIdentity
	}
	return nil
}
func (c *Collector) commit(ctx context.Context, source int64, expected dbgen.EnsSyncProgress, target *types.Header, deployments []verifiedDeployment, blocks []collectedBlock) error {
	err := dbaccess.WithTransaction(ctx, c.repo.db, func(q *dbgen.Queries) error {
		progress, e := c.lock(ctx, q, source)
		if e != nil {
			return e
		}
		if progress.Revision != expected.Revision || !bytes.Equal(progress.ScannedHash, expected.ScannedHash) || progress.State == "halted" || progress.State == "unsupported" {
			return ErrFenceLost
		}
		chain, _ := unsignedNumeric(c.manifest.SourceChainID)
		if c.manifest.Namespace == enscontract.Custom {
			tip, e := q.GetENSSyncCoreTip(ctx, chain, c.manifest.Genesis.Bytes())
			if e != nil {
				return e
			}
			if !bytes.Equal(tip.Hash, target.Hash().Bytes()) {
				return ErrFenceLost
			}
			for _, b := range blocks {
				row, e := q.GetENSSyncCoreBlock(ctx, chain, c.manifest.Genesis.Bytes(), numeric(b.Header.Number.Uint64()))
				if e != nil {
					return ErrFenceLost
				}
				if !bytes.Equal(row.Hash, b.Header.Hash().Bytes()) || !row.UpdatedAt.Time.Equal(b.Attachment.Time) {
					return ErrFenceLost
				}
			}
			for _, d := range deployments {
				row, e := q.GetENSSyncCoreBlock(ctx, chain, c.manifest.Genesis.Bytes(), numeric(d.Deployment.Number))
				if e != nil || !bytes.Equal(row.Hash, d.Deployment.Hash.Bytes()) {
					return ErrFenceLost
				}
			}
		}
		if e = putHeader(ctx, q, source, target); e != nil {
			return e
		}
		for _, verified := range deployments {
			d := verified.Deployment
			if e = putHeader(ctx, q, source, verified.Header); e != nil {
				return e
			}
			protocol := "v1"
			if strings.HasPrefix(d.Profile, "v2_") {
				protocol = "v2"
			}
			if c.manifest.Namespace == enscontract.Custom {
				protocol = "custom"
			}
			input := dbgen.PutENSSyncDeploymentParams{SourceID: source, Address: d.Address.Bytes(), Protocol: protocol, BlockHash: d.Hash.Bytes(), CodeHash: d.CodeHash.Bytes(), AbiDigest: c.profiles[d.Address].digest.Bytes()}
			if d.Implementation != (common.Address{}) {
				input.ImplementationAddress = d.Implementation.Bytes()
				input.ImplementationCodeHash = d.ImplementationCodeHash.Bytes()
			}
			if e = q.PutENSSyncDeployment(ctx, input); e != nil {
				return e
			}
			if _, found := q.GetENSSyncLatestProjection(ctx, source, "lifecycle", contractRevisionKey(d.Address)); errors.Is(found, pgx.ErrNoRows) {
				if e = saveProjection(ctx, q, source, d.Address, d.Hash, projection{Kind: "lifecycle", Key: contractRevisionKey(d.Address), Fields: map[string]any{"deployment": d.Hash.Hex()}}, &progress.ProjectionRevision); e != nil {
					return e
				}
			} else if found != nil {
				return found
			}
			if len(verified.Document) > 0 {
				if e = q.PutENSSyncGenesisBaseline(ctx, dbgen.PutENSSyncGenesisBaselineParams{SourceID: source, Deployment: d.Address.Bytes(), GenesisHash: d.Hash.Bytes(), DocumentDigest: verified.Document}); e != nil {
					return e
				}
			}
		}
		n, e := q.SetENSSyncCollectionTarget(ctx, dbgen.SetENSSyncCollectionTargetParams{SourceID: source, Hash: target.Hash().Bytes(), Revision: progress.Revision, HealthyUntil: stamp(time.Now().Add(c.options.Health))})
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrFenceLost
		}
		for _, b := range blocks {
			if e = putHeader(ctx, q, source, b.Header); e != nil {
				return e
			}
			if e = c.projectBlock(ctx, q, source, b, &progress.ProjectionRevision); e != nil {
				return e
			}
			if e = q.SaveENSSyncScan(ctx, dbgen.SaveENSSyncScanParams{SourceID: source, BlockNumber: numeric(b.Header.Number.Uint64()), BlockHash: b.Header.Hash().Bytes(), CoreCanonicalAt: b.Attachment}); e != nil {
				return e
			}
			n, e = q.AdvanceENSSyncCoverage(ctx, source, b.Header.Hash().Bytes(), progress.Revision)
			if e != nil {
				return e
			}
			if n != 1 {
				return ErrFenceLost
			}
		}
		if progress.ProjectionRevision != expected.ProjectionRevision {
			return q.EmitENSInvalidation(ctx, strconv.FormatInt(source, 10))
		}
		return nil
	})
	return collectionError(err)
}
func (c *Collector) projectBlock(ctx context.Context, q *dbgen.Queries, source int64, b collectedBlock, revision *int64) error {
	total := 0
	for i, log := range b.Logs {
		if log.Removed || log.BlockHash != b.Header.Hash() || log.BlockNumber != b.Header.Number.Uint64() || log.Index > math.MaxInt32 || log.TxIndex > math.MaxInt32 || len(log.Topics) > 4 || len(log.Data) > 65536 || (i > 0 && (log.Index <= b.Logs[i-1].Index || log.TxIndex < b.Logs[i-1].TxIndex)) {
			return ErrInvalidEvent
		}
		total += len(log.Data) + 32*len(log.Topics)
		if total > c.options.MaxBytes {
			return ErrCollectionLimit
		}
		var deployment Deployment
		found := false
		for _, d := range c.manifest.Deployments {
			if d.Address == log.Address && d.Number <= log.BlockNumber {
				deployment = d
				found = true
				break
			}
		}
		if !found {
			return ErrSourceIdentity
		}
		beforeRevision := *revision
		var event decodedEvent
		var e error
		if !strings.HasPrefix(deployment.Profile, "v2_") {
			event, e = decodeEvent(c.manifest, deployment, c.profiles[log.Address], log)
		}
		if e != nil {
			return e
		}
		topics := make([][]byte, len(log.Topics))
		for j, t := range log.Topics {
			topics[j] = t.Bytes()
		}
		data := log.Data
		if data == nil {
			data = []byte{}
		}
		if e = q.PutENSSyncEvent(ctx, dbgen.PutENSSyncEventParams{SourceID: source, BlockHash: log.BlockHash.Bytes(), LogIndex: int32(log.Index), TransactionHash: log.TxHash.Bytes(), TransactionIndex: int32(log.TxIndex), Emitter: log.Address.Bytes(), Topics: topics, Data: data}); e != nil {
			return e
		}
		if strings.HasPrefix(deployment.Profile, "v2_") && len(log.Topics) > 0 {
			if e = c.projectV2(ctx, q, source, deployment, log, revision); e != nil {
				return e
			}
		}
		for _, p := range event.Projections {
			if e = saveProjection(ctx, q, source, deployment.Address, log.BlockHash, p, revision); e != nil {
				return e
			}
		}
		for _, node := range event.Nodes {
			row, e := q.GetENSSyncLatestProjection(ctx, source, "preimage", node.Hex())
			if errors.Is(e, pgx.ErrNoRows) {
				continue
			}
			if e != nil {
				return e
			}
			var value struct{ Name string }
			if json.Unmarshal(row.Payload, &value) != nil {
				return ErrStorage
			}
			if _, ok := normalizedName(value.Name); ok {
				event.Candidates = append(event.Candidates, candidate{"forward", value.Name})
			}
		}
		if *revision > beforeRevision {
			if e = saveProjection(ctx, q, source, deployment.Address, log.BlockHash, projection{Kind: "lifecycle", Key: contractRevisionKey(deployment.Address), Fields: map[string]any{"event": v2Origin(log)}}, revision); e != nil {
				return e
			}
		}
		identity := log.BlockHash.Hex() + "/" + strconv.FormatUint(uint64(log.Index), 10)
		for _, value := range event.Candidates {
			if e = candidateOrigin(ctx, q, source, c.manifest.CoinType, value, "event", identity); e != nil {
				return e
			}
		}
	}
	return nil
}
func saveProjection(ctx context.Context, q *dbgen.Queries, source int64, deployment common.Address, block common.Hash, p projection, revision *int64) error {
	if p.Merge {
		row, e := q.GetENSSyncLatestProjection(ctx, source, p.Kind, p.Key)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if e == nil {
			fields := map[string]any{}
			if json.Unmarshal(row.Payload, &fields) != nil {
				return ErrStorage
			}
			maps.Copy(fields, p.Fields)
			p.Fields = fields
		}
	}
	if p.RecordNode != (common.Hash{}) {
		version := "0"
		row, e := q.GetENSSyncLatestProjection(ctx, source, "resolver_record", recordKey(deployment, p.RecordNode, "version"))
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if e == nil {
			var v struct{ Version string }
			if json.Unmarshal(row.Payload, &v) != nil {
				return ErrStorage
			}
			version = v.Version
		}
		p.Fields["version"] = version
	}
	payload, e := projectionJSON(p.Fields)
	if e != nil {
		return e
	}
	next, e := q.PutENSSyncProjection(ctx, dbgen.PutENSSyncProjectionParams{SourceID: source, Kind: p.Kind, Key: p.Key, ExpectedRevision: *revision, Deployment: deployment.Bytes(), BlockHash: block.Bytes(), Payload: payload})
	if e != nil {
		return e
	}
	if next > *revision {
		*revision = next
	}
	return nil
}
