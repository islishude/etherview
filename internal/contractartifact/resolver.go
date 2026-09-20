package contractartifact

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/islishude/etherview/internal/db/gen"
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
	ValidToBlock          pgtype.Text
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
	CreatedAt             pgtype.Timestamptz
}

type Result struct {
	Resolution Resolution
	Target     Target
	Source     Source
}

type Resolver struct {
	db dbaccess.Database
}

func NewResolver(db dbaccess.Database) (*Resolver, error) {
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
	tx, err := resolver.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Result{}, false, fmt.Errorf("begin contract artifact snapshot: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)

	result := Result{Target: Target{ChainID: chainID, Address: append([]byte(nil), address...)}}
	var contextNumber string
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).ContractArtifactCurrentTarget(ctx, queryValue0, address)
		if err != nil {
			return err
		}
		result.Target.CodeHash = queryRow.CodeHash
		result.Target.BlockNumber = queryRow.ObservationBlockNumber
		result.Target.BlockHash = queryRow.BlockHash
		contextNumber = queryRow.TipNumber
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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
	tx, err := resolver.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Result{}, false, fmt.Errorf("begin contract artifact snapshot: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
	contextNumber := strconv.FormatUint(blockNumber, 10)
	result := Result{Target: Target{
		ChainID: chainID, Address: append([]byte(nil), address...),
	}}
	var sourceContext string
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(contextNumber); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).ContractArtifactTargetAtBlock(ctx, dbgen.ContractArtifactTargetAtBlockParams{ChainID: queryValue0, Address: address, Number: queryValue1, BlockHash: blockHash})
		if err != nil {
			return err
		}
		result.Target.CodeHash = queryRow.CodeHash
		result.Target.BlockNumber = queryRow.ContextNumber
		result.Target.BlockHash = queryRow.BlockHash
		sourceContext = queryRow.ContextNumber_2
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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
	tx pgx.Tx,
	result Result,
	contextNumber string,
) (Result, bool, error) {

	var exact bool
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(result.Target.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(contextNumber); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).ContractArtifactArtifactSource(ctx, dbgen.ContractArtifactArtifactSourceParams{ChainID: queryValue0, Address: result.Target.Address, CodeHash: result.Target.CodeHash, MaxValidFromBlock: queryValue1})
		if err != nil {
			return err
		}
		if queryRow.Exact == nil {
			return errors.New("invalid stored query value")
		}
		exact = *queryRow.Exact
		result.Source.Address = queryRow.Address
		result.Source.CodeHash = queryRow.CodeHash
		result.Source.ValidFromBlock = queryRow.VerifiedValidFromBlock
		resultValue5, err := dbaccess.NumericText(queryRow.ValidToBlock)
		if err != nil {
			return err
		}
		result.Source.ValidToBlock = resultValue5
		result.Source.VerificationJobID = queryRow.VerifiedVerificationJobID
		result.Source.RequestDigest = queryRow.RequestDigest
		result.Source.FileName = queryRow.FileName
		result.Source.ContractName = queryRow.ContractName
		result.Source.Language = queryRow.Language
		result.Source.CompilerVersion = queryRow.CompilerVersion
		result.Source.MatchType = queryRow.MatchType
		result.Source.ABI = queryRow.Abi
		result.Source.Sources = queryRow.Sources
		result.Source.Settings = queryRow.Settings
		result.Source.CompilationArtifacts = queryRow.CompilationArtifacts
		result.Source.CreationCodeArtifacts = queryRow.CreationCodeArtifacts
		result.Source.RuntimeCodeArtifacts = queryRow.RuntimeCodeArtifacts
		result.Source.CreationMatch = queryRow.CreationMatch
		result.Source.RuntimeMatch = queryRow.RuntimeMatch
		result.Source.ConstructorArguments = queryRow.ConstructorArguments
		result.Source.Libraries = queryRow.Libraries
		result.Source.IsBlueprint = queryRow.IsBlueprint
		result.Source.CreatedAt = queryRow.CreatedAt
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
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
	if err := tx.Commit(ctx); err != nil {
		return Result{}, false, fmt.Errorf("commit contract artifact snapshot: %w", err)
	}
	return result, true, nil
}
