// Package state decorates the PostgreSQL read model with current native state
// queried at one fixed canonical block. It never reconstructs balances from
// value transfers and never claims historical state without RPC support.
package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/publicquery"
	"github.com/islishude/etherview/internal/query"
)

type CanonicalRef struct {
	Number uint64
	Hash   common.Hash
}

type CapabilityError struct{ Code string }

func (CapabilityError) Error() string { return "state capability unavailable" }
func (CapabilityError) Unwrap() error { return publicquery.ErrUnavailable }

type CanonicalSource interface {
	Tip(context.Context) (CanonicalRef, error)
	IsCanonical(context.Context, CanonicalRef) (bool, error)
}

type AddressOriginReader interface {
	AddressOrigin(
		context.Context,
		string,
		gen.AddressSummaryType,
		uint64,
		common.Hash,
	) (gen.AddressOrigin, error)
}

type AddressDelegationHistoryReader interface {
	HasAddressDelegationHistory(context.Context, string, uint64, common.Hash) (bool, error)
}

type PostgresCanonicalSource struct {
	DB      *sql.DB
	ChainID string
}

func (s PostgresCanonicalSource) Tip(ctx context.Context) (CanonicalRef, error) {
	if s.DB == nil || s.ChainID == "" {
		return CanonicalRef{}, errors.New("canonical state source is not configured")
	}
	var number string
	var hashBytes []byte
	err := s.DB.QueryRowContext(ctx, dbgen.StateCanonicalTip, s.ChainID).Scan(&number, &hashBytes)
	if err == sql.ErrNoRows {
		return CanonicalRef{}, publicquery.ErrNotReady
	}
	if err != nil {
		return CanonicalRef{}, fmt.Errorf("query canonical state tip: %w", err)
	}
	height, err := strconv.ParseUint(number, 10, 64)
	if err != nil || strconv.FormatUint(height, 10) != number {
		return CanonicalRef{}, fmt.Errorf("decode canonical state height %q", number)
	}
	hash, err := bytesHash(hashBytes)
	if err != nil {
		return CanonicalRef{}, err
	}
	return CanonicalRef{Number: height, Hash: hash}, nil
}

func (s PostgresCanonicalSource) IsCanonical(ctx context.Context, reference CanonicalRef) (bool, error) {
	var canonical bool
	err := s.DB.QueryRowContext(ctx, dbgen.StateIsCanonical, s.ChainID, fmt.Sprint(reference.Number), reference.Hash.Bytes()).Scan(&canonical)
	return canonical, err
}

type Reader struct {
	Base              publicquery.Reader
	Origin            AddressOriginReader
	DelegationHistory AddressDelegationHistoryReader
	Canonical         CanonicalSource
	Pool              *ethrpc.Pool
	Completeness      gen.Completeness
}

var _ publicquery.Reader = (*Reader)(nil)

func (r *Reader) Status(ctx context.Context) (publicquery.StatusSnapshot, error) {
	return r.Base.Status(ctx)
}

func (r *Reader) Blocks(ctx context.Context, cursor string, limit int) ([]gen.Block, string, error) {
	return r.Base.Blocks(ctx, cursor, limit)
}

func (r *Reader) Block(ctx context.Context, id string) (gen.Block, error) {
	return r.Base.Block(ctx, id)
}

func (r *Reader) BlockTransactions(ctx context.Context, id, cursor string, limit int) ([]gen.Transaction, string, error) {
	return r.Base.BlockTransactions(ctx, id, cursor, limit)
}

func (r *Reader) Transactions(ctx context.Context, cursor string, limit int) ([]gen.Transaction, string, error) {
	return r.Base.Transactions(ctx, cursor, limit)
}

func (r *Reader) Transaction(ctx context.Context, hash string) (gen.Transaction, error) {
	return r.Base.Transaction(ctx, hash)
}

func (r *Reader) Search(ctx context.Context, value, cursor string, limit int) ([]gen.SearchResult, string, error) {
	return r.Base.Search(ctx, value, cursor, limit)
}

