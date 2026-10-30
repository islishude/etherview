package ensread

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	goens "github.com/ensdomains/go-ens/v4"
	"github.com/ethereum/go-ethereum/common"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/islishude/etherview/internal/publicquery"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type historyCursor struct {
	Chain      uint64 `json:"c"`
	Source     int64  `json:"s"`
	Manifest   string `json:"m"`
	Node       string `json:"n"`
	Hash       string `json:"h"`
	Revision   int64  `json:"r"`
	Watermark  int64  `json:"w"`
	Expires    int64  `json:"e"`
	Before     string `json:"b,omitempty"`
	Tx         int64  `json:"t,omitempty"`
	EventBlock string `json:"eb,omitempty"`
	EventTx    string `json:"et,omitempty"`
	After      int64  `json:"a,omitempty"`
}

func (h historyCursor) snapshot() historyCursor {
	h.Before = ""
	h.Tx = 0
	h.EventBlock = ""
	h.EventTx = ""
	h.After = 0
	return h
}
func historyNumber(s string) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if s == "" {
		s = "0"
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return n, enscontract.ErrSnapshotInvalid
		}
	}
	if len(s) > 78 || len(s) > 1 && s[0] == '0' {
		return n, enscontract.ErrSnapshotInvalid
	}
	if e := n.Scan(s); e != nil {
		return n, enscontract.ErrSnapshotInvalid
	}
	return n, nil
}
func historyNode(name string) common.Hash { node, _ := goens.NameHash(name); return common.Hash(node) }
func (r *Reader) historySnapshot(ctx context.Context, q *dbgen.Queries, scope dbgen.ReadENSHistoryScopeRow, node common.Hash, token string) (historyCursor, pgtype.Numeric, error) {
	h := historyCursor{Chain: r.options.ChainID, Source: scope.ID, Manifest: common.BytesToHash(scope.ManifestDigest).Hex(), Node: node.Hex(), Hash: common.BytesToHash(scope.ScannedHash).Hex(), Revision: scope.Revision, Expires: time.Now().Add(r.options.SnapshotTTL).Unix()}
	if token != "" {
		if publicquery.DecodeCursor(token, &h) != nil || h.Chain != r.options.ChainID || h.Source != scope.ID || h.Manifest != common.BytesToHash(scope.ManifestDigest).Hex() || h.Node != node.Hex() || h.Revision != scope.Revision || h.Expires <= time.Now().Unix() || h.Expires > time.Now().Add(r.options.SnapshotTTL).Unix()+1 {
			return h, pgtype.Numeric{}, enscontract.ErrSnapshotInvalid
		}
	}
	hash, e := historyHash(h.Hash)
	if e != nil {
		return h, pgtype.Numeric{}, enscontract.ErrSnapshotInvalid
	}
	fence, e := q.ReadENSHistoryFence(ctx, scope.ID, node.Bytes(), hash.Bytes())
	if errors.Is(e, pgx.ErrNoRows) {
		return h, pgtype.Numeric{}, enscontract.ErrSnapshotInvalid
	}
	if e != nil {
		return h, pgtype.Numeric{}, e
	}
	if token != "" && h.Watermark != fence.Watermark {
		return h, pgtype.Numeric{}, enscontract.ErrSnapshotInvalid
	}
	h.Watermark = fence.Watermark
	return h, fence.Number, nil
}
func historyCoverage(scope dbgen.ReadENSHistoryScopeRow) enscontract.HistoryCoverage {
	start := numberText(scope.StartNumber)
	value := enscontract.HistoryCoverage{State: "pending", Start: "0", Scanned: numberText(scope.ScannedNumber), Target: numberText(scope.TargetNumber), Pending: strconv.FormatInt(scope.Pending, 10)}
	if start != nil {
		value.Start = *start
	}
	if value.Scanned != nil {
		value.State = "partial"
	}
	if scope.State == "unsupported" || scope.State == "halted" {
		value.State = "unavailable"
	} else if scope.Canonical && value.Scanned != nil && value.Target != nil && *value.Scanned == *value.Target && scope.Pending == 0 && scope.State == "active" && scope.HealthyUntil.Valid && time.Now().Before(scope.HealthyUntil.Time) {
		value.State = "complete"
	}
	return value
}
func (r *Reader) NameHistory(ctx context.Context, request enscontract.HistoryRequest) (enscontract.HistoryPage, string, error) {
	name, e := normalize(request.Name)
	if e != nil {
		return enscontract.HistoryPage{}, "", e
	}
	if request.Limit < 1 || request.Limit > 100 {
		return enscontract.HistoryPage{}, "", enscontract.ErrSnapshotInvalid
	}
	page := enscontract.HistoryPage{Name: name, State: "not_indexed", Items: []enscontract.HistoryTransaction{}, Coverage: enscontract.HistoryCoverage{State: "pending", Start: "0", Pending: "0"}}
	next := ""
	e = dbaccess.WithTransactionOptions(ctx, r.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(q *dbgen.Queries) error {
		known, err := q.ReadENSKnownName(ctx, name, r.chain, r.options.Policy.Bytes())
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if known {
			page.State = "indexed"
		}
		scope, err := q.ReadENSHistoryScope(ctx, r.chain, r.options.Policy.Bytes())
		if errors.Is(err, pgx.ErrNoRows) {
			if request.Cursor != "" {
				return enscontract.ErrSnapshotInvalid
			}
			return nil
		}
		if err != nil {
			return err
		}
		page.Coverage = historyCoverage(scope)
		if !scope.Canonical || len(scope.ScannedHash) == 0 {
			if request.Cursor != "" {
				return enscontract.ErrSnapshotInvalid
			}
			return nil
		}
		node := historyNode(name)
		h, number, err := r.historySnapshot(ctx, q, scope, node, request.Cursor)
		if err != nil {
			return err
		}
		if h.EventBlock != "" || h.EventTx != "" || h.After != 0 || h.Tx < 0 {
			return enscontract.ErrSnapshotInvalid
		}
		page.Snapshot, err = publicquery.EncodeCursor(h.snapshot())
		if err != nil {
			return err
		}
		before, err := historyNumber(h.Before)
		if err != nil {
			return err
		}
		rows, err := q.ListENSHistoryTransactions(ctx, dbgen.ListENSHistoryTransactionsParams{SourceID: scope.ID, Node: node.Bytes(), SnapshotNumber: number, HasBoundary: h.Before != "", BeforeNumber: before, BeforeTx: h.Tx, RowLimit: int32(request.Limit + 1)})
		if err != nil {
			return err
		}
		more := len(rows) > request.Limit
		if more {
			rows = rows[:request.Limit]
		}
		for _, row := range rows {
			n := numberText(row.BlockNumber)
			if n == nil {
				return errors.New("invalid history number")
			}
			page.Items = append(page.Items, enscontract.HistoryTransaction{TransactionHash: common.BytesToHash(row.TxHash).Hex(), BlockHash: common.BytesToHash(row.BlockHash).Hex(), BlockNumber: *n, Timestamp: row.BlockTime.Time.UTC().Format(time.RFC3339), Actions: row.Actions, EventCount: strconv.FormatInt(row.EventCount, 10)})
		}
		if len(rows) > 0 {
			page.State = "indexed"
		}
		if more {
			last := rows[len(rows)-1]
			h.Before = *numberText(last.BlockNumber)
			h.Tx = last.TxIndex
			next, err = publicquery.EncodeCursor(h)
		}
		return err
	})
	if e != nil {
		return page, "", readError(e)
	}
	return page, next, nil
}
func (r *Reader) NameHistoryEvents(ctx context.Context, request enscontract.HistoryEventsRequest) (enscontract.HistoryEventsPage, string, error) {
	result := enscontract.HistoryEventsPage{Items: []enscontract.HistoryEvent{}}
	name, e := normalize(request.Name)
	if e != nil {
		return result, "", e
	}
	tx, e := historyHash(request.TransactionHash)
	if e != nil {
		return result, "", enscontract.ErrSnapshotInvalid
	}
	block, e := historyHash(request.BlockHash)
	if e != nil || request.Snapshot == "" || request.Limit < 1 || request.Limit > 100 {
		return result, "", enscontract.ErrSnapshotInvalid
	}
	next := ""
	e = dbaccess.WithTransactionOptions(ctx, r.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(q *dbgen.Queries) error {
		scope, err := q.ReadENSHistoryScope(ctx, r.chain, r.options.Policy.Bytes())
		if errors.Is(err, pgx.ErrNoRows) {
			return enscontract.ErrSnapshotInvalid
		}
		if err != nil {
			return err
		}
		node := historyNode(name)
		snapshot, number, err := r.historySnapshot(ctx, q, scope, node, request.Snapshot)
		if err != nil {
			return err
		}
		if snapshot != snapshot.snapshot() {
			return enscontract.ErrSnapshotInvalid
		}
		h := snapshot
		h.EventBlock = block.Hex()
		h.EventTx = tx.Hex()
		h.After = -1
		if request.Cursor != "" {
			if publicquery.DecodeCursor(request.Cursor, &h) != nil || h.snapshot() != snapshot || h.EventBlock != block.Hex() || h.EventTx != tx.Hex() || h.After < 0 || h.Before != "" || h.Tx != 0 {
				return enscontract.ErrSnapshotInvalid
			}
		}
		rows, err := q.ListENSHistoryTransactionEvents(ctx, dbgen.ListENSHistoryTransactionEventsParams{SourceID: scope.ID, Node: node.Bytes(), BlockHash: block.Bytes(), TransactionHash: tx.Bytes(), SnapshotNumber: number, AfterLog: h.After, RowLimit: int32(request.Limit + 1)})
		if err != nil {
			return err
		}
		more := len(rows) > request.Limit
		if more {
			rows = rows[:request.Limit]
		}
		for _, row := range rows {
			item := enscontract.HistoryEvent{LogIndex: strconv.FormatInt(row.LogIndex, 10), Emitter: common.BytesToAddress(row.Address).Hex(), EventName: row.EventName, Action: row.Action}
			if json.Unmarshal(row.Fields, &item.Fields) != nil {
				return errors.New("invalid history fields")
			}
			result.Items = append(result.Items, item)
		}
		if more {
			h.After = rows[len(rows)-1].LogIndex
			next, err = publicquery.EncodeCursor(h)
		}
		return err
	})
	if e != nil {
		return result, "", readError(e)
	}
	return result, next, nil
}

func historyHash(value string) (common.Hash, error) {
	if len(value) != 66 || !strings.HasPrefix(value, "0x") {
		return common.Hash{}, enscontract.ErrSnapshotInvalid
	}
	bytes, err := hex.DecodeString(value[2:])
	if err != nil {
		return common.Hash{}, enscontract.ErrSnapshotInvalid
	}
	return common.BytesToHash(bytes), nil
}
