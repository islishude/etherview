package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/browsing"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/stagecontract"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type AddressLogRequest struct {
	ChainID, Address, Cursor, EventSignature, Topic, Name string
	FromBlock, ToBlock                                    string
	TopicIndex                                            *int
	Limit                                                 int
}
type AddressLog struct {
	Log                                                       TransactionLog
	BlockNumber, BlockHash, TransactionHash, TransactionIndex string
	BlockTimestamp                                            time.Time
}
type AddressLogPage struct {
	Items      []AddressLog
	NextCursor string
	Snapshot   Snapshot
}
type AddressLogEvent struct{ Name, Signature string }
type AddressLogEventPage struct {
	Items      []AddressLogEvent
	NextCursor string
	Snapshot   Snapshot
}

type addressLogCursor struct {
	Version      int    `json:"v"`
	Kind         string `json:"k"`
	Chain        string `json:"c"`
	Address      string `json:"a"`
	Filter       string `json:"f"`
	Number       string `json:"n"`
	Hash         string `json:"h"`
	CoreRevision string `json:"r"`
	ABIRevision  string `json:"d,omitempty"`
	BeforeNumber string `json:"b"`
	BeforeTx     int64  `json:"t"`
	BeforeLog    int64  `json:"l"`
	AfterID      int64  `json:"i,omitempty"`
}

func (r AddressLogRequest) validate(directory bool) error {
	if validateChainID(r.ChainID) != nil || r.Limit < 0 || r.Limit > 100 || len(r.EventSignature) > 4096 || len(r.Name) > 128 {
		return ErrInvalidInput
	}
	if (r.FromBlock != "" && !canonicalUint256(r.FromBlock)) || (r.ToBlock != "" && !canonicalUint256(r.ToBlock)) || (r.FromBlock != "" && r.ToBlock != "" && compareUnsignedDecimal(r.FromBlock, r.ToBlock) > 0) {
		return ErrInvalidInput
	}
	if directory {
		if r.EventSignature != "" || r.Topic != "" || r.TopicIndex != nil {
			return ErrInvalidInput
		}
	} else {
		if r.Name != "" || (r.EventSignature != "" && (r.Topic != "" || r.TopicIndex != nil)) || (r.Topic != "") != (r.TopicIndex != nil) {
			return ErrInvalidInput
		}
		if r.TopicIndex != nil {
			if *r.TopicIndex < 0 || *r.TopicIndex > 3 {
				return ErrInvalidInput
			}
			if _, err := decodeFixedHex(r.Topic, 32); err != nil {
				return ErrInvalidInput
			}
		}
	}
	return nil
}

func logNumeric(value string) pgtype.Numeric { var n pgtype.Numeric; _ = n.Scan(value); return n }

func (catalog *Postgres) addressLogSnapshot(ctx context.Context, tx pgx.Tx, request AddressLogRequest, directory bool) (Snapshot, addressLogCursor, error) {
	_, address, err := checksumInputAddress(request.Address)
	if err != nil {
		return Snapshot{}, addressLogCursor{}, err
	}
	kind := "logs"
	if directory {
		kind = "events"
	}
	index := ""
	if request.TopicIndex != nil {
		index = strconv.Itoa(*request.TopicIndex)
	}
	digest := sha256.Sum256([]byte(request.EventSignature + "\x00" + index + "\x00" + strings.ToLower(request.Topic) + "\x00" + strings.ToLower(request.Name) + "\x00" + request.FromBlock + "\x00" + request.ToBlock))
	filter := hex.EncodeToString(digest[:])
	cursor := addressLogCursor{Version: 2, Kind: kind, Chain: request.ChainID, Address: address, Filter: filter, BeforeTx: math.MaxInt64, BeforeLog: math.MaxInt64}
	var snapshot Snapshot
	if request.Cursor == "" {
		snapshot, err = readCanonicalSnapshot(ctx, tx, request.ChainID)
		if err != nil {
			return snapshot, cursor, err
		}
		cursor.Number, cursor.Hash, cursor.BeforeNumber = snapshot.BlockNumber, snapshot.BlockHash, snapshot.BlockNumber
	} else {
		if decodeCursor(request.Cursor, &cursor) != nil || cursor.Version != 2 || cursor.Kind != kind || cursor.Chain != request.ChainID || cursor.Address != address || cursor.Filter != filter || !canonicalUint256(cursor.BeforeNumber) || cursor.BeforeTx < 0 || cursor.BeforeLog < 0 || cursor.AfterID < 0 {
			return snapshot, cursor, ErrInvalidCursor
		}
		snapshot = Snapshot{ChainID: request.ChainID, BlockNumber: cursor.Number, BlockHash: cursor.Hash}
		if err = validateCanonicalSnapshot(ctx, tx, snapshot); err != nil {
			return snapshot, cursor, err
		}
		if compareUnsignedDecimal(cursor.BeforeNumber, snapshot.BlockNumber) > 0 {
			return snapshot, cursor, ErrInvalidCursor
		}
	}
	snapshot.Revision = cursor.CoreRevision
	if err = readAddressLogBrowsing(ctx, tx, &snapshot, request, StageCore); err != nil {
		return snapshot, cursor, err
	}
	cursor.CoreRevision = snapshot.Revision
	if directory || request.EventSignature != "" {
		snapshot.Revision = cursor.ABIRevision
		if err = readAddressLogBrowsing(ctx, tx, &snapshot, request, StageABI); err != nil {
			return snapshot, cursor, err
		}
		cursor.ABIRevision = snapshot.Revision
	}
	return snapshot, cursor, nil
}

