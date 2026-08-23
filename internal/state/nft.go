package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
	"github.com/islishude/etherview/internal/query"
)

var (
	erc721OwnerOfSelector    = []byte{0x63, 0x52, 0x21, 0x1e}
	erc1155BalanceOfSelector = []byte{0x00, 0xfd, 0xd5, 0x8e}
	maximumUint256           = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

	// ErrExactNFTObservationConflict means two exact block-hash RPC reads
	// disagreed for the same immutable NFT fact. The first durable observation
	// remains authoritative and is never overwritten.
	ErrExactNFTObservationConflict = errors.New("exact NFT state observation conflicts with persisted block fact")
)

// NFTReconciler turns event-derived NFT candidates into exact state
// observations at one canonical block hash. Persisted observations are keyed
// by that hash and can therefore be reused without treating an orphan as
// current after a reorg.
type NFTReconciler struct {
	db        *sql.DB
	pool      *ethrpc.Pool
	canonical CanonicalSource
}

type parsedNFTCandidate struct {
	standard catalogStandard
	address  common.Address
	tokenID  *big.Int
}

type nftBalanceBatchResult struct {
	index            int
	balance          catalog.NFTBalanceObservation
	ownerObservation *catalog.NFTOwnerObservation
}

var _ catalog.NFTStateReconciler = (*NFTReconciler)(nil)

func NewNFTReconciler(db *sql.DB, pool *ethrpc.Pool, canonical CanonicalSource) (*NFTReconciler, error) {
	if db == nil {
		return nil, errors.New("NFT reconciler requires PostgreSQL")
	}
	if pool == nil {
		return nil, errors.New("NFT reconciler requires a state RPC pool")
	}
	if canonical == nil {
		return nil, errors.New("NFT reconciler requires a canonical source")
	}
	return &NFTReconciler{db: db, pool: pool, canonical: canonical}, nil
}

func (reconciler *NFTReconciler) Owner(
	ctx context.Context,
	snapshot catalog.Snapshot,
	tokenAddressText string,
	tokenIDText string,
) (catalog.NFTOwnerObservation, error) {
	reference, chainID, tokenAddress, tokenID, err := validateNFTRequest(snapshot, tokenAddressText, tokenIDText)
	if err != nil {
		return catalog.NFTOwnerObservation{}, err
	}
	if cached, found, err := reconciler.cachedOwner(ctx, chainID, tokenAddress, tokenID, reference); err != nil {
		return catalog.NFTOwnerObservation{}, err
	} else if found {
		if err := reconciler.requireCanonical(ctx, reference); err != nil {
			return catalog.NFTOwnerObservation{}, err
		}
		return cached, nil
	}

	endpoint, err := reconciler.pool.Acquire(ethrpc.PurposeState)
	if err != nil {
		return catalog.NFTOwnerObservation{}, nftRPCUnavailable()
	}
	observation, err := callERC721Owner(ctx, endpoint, reference, tokenAddress, tokenID)
	if err != nil {
		reconciler.pool.ReportFailure(endpoint.Name)
		return catalog.NFTOwnerObservation{}, err
	}
	reconciler.pool.ReportSuccess(endpoint.Name)
	if err := reconciler.requireCanonical(ctx, reference); err != nil {
		return catalog.NFTOwnerObservation{}, err
	}
	if err := reconciler.persistOwner(ctx, chainID, tokenAddress, tokenID, reference, observation); err != nil {
		return catalog.NFTOwnerObservation{}, err
	}
	if err := reconciler.requireCanonical(ctx, reference); err != nil {
		return catalog.NFTOwnerObservation{}, err
	}
	return observation, nil
}

