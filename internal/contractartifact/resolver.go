package contractartifact

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type Resolution string

const (
	ResolutionExactAddress Resolution = "exact_address"
	ResolutionCodeHash     Resolution = "code_hash"
)

type Target struct {
	ChainID     string
	Address     []byte
	CodeHash    []byte
	BlockNumber string
	BlockHash   []byte
}

type Source struct {
	Address               []byte
	CodeHash              []byte
	ValidFromBlock        string
	ValidToBlock          sql.NullString
	VerificationJobID     string
	RequestDigest         []byte
	FileName              string
	ContractName          string
	Language              string
	CompilerVersion       string
	MatchType             string
	ABI                   []byte
	Sources               []byte
	Settings              []byte
	CompilationArtifacts  []byte
	CreationCodeArtifacts []byte
	RuntimeCodeArtifacts  []byte
	CreationMatch         []byte
	RuntimeMatch          []byte
	ConstructorArguments  []byte
	Libraries             []byte
	IsBlueprint           bool
	CreatedAt             sql.NullTime
}

type Result struct {
	Resolution Resolution
	Target     Target
	Source     Source
}

type Resolver struct {
	db *sql.DB
}

func NewResolver(db *sql.DB) (*Resolver, error) {
	if db == nil {
		return nil, errors.New("contract artifact database is nil")
	}
	return &Resolver{db: db}, nil
}

func (resolver *Resolver) ResolveCurrent(
	ctx context.Context,
	chainID string,
	address []byte,
) (Result, bool, error) {
	if resolver == nil || resolver.db == nil || chainID == "" || len(address) != 20 {
		return Result{}, false, errors.New("contract artifact identity is invalid")
	}
	tx, err := resolver.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return Result{}, false, fmt.Errorf("begin contract artifact snapshot: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	result := Result{Target: Target{ChainID: chainID, Address: append([]byte(nil), address...)}}
	var contextNumber string
	err = tx.QueryRowContext(ctx, currentTargetSQL, chainID, address).Scan(
		&result.Target.CodeHash,
		&result.Target.BlockNumber,
		&result.Target.BlockHash,
		&contextNumber,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, fmt.Errorf("resolve current contract code identity: %w", err)
	}
	if len(result.Target.CodeHash) != 32 || len(result.Target.BlockHash) != 32 {
		return Result{}, false, errors.New("stored current contract code identity is invalid")
	}

	var exact bool
	err = tx.QueryRowContext(ctx, artifactSourceSQL,
		chainID, address, result.Target.CodeHash, contextNumber,
	).Scan(
		&exact,
		&result.Source.Address,
		&result.Source.CodeHash,
		&result.Source.ValidFromBlock,
		&result.Source.ValidToBlock,
		&result.Source.VerificationJobID,
		&result.Source.RequestDigest,
		&result.Source.FileName,
		&result.Source.ContractName,
		&result.Source.Language,
		&result.Source.CompilerVersion,
		&result.Source.MatchType,
		&result.Source.ABI,
		&result.Source.Sources,
		&result.Source.Settings,
		&result.Source.CompilationArtifacts,
		&result.Source.CreationCodeArtifacts,
		&result.Source.RuntimeCodeArtifacts,
		&result.Source.CreationMatch,
		&result.Source.RuntimeMatch,
		&result.Source.ConstructorArguments,
		&result.Source.Libraries,
		&result.Source.IsBlueprint,
		&result.Source.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return Result{}, false, fmt.Errorf("commit contract artifact snapshot: %w", err)
		}
		return result, false, nil
	}
	if err != nil {
		return Result{}, false, fmt.Errorf("resolve verified contract artifact: %w", err)
	}
	if len(result.Source.Address) != 20 || len(result.Source.CodeHash) != 32 ||
		len(result.Source.RequestDigest) != 32 || !result.Source.CreatedAt.Valid {
		return Result{}, false, errors.New("stored verified contract artifact identity is invalid")
	}
	if exact {
		result.Resolution = ResolutionExactAddress
	} else {
		result.Resolution = ResolutionCodeHash
	}
	if err := tx.Commit(); err != nil {
		return Result{}, false, fmt.Errorf("commit contract artifact snapshot: %w", err)
	}
	return result, true, nil
}

const currentTargetSQL = `
WITH canonical_tip AS (
    SELECT number
    FROM canonical_blocks
    WHERE chain_id = $1::numeric
    ORDER BY number DESC
    LIMIT 1
)
SELECT observation.code_hash, observation.block_number::text,
       observation.block_hash, tip.number::text
FROM canonical_tip AS tip
JOIN LATERAL (
    SELECT candidate.code_hash, candidate.block_number, candidate.block_hash
    FROM contract_code_observations AS candidate
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = candidate.chain_id
     AND canonical.number = candidate.block_number
     AND canonical.block_hash = candidate.block_hash
    WHERE candidate.chain_id = $1::numeric
      AND candidate.address = $2
      AND candidate.canonical
      AND candidate.block_number <= tip.number
    ORDER BY candidate.block_number DESC,
             candidate.observed_at DESC,
             candidate.code_hash DESC
    LIMIT 1
) AS observation ON TRUE`

const artifactSourceSQL = `
SELECT
       (verified.address = $2
        AND verified.valid_from_block <= $4::numeric
        AND (verified.valid_to_block IS NULL OR verified.valid_to_block >= $4::numeric)) AS exact,
       verified.address, verified.code_hash, verified.valid_from_block::text,
       verified.valid_to_block::text, verified.verification_job_id::text,
       verified.request_digest, verified.file_name, verified.contract_name,
       verified.language, verified.compiler_version, verified.match_type,
       verified.abi, verified.sources, verified.settings,
       verified.compilation_artifacts, verified.creation_code_artifacts,
       verified.runtime_code_artifacts, result.outcome->'creation_match',
       result.outcome->'runtime_match', verified.constructor_arguments,
       verified.libraries, verified.is_blueprint, verified.created_at
FROM verified_contracts AS verified
JOIN verification_results AS result
  ON result.job_id = verified.verification_job_id
 AND result.request_digest = verified.request_digest
 AND result.outcome_kind = 'verification_success'
WHERE verified.chain_id = $1::numeric
  AND verified.code_hash = $3
ORDER BY
    (verified.address = $2
     AND verified.valid_from_block <= $4::numeric
     AND (verified.valid_to_block IS NULL OR verified.valid_to_block >= $4::numeric)) DESC,
    (verified.abi IS NOT NULL) DESC,
    (verified.match_type = 'full') DESC,
    verified.request_digest ASC,
    verified.verification_job_id ASC,
    verified.address ASC,
    verified.valid_from_block DESC
LIMIT 1`
