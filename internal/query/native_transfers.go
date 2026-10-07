package query

import (
	"context"
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/publicquery"
	"github.com/jackc/pgx/v5"
)

type nativeTransferCursor struct {
	Version           int    `json:"version"`
	ChainID           string `json:"chain_id"`
	IndexStart        uint64 `json:"index_start"`
	Address           string `json:"address"`
	TransactionHash   string `json:"transaction_hash"`
	SnapshotNumber    uint64 `json:"snapshot_number"`
	SnapshotHash      string `json:"snapshot_hash"`
	BeforeBlock       uint64 `json:"before_block"`
	BeforeTransaction int64  `json:"before_transaction"`
	BeforeLog         int64  `json:"before_log"`
}

func (r *PostgresReader) NativeTransfers(ctx context.Context, request publicquery.NativeTransferRequest) (publicquery.NativeTransferPage, error) {
	page := publicquery.NativeTransferPage{Items: []gen.NativeTransfer{}}
	if request.Limit < 1 || request.Limit > 100 || (request.Address == "") == (request.TransactionHash == "") {
		return page, publicquery.ErrInvalidInput
	}
	var address, hash []byte
	if request.Address != "" {
		a, err := ethrpc.ParseAddress(request.Address)
		if err != nil {
			return page, publicquery.ErrInvalidInput
		}
		request.Address = strings.ToLower(a.Hex())
		address = a.Bytes()
	}
	if request.TransactionHash != "" {
		h, err := ethrpc.ParseHash(request.TransactionHash)
		if err != nil {
			return page, publicquery.ErrInvalidInput
		}
		request.TransactionHash = strings.ToLower(h.Hex())
		hash = h.Bytes()
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return page, err
	}
	defer dbaccess.Rollback(ctx, tx)
	chain, err := r.chainNumeric()
	if err != nil {
		return page, err
	}
	q := dbgen.New(tx)
	cursor := nativeTransferCursor{Version: 1, ChainID: r.chainID, IndexStart: r.startBlock, Address: request.Address, TransactionHash: request.TransactionHash}
	if request.Cursor == "" {
		snapshot, err := r.currentBlockCursor(ctx, tx)
		if err != nil {
			return page, err
		}
		cursor.SnapshotNumber = snapshot.SnapshotNumber
		cursor.SnapshotHash = snapshot.SnapshotHash
	} else {
		if err := publicquery.DecodeCursor(request.Cursor, &cursor); err != nil {
			return page, publicquery.ErrInvalidCursor
		}
		if cursor.Version != 1 || cursor.ChainID != r.chainID || cursor.IndexStart != r.startBlock || cursor.Address != request.Address || cursor.TransactionHash != request.TransactionHash || cursor.SnapshotNumber < r.startBlock || cursor.BeforeBlock < r.startBlock || cursor.BeforeBlock > cursor.SnapshotNumber || cursor.BeforeTransaction < 0 || cursor.BeforeLog < 0 {
			return page, publicquery.ErrInvalidCursor
		}
		snapshotHash, err := ethrpc.ParseHash(cursor.SnapshotHash)
		if err != nil {
			return page, publicquery.ErrInvalidCursor
		}
		valid, err := q.NativeTransferValidateSnapshot(ctx, chain, numericUint64(cursor.SnapshotNumber), snapshotHash[:])
		if err != nil {
			return page, err
		}
		if !valid {
			return page, publicquery.ErrInvalidCursor
		}
	}
	startText, err := q.NativeTransferActivation(ctx, chain, numericUint64(r.startBlock), numericUint64(cursor.SnapshotNumber))
	if err != nil {
		return page, err
	}
	if request.TransactionHash != "" {
		block, err := q.NativeTransferTransactionBlock(ctx, chain, hash, numericUint64(cursor.SnapshotNumber))
		if errors.Is(err, pgx.ErrNoRows) {
			return page, publicquery.ErrNotFound
		}
		if err != nil {
			return page, err
		}
		if !block.Amsterdam {
			return page, nil
		}
		startText = block.BlockNumber
	}
	if startText == "" {
		return page, nil
	}
	start, err := parseDecimalUint64(startText)
	if err != nil {
		return page, err
	}
	end := cursor.SnapshotNumber
	if request.TransactionHash != "" {
		end = start
	}
	if start < r.startBlock {
		return page, publicquery.ErrNotFound
	}
	if start > end {
		return page, publicquery.ErrInvalidCursor
	}
	complete, err := q.NativeTransferCoverage(ctx, chain, numericUint64(start), numericUint64(end))
	if err != nil {
		return page, err
	}
	if !complete {
		return page, publicquery.NewCapabilityUnavailableError("native_transfers", "pending", "coverage_incomplete")
	}
	rows, err := q.NativeTransferList(ctx, dbgen.NativeTransferListParams{ChainID: chain, StartNumber: numericUint64(start), SnapshotNumber: numericUint64(end), ByTransaction: hash != nil, TransactionHash: hash, ByAddress: address != nil, Address: address, HasCursor: request.Cursor != "", BeforeBlock: numericUint64(cursor.BeforeBlock), BeforeTransaction: cursor.BeforeTransaction, BeforeLog: cursor.BeforeLog, PageLimit: int32(request.Limit + 1)})
	if err != nil {
		return page, err
	}
	page.Applicable = true
	page.CoverageStart = start
	page.CoverageEnd = end
	hasMore := len(rows) > request.Limit
	if hasMore {
		rows = rows[:request.Limit]
	}
	for _, row := range rows {
		model, err := nativeTransferModel(row)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, model)
	}
	if hasMore {
		last := rows[len(rows)-1]
		cursor.BeforeBlock, err = parseDecimalUint64(last.BlockNumber)
		if err != nil {
			return page, err
		}
		cursor.BeforeTransaction = last.TransactionIndex
		cursor.BeforeLog = last.LogIndex
		page.NextCursor, err = publicquery.EncodeCursor(cursor)
		if err != nil {
			return page, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return page, err
	}
	return page, nil
}