func (reconciler *NFTReconciler) Balances(
	ctx context.Context,
	snapshot catalog.Snapshot,
	ownerAddressText string,
	candidates []catalog.NFTBalanceCandidate,
) ([]catalog.NFTBalanceObservation, error) {
	if reconciler == nil || reconciler.db == nil || reconciler.pool == nil || reconciler.canonical == nil {
		return nil, nftRPCUnavailable()
	}
	if len(candidates) == 0 {
		return []catalog.NFTBalanceObservation{}, nil
	}
	reference, chainID, err := validateNFTSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	ownerAddress, err := ethrpc.ParseAddress(ownerAddressText)
	if err != nil {
		return nil, errors.New("invalid NFT owner address")
	}

	parsed := make([]parsedNFTCandidate, len(candidates))
	results := make([]catalog.NFTBalanceObservation, len(candidates))
	missing := make([]int, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for index, candidate := range candidates {
		standard, err := parseCatalogStandard(candidate.Standard)
		if err != nil {
			return nil, err
		}
		address, err := ethrpc.ParseAddress(candidate.TokenAddress)
		if err != nil {
			return nil, fmt.Errorf("invalid NFT candidate address %d", index)
		}
		tokenID, err := parseUint256(candidate.TokenID)
		if err != nil {
			return nil, fmt.Errorf("invalid NFT candidate token ID %d", index)
		}
		key := standard.String() + ":" + strings.ToLower(address.String()) + ":" + tokenID.String()
		if _, duplicate := seen[key]; duplicate {
			return nil, errors.New("duplicate NFT balance candidate")
		}
		seen[key] = struct{}{}
		parsed[index] = parsedNFTCandidate{standard: standard, address: address, tokenID: tokenID}

		cached, found, err := reconciler.cachedBalance(ctx, chainID, ownerAddress, parsed[index], reference)
		if err != nil {
			return nil, err
		}
		if found {
			results[index] = cached
			continue
		}
		missing = append(missing, index)
	}
	if len(missing) == 0 {
		if err := reconciler.requireCanonical(ctx, reference); err != nil {
			return nil, err
		}
		return results, nil
	}

	endpoint, err := reconciler.pool.Acquire(ethrpc.PurposeState)
	if err != nil {
		return nil, nftRPCUnavailable()
	}
	batchResults, err := callNFTBalanceBatch(ctx, endpoint, reference, ownerAddress, parsed, missing)
	if err != nil {
		reconciler.pool.ReportFailure(endpoint.Name)
		return nil, err
	}
	ownerObservations := make(map[int]catalog.NFTOwnerObservation)
	for _, result := range batchResults {
		results[result.index] = result.balance
		if result.ownerObservation != nil {
			ownerObservations[result.index] = *result.ownerObservation
		}
	}
	reconciler.pool.ReportSuccess(endpoint.Name)
	if err := reconciler.requireCanonical(ctx, reference); err != nil {
		return nil, err
	}
	if err := reconciler.persistBalances(ctx, chainID, ownerAddress, parsed, results, missing, ownerObservations, reference); err != nil {
		return nil, err
	}
	if err := reconciler.requireCanonical(ctx, reference); err != nil {
		return nil, err
	}
	return results, nil
}

type catalogStandard uint8

const (
	standardERC721 catalogStandard = iota + 1
	standardERC1155
)

func (standard catalogStandard) String() string {
	if standard == standardERC721 {
		return "erc721"
	}
	return "erc1155"
}

func parseCatalogStandard(value string) (catalogStandard, error) {
	switch value {
	case "erc721":
		return standardERC721, nil
	case "erc1155":
		return standardERC1155, nil
	default:
		return 0, errors.New("NFT candidate has an unsupported standard")
	}
}

func validateNFTRequest(
	snapshot catalog.Snapshot,
	tokenAddressText string,
	tokenIDText string,
) (CanonicalRef, string, common.Address, *big.Int, error) {
	reference, chainID, err := validateNFTSnapshot(snapshot)
	if err != nil {
		return CanonicalRef{}, "", common.Address{}, nil, err
	}
	tokenAddress, err := ethrpc.ParseAddress(tokenAddressText)
	if err != nil {
		return CanonicalRef{}, "", common.Address{}, nil, errors.New("invalid NFT contract address")
	}
	tokenID, err := parseUint256(tokenIDText)
	if err != nil {
		return CanonicalRef{}, "", common.Address{}, nil, errors.New("invalid NFT token ID")
	}
	return reference, chainID, tokenAddress, tokenID, nil
}

func validateNFTSnapshot(snapshot catalog.Snapshot) (CanonicalRef, string, error) {
	chainID, err := parseUint256(snapshot.ChainID)
	if err != nil || chainID.Sign() == 0 {
		return CanonicalRef{}, "", errors.New("invalid NFT state chain ID")
	}
	blockNumber, err := strconv.ParseUint(snapshot.BlockNumber, 10, 64)
	if err != nil || strconv.FormatUint(blockNumber, 10) != snapshot.BlockNumber {
		return CanonicalRef{}, "", errors.New("invalid NFT state block number")
	}
	blockHash, err := ethrpc.ParseHash(snapshot.BlockHash)
	if err != nil {
		return CanonicalRef{}, "", errors.New("invalid NFT state block hash")
	}
	return CanonicalRef{Number: blockNumber, Hash: blockHash}, chainID.String(), nil
}

func parseUint256(value string) (*big.Int, error) {
	if value == "" || len(value) > 1 && value[0] == '0' {
		return nil, errors.New("uint256 is not canonical decimal")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return nil, errors.New("uint256 contains a non-decimal digit")
		}
	}
	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok || parsed.Sign() < 0 || parsed.Cmp(maximumUint256) > 0 {
		return nil, errors.New("uint256 is out of range")
	}
	return parsed, nil
}

