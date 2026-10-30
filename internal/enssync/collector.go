package enssync

import (
	"context"
	"errors"
	"time"

	"github.com/ethereum/go-ethereum/common"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
)

// CollectorOptions bounds one reconciliation. A disabled Step makes no source calls.
type CollectorOptions struct {
	MaxBlocks, MaxLogs, MaxBytes, MaxAddresses int
	Timeout, Health                            time.Duration
}

type Collector struct {
	repo     *Repository
	manifest Manifest
	digest   common.Hash
	profiles map[common.Address]contractProfile
	pool     *ethrpc.Pool
	options  CollectorOptions
}

type CollectionResult struct {
	SourceID int64
	Scanned  int
	Rewound  bool
}

func NewCollector(repo *Repository, manifest Manifest, pool *ethrpc.Pool, options CollectorOptions) (*Collector, error) {
	if repo == nil {
		return nil, ErrInvalidInput
	}
	m, digest, profiles, err := validateManifest(manifest)
	if err != nil {
		return nil, err
	}
	if options.MaxBlocks == 0 {
		options.MaxBlocks = 8
	}
	if options.MaxLogs == 0 {
		options.MaxLogs = 4096
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = 1 << 20
	}
	if options.MaxAddresses == 0 {
		options.MaxAddresses = 100
	}
	if options.Timeout == 0 {
		// One collection round verifies deployments and reads multiple exact
		// headers/log sets. It needs a separate budget from one RPC request.
		options.Timeout = time.Minute
	}
	if options.Health == 0 {
		options.Health = 15 * time.Minute
	}
	if options.MaxBlocks < 1 || options.MaxBlocks > 256 || options.MaxLogs < 1 || options.MaxLogs > 16384 || options.MaxBytes < 1 || options.MaxBytes > 16<<20 || options.MaxAddresses < 1 || options.MaxAddresses > 1000 || options.Timeout < 100*time.Millisecond || options.Timeout > time.Minute || options.Health < time.Second || options.Health > 24*time.Hour {
		return nil, ErrInvalidInput
	}
	return &Collector{repo, m, digest, profiles, pool, options}, nil
}

