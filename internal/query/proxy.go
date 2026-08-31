package query

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/islishude/etherview/internal/cwiaargs"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/proxycontract"
	"github.com/islishude/etherview/internal/publicquery"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
)

const (
	ProxyStatusNotDetected        = "not_detected"
	ProxyStatusDetectedUnverified = "detected_unverified"
	ProxyStatusVerified           = "verified"
	ProxyStatusUnavailable        = "unavailable"
	ProxyStatusFailed             = "failed"

	ProxyCoverageComplete = "complete"
	ProxyCoveragePartial  = "partial"
)

type ProxySnapshot struct {
	Number string
	Hash   string
}

type ProxyIdentity struct {
	Address            string
	CodeHash           string
	ArtifactResolution string
	ArtifactKind       string
	StandardVersion    string
	Verified           bool
}

type ProxyEvidence struct {
	Subject     string
	Source      string
	Result      string
	Address     string
	CodeHash    string
	BlockNumber string
	BlockHash   string
}

type ProxyManagement struct {
	Kind               string
	Target             ProxyIdentity
	AffectedProxyCount string
}

type ProxyDetail struct {
	Address               string
	Status                string
	Snapshot              ProxySnapshot
	Mechanism             string
	Pattern               string
	StandardVersion       string
	Confidence            string
	EvidenceState         string
	ImmutableArgs         string
	ImmutableArgsDecoding *CWIAImmutableArgsDecoding
	BindingID             string
	Proxy                 *ProxyIdentity
	Implementation        *ProxyIdentity
	Admin                 *ProxyIdentity
	Beacon                *ProxyIdentity
	Management            *ProxyManagement
	Evidence              []ProxyEvidence
	DetectionV2           json.RawMessage
}

type ProxyHistoryCoverage struct {
	State     string
	FromBlock string
	ToBlock   string
}

type ProxyUpgrade struct {
	ChangeType        string
	EvidenceType      string
	BlockNumber       string
	BlockHash         string
	BlockTimestamp    time.Time
	OldImplementation *ProxyIdentity
	NewImplementation ProxyIdentity
	TransactionHash   string
	LogIndex          string
	EmitterAddress    string
	Beacon            *ProxyIdentity
	Management        *ProxyManagement

	eventOrder int64
	sourceRank int32
}

type ProxyUpgradePage struct {
	ProxyAddress string
	Coverage     ProxyHistoryCoverage
	Items        []ProxyUpgrade
	Snapshot     ProxySnapshot
	NextCursor   string
}

type ProxyInitialization struct {
	Version         string
	BlockNumber     string
	BlockHash       string
	BlockTimestamp  time.Time
	TransactionHash string
	LogIndex        string
	Implementation  ProxyIdentity
}

type ProxyInitializationPage struct {
	ContractAddress string
	Coverage        ProxyHistoryCoverage
	Items           []ProxyInitialization
	Snapshot        ProxySnapshot
	NextCursor      string
}

type DiamondFacetCut struct {
	CutIndex     int
	Action       string
	FacetAddress string
	Selectors    []string
}

type DiamondCut struct {
	BlockNumber      string
	BlockHash        string
	BlockTimestamp   time.Time
	TransactionHash  string
	TransactionIndex string
	LogIndex         string
	InitAddress      string
	InitCalldata     string
	Cuts             []DiamondFacetCut
}

type DiamondCutPage struct {
	DiamondAddress string
	Coverage       ProxyHistoryCoverage
	Items          []DiamondCut
	Snapshot       ProxySnapshot
	NextCursor     string
}

type proxyHistoryCursor struct {
	Version           int    `json:"v"`
	ChainID           string `json:"chain_id"`
	Address           string `json:"address"`
	Kind              string `json:"kind"`
	SnapshotNumber    uint64 `json:"snapshot_number"`
	SnapshotHash      string `json:"snapshot_hash"`
	DurableJobID      int64  `json:"durable_job_id"`
	JobGeneration     int64  `json:"job_generation"`
	HistoryEpoch      int64  `json:"history_epoch"`
	BeforeBlockNumber uint64 `json:"before_block_number"`
	BeforeEventOrder  int64  `json:"before_event_order,omitempty"`
	BeforeSourceRank  int32  `json:"before_source_rank,omitempty"`
	BeforeLogIndex    int64  `json:"before_log_index,omitempty"`
}