func (r AddressLogRequest) bounds(snapshot string) (string, string) {
	from, to := r.FromBlock, snapshot
	if from == "" {
		from = "0"
	}
	if r.ToBlock != "" && compareUnsignedDecimal(r.ToBlock, to) < 0 {
		to = r.ToBlock
	}
	return from, to
}

func readAddressLogBrowsing(ctx context.Context, tx pgx.Tx, snapshot *Snapshot, request AddressLogRequest, stage Stage) error {
	from, to := request.bounds(snapshot.BlockNumber)
	status, err := browsing.ReadRange(ctx, tx, snapshot.ChainID, snapshot.BlockNumber, snapshot.BlockHash, stagecontract.ID{Name: string(stage), Version: uint32(stage.Version())}, from, to, "")
	if err != nil {
		return err
	}
	if compareUnsignedDecimal(from, to) > 0 {
		status.State = "pending"
	}
	revision := from + ":" + to + ":" + status.Revision
	if request.Cursor != "" && snapshot.Revision != revision {
		return ErrInvalidCursor
	}
	snapshot.Revision, snapshot.Browsing = revision, status
	return nil
}

func (catalog *Postgres) AddressLogs(ctx context.Context, request AddressLogRequest) (AddressLogPage, error) {
	page := AddressLogPage{Items: []AddressLog{}}
	if err := request.validate(false); err != nil {
		return page, err
	}
	address, _, err := checksumInputAddress(request.Address)
	if err != nil {
		return page, err
	}
	limit := request.Limit
	if limit == 0 {
		limit = 25
	}
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return page, err
	}
	defer dbaccess.Rollback(ctx, tx)
	snapshot, cursor, err := catalog.addressLogSnapshot(ctx, tx, request, false)
	if err != nil {
		return page, err
	}
	page.Snapshot = snapshot
	q := dbgen.New(tx)
	from, to := request.bounds(snapshot.BlockNumber)
	params := dbgen.AddressLogsAllParams{ChainID: logNumeric(request.ChainID), Address: address, SnapshotNumber: logNumeric(to), FromBlock: logNumeric(from), BeforeNumber: logNumeric(cursor.BeforeNumber), BeforeTx: cursor.BeforeTx, BeforeLog: cursor.BeforeLog, PageLimit: int32(limit + 1)}
	rows, err := addressLogRows(ctx, q, params, request)
	if err != nil {
		return page, fmt.Errorf("read address logs: %w", err)
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	bytes := 0
	budget := &logReadBudget{bytes: catalog.options.MaxTraceDataBytes, candidates: 256}
	for _, row := range rows {
		detail, err := q.AddressLogDetail(ctx, dbgen.AddressLogDetailParams{ChainID: params.ChainID, BlockHash: row.BlockHash, TxHash: row.TxHash, BlockNumber: logNumeric(row.BlockNumber), LogIndex: row.LogIndex})
		if err != nil {
			return page, fmt.Errorf("read address log detail: %w", err)
		}
		bytes += len(detail.Raw) + len(detail.Arguments) + len(detail.Candidates)
		if bytes > catalog.options.MaxTraceDataBytes {
			if len(page.Items) == 0 {
				return page, StageUnavailableError{Stage: StageCore, State: StageUnavailable}
			}
			more = true
			break
		}
		identity := TransactionResourceIdentity{ChainID: request.ChainID, BlockNumber: row.BlockNumber, BlockHash: "0x" + hex.EncodeToString(row.BlockHash), TransactionHash: "0x" + hex.EncodeToString(row.TxHash), TransactionIndex: strconv.FormatInt(row.TxIndex, 10)}
		item, err := projectStoredTransactionLog(ctx, tx, identity, dbgen.CatalogTransactionLogsRow(detail), budget)
		if err != nil {
			return page, err
		}
		seconds, err := strconv.ParseInt(row.BlockTimestamp, 10, 64)
		if err != nil || seconds < 0 {
			return page, ErrCorruptData
		}
		page.Items = append(page.Items, AddressLog{Log: item, BlockNumber: identity.BlockNumber, BlockHash: identity.BlockHash, TransactionHash: identity.TransactionHash, TransactionIndex: identity.TransactionIndex, BlockTimestamp: time.Unix(seconds, 0).UTC()})
		cursor.BeforeNumber, cursor.BeforeTx, cursor.BeforeLog = row.BlockNumber, row.TxIndex, row.LogIndex
	}
	if more {
		page.NextCursor, err = encodeCursor(cursor)
		if err != nil {
			return page, err
		}
	}
	return page, commitRead(ctx, tx)
}