func callERC721Owner(
	ctx context.Context,
	endpoint *ethrpc.Endpoint,
	reference CanonicalRef,
	contract common.Address,
	tokenID *big.Int,
) (catalog.NFTOwnerObservation, error) {
	result, err := fixedNFTCall(ctx, endpoint, reference, contract, erc721OwnerCallData(tokenID))
	return decodeERC721Owner(result, err)
}

func erc721OwnerCallData(tokenID *big.Int) []byte {
	callData := make([]byte, 4+32)
	copy(callData, erc721OwnerOfSelector)
	tokenID.FillBytes(callData[4:])
	return callData
}

func decodeERC721Owner(result []byte, err error) (catalog.NFTOwnerObservation, error) {
	if err != nil {
		if nftExecutionReverted(err) {
			return catalog.NFTOwnerObservation{Confidence: catalog.NFTStateConfidenceRPCExact}, nil
		}
		return catalog.NFTOwnerObservation{}, nftRPCUnavailable()
	}
	if len(result) != 32 {
		return catalog.NFTOwnerObservation{}, nftRPCUnavailable()
	}
	for _, value := range result[:12] {
		if value != 0 {
			return catalog.NFTOwnerObservation{}, nftRPCUnavailable()
		}
	}
	owner := common.BytesToAddress(result[12:])
	if owner == (common.Address{}) {
		return catalog.NFTOwnerObservation{}, nftRPCUnavailable()
	}
	checksummed, err := query.ChecksumAddress(owner.Hex())
	if err != nil {
		return catalog.NFTOwnerObservation{}, errors.New("checksum exact ERC-721 owner")
	}
	return catalog.NFTOwnerObservation{
		Exists: true, Owner: checksummed, Confidence: catalog.NFTStateConfidenceRPCExact,
	}, nil
}

func callERC1155Balance(
	ctx context.Context,
	endpoint *ethrpc.Endpoint,
	reference CanonicalRef,
	contract common.Address,
	owner common.Address,
	tokenID *big.Int,
) (string, error) {
	result, err := fixedNFTCall(ctx, endpoint, reference, contract, erc1155BalanceCallData(owner, tokenID))
	return decodeERC1155Balance(result, err)
}

func erc1155BalanceCallData(owner common.Address, tokenID *big.Int) []byte {
	ownerBytes := owner.Bytes()
	callData := make([]byte, 4+64)
	copy(callData, erc1155BalanceOfSelector)
	copy(callData[4+32-len(ownerBytes):4+32], ownerBytes)
	tokenID.FillBytes(callData[4+32:])
	return callData
}

func decodeERC1155Balance(result []byte, err error) (string, error) {
	if err != nil || len(result) != 32 {
		return "", nftRPCUnavailable()
	}
	return new(big.Int).SetBytes(result).String(), nil
}