// Proxy returns the latest published proxy@2 state from the writer. Exact
// implementation-as-proxy and management interaction is exposed only when an
// immutable P30 binding still matches the current canonical state, code epochs,
// shared Beacon/UUPS observations, and continuous interaction coverage.
func (r *PostgresReader) Proxy(ctx context.Context, rawAddress string) (ProxyDetail, error) {
	address, err := ethrpc.ParseAddress(rawAddress)
	if err != nil {
		return ProxyDetail{}, fmt.Errorf("invalid proxy address: %w", err)
	}
	chainID, err := r.proxyChainNumeric()
	if err != nil {
		return ProxyDetail{}, err
	}
	result := ProxyDetail{Address: address.Hex(), Evidence: []ProxyEvidence{}}
	cwiaAnalysis := cwiaargs.UnavailableAnalysis()
	cwiaResolution := ""
	err = r.withProxyReadTransaction(ctx, func(queries *dbgen.Queries) error {
		snapshot, queryErr := queries.GetProxyAPISnapshot(ctx, chainID)
		if queryErr != nil {
			return queryErr
		}
		result.Snapshot, queryErr = proxySnapshot(snapshot.SnapshotNumber, snapshot.SnapshotHash)
		if queryErr != nil {
			return queryErr
		}
		switch snapshot.StageState {
		case "failed":
			result.Status = ProxyStatusFailed
			return nil
		case "unavailable":
			result.Status = ProxyStatusUnavailable
			return nil
		case "complete":
		default:
			return fmt.Errorf("stored proxy stage state %q is invalid", snapshot.StageState)
		}
		v2, v2Err := queries.GetLatestPublishedProxyDetectionV2(
			ctx, chainID, address.Bytes(),
		)
		if v2Err == nil {
			if !json.Valid(v2) {
				return errors.New("stored proxy detection V2 is invalid JSON")
			}
			result.DetectionV2 = append(json.RawMessage(nil), v2...)
		} else if !errors.Is(v2Err, pgx.ErrNoRows) {
			return v2Err
		}

		detection, detectionErr := queries.GetLatestPublishedProxyDetection(
			ctx, chainID, address.Bytes(),
		)
		if errors.Is(detectionErr, pgx.ErrNoRows) {
			result.Status = ProxyStatusNotDetected
			negative, negativeErr := queries.GetLatestPublishedProxyNegativeEvidence(
				ctx, chainID, address.Bytes(),
			)
			if negativeErr == nil {
				evidence, evidenceErr := negativeProxyEvidence(address, negative)
				if evidenceErr != nil {
					return evidenceErr
				}
				result.Evidence = append(result.Evidence, evidence)
			} else if !errors.Is(negativeErr, pgx.ErrNoRows) {
				return negativeErr
			}
			return nil
		}
		if detectionErr != nil {
			return detectionErr
		}
		if queryErr = applyProxyDetection(&result, address, detection); queryErr != nil {
			return queryErr
		}

		binding, bindingErr := queries.GetCurrentVerifiedProxyBinding(
			ctx, chainID, address.Bytes(),
		)
		if bindingErr != nil && !errors.Is(bindingErr, pgx.ErrNoRows) {
			return bindingErr
		}
		if bindingErr == nil {
			if queryErr = applyVerifiedProxyBinding(&result, address, binding); queryErr != nil {
				return queryErr
			}
		}
		if bindingErr == nil && result.Management != nil && result.Management.Kind == "upgradeable_beacon" {
			count, countErr := queries.CountCurrentBeaconProxies(
				ctx, binding.BeaconAddress, chainID,
			)
			if countErr != nil {
				return countErr
			}
			if _, parseErr := parseDecimalUint64(count); parseErr != nil {
				return fmt.Errorf("decode affected Beacon proxy count: %w", parseErr)
			}
			result.Management.AffectedProxyCount = count
		}
		if result.Mechanism == string(proxycontract.CWIA) {
			if result.Implementation == nil {
				return errors.New("CWIA implementation identity is missing")
			}
			implementation, parseErr := ethrpc.ParseAddress(result.Implementation.Address)
			if parseErr != nil {
				return errors.New("CWIA implementation address is invalid")
			}
			codeHash, parseErr := hexutil.Decode(result.Implementation.CodeHash)
			if parseErr != nil || len(codeHash) != common.HashLength {
				return errors.New("CWIA implementation code hash is invalid")
			}
			snapshotNumber, parseErr := parseDecimalUint64(result.Snapshot.Number)
			if parseErr != nil {
				return errors.New("CWIA schema snapshot number is invalid")
			}
			rows, analysisErr := queries.GetCWIAImplementationAnalyses(
				ctx, dbgen.GetCWIAImplementationAnalysesParams{
					ImplementationAddress: implementation.Bytes(),
					SnapshotNumber:        numericUint64(snapshotNumber), ChainID: chainID,
					ImplementationCodeHash: codeHash,
				},
			)
			if analysisErr != nil {
				return analysisErr
			}
			cwiaAnalysis, cwiaResolution = selectCWIAAnalysis(rows)
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ProxyDetail{}, publicquery.ErrNotReady
	}
	if err != nil {
		return ProxyDetail{}, fmt.Errorf("query proxy detail: %w", err)
	}
	if result.Mechanism == string(proxycontract.CWIA) {
		decoding := cwiaargs.Decode(result.ImmutableArgs, cwiaAnalysis, cwiaResolution)
		result.ImmutableArgsDecoding = &decoding
	}
	return result, nil
}

func (r *PostgresReader) ProxyUpgrades(
	ctx context.Context,
	rawAddress string,
	encodedCursor string,
	limit int,
) (ProxyUpgradePage, error) {
	if limit <= 0 || limit > 100 {
		return ProxyUpgradePage{}, fmt.Errorf("proxy upgrade limit %d is outside 1..100", limit)
	}
	address, cursor, err := r.decodeProxyHistoryCursor(rawAddress, encodedCursor, "upgrades")
	if err != nil {
		return ProxyUpgradePage{}, err
	}
	chainID, err := r.proxyChainNumeric()
	if err != nil {
		return ProxyUpgradePage{}, err
	}
	result := ProxyUpgradePage{ProxyAddress: address.Hex(), Items: []ProxyUpgrade{}}
	err = r.withProxyReadTransaction(ctx, func(queries *dbgen.Queries) error {
		if err := r.prepareProxyHistorySnapshot(ctx, queries, chainID, address, encodedCursor, &cursor); err != nil {
			return err
		}
		result.Snapshot = ProxySnapshot{Number: strconv.FormatUint(cursor.SnapshotNumber, 10), Hash: cursor.SnapshotHash}
		coverage, err := loadProxyHistoryCoverage(ctx, queries, chainID, address, cursor, "upgrades")
		if err != nil {
			return err
		}
		result.Coverage = coverage
		rows, err := queries.ListProxyUpgradeHistory(ctx, dbgen.ListProxyUpgradeHistoryParams{
			ChainID: chainID, PageLimit: int32(limit + 1),
			SnapshotNumber: numericUint64(cursor.SnapshotNumber),
			ProxyAddress:   address.Bytes(), HasBoundary: encodedCursor != "",
			BeforeBlockNumber: numericUint64(cursor.BeforeBlockNumber),
			BeforeEventOrder:  cursor.BeforeEventOrder,
			BeforeSourceRank:  cursor.BeforeSourceRank,
		})
		if err != nil {
			return err
		}
		for index, row := range rows {
			if index == limit {
				break
			}
			item, err := proxyUpgradeModel(row)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, item)
		}
		if len(rows) > limit && len(result.Items) != 0 {
			last := result.Items[len(result.Items)-1]
			cursor.BeforeBlockNumber, err = parseDecimalUint64(last.BlockNumber)
			if err != nil {
				return err
			}
			cursor.BeforeEventOrder = last.eventOrder
			cursor.BeforeSourceRank = last.sourceRank
			result.NextCursor, err = publicquery.EncodeCursor(cursor)
			if err != nil {
				return fmt.Errorf("encode proxy upgrade cursor: %w", err)
			}
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ProxyUpgradePage{}, publicquery.ErrNotReady
	}
	if err != nil {
		if errors.Is(err, ErrInvalidCursor) {
			return ProxyUpgradePage{}, ErrInvalidCursor
		}
		return ProxyUpgradePage{}, fmt.Errorf("query proxy upgrades: %w", err)
	}
	return result, nil
}

func (r *PostgresReader) ProxyInitializations(
	ctx context.Context,
	rawAddress string,
	encodedCursor string,
	limit int,
) (ProxyInitializationPage, error) {
	if limit <= 0 || limit > 100 {
		return ProxyInitializationPage{}, fmt.Errorf("proxy initialization limit %d is outside 1..100", limit)
	}
	address, cursor, err := r.decodeProxyHistoryCursor(rawAddress, encodedCursor, "initializations")
	if err != nil {
		return ProxyInitializationPage{}, err
	}
	chainID, err := r.proxyChainNumeric()
	if err != nil {
		return ProxyInitializationPage{}, err
	}
	result := ProxyInitializationPage{ContractAddress: address.Hex(), Items: []ProxyInitialization{}}
	err = r.withProxyReadTransaction(ctx, func(queries *dbgen.Queries) error {
		if err := r.prepareProxyHistorySnapshot(ctx, queries, chainID, address, encodedCursor, &cursor); err != nil {
			return err
		}
		result.Snapshot = ProxySnapshot{Number: strconv.FormatUint(cursor.SnapshotNumber, 10), Hash: cursor.SnapshotHash}
		coverage, err := loadProxyHistoryCoverage(ctx, queries, chainID, address, cursor, "initializations")
		if err != nil {
			return err
		}
		result.Coverage = coverage
		rows, err := queries.ListProxyInitializationHistory(ctx, dbgen.ListProxyInitializationHistoryParams{
			ChainID: chainID, PageLimit: int32(limit + 1), ProxyAddress: address.Bytes(),
			SnapshotNumber:    numericUint64(cursor.SnapshotNumber),
			HasBoundary:       encodedCursor != "",
			BeforeBlockNumber: numericUint64(cursor.BeforeBlockNumber),
			BeforeLogIndex:    cursor.BeforeLogIndex,
		})
		if err != nil {
			return err
		}
		for index, row := range rows {
			if index == limit {
				break
			}
			item, err := proxyInitializationModel(row)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, item)
		}
		if len(rows) > limit && len(result.Items) != 0 {
			last := result.Items[len(result.Items)-1]
			cursor.BeforeBlockNumber, err = parseDecimalUint64(last.BlockNumber)
			if err != nil {
				return err
			}
			logIndex, err := strconv.ParseInt(last.LogIndex, 10, 64)
			if err != nil || logIndex < 0 {
				return errors.New("stored proxy initialization log index is invalid")
			}
			cursor.BeforeLogIndex = logIndex
			result.NextCursor, err = publicquery.EncodeCursor(cursor)
			if err != nil {
				return fmt.Errorf("encode proxy initialization cursor: %w", err)
			}
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ProxyInitializationPage{}, publicquery.ErrNotReady
	}
	if err != nil {
		if errors.Is(err, ErrInvalidCursor) {
			return ProxyInitializationPage{}, ErrInvalidCursor
		}
		return ProxyInitializationPage{}, fmt.Errorf("query proxy initializations: %w", err)
	}
	return result, nil
}

func (r *PostgresReader) DiamondCuts(
	ctx context.Context,
	rawAddress string,
	encodedCursor string,
	limit int,
) (DiamondCutPage, error) {
	if limit <= 0 || limit > 100 {
		return DiamondCutPage{}, fmt.Errorf("DiamondCut history limit %d is outside 1..100", limit)
	}
	address, cursor, err := r.decodeProxyHistoryCursor(rawAddress, encodedCursor, "diamond_cuts")
	if err != nil {
		return DiamondCutPage{}, err
	}
	chainID, err := r.proxyChainNumeric()
	if err != nil {
		return DiamondCutPage{}, err
	}
	result := DiamondCutPage{DiamondAddress: address.Hex(), Items: []DiamondCut{}}
	err = r.withProxyReadTransaction(ctx, func(queries *dbgen.Queries) error {
		if err := r.prepareProxyHistorySnapshot(ctx, queries, chainID, address, encodedCursor, &cursor); err != nil {
			return err
		}
		result.Snapshot = ProxySnapshot{
			Number: strconv.FormatUint(cursor.SnapshotNumber, 10), Hash: cursor.SnapshotHash,
		}
		result.Coverage = ProxyHistoryCoverage{
			State: ProxyCoveragePartial, ToBlock: result.Snapshot.Number,
		}
		coverage, coverageErr := queries.GetDiamondCutHistoryCoverage(
			ctx, dbgen.GetDiamondCutHistoryCoverageParams{
				ChainID: chainID, SnapshotNumber: numericUint64(cursor.SnapshotNumber),
				SnapshotHash:   common.HexToHash(cursor.SnapshotHash).Bytes(),
				DiamondAddress: address.Bytes(),
			},
		)
		if coverageErr == nil {
			if _, parseErr := parseDecimalUint64(coverage.FromBlock); parseErr != nil {
				return fmt.Errorf("decode DiamondCut coverage start: %w", parseErr)
			}
			if _, parseErr := parseDecimalUint64(coverage.ToBlock); parseErr != nil {
				return fmt.Errorf("decode DiamondCut coverage end: %w", parseErr)
			}
			result.Coverage.FromBlock = coverage.FromBlock
			result.Coverage.ToBlock = coverage.ToBlock
			if coverage.Complete {
				result.Coverage.State = ProxyCoverageComplete
			}
		} else if !errors.Is(coverageErr, pgx.ErrNoRows) {
			return coverageErr
		}
		rows, queryErr := queries.ListDiamondCutHistory(ctx, dbgen.ListDiamondCutHistoryParams{
			ChainID: chainID, DiamondAddress: address.Bytes(),
			SnapshotNumber:    numericUint64(cursor.SnapshotNumber),
			HasBoundary:       encodedCursor != "",
			BeforeBlockNumber: numericUint64(cursor.BeforeBlockNumber),
			BeforeLogIndex:    cursor.BeforeLogIndex, PageLimit: int32(limit + 1),
		})
		if queryErr != nil {
			return queryErr
		}
		for index, row := range rows {
			if index == limit {
				break
			}
			item, modelErr := diamondCutModel(row)
			if modelErr != nil {
				return modelErr
			}
			result.Items = append(result.Items, item)
		}
		if len(rows) > limit && len(result.Items) != 0 {
			last := result.Items[len(result.Items)-1]
			cursor.BeforeBlockNumber, err = parseDecimalUint64(last.BlockNumber)
			if err != nil {
				return err
			}
			cursor.BeforeLogIndex, err = strconv.ParseInt(last.LogIndex, 10, 64)
			if err != nil || cursor.BeforeLogIndex < 0 {
				return errors.New("stored DiamondCut log index is invalid")
			}
			result.NextCursor, err = publicquery.EncodeCursor(cursor)
			if err != nil {
				return fmt.Errorf("encode DiamondCut cursor: %w", err)
			}
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return DiamondCutPage{}, publicquery.ErrNotReady
	}
	if err != nil {
		if errors.Is(err, ErrInvalidCursor) {
			return DiamondCutPage{}, ErrInvalidCursor
		}
		return DiamondCutPage{}, fmt.Errorf("query DiamondCut history: %w", err)
	}
	return result, nil
}

func (r *PostgresReader) proxyChainNumeric() (pgtype.Numeric, error) {
	value, ok := new(big.Int).SetString(r.chainID, 10)
	if !ok || value.Sign() <= 0 {
		return pgtype.Numeric{}, errors.New("query reader chain ID is invalid")
	}
	return pgtype.Numeric{Int: value, Valid: true}, nil
}

func numericUint64(value uint64) pgtype.Numeric {
	return pgtype.Numeric{Int: new(big.Int).SetUint64(value), Valid: true}
}

func (r *PostgresReader) withProxyReadTransaction(
	ctx context.Context,
	callback func(*dbgen.Queries) error,
) error {
	connection, err := r.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire proxy writer connection: %w", err)
	}
	defer func() { _ = connection.Close() }()
	return connection.Raw(func(driverConnection any) error {
		stdlibConnection, ok := driverConnection.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("proxy queries require pgx stdlib, got %T", driverConnection)
		}
		transaction, err := stdlibConnection.Conn().BeginTx(ctx, pgx.TxOptions{
			IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
		})
		if err != nil {
			return fmt.Errorf("begin stable proxy writer query: %w", err)
		}
		defer func() { _ = transaction.Rollback(ctx) }()
		if err := callback(dbgen.New(transaction)); err != nil {
			return err
		}
		if err := transaction.Commit(ctx); err != nil {
			return fmt.Errorf("commit stable proxy writer query: %w", err)
		}
		return nil
	})
}

func proxySnapshot(number string, hash []byte) (ProxySnapshot, error) {
	if _, err := parseDecimalUint64(number); err != nil {
		return ProxySnapshot{}, fmt.Errorf("decode proxy snapshot number: %w", err)
	}
	if len(hash) != common.HashLength {
		return ProxySnapshot{}, errors.New("stored proxy snapshot hash is invalid")
	}
	return ProxySnapshot{Number: number, Hash: strings.ToLower(common.BytesToHash(hash).Hex())}, nil
}

func (r *PostgresReader) decodeProxyHistoryCursor(
	rawAddress string,
	encoded string,
	kind string,
) (common.Address, proxyHistoryCursor, error) {
	address, err := ethrpc.ParseAddress(rawAddress)
	if err != nil {
		return common.Address{}, proxyHistoryCursor{}, fmt.Errorf("invalid proxy address: %w", err)
	}
	cursor := proxyHistoryCursor{Version: 1, ChainID: r.chainID,
		Address: strings.ToLower(address.Hex()), Kind: kind}
	if encoded == "" {
		return address, cursor, nil
	}
	if err := publicquery.DecodeCursor(encoded, &cursor); err != nil ||
		cursor.Version != 1 || cursor.ChainID != r.chainID ||
		cursor.Address != strings.ToLower(address.Hex()) || cursor.Kind != kind ||
		cursor.BeforeBlockNumber > cursor.SnapshotNumber ||
		cursor.SnapshotHash == "" || cursor.DurableJobID <= 0 ||
		cursor.JobGeneration <= 0 || cursor.HistoryEpoch < 0 || cursor.BeforeEventOrder < 0 ||
		cursor.BeforeSourceRank < 0 || cursor.BeforeLogIndex < 0 {
		return common.Address{}, proxyHistoryCursor{}, ErrInvalidCursor
	}
	if _, err := ethrpc.ParseHash(cursor.SnapshotHash); err != nil {
		return common.Address{}, proxyHistoryCursor{}, ErrInvalidCursor
	}
	return address, cursor, nil
}

func (r *PostgresReader) prepareProxyHistorySnapshot(
	ctx context.Context,
	queries *dbgen.Queries,
	chainID pgtype.Numeric,
	address common.Address,
	encoded string,
	cursor *proxyHistoryCursor,
) error {
	if encoded == "" {
		snapshot, err := queries.GetProxyAPISnapshot(ctx, chainID)
		if err != nil {
			return err
		}
		number, err := parseDecimalUint64(snapshot.SnapshotNumber)
		if err != nil || len(snapshot.SnapshotHash) != common.HashLength {
			return errors.New("stored proxy history snapshot is invalid")
		}
		cursor.SnapshotNumber = number
		cursor.SnapshotHash = strings.ToLower(common.BytesToHash(snapshot.SnapshotHash).Hex())
		if snapshot.DurableJobID <= 0 || snapshot.JobGeneration <= 0 {
			return publicquery.ErrNotReady
		}
		cursor.DurableJobID = snapshot.DurableJobID
		cursor.JobGeneration = snapshot.JobGeneration
		if snapshot.HistoryEpoch < 0 {
			return errors.New("stored proxy history epoch is invalid")
		}
		cursor.HistoryEpoch = snapshot.HistoryEpoch
		cursor.BeforeBlockNumber = number
		cursor.BeforeEventOrder = math.MaxInt64
		cursor.BeforeSourceRank = math.MaxInt32
		cursor.BeforeLogIndex = math.MaxInt64
		return nil
	}
	hash, err := ethrpc.ParseHash(cursor.SnapshotHash)
	if err != nil {
		return ErrInvalidCursor
	}
	canonical, err := queries.ValidateProxyAPISnapshot(ctx, dbgen.ValidateProxyAPISnapshotParams{
		ChainID: chainID, SnapshotNumber: numericUint64(cursor.SnapshotNumber),
		SnapshotHash: hash.Bytes(), DurableJobID: cursor.DurableJobID,
		JobGeneration: cursor.JobGeneration, HistoryEpoch: cursor.HistoryEpoch,
	})
	if err != nil {
		return err
	}
	if !canonical {
		return ErrInvalidCursor
	}
	_ = address
	return nil
}

func loadProxyHistoryCoverage(
	ctx context.Context,
	queries *dbgen.Queries,
	chainID pgtype.Numeric,
	address common.Address,
	cursor proxyHistoryCursor,
	historyKind string,
) (ProxyHistoryCoverage, error) {
	hash, _ := ethrpc.ParseHash(cursor.SnapshotHash)
	row, err := queries.GetProxyHistoryCoverage(ctx, dbgen.GetProxyHistoryCoverageParams{
		ChainID: chainID, SnapshotNumber: numericUint64(cursor.SnapshotNumber),
		SnapshotHash: hash.Bytes(), ProxyAddress: address.Bytes(), HistoryKind: historyKind,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ProxyHistoryCoverage{State: ProxyCoveragePartial}, nil
	}
	if err != nil {
		return ProxyHistoryCoverage{}, err
	}
	state := ProxyCoveragePartial
	if row.Complete {
		state = ProxyCoverageComplete
	}
	return ProxyHistoryCoverage{State: state, FromBlock: row.FromBlock, ToBlock: row.ToBlock}, nil
}

func applyProxyDetection(
	detail *ProxyDetail,
	address common.Address,
	row dbgen.GetLatestPublishedProxyDetectionRow,
) error {
	proxy, err := currentProxyIdentity(address.Bytes(), row.ProxyCodeHash, row.ProxyVerified)
	if err != nil {
		return err
	}
	implementation, err := optionalCurrentProxyIdentity(
		row.ImplementationAddress, row.ImplementationCodeHash, row.ImplementationVerified,
	)
	if err != nil {
		return err
	}
	if implementation == nil && row.ProxyKind != "beacon" {
		return errors.New("stored proxy implementation identity is missing")
	}
	if implementation != nil && !implementation.Verified && row.ImplementationArtifactAvailable {
		implementation.ArtifactResolution = "code_hash"
	}
	admin, err := optionalCurrentProxyIdentity(row.AdminAddress, row.AdminCodeHash, row.AdminVerified)
	if err != nil {
		return err
	}
	beacon, err := optionalCurrentProxyIdentity(row.BeaconAddress, row.BeaconCodeHash, row.BeaconVerified)
	if err != nil {
		return err
	}
	blockHash, err := requiredHash(row.ObservationBlockHash, "proxy observation block hash")
	if err != nil {
		return err
	}
	if _, err := parseDecimalUint64(row.ObservationBlockNumber); err != nil {
		return fmt.Errorf("decode proxy observation block number: %w", err)
	}
	detail.Status = ProxyStatusDetectedUnverified
	detail.Mechanism = row.ProxyKind
	detail.Pattern = row.ProxyPattern
	detail.StandardVersion = optionalString(row.StandardVersion)
	detail.Confidence = row.Confidence
	detail.EvidenceState = row.EvidenceState
	detail.ImmutableArgs = optionalData(row.ImmutableArgs)
	if detail.Pattern == "clone" && detail.ImmutableArgs == "" {
		detail.ImmutableArgs = "0x"
	}
	detail.Proxy, detail.Implementation = proxy, implementation
	detail.Admin, detail.Beacon = admin, beacon
	implementationBlockNumber, implementationBlockHash := row.ObservationBlockNumber, blockHash
	if row.ProxyKind == "beacon" && implementation != nil {
		if _, err := parseDecimalUint64(row.ImplementationObservationBlockNumber); err != nil {
			return fmt.Errorf("decode Beacon implementation observation block: %w", err)
		}
		implementationBlockHash, err = requiredHash(
			row.ImplementationObservationBlockHash, "Beacon implementation observation block hash",
		)
		if err != nil {
			return err
		}
		implementationBlockNumber = row.ImplementationObservationBlockNumber
	}
	detail.Evidence = recognitionEvidence(*detail, row.ObservationBlockNumber, blockHash,
		implementationBlockNumber, implementationBlockHash, false)
	return nil
}

func applyVerifiedProxyBinding(
	detail *ProxyDetail,
	address common.Address,
	row dbgen.GetCurrentVerifiedProxyBindingRow,
) error {
	if row.BindingID == "" || row.SnapshotNumber != detail.Snapshot.Number ||
		len(row.SnapshotHash) != common.HashLength ||
		!strings.EqualFold(strings.ToLower(common.BytesToHash(row.SnapshotHash).Hex()), detail.Snapshot.Hash) {
		return errors.New("stored proxy binding snapshot is invalid")
	}
	if len(row.ObservationBlockHash) != common.HashLength {
		return errors.New("stored proxy binding observation block hash is invalid")
	}
	if row.ProxyPattern == "" || row.ProxyPattern == "unknown" ||
		(row.ProxyPattern == "clone") == row.ProxyVerified {
		return errors.New("stored verified proxy binding identity is invalid")
	}
	if (row.ProxyPattern == "clone" && row.StandardVersion != nil) ||
		(row.ProxyPattern != "clone" &&
			(row.StandardVersion == nil || *row.StandardVersion != "5.6.1")) {
		return errors.New("stored verified proxy binding standard is invalid")
	}
	proxy, err := currentProxyIdentity(address.Bytes(), row.ProxyCodeHash, row.ProxyVerified)
	if err != nil {
		return err
	}
	implementation, err := currentProxyIdentity(row.ImplementationAddress, row.ImplementationCodeHash, true)
	if err != nil {
		return err
	}
	admin, err := optionalCurrentProxyIdentity(row.AdminAddress, row.AdminCodeHash, true)
	if err != nil {
		return err
	}
	beacon, err := optionalCurrentProxyIdentity(row.BeaconAddress, row.BeaconCodeHash, true)
	if err != nil {
		return err
	}
	detail.Status = ProxyStatusVerified
	detail.BindingID = row.BindingID
	detail.Mechanism = row.ProxyKind
	detail.Pattern = row.ProxyPattern
	detail.StandardVersion = optionalString(row.StandardVersion)
	detail.EvidenceState = "exact"
	detail.Confidence = "verified"
	detail.Proxy, detail.Implementation = proxy, implementation
	detail.Admin, detail.Beacon = admin, beacon
	decorateProxyArtifacts(detail)
	if row.ManagementKind != "none" {
		management, err := currentProxyIdentity(row.ManagementAddress, row.ManagementCodeHash, true)
		if err != nil {
			return err
		}
		management.ArtifactKind = map[string]string{
			"proxy_admin": "proxy_admin", "upgradeable_beacon": "upgradeable_beacon",
		}[row.ManagementKind]
		management.StandardVersion = "5.6.1"
		detail.Management = &ProxyManagement{Kind: row.ManagementKind, Target: *management}
	} else {
		detail.Management = nil
	}
	if _, err := parseDecimalUint64(row.ImplementationObservationBlockNumber); err != nil {
		return fmt.Errorf("decode bound implementation observation block: %w", err)
	}
	implementationBlockHash, err := requiredHash(
		row.ImplementationObservationBlockHash, "bound implementation observation block hash",
	)
	if err != nil {
		return err
	}
	detail.Evidence = recognitionEvidence(*detail, row.ObservationBlockNumber,
		strings.ToLower(common.BytesToHash(row.ObservationBlockHash).Hex()),
		row.ImplementationObservationBlockNumber, implementationBlockHash, true)
	return nil
}

func decorateProxyArtifacts(detail *ProxyDetail) {
	if detail.Proxy != nil {
		detail.Proxy.StandardVersion = detail.StandardVersion
		detail.Proxy.ArtifactKind = map[string]string{
			"erc1967": "erc1967_proxy", "transparent": "transparent_proxy",
			"uups": "erc1967_proxy", "beacon": "beacon_proxy",
		}[detail.Pattern]
	}
	if detail.Implementation != nil && detail.Pattern == "uups" {
		detail.Implementation.ArtifactKind = "uups_implementation"
		detail.Implementation.StandardVersion = "5.6.1"
	}
	if detail.Admin != nil && detail.Pattern == "transparent" {
		detail.Admin.ArtifactKind = "proxy_admin"
		detail.Admin.StandardVersion = "5.6.1"
	}
	if detail.Beacon != nil && detail.Pattern == "beacon" {
		detail.Beacon.ArtifactKind = "upgradeable_beacon"
		detail.Beacon.StandardVersion = "5.6.1"
	}
}

func recognitionEvidence(
	detail ProxyDetail,
	blockNumber string,
	blockHash string,
	implementationBlockNumber string,
	implementationBlockHash string,
	verified bool,
) []ProxyEvidence {
	result := "corroborating"
	if verified {
		result = "authoritative"
	}
	evidence := []ProxyEvidence{{Subject: "proxy", Source: "runtime_code", Result: result,
		Address: detail.Address, CodeHash: identityHash(detail.Proxy), BlockNumber: blockNumber, BlockHash: blockHash}}
	if detail.Implementation != nil && implementationBlockNumber != "" && implementationBlockHash != "" {
		source := "implementation_slot"
		if detail.Mechanism == "eip1167" || detail.Mechanism == "cwia" {
			source = "runtime_code"
		} else if detail.Beacon != nil {
			source = "direct_call"
		}
		evidence = append(evidence, ProxyEvidence{Subject: "implementation", Source: source,
			Result: result, Address: detail.Implementation.Address, CodeHash: detail.Implementation.CodeHash,
			BlockNumber: implementationBlockNumber, BlockHash: implementationBlockHash})
	}
	if detail.Beacon != nil {
		source := "beacon_slot"
		if verified {
			source = "runtime_immutable"
		}
		evidence = append(evidence, ProxyEvidence{Subject: "beacon", Source: source, Result: result,
			Address: detail.Beacon.Address, CodeHash: detail.Beacon.CodeHash, BlockNumber: blockNumber, BlockHash: blockHash})
	}
	if detail.Admin != nil {
		source := "admin_slot"
		if verified {
			source = "runtime_immutable"
		}
		evidence = append(evidence, ProxyEvidence{Subject: "admin", Source: source,
			Result: result, Address: detail.Admin.Address, CodeHash: detail.Admin.CodeHash,
			BlockNumber: blockNumber, BlockHash: blockHash})
	}
	return evidence
}

func negativeProxyEvidence(
	address common.Address,
	row dbgen.GetLatestPublishedProxyNegativeEvidenceRow,
) (ProxyEvidence, error) {
	if _, err := parseDecimalUint64(row.BlockNumber); err != nil {
		return ProxyEvidence{}, fmt.Errorf("decode negative proxy evidence block: %w", err)
	}
	blockHash, err := requiredHash(row.BlockHash, "negative proxy evidence block hash")
	if err != nil {
		return ProxyEvidence{}, err
	}
	codeHash, err := requiredHash(row.CodeHash, "negative proxy evidence code hash")
	if err != nil {
		return ProxyEvidence{}, err
	}
	return ProxyEvidence{Subject: "proxy", Source: "runtime_code", Result: "rejected",
		Address: address.Hex(), CodeHash: codeHash, BlockNumber: row.BlockNumber, BlockHash: blockHash}, nil
}

func proxyUpgradeModel(row dbgen.ListProxyUpgradeHistoryRow) (ProxyUpgrade, error) {
	blockHash, err := requiredHash(row.BlockHash, "proxy upgrade block hash")
	if err != nil {
		return ProxyUpgrade{}, err
	}
	timestamp, err := proxyBlockTimestamp(row.BlockTimestamp)
	if err != nil {
		return ProxyUpgrade{}, err
	}
	newImplementation, err := historicalProxyIdentity(
		row.NewImplementationAddress, row.NewImplementationCodeHash,
		row.NewImplementationVerified,
	)
	if err != nil || newImplementation == nil {
		return ProxyUpgrade{}, errors.New("stored proxy upgrade implementation is invalid")
	}
	var oldImplementation *ProxyIdentity
	if len(row.OldImplementationAddress) != 0 {
		oldImplementation, err = historicalProxyIdentity(
			row.OldImplementationAddress, row.OldImplementationCodeHash,
			row.OldImplementationVerified,
		)
		if err != nil {
			return ProxyUpgrade{}, err
		}
	}
	item := ProxyUpgrade{ChangeType: row.ChangeType, EvidenceType: row.EvidenceType,
		BlockNumber: row.BlockNumber, BlockHash: blockHash, BlockTimestamp: timestamp,
		OldImplementation: oldImplementation, NewImplementation: *newImplementation,
		eventOrder: row.EventOrder, sourceRank: row.SourceRank}
	if _, err := parseDecimalUint64(row.BlockNumber); err != nil {
		return ProxyUpgrade{}, fmt.Errorf("decode proxy upgrade block number: %w", err)
	}
	if len(row.TransactionHash) != 0 {
		item.TransactionHash, err = requiredHash(row.TransactionHash, "proxy upgrade transaction hash")
		if err != nil {
			return ProxyUpgrade{}, err
		}
	}
	if row.LogIndex != nil {
		if *row.LogIndex < 0 {
			return ProxyUpgrade{}, errors.New("stored proxy upgrade log index is negative")
		}
		item.LogIndex = strconv.FormatInt(*row.LogIndex, 10)
	}
	item.EmitterAddress, err = optionalAddress(row.EmitterAddress)
	if err != nil {
		return ProxyUpgrade{}, err
	}
	item.Beacon, err = historicalProxyIdentity(row.BeaconAddress, row.BeaconCodeHash, false)
	if err != nil {
		return ProxyUpgrade{}, err
	}
	if row.ManagementKind != "" && len(row.ManagementAddress) != 0 {
		management, err := historicalProxyIdentity(
			row.ManagementAddress, row.ManagementCodeHash, row.ManagementVerified,
		)
		if err != nil || management == nil {
			return ProxyUpgrade{}, errors.New("stored proxy upgrade management identity is invalid")
		}
		item.Management = &ProxyManagement{Kind: row.ManagementKind, Target: *management}
	}
	return item, nil
}

func proxyInitializationModel(row dbgen.ListProxyInitializationHistoryRow) (ProxyInitialization, error) {
	if _, err := parseDecimalUint64(row.Version); err != nil {
		return ProxyInitialization{}, fmt.Errorf("decode initialization version: %w", err)
	}
	if _, err := parseDecimalUint64(row.BlockNumber); err != nil {
		return ProxyInitialization{}, fmt.Errorf("decode initialization block: %w", err)
	}
	if row.LogIndex < 0 {
		return ProxyInitialization{}, errors.New("stored initialization log index is negative")
	}
	blockHash, err := requiredHash(row.BlockHash, "initialization block hash")
	if err != nil {
		return ProxyInitialization{}, err
	}
	transactionHash, err := requiredHash(row.TransactionHash, "initialization transaction hash")
	if err != nil {
		return ProxyInitialization{}, err
	}
	timestamp, err := proxyBlockTimestamp(row.BlockTimestamp)
	if err != nil {
		return ProxyInitialization{}, err
	}
	implementation, err := historicalProxyIdentity(
		row.ImplementationAddress, row.ImplementationCodeHash, row.ImplementationVerified,
	)
	if err != nil || implementation == nil {
		return ProxyInitialization{}, errors.New("stored initialization implementation is invalid")
	}
	return ProxyInitialization{Version: row.Version, BlockNumber: row.BlockNumber,
		BlockHash: blockHash, BlockTimestamp: timestamp, TransactionHash: transactionHash,
		LogIndex: strconv.FormatInt(row.LogIndex, 10), Implementation: *implementation}, nil
}

func diamondCutModel(row dbgen.ListDiamondCutHistoryRow) (DiamondCut, error) {
	if _, err := parseDecimalUint64(row.BlockNumber); err != nil {
		return DiamondCut{}, fmt.Errorf("decode DiamondCut block number: %w", err)
	}
	if row.TransactionIndex < 0 || row.LogIndex < 0 {
		return DiamondCut{}, errors.New("stored DiamondCut position is negative")
	}
	blockHash, err := requiredHash(row.BlockHash, "DiamondCut block hash")
	if err != nil {
		return DiamondCut{}, err
	}
	transactionHash, err := requiredHash(row.TransactionHash, "DiamondCut transaction hash")
	if err != nil {
		return DiamondCut{}, err
	}
	timestamp, err := proxyBlockTimestamp(row.BlockTimestamp)
	if err != nil {
		return DiamondCut{}, err
	}
	if len(row.InitAddress) != common.AddressLength {
		return DiamondCut{}, errors.New("stored DiamondCut init address is invalid")
	}
	var stored []struct {
		CutIndex  int      `json:"cut_index"`
		Action    uint8    `json:"action"`
		Facet     string   `json:"facet_address"`
		Selectors []string `json:"selectors"`
	}
	if !json.Valid(row.Cuts) || json.Unmarshal(row.Cuts, &stored) != nil ||
		len(stored) > proxycontract.DiamondMaxFacets {
		return DiamondCut{}, errors.New("stored DiamondCut payload is invalid")
	}
	cuts := make([]DiamondFacetCut, len(stored))
	selectorCount := 0
	for index, cut := range stored {
		if cut.CutIndex != index || cut.Action > 2 || len(cut.Selectors) == 0 ||
			len(cut.Selectors) > proxycontract.DiamondMaxSelectorsPerFacet {
			return DiamondCut{}, errors.New("stored DiamondCut entry is invalid")
		}
		facet, err := ethrpc.ParseAddress(cut.Facet)
		if err != nil {
			return DiamondCut{}, errors.New("stored DiamondCut facet address is invalid")
		}
		action := [...]string{"add", "replace", "remove"}[cut.Action]
		cuts[index] = DiamondFacetCut{
			CutIndex: cut.CutIndex, Action: action, FacetAddress: facet.Hex(),
			Selectors: make([]string, len(cut.Selectors)),
		}
		for selectorIndex, selector := range cut.Selectors {
			if len(selector) != 10 || !strings.HasPrefix(selector, "0x") {
				return DiamondCut{}, errors.New("stored DiamondCut selector is invalid")
			}
			decoded, decodeErr := hex.DecodeString(selector[2:])
			if decodeErr != nil || len(decoded) != 4 {
				return DiamondCut{}, errors.New("stored DiamondCut selector is invalid")
			}
			cuts[index].Selectors[selectorIndex] = "0x" + strings.ToLower(selector[2:])
		}
		selectorCount += len(cut.Selectors)
		if selectorCount > proxycontract.DiamondMaxSelectorsTotal {
			return DiamondCut{}, errors.New("stored DiamondCut selector count exceeds the public bound")
		}
	}
	return DiamondCut{
		BlockNumber: row.BlockNumber, BlockHash: blockHash, BlockTimestamp: timestamp,
		TransactionHash:  transactionHash,
		TransactionIndex: strconv.FormatInt(row.TransactionIndex, 10),
		LogIndex:         strconv.FormatInt(row.LogIndex, 10),
		InitAddress:      common.BytesToAddress(row.InitAddress).Hex(),
		InitCalldata:     "0x" + hex.EncodeToString(row.InitCalldata), Cuts: cuts,
	}, nil
}

func currentProxyIdentity(address, codeHash []byte, verified bool) (*ProxyIdentity, error) {
	if len(address) != common.AddressLength || len(codeHash) != common.HashLength {
		return nil, errors.New("stored current proxy identity is invalid")
	}
	identity := &ProxyIdentity{Address: common.BytesToAddress(address).Hex(),
		CodeHash: strings.ToLower(common.BytesToHash(codeHash).Hex()), Verified: verified}
	if verified {
		identity.ArtifactResolution = "exact_address"
	}
	return identity, nil
}

func optionalCurrentProxyIdentity(address, codeHash []byte, verified bool) (*ProxyIdentity, error) {
	if len(address) == 0 && len(codeHash) == 0 {
		return nil, nil
	}
	return currentProxyIdentity(address, codeHash, verified)
}

func historicalProxyIdentity(address, codeHash []byte, verified bool) (*ProxyIdentity, error) {
	if len(address) == 0 {
		if len(codeHash) != 0 {
			return nil, errors.New("stored historical proxy identity has a hash without an address")
		}
		return nil, nil
	}
	if len(address) != common.AddressLength || (len(codeHash) != 0 && len(codeHash) != common.HashLength) {
		return nil, errors.New("stored historical proxy identity is invalid")
	}
	identity := &ProxyIdentity{Address: common.BytesToAddress(address).Hex(), Verified: verified}
	if len(codeHash) != 0 {
		identity.CodeHash = strings.ToLower(common.BytesToHash(codeHash).Hex())
	}
	return identity, nil
}

func requiredHash(value []byte, field string) (string, error) {
	if len(value) != common.HashLength {
		return "", fmt.Errorf("stored %s is invalid", field)
	}
	return strings.ToLower(common.BytesToHash(value).Hex()), nil
}

func optionalAddress(value []byte) (string, error) {
	if len(value) == 0 {
		return "", nil
	}
	if len(value) != common.AddressLength {
		return "", errors.New("stored optional proxy address is invalid")
	}
	return common.BytesToAddress(value).Hex(), nil
}

func proxyBlockTimestamp(value string) (time.Time, error) {
	seconds, err := strconv.ParseUint(value, 10, 64)
	if err != nil || seconds > math.MaxInt64 {
		return time.Time{}, errors.New("stored proxy block timestamp is invalid")
	}
	return time.Unix(int64(seconds), 0).UTC(), nil
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func optionalData(value []byte) string {
	if value == nil {
		return ""
	}
	return "0x" + hex.EncodeToString(value)
}

func identityHash(identity *ProxyIdentity) string {
	if identity == nil {
		return ""
	}
	return identity.CodeHash
}
