package verify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/enrich"
)

// requestVerificationProxyReplayTx persists explicit fixed-block candidates
// before waking proxy@2. It does not depend on an observation already existing
// in the verification block, which is important for otherwise quiet proxies.
func (repository *PostgresRepository) requestVerificationProxyReplayTx(
	ctx context.Context,
	tx *sql.Tx,
	job VerificationJob,
	blockNumber uint64,
	target common.Address,
	artifact *recognizedProxyArtifact,
) error {
	if tx == nil || target == (common.Address{}) || job.RequestV2 == nil || job.RequestV2.Target == nil {
		return errors.New("verification proxy replay identity is invalid")
	}
	blockHashBytes, err := decodeFixedHex(job.RequestV2.Target.AtBlockHash, common.HashLength)
	if err != nil {
		return errors.New("verification proxy replay block hash is invalid")
	}
	blockHash := common.BytesToHash(blockHashBytes)
	directTargetKind := verificationProxyReplayTargetKind(artifact)
	// Verification publication is O(1): it persists only the address whose
	// code identity was verified. Association discovery belongs to ordinary
	// direct/current proxy replays; expanding historical associations here
	// would turn one verification into unbounded fixed-block RPC fanout.
	if _, err := tx.ExecContext(ctx, verificationProxyReplayTargetSQL,
		strconv.FormatUint(job.RequestV2.Target.ChainID, 10), target[:],
		strconv.FormatUint(blockNumber, 10), blockHash[:], directTargetKind, job.ID,
	); err != nil {
		return fmt.Errorf("persist verification proxy replay target: %w", err)
	}
	queue, err := enrich.NewPostgresJobQueue(repository.db)
	if err != nil {
		return err
	}
	_, err = queue.EnqueueTx(ctx, tx, enrich.EnqueueRequest{
		Stage: enrich.ProxyStage, ChainID: strconv.FormatUint(job.RequestV2.Target.ChainID, 10),
		BlockHash: blockHash, BlockNumber: blockNumber,
		Replay: enrich.ReplaySource{Kind: "verification-publication", Key: job.ID},
	})
	if err != nil {
		return fmt.Errorf("request durable proxy replay after verification publication: %w", err)
	}
	return nil
}

func verificationProxyReplayTargetKind(artifact *recognizedProxyArtifact) string {
	if artifact == nil {
		return "proxy"
	}
	switch artifact.Kind {
	case proxyArtifactUpgradeableBeacon:
		return "beacon"
	case proxyArtifactUUPS:
		return "uups"
	default:
		return "proxy"
	}
}

const verificationProxyReplayTargetSQL = `
INSERT INTO proxy_replay_targets (
    chain_id, block_number, block_hash, address, target_kind,
    source_kind, source_verification_job_id
) VALUES (
    $1::numeric, $3::numeric, $4, $2, $5,
    'verification_publication', $6::uuid
)
ON CONFLICT DO NOTHING`
