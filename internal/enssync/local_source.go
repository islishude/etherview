package enssync

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/chainbundle"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type collectedBlock struct {
	Header     *types.Header
	Logs       []types.Log
	Attachment pgtype.Timestamptz
}
type verifiedDeployment struct {
	Deployment Deployment
	Header     *types.Header
	Document   []byte
}

func numeric(number uint64) pgtype.Numeric {
	return pgtype.Numeric{Int: new(big.Int).SetUint64(number), Valid: true}
}
func stamp(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
func uintNumber(n pgtype.Numeric) (uint64, error) {
	text, err := dbaccess.NumericText(n)
	if err != nil || !text.Valid {
		return 0, ErrStorage
	}
	i, ok := new(big.Int).SetString(text.String, 10)
	if !ok || !i.IsUint64() {
		return 0, ErrStorage
	}
	return i.Uint64(), nil
}
func storedHeader(raw []byte) (*types.Header, error) {
	h, err := chainbundle.DecodeStoredHeader(raw)
	if err != nil || !h.Number.IsUint64() {
		return nil, ErrStorage
	}
	return h, nil
}

func (r *Repository) localTarget(ctx context.Context, m Manifest) (*types.Header, error) {
	chain, _ := unsignedNumeric(m.SourceChainID)
	row, err := dbgen.New(r.db).GetENSSyncCoreTip(ctx, chain, m.Genesis.Bytes())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCoverageGap
	}
	if err != nil {
		return nil, ErrStorage
	}
	return storedHeader(row.Raw)
}
func (r *Repository) localBlock(ctx context.Context, m Manifest, number uint64, limit int) (collectedBlock, error) {
	var block collectedBlock
	chain, _ := unsignedNumeric(m.SourceChainID)
	row, err := dbgen.New(r.db).GetENSSyncCoreBlock(ctx, chain, m.Genesis.Bytes(), numeric(number))
	if errors.Is(err, pgx.ErrNoRows) {
		return block, ErrCoverageGap
	}
	if err != nil {
		return block, ErrStorage
	}
	header, err := storedHeader(row.Raw)
	if err != nil {
		return block, err
	}
	block.Header = header
	block.Attachment = row.UpdatedAt
	addresses := [][]byte{}
	for _, d := range m.Deployments {
		if d.Number <= number {
			addresses = append(addresses, d.Address.Bytes())
		}
	}
	logs, err := dbgen.New(r.db).ListENSSyncCoreLogs(ctx, dbgen.ListENSSyncCoreLogsParams{ChainID: chain, Number: numeric(number), BlockHash: header.Hash().Bytes(), Addresses: addresses, AllEmitters: m.hasV2(), RowLimit: int32(limit + 1)})
	if err != nil {
		return block, ErrStorage
	}
	if len(logs) > limit {
		return block, ErrCollectionLimit
	}
	for _, log := range logs {
		if log.TxIndex < 0 || log.LogIndex < 0 {
			return block, ErrInvalidEvent
		}
		entry, err := chainbundle.DecodeLog(log.Raw, common.BytesToHash(log.TxHash), header.Hash(), number, uint64(log.TxIndex), uint64(log.LogIndex))
		if err != nil {
			return block, ErrInvalidEvent
		}
		block.Logs = append(block.Logs, *entry)
	}
	return block, nil
}
func (r *Repository) localDeployment(ctx context.Context, m Manifest, d Deployment) (verifiedDeployment, error) {
	var result verifiedDeployment
	chain, _ := unsignedNumeric(m.SourceChainID)
	// Deployment identity may precede the indexed range. It is usable only when
	// exact core history and authenticated code evidence are actually present.
	row, err := dbgen.New(r.db).GetENSSyncCoreBlock(ctx, chain, m.Genesis.Bytes(), numeric(d.Number))
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrCoverageGap
	}
	if err != nil {
		return result, ErrStorage
	}
	header, err := storedHeader(row.Raw)
	if err != nil {
		return result, err
	}
	if header.Hash() != d.Hash {
		return result, ErrSourceIdentity
	}
	code, err := dbgen.New(r.db).GetENSSyncCoreDeploymentCode(ctx, chain, d.Address.Bytes(), d.Hash.Bytes())
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrCoverageGap
	}
	if err != nil {
		return result, ErrStorage
	}
	if len(code.Code) == 0 || crypto.Keccak256Hash(code.Code) != d.CodeHash || common.BytesToHash(code.CodeHash) != d.CodeHash {
		return result, ErrUnsupportedDeployment
	}
	return verifiedDeployment{d, header, code.DocumentDigest}, nil
}