func callNFTBalanceBatch(
	ctx context.Context,
	endpoint *ethrpc.Endpoint,
	reference CanonicalRef,
	owner common.Address,
	candidates []parsedNFTCandidate,
	missing []int,
) ([]nftBalanceBatchResult, error) {
	rpcResults := make([]hexutil.Bytes, len(missing))
	elements := make([]rpc.BatchElem, len(missing))
	for batchIndex, candidateIndex := range missing {
		candidate := candidates[candidateIndex]
		var callData []byte
		switch candidate.standard {
		case standardERC721:
			callData = erc721OwnerCallData(candidate.tokenID)
		case standardERC1155:
			callData = erc1155BalanceCallData(owner, candidate.tokenID)
		default:
			return nil, errors.New("NFT balance candidate has an unsupported standard")
		}
		elements[batchIndex] = rpc.BatchElem{
			Method: "eth_call",
			Args: []any{
				fixedNFTCallObject(candidate.address, callData),
				canonicalSelector(reference),
			},
			Result: &rpcResults[batchIndex],
		}
	}
	if err := endpoint.BatchCallContext(ctx, elements); err != nil {
		return nil, nftRPCUnavailable()
	}
	results := make([]nftBalanceBatchResult, len(missing))
	for batchIndex, candidateIndex := range missing {
		candidate := candidates[candidateIndex]
		result := nftBalanceBatchResult{index: candidateIndex}
		switch candidate.standard {
		case standardERC721:
			observation, err := decodeERC721Owner(rpcResults[batchIndex], elements[batchIndex].Error)
			if err != nil {
				return nil, err
			}
			balance := "0"
			if observation.Exists {
				observedOwner, err := ethrpc.ParseAddress(observation.Owner)
				if err != nil {
					return nil, errors.New("invalid persisted ERC-721 owner")
				}
				if observedOwner == owner {
					balance = "1"
				}
			}
			result.balance = exactNFTBalance(balance)
			result.ownerObservation = &observation
		case standardERC1155:
			balance, err := decodeERC1155Balance(rpcResults[batchIndex], elements[batchIndex].Error)
			if err != nil {
				return nil, err
			}
			result.balance = exactNFTBalance(balance)
		}
		results[batchIndex] = result
	}
	return results, nil
}

func fixedNFTCall(
	ctx context.Context,
	endpoint *ethrpc.Endpoint,
	reference CanonicalRef,
	contract common.Address,
	callData []byte,
) ([]byte, error) {
	var result hexutil.Bytes
	if err := endpoint.CallContext(
		ctx,
		&result,
		"eth_call",
		fixedNFTCallObject(contract, callData),
		canonicalSelector(reference),
	); err != nil {
		return nil, err
	}
	return []byte(result), nil
}

func fixedNFTCallObject(contract common.Address, callData []byte) map[string]any {
	return map[string]any{"to": contract, "data": hexutil.Bytes(callData)}
}

func nftExecutionReverted(err error) bool {
	var rpcError rpc.Error
	if !errors.As(err, &rpcError) {
		return false
	}
	message := strings.ToLower(rpcError.Error())
	return rpcError.ErrorCode() == 3 || strings.Contains(message, "execution reverted") || strings.Contains(message, "revert")
}

func nftRPCUnavailable() error {
	return fmt.Errorf("%w: exact NFT state RPC is unavailable", httpapi.ErrUnavailable)
}

func exactNFTBalance(balance string) catalog.NFTBalanceObservation {
	return catalog.NFTBalanceObservation{Balance: balance, Confidence: catalog.NFTStateConfidenceRPCExact}
}

func (reconciler *NFTReconciler) requireCanonical(ctx context.Context, reference CanonicalRef) error {
	canonical, err := reconciler.canonical.IsCanonical(ctx, reference)
	if err != nil {
		return fmt.Errorf("recheck exact NFT state block: %w", err)
	}
	if !canonical {
		return fmt.Errorf("%w: canonical block changed during NFT state reconciliation", httpapi.ErrNotReady)
	}
	return nil
}

func (reconciler *NFTReconciler) cachedOwner(
	ctx context.Context,
	chainID string,
	contract common.Address,
	tokenID *big.Int,
	reference CanonicalRef,
) (catalog.NFTOwnerObservation, bool, error) {
	contractBytes := contract.Bytes()
	hashBytes := reference.Hash.Bytes()
	var state, confidence string
	var ownerBytes []byte
	err := reconciler.db.QueryRowContext(ctx, dbgen.StateERC721OwnerObservation,
		chainID, contractBytes, tokenID.String(), strconv.FormatUint(reference.Number, 10), hashBytes,
	).Scan(&state, &ownerBytes, &confidence)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.NFTOwnerObservation{}, false, nil
	}
	if err != nil {
		return catalog.NFTOwnerObservation{}, false, fmt.Errorf("read exact ERC-721 owner observation: %w", err)
	}
	observation, err := decodeOwnerObservation(state, ownerBytes, confidence)
	if err != nil {
		return catalog.NFTOwnerObservation{}, false, err
	}
	return observation, true, nil
}