// Step discovers local addresses independently of event progress, then collects
// a contiguous bounded source segment. External work never holds a DB snapshot.
func (c *Collector) Step(ctx context.Context, enabled bool) (result CollectionResult, err error) {
	if !enabled {
		return result, nil
	}
	c = c.copyForStep()
	ctx, cancel := context.WithTimeout(ctx, c.options.Timeout)
	defer cancel()
	result.SourceID, err = c.EnsureSource(ctx)
	if err != nil {
		return result, ErrStorage
	}
	source := result.SourceID
	progress, err := dbgen.New(c.repo.db).GetENSSyncProgress(ctx, source)
	if err != nil {
		return result, ErrStorage
	}
	if progress.State == "halted" {
		return result, ErrSourceIntegrity
	}
	if progress.State == "unsupported" {
		return result, ErrUnsupportedDeployment
	}
	defer func() {
		if err == nil || errors.Is(err, ErrFenceLost) || ctx.Err() != nil {
			return
		}
		state, code := "pending", "storage_unavailable"
		switch {
		case errors.Is(err, ErrSourceIntegrity):
			state, code = "halted", "finalized_integrity"
		case errors.Is(err, ErrSourceIdentity), errors.Is(err, ErrUnsupportedDeployment):
			state, code = "unsupported", "deployment_identity"
		case errors.Is(err, ErrCoverageGap):
			code = "coverage_gap"
		case errors.Is(err, ErrRPCUnavailable):
			code = "rpc_unavailable"
		case errors.Is(err, ErrInvalidEvent):
			state, code = "unsupported", "invalid_event"
		case errors.Is(err, ErrCollectionLimit):
			code = "collection_limit"
		}
		_, _ = dbgen.New(c.repo.db).FailENSSyncCollection(ctx, dbgen.FailENSSyncCollectionParams{SourceID: source, Revision: progress.Revision, State: state, FailureCode: &code})
	}()
	if err = c.discoverAddresses(ctx, source); err != nil {
		return result, err
	}
	result.Rewound, err = c.rewind(ctx, source, progress)
	if err != nil || result.Rewound {
		return result, err
	}
	target, err := c.repo.localTarget(ctx, c.manifest)
	var session rpcSession
	if err == nil && c.pool != nil {
		endpoint, e := c.pool.Acquire(ethrpc.PurposeState)
		if e != nil {
			return result, ErrRPCUnavailable
		}
		session = rpcSession{endpoint, c.options}
		if e = session.verifyIdentity(ctx, c.manifest); e != nil {
			return result, e
		}
	}
	if err != nil {
		return result, err
	}
	if target.Number.Uint64() < c.manifest.Start {
		return result, ErrCoverageGap
	}
	// Publish the current core target independently of historical collection.
	// Workers authenticate all reviewed entrypoints at their own exact block.
	if err = c.commit(ctx, source, progress, target, nil, nil); err != nil {
		return result, err
	}
	deployments := make([]verifiedDeployment, 0, len(c.manifest.Deployments))
	for _, d := range c.manifest.Deployments {
		if d.Number > target.Number.Uint64() {
			continue
		}
		var verified verifiedDeployment
		if session.endpoint != nil {
			// Historical identities must already exist in core storage.
			var local collectedBlock
			local, err = c.repo.localBlock(ctx, c.manifest, d.Number, c.options.MaxLogs)
			if err == nil && local.Header.Hash() != d.Hash {
				err = ErrSourceIdentity
			}
			if err == nil {
				verified.Header, err = session.verifyDeployment(ctx, d, target)
			}
			verified.Deployment = d
		} else {
			verified, err = c.repo.localDeployment(ctx, c.manifest, d)
		}
		if err != nil {
			return result, err
		}
		deployments = append(deployments, verified)
	}
	next := c.manifest.Start
	if progress.ScannedNumber.Valid {
		n, e := uintNumber(progress.ScannedNumber)
		if e != nil {
			return result, e
		}
		if n == ^uint64(0) {
			return result, ErrCollectionLimit
		}
		next = n + 1
	}
	blocks := make([]collectedBlock, 0, c.options.MaxBlocks)
	parent := common.BytesToHash(progress.ScannedHash)
	for number := next; number <= target.Number.Uint64() && len(blocks) < c.options.MaxBlocks; number++ {
		block, err := c.repo.localBlock(ctx, c.manifest, number, c.options.MaxLogs)
		if err != nil {
			return result, err
		}
		if (number == c.manifest.Start && block.Header.Hash() != c.manifest.StartHash) || (number > c.manifest.Start && block.Header.ParentHash != parent) {
			return result, ErrSourceIdentity
		}
		if number == target.Number.Uint64() && block.Header.Hash() != target.Hash() {
			return result, ErrSourceIntegrity
		}
		if c.manifest.hasV2() {
			var found []verifiedDeployment
			block, found, err = c.discoverV2(ctx, source, session, block)
			if err != nil {
				return result, err
			}
			deployments = append(deployments, found...)
		}
		parent = block.Header.Hash()
		blocks = append(blocks, block)
		if number == ^uint64(0) {
			break
		}
	}
	if err = c.commit(ctx, source, progress, target, deployments, blocks); err != nil {
		return result, err
	}
	result.Scanned = len(blocks)
	if err = c.expandGraph(ctx, source); err != nil {
		return result, err
	}
	if err = c.repairHistory(ctx, source); err != nil {
		return result, err
	}
	return result, nil
}

// EnsureSource registers only reviewed identity; it performs no source calls.
func (c *Collector) EnsureSource(ctx context.Context) (int64, error) {
	targetChain, _ := unsignedNumeric(c.manifest.TargetChainID)
	sourceChain, _ := unsignedNumeric(c.manifest.SourceChainID)
	return c.repo.CreateSource(ctx, dbgen.CreateENSSyncSourceParams{TargetChainID: targetChain, SourceChainID: sourceChain, GenesisHash: c.manifest.Genesis.Bytes(), PolicyDigest: c.manifest.Policy.Bytes(), ManifestDigest: c.digest.Bytes(), StartNumber: numeric(c.manifest.Start), StartHash: c.manifest.StartHash.Bytes()})
}