func nativeTransferModel(row dbgen.NativeTransferListRow) (gen.NativeTransfer, error) {
	if len(row.BlockHash) != 32 || len(row.TransactionHash) != 32 || len(row.FromAddress) != 20 || len(row.ToAddress) != 20 || row.TransactionIndex < 0 || row.LogIndex < 0 {
		return gen.NativeTransfer{}, errors.New("invalid stored native transfer identity")
	}
	if _, err := parseDecimalUint64(row.BlockNumber); err != nil {
		return gen.NativeTransfer{}, err
	}
	timestamp, err := parseDecimalUint64(row.BlockTimestamp)
	if err != nil || timestamp > math.MaxInt64 {
		return gen.NativeTransfer{}, errors.New("invalid native transfer timestamp")
	}
	date, err := quantityTime(timestamp)
	if err != nil {
		return gen.NativeTransfer{}, err
	}
	value, ok := new(big.Int).SetString(row.Amount, 10)
	if !ok || value.String() != row.Amount || value.Sign() <= 0 || value.BitLen() > 256 {
		return gen.NativeTransfer{}, errors.New("invalid native transfer amount")
	}
	return gen.NativeTransfer{BlockNumber: row.BlockNumber, BlockHash: common.BytesToHash(row.BlockHash).Hex(), TransactionHash: common.BytesToHash(row.TransactionHash).Hex(), TransactionIndex: strconv.FormatInt(row.TransactionIndex, 10), LogIndex: strconv.FormatInt(row.LogIndex, 10), From: common.BytesToAddress(row.FromAddress).Hex(), To: common.BytesToAddress(row.ToAddress).Hex(), Amount: row.Amount, Timestamp: date}, nil
}
