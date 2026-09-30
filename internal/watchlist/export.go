package watchlist

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/api/gen"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const MaximumExportBytes = 16 << 20

type Export struct {
	Bytes       []byte
	BlockNumber string
	BlockHash   string
}

func (s *Service) Export(ctx context.Context, user string, input gen.AddressExportRequest) (Export, error) {
	owner, err := uid(user)
	if err != nil {
		return Export{}, err
	}
	address, err := ethrpc.ParseAddress(input.Address)
	if err != nil || !validDirection(string(input.Direction)) || (input.Kind != "transaction" && input.Kind != "erc20" && input.Kind != "nft") || input.From.Unix() < 0 || !input.To.After(input.From) || input.To.Sub(input.From) > 31*24*time.Hour {
		return Export{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	token := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	err = dbaccess.WithTransaction(ctx, s.db, func(q *dbgen.Queries) error {
		if e := q.ExportLock(ctx); e != nil {
			return e
		}
		if _, e := q.WatchLockOwner(ctx, owner, s.numeric); e != nil {
			return e
		}
		n, e := q.ExportAdmit(ctx, token, owner)
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrRateLimit
		}
		return nil
	})
	if err != nil {
		return Export{}, err
	}
	defer func() {
		cleanup, c := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer c()
		_ = dbgen.New(s.db).ExportRelease(cleanup, token)
	}()
	var out Export
	err = dbaccess.WithTransactionOptions(ctx, s.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(q *dbgen.Queries) error {
		tip, e := q.WatchTip(ctx, s.numeric)
		if e != nil {
			return ErrUnavailable
		}
		out.BlockNumber = tip.BlockNumber
		out.BlockHash = "0x" + hex.EncodeToString(tip.BlockHash)
		from, to := epochNumeric(input.From), epochNumeric(input.To)
		coverage, e := q.ExportCoverage(ctx, dbgen.ExportCoverageParams{ChainID: s.numeric, Tip: number(tip.BlockNumber), FromTimestamp: from, ToTimestamp: to})
		if e != nil {
			return e
		}
		if !coverage.CoreComplete || (input.Kind != "transaction" && !coverage.TokenComplete) {
			return ErrUnavailable
		}
		rows, e := q.ExportActivity(ctx, dbgen.ExportActivityParams{ChainID: s.numeric, Tip: number(tip.BlockNumber), FromTimestamp: from, ToTimestamp: to, Kind: string(input.Kind), Direction: string(input.Direction), Address: strings.ToLower(address.Hex())})
		if e != nil {
			return e
		}
		if len(rows) > 10000 {
			return ErrExportLimit
		}
		out.Bytes, e = encodeCSV(ctx, s.chain, out, rows, address.Hex())
		return e
	})
	if err != nil {
		return Export{}, err
	}
	return out, nil
}
func value(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
func safeCell(s string) string {
	trimmed := strings.TrimLeft(s, " \t\r\n")
	if strings.HasPrefix(s, "\t") || strings.HasPrefix(s, "\r") || strings.HasPrefix(s, "\n") || (len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0]))) {
		return "'" + s
	}
	return s
}

func epochNumeric(t time.Time) pgtype.Numeric {
	return number(fmt.Sprintf("%d.%09d", t.Unix(), t.Nanosecond()))
}

// encodeCSV finishes the bounded file before its snapshot transaction is closed.
func encodeCSV(ctx context.Context, chain string, snapshot Export, rows [][]byte, address string) ([]byte, error) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	writer.UseCRLF = true
	if e := writer.Write([]string{"chain_id", "snapshot_block_number", "snapshot_block_hash", "block_number", "block_hash", "timestamp_utc", "transaction_hash", "transaction_index", "kind", "direction", "from", "to", "value", "status", "token_address", "event_kind", "log_index", "sub_index", "token_id", "amount", "decimals"}); e != nil {
		return nil, e
	}
	for _, raw := range rows {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		a, e := decodeActivity(raw, address)
		if e != nil {
			return nil, e
		}
		sec, e := strconv.ParseInt(a.Timestamp, 10, 64)
		if e != nil {
			return nil, ErrUnavailable
		}
		fields := []string{chain, snapshot.BlockNumber, snapshot.BlockHash, a.BlockNumber, a.BlockHash, time.Unix(sec, 0).UTC().Format(time.RFC3339), a.TransactionHash, a.TransactionIndex, a.Kind, a.Direction, value(a.From), value(a.To), value(a.Value), value(a.Status), value(a.TokenAddress), value(a.EventKind), value(a.LogIndex), value(a.SubIndex), value(a.TokenId), value(a.Amount), value(a.Decimals)}
		for i, f := range fields {
			fields[i] = safeCell(f)
		}
		if e := writer.Write(fields); e != nil {
			return nil, e
		}
		writer.Flush()
		if e := writer.Error(); e != nil {
			return nil, e
		}
		if buffer.Len() > MaximumExportBytes {
			return nil, ErrExportLimit
		}
	}
	writer.Flush()
	if e := writer.Error(); e != nil {
		return nil, e
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	return buffer.Bytes(), nil
}