func decodeOwnerObservation(state string, ownerBytes []byte, confidence string) (catalog.NFTOwnerObservation, error) {
	if confidence != catalog.NFTStateConfidenceRPCExact {
		return catalog.NFTOwnerObservation{}, errors.New("invalid ERC-721 observation confidence")
	}
	switch state {
	case "not_found":
		if ownerBytes != nil {
			return catalog.NFTOwnerObservation{}, errors.New("not-found ERC-721 observation has an owner")
		}
		return catalog.NFTOwnerObservation{Confidence: confidence}, nil
	case "owned":
		if len(ownerBytes) != 20 {
			return catalog.NFTOwnerObservation{}, errors.New("ERC-721 observation owner has invalid length")
		}
		owner, err := query.ChecksumAddress(common.BytesToAddress(ownerBytes).Hex())
		if err != nil {
			return catalog.NFTOwnerObservation{}, errors.New("invalid ERC-721 observation owner")
		}
		return catalog.NFTOwnerObservation{Exists: true, Owner: owner, Confidence: confidence}, nil
	default:
		return catalog.NFTOwnerObservation{}, errors.New("invalid ERC-721 observation state")
	}
}

func (reconciler *NFTReconciler) cachedBalance(
	ctx context.Context,
	chainID string,
	owner common.Address,
	candidate parsedNFTCandidate,
	reference CanonicalRef,
) (catalog.NFTBalanceObservation, bool, error) {
	if candidate.standard == standardERC721 {
		observation, found, err := reconciler.cachedOwner(ctx, chainID, candidate.address, candidate.tokenID, reference)
		if err != nil || !found {
			return catalog.NFTBalanceObservation{}, found, err
		}
		balance := "0"
		if observation.Exists {
			observedOwner, parseErr := ethrpc.ParseAddress(observation.Owner)
			if parseErr != nil {
				return catalog.NFTBalanceObservation{}, false, errors.New("invalid cached ERC-721 owner")
			}
			if observedOwner == owner {
				balance = "1"
			}
		}
		return exactNFTBalance(balance), true, nil
	}
	contractBytes := candidate.address.Bytes()
	ownerBytes := owner.Bytes()
	hashBytes := reference.Hash.Bytes()
	var balance, confidence string
	err := reconciler.db.QueryRowContext(ctx, dbgen.StateERC1155BalanceObservation,
		chainID, contractBytes, candidate.tokenID.String(), ownerBytes,
		strconv.FormatUint(reference.Number, 10), hashBytes,
	).Scan(&balance, &confidence)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.NFTBalanceObservation{}, false, nil
	}
	if err != nil {
		return catalog.NFTBalanceObservation{}, false, fmt.Errorf("read exact ERC-1155 balance observation: %w", err)
	}
	if _, err := parseUint256(balance); err != nil || confidence != catalog.NFTStateConfidenceRPCExact {
		return catalog.NFTBalanceObservation{}, false, errors.New("invalid ERC-1155 balance observation")
	}
	return exactNFTBalance(balance), true, nil
}

func (reconciler *NFTReconciler) persistOwner(
	ctx context.Context,
	chainID string,
	contract common.Address,
	tokenID *big.Int,
	reference CanonicalRef,
	observation catalog.NFTOwnerObservation,
) error {
	tx, err := reconciler.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin ERC-721 observation transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := insertOwnerObservation(ctx, tx, chainID, contract, tokenID, reference, observation); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit ERC-721 observation: %w", err)
	}
	return nil
}