func (r *Reader) Address(ctx context.Context, value string) (gen.AddressSummary, error) {
	if r == nil || r.Base == nil || r.Canonical == nil || r.Pool == nil {
		return gen.AddressSummary{}, CapabilityError{Code: "not_configured"}
	}
	address, err := ethrpc.ParseAddress(value)
	if err != nil {
		return gen.AddressSummary{}, fmt.Errorf("invalid address: %w", err)
	}
	reference, err := r.Canonical.Tip(ctx)
	if err != nil {
		return gen.AddressSummary{}, err
	}
	endpoint, err := r.Pool.Acquire(ethrpc.PurposeState)
	if err != nil {
		return gen.AddressSummary{}, CapabilityError{Code: "endpoint_unavailable"}
	}
	selector := rpc.BlockNumberOrHashWithHash(reference.Hash, true)
	var balance, nonce hexutil.Big
	var code hexutil.Bytes
	elements := []rpc.BatchElem{
		{Method: "eth_getBalance", Args: []any{address, selector}, Result: &balance},
		{Method: "eth_getTransactionCount", Args: []any{address, selector}, Result: &nonce},
		{Method: "eth_getCode", Args: []any{address, selector}, Result: &code},
	}
	if err := endpoint.BatchCallContext(ctx, elements); err != nil {
		r.Pool.ReportFailure(endpoint.Name)
		return gen.AddressSummary{}, stateUnavailable(err)
	}
	for _, element := range elements {
		if element.Error != nil {
			r.Pool.ReportFailure(endpoint.Name)
			return gen.AddressSummary{}, stateUnavailable(element.Error)
		}
	}
	canonical, err := r.Canonical.IsCanonical(ctx, reference)
	if err != nil {
		return gen.AddressSummary{}, fmt.Errorf("recheck account state block: %w", err)
	}
	if !canonical {
		return gen.AddressSummary{}, fmt.Errorf("%w: canonical block changed during state query", publicquery.ErrNotReady)
	}
	r.Pool.ReportSuccess(endpoint.Name)
	balanceDecimal := decimal(balance)
	nonceDecimal := decimal(nonce)
	checksummed, err := query.ChecksumAddress(address.Hex())
	if err != nil {
		return gen.AddressSummary{}, err
	}
	accountType, codeHash := classifyCode(code)
	hasDelegationHistory := false
	if accountType == gen.AddressSummaryTypeEoa || accountType == gen.AddressSummaryTypeDelegatedEoa {
		if r.DelegationHistory == nil {
			return gen.AddressSummary{}, CapabilityError{Code: "delegation_history_not_configured"}
		}
		hasDelegationHistory, err = r.DelegationHistory.HasAddressDelegationHistory(
			ctx, checksummed, reference.Number, reference.Hash,
		)
		if err != nil {
			return gen.AddressSummary{}, err
		}
		canonical, err = r.Canonical.IsCanonical(ctx, reference)
		if err != nil {
			return gen.AddressSummary{}, fmt.Errorf("recheck delegation history block: %w", err)
		}
		if !canonical {
			return gen.AddressSummary{}, fmt.Errorf("%w: canonical block changed during delegation history query", publicquery.ErrNotReady)
		}
	}
	var delegation *gen.DelegationBinding
	if accountType == gen.AddressSummaryTypeDelegatedEoa {
		binding, bindingErr := r.delegationBinding(ctx, address, reference, endpoint, code)
		if bindingErr != nil {
			binding = unavailableDelegationBinding(r, address, reference)
		}
		delegation = &binding
		canonical, err = r.Canonical.IsCanonical(ctx, reference)
		if err != nil {
			return gen.AddressSummary{}, fmt.Errorf("recheck delegation summary block: %w", err)
		}
		if !canonical {
			return gen.AddressSummary{}, fmt.Errorf("%w: canonical block changed during delegation summary", publicquery.ErrNotReady)
		}
	}
	completeness := r.Completeness
	completeness.Core = gen.StageStateComplete
	completeness.State = gen.StageStateComplete
	if completeness.Trace == "" {
		completeness.Trace = gen.StageStateUnavailable
	}
	if completeness.Metadata == "" {
		completeness.Metadata = gen.StageStateUnavailable
	}
	origin := gen.AddressOrigin{Kind: gen.Funding, State: gen.AddressOriginStateUnavailable}
	if accountType == gen.AddressSummaryTypeContract {
		origin.Kind = gen.ContractCreation
	}
	if r.Origin != nil {
		origin, err = r.Origin.AddressOrigin(ctx, checksummed, accountType, reference.Number, reference.Hash)
		if err != nil {
			return gen.AddressSummary{}, err
		}
		canonical, err = r.Canonical.IsCanonical(ctx, reference)
		if err != nil {
			return gen.AddressSummary{}, fmt.Errorf("recheck account origin block: %w", err)
		}
		if !canonical {
			return gen.AddressSummary{}, fmt.Errorf("%w: canonical block changed during origin query", publicquery.ErrNotReady)
		}
	}
	return gen.AddressSummary{
		Address: checksummed, AtBlock: strings.ToLower(reference.Hash.Hex()),
		Balance: balanceDecimal, Nonce: nonceDecimal, Type: accountType,
		CodeHash: codeHash, Origin: &origin, Completeness: completeness,
		Delegation: delegation, HasDelegationHistory: hasDelegationHistory,
	}, nil
}

