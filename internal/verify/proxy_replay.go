package verify

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	pgx "github.com/jackc/pgx/v5"

	"github.com/ethereum/go-ethereum/common"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/stagecontract"
)

// requestVerificationProxyReplayTx persists explicit fixed-block candidates
// before waking proxy@2. It does not depend on an observation already existing
// in the verification block, which is important for otherwise quiet proxies.
func (repository *PostgresRepository) requestVerificationProxyReplayTx(
	ctx context.Context,
	tx pgx.Tx,
	job VerificationJob,
	blockNumber uint64,
	target common.Address,
	artifact *recognizedProxyArtifact,
	prepared *enrich.PreparedPublication,
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
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(strconv.FormatUint(job.RequestV2.Target.ChainID, 10)); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(blockNumber, 10)); err != nil {
			return err
		}
		var queryValue2 pgtype.UUID
		if err := queryValue2.Scan(job.ID); err != nil {
			return err
		}
		return dbgen.New(tx).VerifyLegacyVerificationProxyReplayTarget(ctx, dbgen.VerifyLegacyVerificationProxyReplayTargetParams{ChainID: queryValue0, Address: target[:], BlockNumber: queryValue1, BlockHash: blockHash[:], TargetKind: directTargetKind, SourceVerificationJobID: queryValue2})
	}(); err != nil {
		return fmt.Errorf("persist verification proxy replay target: %w", err)
	}
	queue, err := enrich.NewPostgresJobQueue(repository.db)
	if err != nil {
		return err
	}
	err = queue.ReplayPreparedTx(ctx, tx, prepared, stagecontract.EnqueueRequest{
		Stage: stagecontract.Proxy, ChainID: strconv.FormatUint(job.RequestV2.Target.ChainID, 10),
		BlockHash: blockHash, BlockNumber: blockNumber,
		Replay: stagecontract.ReplaySource{Kind: "verification-publication", Key: job.ID},
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

// prepareVerificationProxyPublicationTx only discovers lock identity. The caller
// must recheck canonical evidence after acquiring this publication fence.
func (repository *PostgresRepository) prepareVerificationProxyPublicationTx(ctx context.Context, tx pgx.Tx, chainID string, blockHash []byte) (*enrich.PreparedPublication, uint64, error) {
	var chain pgtype.Numeric
	if err := chain.Scan(chainID); err != nil {
		return nil, 0, err
	}
	number, err := dbgen.New(tx).VerifyPublicationBlockIdentity(ctx, chain, blockHash)
	if err != nil {
		return nil, 0, err
	}
	blockNumber, err := strconv.ParseUint(number, 10, 64)
	if err != nil {
		return nil, 0, err
	}
	queue, err := enrich.NewPostgresJobQueue(repository.db)
	if err != nil {
		return nil, 0, err
	}
	prepared, err := queue.PreparePublicationTx(ctx, tx, stagecontract.EnqueueRequest{Stage: stagecontract.Proxy, ChainID: chainID, BlockHash: common.BytesToHash(blockHash), BlockNumber: blockNumber})
	return prepared, blockNumber, err
}

func (repository *PostgresRepository) prepareCanonicalVerificationTarget(ctx context.Context, tx pgx.Tx, target *VerificationTarget) (*enrich.PreparedPublication, uint64, error) {
	if target == nil {
		return nil, 0, ErrTargetNotCanonical
	}
	hash, err := decodeFixedHex(target.AtBlockHash, 32)
	if err != nil {
		return nil, 0, ErrTargetNotCanonical
	}
	prepared, number, err := repository.prepareVerificationProxyPublicationTx(ctx, tx, strconv.FormatUint(target.ChainID, 10), hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, ErrTargetNotCanonical
	}
	if err != nil {
		return nil, 0, err
	}
	canonicalNumber, err := canonicalV2Target(ctx, tx, target)
	if err != nil || number != canonicalNumber {
		return nil, 0, ErrTargetNotCanonical
	}
	return prepared, number, nil
}
