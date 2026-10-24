package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/consensus"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
)

type ConsensusDiscovery struct {
	DB     dbaccess.Database
	Queue  *PostgresJobQueue
	Client *consensus.Client
	Config config.Config
	Logger *slog.Logger
}

func (*ConsensusDiscovery) Name() string { return "consensus-discovery" }
func (s *ConsensusDiscovery) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.Config.Consensus.PollInterval)
	defer ticker.Stop()
	for {
		if err := s.tick(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if s.Logger != nil {
				s.Logger.WarnContext(ctx, "consensus discovery failed", "error_code", "consensus_discovery_failed")
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
func (s *ConsensusDiscovery) tick(ctx context.Context) error {
	chain, _ := consensusNumeric(strconv.FormatUint(s.Config.Chain.ID, 10))
	q := dbgen.New(s.DB)
	for _, stage := range []StageID{ConsensusStage, BlobStage, RequestsStage} {
		if stage == BlobStage && !s.Config.Consensus.Blobs.Enabled {
			continue
		}
		if err := q.ConsensusInitializeDiscovery(ctx, chain, stage.Name); err != nil {
			return err
		}
		rows, err := q.ConsensusDiscover(ctx, chain, stage.Name)
		if err != nil {
			return err
		}
		for _, row := range rows {
			number, err := strconv.ParseUint(row.BlockNumber, 10, 64)
			if err != nil {
				return err
			}
			replay := ReplaySource{}
			if row.ReplayKey != "" {
				replay = ReplaySource{Kind: "canonical-attach", Key: row.ReplayKey}
			}
			_, err = s.Queue.Enqueue(ctx, EnqueueRequest{Kind: "enrichment", Stage: stage, ChainID: strconv.FormatUint(s.Config.Chain.ID, 10), BlockNumber: number, BlockHash: common.BytesToHash(row.BlockHash), Priority: -10, MaxAttempts: DefaultEnrichmentMaxAttempts, Replay: replay})
			if err != nil {
				return err
			}
		}
		if len(rows) > 0 {
			last, err := consensusNumeric(rows[len(rows)-1].BlockNumber)
			if err != nil {
				return err
			}
			latest, err := consensusNumeric(rows[0].BlockNumber)
			if err != nil {
				return err
			}
			if err := q.ConsensusAdvanceDiscovery(ctx, dbgen.ConsensusAdvanceDiscoveryParams{ChainID: chain, Stage: stage.Name, LastNumber: last, LatestNumber: latest, Generation: rows[0].Generation}); err != nil {
				return err
			}
		}
	}
	generation, err := q.ConsensusClaimPoll(ctx, chain, (8 * s.Config.Consensus.RequestTimeout).Milliseconds())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	overview, identity, fetchErr := s.observe(ctx)
	code := ""
	if fetchErr != nil {
		code = "consensus_unavailable"
		if remote, ok := errors.AsType[*consensus.Error](fetchErr); ok {
			code = remote.Code
		}
	}
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer dbaccess.Rollback(ctx, tx)
	queries := dbgen.New(tx)
	if fetchErr == nil {
		n, e := queries.ConsensusBindNetwork(ctx, chain, identity)
		if e != nil {
			return e
		}
		if n != 1 {
			code = "beacon_network_mismatch"
		}
	}
	n, err := queries.ConsensusPublishOverview(ctx, dbgen.ConsensusPublishOverviewParams{ChainID: chain, Generation: generation, Overview: overview, ErrorCode: code, PollMs: s.Config.Consensus.PollInterval.Milliseconds()})
	if err != nil {
		return err
	}
	if n != 1 {
		return nil
	}
	{
		if err := queries.ConsensusEvent(ctx, chain); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *ConsensusDiscovery) observe(ctx context.Context) ([]byte, []byte, error) {
	network, err := s.Client.Network(ctx, s.Config.Chain.ID)
	if err != nil {
		return nil, nil, err
	}
	chain, _ := consensusNumeric(strconv.FormatUint(s.Config.Chain.ID, 10))
	raw, err := dbgen.New(s.DB).ConsensusHeadSource(ctx, chain)
	if err != nil {
		return nil, nil, err
	}
	bundle, err := chainbundle.DecodeStoredBlock(raw)
	if err != nil {
		return nil, nil, err
	}
	if _, err := s.Client.Observe(ctx, network, bundle.Block); err != nil {
		return nil, nil, err
	}
	overview, err := s.Client.Overview(ctx, network)
	if err != nil {
		return nil, nil, err
	}
	overview.BlobsEnabled = s.Config.Consensus.Blobs.Enabled
	encoded, err := json.Marshal(overview)
	if err != nil {
		return nil, nil, err
	}
	identity, err := json.Marshal(network)
	return encoded, identity, err
}