func (r *Reader) AddressDelegation(ctx context.Context, value string) (gen.DelegationBinding, error) {
	if r == nil || r.Canonical == nil || r.Pool == nil {
		return gen.DelegationBinding{}, CapabilityError{Code: "not_configured"}
	}
	authority, err := ethrpc.ParseAddress(value)
	if err != nil {
		return gen.DelegationBinding{}, fmt.Errorf("invalid address: %w", err)
	}
	reference, err := r.Canonical.Tip(ctx)
	if err != nil {
		return gen.DelegationBinding{}, err
	}
	endpoint, err := r.Pool.Acquire(ethrpc.PurposeState)
	if err != nil {
		return gen.DelegationBinding{}, CapabilityError{Code: "endpoint_unavailable"}
	}
	selector := rpc.BlockNumberOrHashWithHash(reference.Hash, true)
	var code hexutil.Bytes
	if err := endpoint.Client.CallContext(ctx, &code, "eth_getCode", authority, selector); err != nil {
		r.Pool.ReportFailure(endpoint.Name)
		return r.canonicalUnavailableDelegation(ctx, authority, reference)
	}
	binding, err := r.delegationBinding(ctx, authority, reference, endpoint, code)
	if err != nil {
		r.Pool.ReportFailure(endpoint.Name)
		return r.canonicalUnavailableDelegation(ctx, authority, reference)
	}
	canonical, err := r.Canonical.IsCanonical(ctx, reference)
	if err != nil {
		return gen.DelegationBinding{}, fmt.Errorf("recheck delegation block: %w", err)
	}
	if !canonical {
		return gen.DelegationBinding{}, fmt.Errorf("%w: canonical block changed during delegation query", publicquery.ErrNotReady)
	}
	r.Pool.ReportSuccess(endpoint.Name)
	return binding, nil
}

func (r *Reader) canonicalUnavailableDelegation(
	ctx context.Context, authority common.Address, reference CanonicalRef,
) (gen.DelegationBinding, error) {
	canonical, err := r.Canonical.IsCanonical(ctx, reference)
	if err != nil {
		return gen.DelegationBinding{}, fmt.Errorf("recheck unavailable delegation block: %w", err)
	}
	if !canonical {
		return gen.DelegationBinding{}, fmt.Errorf("%w: canonical block changed during delegation query", publicquery.ErrNotReady)
	}
	return unavailableDelegationBinding(r, authority, reference), nil
}

func (r *Reader) delegationBinding(
	ctx context.Context, authority common.Address, reference CanonicalRef,
	endpoint *ethrpc.Endpoint, authorityCode []byte,
) (gen.DelegationBinding, error) {
	authorityText, err := query.ChecksumAddress(authority.Hex())
	if err != nil {
		return gen.DelegationBinding{}, err
	}
	binding := gen.DelegationBinding{
		Authority: authorityText, Status: gen.DelegationBindingStatusNotDelegated,
		ChainId: r.chainID(), BlockNumber: strconv.FormatUint(reference.Number, 10),
		BlockHash: strings.ToLower(reference.Hash.Hex()),
	}
	delegate, delegated := types.ParseDelegation(authorityCode)
	if !delegated {
		return binding, nil
	}
	selector := rpc.BlockNumberOrHashWithHash(reference.Hash, true)
	var delegateCode hexutil.Bytes
	if err := endpoint.Client.CallContext(ctx, &delegateCode, "eth_getCode", delegate, selector); err != nil {
		return gen.DelegationBinding{}, stateUnavailable(err)
	}
	delegateText, err := query.ChecksumAddress(delegate.Hex())
	if err != nil {
		return gen.DelegationBinding{}, err
	}
	hash := strings.ToLower(crypto.Keccak256Hash(delegateCode).Hex())
	binding.Status = gen.DelegationBindingStatusDelegated
	binding.Delegate = &delegateText
	binding.DelegateCodeHash = &hash
	return binding, nil
}

func unavailableDelegationBinding(r *Reader, authority common.Address, reference CanonicalRef) gen.DelegationBinding {
	authorityText, _ := query.ChecksumAddress(authority.Hex())
	reason := gen.DelegationBindingReasonStateUnavailable
	return gen.DelegationBinding{
		Authority: authorityText, Status: gen.DelegationBindingStatusUnavailable,
		ChainId: r.chainID(), BlockNumber: strconv.FormatUint(reference.Number, 10),
		BlockHash: strings.ToLower(reference.Hash.Hex()), Reason: &reason,
	}
}

func (r *Reader) chainID() string {
	if source, ok := r.Canonical.(PostgresCanonicalSource); ok {
		return source.ChainID
	}
	if source, ok := r.Canonical.(*PostgresCanonicalSource); ok && source != nil {
		return source.ChainID
	}
	return "0"
}

func stateUnavailable(error) error {
	return CapabilityError{Code: "rpc_failure"}
}

func classifyCode(code hexutil.Bytes) (gen.AddressSummaryType, *string) {
	bytes := []byte(code)
	if len(bytes) == 0 {
		return gen.AddressSummaryTypeEoa, nil
	}
	typeValue := gen.AddressSummaryTypeContract
	if len(bytes) == 23 && bytes[0] == 0xef && bytes[1] == 0x01 && bytes[2] == 0x00 {
		typeValue = gen.AddressSummaryTypeDelegatedEoa
	}
	hash := crypto.Keccak256Hash(bytes).Hex()
	return typeValue, &hash
}

func decimal(value hexutil.Big) string {
	return value.ToInt().String()
}

func bytesHash(value []byte) (common.Hash, error) {
	if len(value) != common.HashLength {
		return common.Hash{}, fmt.Errorf("canonical hash has %d bytes, expected 32", len(value))
	}
	return common.BytesToHash(value), nil
}