func (catalog *Postgres) AddressLogEvents(ctx context.Context, request AddressLogRequest) (AddressLogEventPage, error) {
	page := AddressLogEventPage{Items: []AddressLogEvent{}}
	if err := request.validate(true); err != nil {
		return page, err
	}
	address, _, err := checksumInputAddress(request.Address)
	if err != nil {
		return page, err
	}
	limit := request.Limit
	if limit == 0 {
		limit = 25
	}
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return page, err
	}
	defer dbaccess.Rollback(ctx, tx)
	snapshot, cursor, err := catalog.addressLogSnapshot(ctx, tx, request, true)
	if err != nil {
		return page, err
	}
	page.Snapshot = snapshot
	from, to := request.bounds(snapshot.BlockNumber)
	prefix := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(strings.ToLower(request.Name)) + "%"
	rows, err := dbgen.New(tx).AddressLogEvents(ctx, dbgen.AddressLogEventsParams{ChainID: logNumeric(request.ChainID), Address: address, SnapshotNumber: logNumeric(to), FromBlock: logNumeric(from), NamePrefix: prefix, AfterID: cursor.AfterID, PageLimit: int32(limit + 1)})
	if err != nil {
		return page, fmt.Errorf("read address event directory: %w", err)
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Items = append(page.Items, AddressLogEvent{Name: row.Name, Signature: row.Signature})
		cursor.AfterID = row.SignatureID
	}
	if more {
		page.NextCursor, err = encodeCursor(cursor)
		if err != nil {
			return page, err
		}
	}
	return page, commitRead(ctx, tx)
}

func addressLogRows(ctx context.Context, q *dbgen.Queries, p dbgen.AddressLogsAllParams, r AddressLogRequest) ([]dbgen.AddressLogsAllRow, error) {
	if r.EventSignature != "" {
		rows, err := q.AddressLogsSignature(ctx, dbgen.AddressLogsSignatureParams{ChainID: p.ChainID, Address: p.Address, SnapshotNumber: p.SnapshotNumber, FromBlock: p.FromBlock, BeforeNumber: p.BeforeNumber, BeforeTx: p.BeforeTx, BeforeLog: p.BeforeLog, PageLimit: p.PageLimit, Signature: r.EventSignature})
		result := make([]dbgen.AddressLogsAllRow, len(rows))
		for i, row := range rows {
			result[i] = dbgen.AddressLogsAllRow(row)
		}
		return result, err
	}
	if r.TopicIndex == nil {
		return q.AddressLogsAll(ctx, p)
	}
	topic, err := decodeFixedHex(r.Topic, 32)
	if err != nil {
		return nil, ErrInvalidInput
	}
	params := dbgen.AddressLogsTopic0Params{ChainID: p.ChainID, Address: p.Address, SnapshotNumber: p.SnapshotNumber, FromBlock: p.FromBlock, BeforeNumber: p.BeforeNumber, BeforeTx: p.BeforeTx, BeforeLog: p.BeforeLog, PageLimit: p.PageLimit, Topic: topic}
	switch *r.TopicIndex {
	case 0:
		rows, err := q.AddressLogsTopic0(ctx, dbgen.AddressLogsTopic0Params(params))
		result := make([]dbgen.AddressLogsAllRow, len(rows))
		for i, row := range rows {
			result[i] = dbgen.AddressLogsAllRow(row)
		}
		return result, err
	case 1:
		rows, err := q.AddressLogsTopic1(ctx, dbgen.AddressLogsTopic1Params(params))
		result := make([]dbgen.AddressLogsAllRow, len(rows))
		for i, row := range rows {
			result[i] = dbgen.AddressLogsAllRow(row)
		}
		return result, err
	case 2:
		rows, err := q.AddressLogsTopic2(ctx, dbgen.AddressLogsTopic2Params(params))
		result := make([]dbgen.AddressLogsAllRow, len(rows))
		for i, row := range rows {
			result[i] = dbgen.AddressLogsAllRow(row)
		}
		return result, err
	case 3:
		rows, err := q.AddressLogsTopic3(ctx, dbgen.AddressLogsTopic3Params(params))
		result := make([]dbgen.AddressLogsAllRow, len(rows))
		for i, row := range rows {
			result[i] = dbgen.AddressLogsAllRow(row)
		}
		return result, err
	}
	return nil, ErrInvalidInput
}
