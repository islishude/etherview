package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/islishude/etherview/internal/consensus"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type retainedBeaconInput struct {
	consensus.Evidence
	raw        json.RawMessage
	generation int64
	attempts   int32
}

func readBeaconInput(ctx context.Context, q *dbgen.Queries, job Job, chain, height pgtype.Numeric) (retainedBeaconInput, error) {
	var input retainedBeaconInput
	_, generation, err := durableJobGeneration(job)
	if err != nil {
		return input, err
	}
	conflict, err := q.BeaconNetworkConflict(ctx, chain)
	if err != nil {
		return input, err
	}
	if conflict {
		return input, &consensus.Error{Code: "beacon_network_mismatch"}
	}
	key := dbgen.BlockInputReadParams{ChainID: chain, BlockNumber: height, BlockHash: job.BlockHash[:], Kind: job.Stage.Name}
	row, err := q.BlockInputRead(ctx, key)
	switch {
	case errors.Is(err, pgx.ErrNoRows), err == nil && (row.ErrorCode != "" || job.Stage == BlobStage) && generation > row.Generation:
		err = q.BlockInputSubmit(ctx, dbgen.BlockInputSubmitParams{ChainID: chain, BlockNumber: height, BlockHash: job.BlockHash[:], Kind: job.Stage.Name, Generation: generation})
		if err != nil {
			return input, err
		}
		return input, errInputPending
	case err != nil:
		return input, err
	case row.ErrorCode != "":
		return input, &consensus.Error{Code: row.ErrorCode}
	case len(row.Evidence) == 0:
		return input, errInputPending
	}
	if json.Unmarshal(row.Evidence, &input.Evidence) != nil || (job.Stage != BlobStage && strconv.FormatUint(input.Network.ChainID, 10) != job.ChainID) {
		return input, Permanent(errors.New("beacon_input_invalid"))
	}
	input.generation, input.attempts, input.raw = row.Generation, row.Attempts, row.Evidence
	return input, nil
}

// Only the exact evidence the processor inspected can be replaced.
func retryBeaconInput(ctx context.Context, q *dbgen.Queries, job Job, chain, height pgtype.Numeric, input retainedBeaconInput, cause error) (StageResult, error) {
	remote, ok := errors.AsType[*consensus.Error](cause)
	if !ok || !remote.Retry {
		return consensusFailure(cause)
	}
	if input.attempts < 4 {
		_, err := q.BlockInputRetryEvidence(ctx, dbgen.BlockInputRetryEvidenceParams{ChainID: chain, BlockNumber: height, BlockHash: job.BlockHash[:], Kind: job.Stage.Name, Generation: input.generation, Previous: input.raw})
		if err != nil {
			return StageResult{}, err
		}
		return StageResult{}, errInputPending
	}
	return consensusFailure(&consensus.Error{Code: remote.Code})
}
