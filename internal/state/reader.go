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

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
	"github.com/islishude/etherview/internal/query"
	"golang.org/x/crypto/sha3"
)

type CanonicalRef struct {
	Number uint64
	Hash   ethrpc.Hash
}

type CapabilityError struct{ Code string }

func (CapabilityError) Error() string { return "state capability unavailable" }
func (CapabilityError) Unwrap() error { return httpapi.ErrUnavailable }

type CanonicalSource interface {
	Tip(context.Context) (CanonicalRef, error)
	IsCanonical(context.Context, CanonicalRef) (bool, error)
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
	err := s.DB.QueryRowContext(ctx, `
		SELECT number::text, block_hash
		FROM canonical_blocks
		WHERE chain_id = $1::numeric
		ORDER BY number DESC
		LIMIT 1`, s.ChainID).Scan(&number, &hashBytes)
	if err == sql.ErrNoRows {
		return CanonicalRef{}, httpapi.ErrNotReady
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
	hash, err := reference.Hash.Bytes()
	if err != nil {
		return false, err
	}
	err = s.DB.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM canonical_blocks
			WHERE chain_id = $1::numeric AND number = $2::numeric AND block_hash = $3
		)`, s.ChainID, fmt.Sprint(reference.Number), hash).Scan(&canonical)
	return canonical, err
}

type Reader struct {
	Base         httpapi.Reader
	Canonical    CanonicalSource
	Pool         *ethrpc.Pool
	Completeness gen.Completeness
}

var _ httpapi.Reader = (*Reader)(nil)

func (r *Reader) Status(ctx context.Context) (httpapi.StatusSnapshot, error) {
	return r.Base.Status(ctx)
}

func (r *Reader) Blocks(ctx context.Context, cursor string, limit int) ([]gen.Block, string, error) {
	return r.Base.Blocks(ctx, cursor, limit)
}

func (r *Reader) Block(ctx context.Context, id string) (gen.Block, error) {
	return r.Base.Block(ctx, id)
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
	selector := map[string]any{"blockHash": reference.Hash.String(), "requireCanonical": true}
	var balance, nonce ethrpc.Quantity
	var code ethrpc.Data
	elements := []ethrpc.BatchElem{
		{Method: "eth_getBalance", Params: []any{address.String(), selector}, Result: &balance},
		{Method: "eth_getTransactionCount", Params: []any{address.String(), selector}, Result: &nonce},
		{Method: "eth_getCode", Params: []any{address.String(), selector}, Result: &code},
	}
	if batch, ok := endpoint.Client.(ethrpc.BatchCaller); ok {
		if err := batch.BatchCall(ctx, elements); err != nil {
			r.Pool.ReportFailure(endpoint.Name)
			return gen.AddressSummary{}, stateUnavailable(err)
		}
		for _, element := range elements {
			if element.Error != nil {
				r.Pool.ReportFailure(endpoint.Name)
				return gen.AddressSummary{}, stateUnavailable(element.Error)
			}
		}
	} else {
		for _, element := range elements {
			if err := endpoint.Client.Call(ctx, element.Method, element.Params, element.Result); err != nil {
				r.Pool.ReportFailure(endpoint.Name)
				return gen.AddressSummary{}, stateUnavailable(err)
			}
		}
	}
	canonical, err := r.Canonical.IsCanonical(ctx, reference)
	if err != nil {
		return gen.AddressSummary{}, fmt.Errorf("recheck account state block: %w", err)
	}
	if !canonical {
		return gen.AddressSummary{}, fmt.Errorf("%w: canonical block changed during state query", httpapi.ErrNotReady)
	}
	r.Pool.ReportSuccess(endpoint.Name)
	balanceDecimal, err := decimal(balance)
	if err != nil {
		return gen.AddressSummary{}, CapabilityError{Code: "malformed_response"}
	}
	nonceDecimal, err := decimal(nonce)
	if err != nil {
		return gen.AddressSummary{}, CapabilityError{Code: "malformed_response"}
	}
	if _, err := ethrpc.ParseData(code.String()); err != nil {
		return gen.AddressSummary{}, CapabilityError{Code: "malformed_response"}
	}
	checksummed, err := query.ChecksumAddress(address.String())
	if err != nil {
		return gen.AddressSummary{}, err
	}
	accountType, codeHash, err := classifyCode(code)
	if err != nil {
		return gen.AddressSummary{}, err
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
	return gen.AddressSummary{
		Address: checksummed, AtBlock: strings.ToLower(reference.Hash.String()),
		Balance: balanceDecimal, Nonce: nonceDecimal, Type: accountType,
		CodeHash: codeHash, Completeness: completeness,
	}, nil
}

func stateUnavailable(error) error {
	return CapabilityError{Code: "rpc_failure"}
}

func classifyCode(code ethrpc.Data) (gen.AddressSummaryType, *string, error) {
	bytes, err := code.Bytes()
	if err != nil {
		return "", nil, err
	}
	if len(bytes) == 0 {
		return gen.AddressSummaryTypeEoa, nil, nil
	}
	typeValue := gen.AddressSummaryTypeContract
	if len(bytes) == 23 && bytes[0] == 0xef && bytes[1] == 0x01 && bytes[2] == 0x00 {
		typeValue = gen.AddressSummaryTypeDelegatedEoa
	}
	hasher := sha3.NewLegacyKeccak256()
	_, _ = hasher.Write(bytes)
	hash := ethrpc.DataFromBytes(hasher.Sum(nil)).String()
	return typeValue, &hash, nil
}

func decimal(value ethrpc.Quantity) (string, error) {
	integer, err := value.Big()
	if err != nil {
		return "", err
	}
	return integer.String(), nil
}

func bytesHash(value []byte) (ethrpc.Hash, error) {
	if len(value) != 32 {
		return "", fmt.Errorf("canonical hash has %d bytes, expected 32", len(value))
	}
	return ethrpc.ParseHash(ethrpc.DataFromBytes(value).String())
}