func (reconciler *NFTReconciler) persistBalances(
	ctx context.Context,
	chainID string,
	owner common.Address,
	parsed []parsedNFTCandidate,
	results []catalog.NFTBalanceObservation,
	missing []int,
	owners map[int]catalog.NFTOwnerObservation,
	reference CanonicalRef,
) error {
	tx, err := reconciler.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin NFT balance observation transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	for _, index := range missing {
		candidate := parsed[index]
		if candidate.standard == standardERC721 {
			if err := insertOwnerObservation(ctx, tx, chainID, candidate.address, candidate.tokenID, reference, owners[index]); err != nil {
				return err
			}
			continue
		}
		if err := insertERC1155Balance(ctx, tx, chainID, candidate.address, candidate.tokenID, owner, reference, results[index]); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit NFT balance observations: %w", err)
	}
	return nil
}

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func insertOwnerObservation(
	ctx context.Context,
	executor sqlExecutor,
	chainID string,
	contract common.Address,
	tokenID *big.Int,
	reference CanonicalRef,
	observation catalog.NFTOwnerObservation,
) error {
	if observation.Confidence != catalog.NFTStateConfidenceRPCExact || !observation.Exists && observation.Owner != "" {
		return errors.New("persist invalid ERC-721 owner observation")
	}
	contractBytes := contract.Bytes()
	hashBytes := reference.Hash.Bytes()
	state := "not_found"
	var ownerBytes []byte
	if observation.Exists {
		owner, err := ethrpc.ParseAddress(observation.Owner)
		if err != nil {
			return errors.New("persist invalid ERC-721 owner")
		}
		ownerBytes = owner.Bytes()
		state = "owned"
	}
	result, err := executor.ExecContext(ctx, dbgen.StateWriteInsertOwnerObservationStatement1, chainID, contractBytes, tokenID.String(), strconv.FormatUint(reference.Number, 10), hashBytes,
		state, ownerBytes,
	)
	if err != nil {
		return fmt.Errorf("persist exact ERC-721 owner observation: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect exact ERC-721 owner persistence: %w", err)
	}
	if rows != 1 {
		return classifyOwnerPersistenceMiss(
			ctx, executor, chainID, contractBytes, tokenID.String(),
			strconv.FormatUint(reference.Number, 10), hashBytes,
		)
	}
	return nil
}

func insertERC1155Balance(
	ctx context.Context,
	executor sqlExecutor,
	chainID string,
	contract common.Address,
	tokenID *big.Int,
	owner common.Address,
	reference CanonicalRef,
	observation catalog.NFTBalanceObservation,
) error {
	if _, err := parseUint256(observation.Balance); err != nil || observation.Confidence != catalog.NFTStateConfidenceRPCExact {
		return errors.New("persist invalid ERC-1155 balance observation")
	}
	contractBytes := contract.Bytes()
	ownerBytes := owner.Bytes()
	hashBytes := reference.Hash.Bytes()
	result, err := executor.ExecContext(ctx, dbgen.StateWriteInsertERC1155BalanceStatement1, chainID, contractBytes, tokenID.String(), ownerBytes, strconv.FormatUint(reference.Number, 10), hashBytes,
		observation.Balance,
	)
	if err != nil {
		return fmt.Errorf("persist exact ERC-1155 balance observation: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect exact ERC-1155 balance persistence: %w", err)
	}
	if rows != 1 {
		return classifyBalancePersistenceMiss(
			ctx, executor, chainID, contractBytes, tokenID.String(), ownerBytes,
			strconv.FormatUint(reference.Number, 10), hashBytes,
		)
	}
	return nil
}

func classifyOwnerPersistenceMiss(
	ctx context.Context,
	executor sqlExecutor,
	chainID string,
	contract []byte,
	tokenID string,
	blockNumber string,
	blockHash []byte,
) error {
	var canonical, stored bool
	err := executor.QueryRowContext(ctx, dbgen.StateWriteClassifyOwnerPersistenceMissStatement1, chainID, contract, tokenID, blockNumber, blockHash).Scan(&canonical, &stored)
	if err != nil {
		return fmt.Errorf("inspect exact ERC-721 owner persistence miss: %w", err)
	}
	if !canonical {
		return fmt.Errorf("%w: canonical block changed before ERC-721 observation persistence", httpapi.ErrNotReady)
	}
	if stored {
		return ErrExactNFTObservationConflict
	}
	return errors.New("exact ERC-721 owner persistence affected no row")
}

func classifyBalancePersistenceMiss(
	ctx context.Context,
	executor sqlExecutor,
	chainID string,
	contract []byte,
	tokenID string,
	owner []byte,
	blockNumber string,
	blockHash []byte,
) error {
	var canonical, stored bool
	err := executor.QueryRowContext(ctx, dbgen.StateWriteClassifyBalancePersistenceMissStatement1, chainID, contract, tokenID, owner, blockNumber, blockHash).Scan(&canonical, &stored)
	if err != nil {
		return fmt.Errorf("inspect exact ERC-1155 balance persistence miss: %w", err)
	}
	if !canonical {
		return fmt.Errorf("%w: canonical block changed before ERC-1155 observation persistence", httpapi.ErrNotReady)
	}
	if stored {
		return ErrExactNFTObservationConflict
	}
	return errors.New("exact ERC-1155 balance persistence affected no row")
}
