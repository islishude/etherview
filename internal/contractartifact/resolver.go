package contractartifact

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/islishude/etherview/internal/db/gen"
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
	err = tx.QueryRowContext(ctx, dbgen.ContractArtifactCurrentTarget, chainID, address).Scan(
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
	return resolveArtifactSourceTx(ctx, tx, result, contextNumber)
}

// ResolveAtBlock resolves only the canonical code identity and exact-address
// verified epoch that were valid at one immutable block identity.
func (resolver *Resolver) ResolveAtBlock(
	ctx context.Context,
	chainID string,
	address []byte,
	blockNumber uint64,
	blockHash []byte,
) (Result, bool, error) {
	if resolver == nil || resolver.db == nil || chainID == "" || len(address) != 20 ||
		len(blockHash) != 32 {
		return Result{}, false, errors.New("contract artifact block identity is invalid")
	}
	tx, err := resolver.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return Result{}, false, fmt.Errorf("begin contract artifact snapshot: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	contextNumber := strconv.FormatUint(blockNumber, 10)
	result := Result{Target: Target{
		ChainID: chainID, Address: append([]byte(nil), address...),
	}}
	var sourceContext string
	err = tx.QueryRowContext(
		ctx, dbgen.ContractArtifactTargetAtBlock,
		chainID, address, contextNumber, blockHash,
	).Scan(
		&result.Target.CodeHash, &result.Target.BlockNumber,
		&result.Target.BlockHash, &sourceContext,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, fmt.Errorf("resolve historical contract code identity: %w", err)
	}
	if len(result.Target.CodeHash) != 32 || len(result.Target.BlockHash) != 32 ||
		result.Target.BlockNumber != contextNumber || sourceContext != contextNumber {
		return Result{}, false, errors.New("stored historical contract code identity is invalid")
	}
	return resolveArtifactSourceTx(ctx, tx, result, sourceContext)
}

func resolveArtifactSourceTx(
	ctx context.Context,
	tx *sql.Tx,
	result Result,
	contextNumber string,
) (Result, bool, error) {

	var exact bool
	err := tx.QueryRowContext(
		ctx, dbgen.ContractArtifactArtifactSource,
		result.Target.ChainID, result.Target.Address, result.Target.CodeHash, contextNumber,
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
