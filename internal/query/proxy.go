package query

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
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
	Address         string
	CodeHash        string
	ArtifactKind    string
	StandardVersion string
	Verified        bool
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
	Address         string
	Status          string
	Snapshot        ProxySnapshot
	Mechanism       string
	Pattern         string
	StandardVersion string
	Confidence      string
	EvidenceState   string
	ImmutableArgs   string
	BindingID       string
	Proxy           *ProxyIdentity
	Implementation  *ProxyIdentity
	Admin           *ProxyIdentity
	Beacon          *ProxyIdentity
	Management      *ProxyManagement
	Evidence        []ProxyEvidence
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
		if errors.Is(bindingErr, pgx.ErrNoRows) {
			return nil
		}
		if bindingErr != nil {
			return bindingErr
		}
		if queryErr = applyVerifiedProxyBinding(&result, address, binding); queryErr != nil {
			return queryErr
		}
		if result.Management != nil && result.Management.Kind == "upgradeable_beacon" {
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
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ProxyDetail{}, httpapi.ErrNotReady
	}
	if err != nil {
		return ProxyDetail{}, fmt.Errorf("query proxy detail: %w", err)
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
			result.NextCursor, err = httpapi.EncodeCursor(cursor)
			if err != nil {
				return fmt.Errorf("encode proxy upgrade cursor: %w", err)
			}
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ProxyUpgradePage{}, httpapi.ErrNotReady
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
			result.NextCursor, err = httpapi.EncodeCursor(cursor)
			if err != nil {
				return fmt.Errorf("encode proxy initialization cursor: %w", err)
			}
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ProxyInitializationPage{}, httpapi.ErrNotReady
	}
	if err != nil {
		if errors.Is(err, ErrInvalidCursor) {
			return ProxyInitializationPage{}, ErrInvalidCursor
		}
		return ProxyInitializationPage{}, fmt.Errorf("query proxy initializations: %w", err)
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
	if err := httpapi.DecodeCursor(encoded, &cursor); err != nil ||
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
			return httpapi.ErrNotReady
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
		if detail.Mechanism == "eip1167" {
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

func currentProxyIdentity(address, codeHash []byte, verified bool) (*ProxyIdentity, error) {
	if len(address) != common.AddressLength || len(codeHash) != common.HashLength {
		return nil, errors.New("stored current proxy identity is invalid")
	}
	return &ProxyIdentity{Address: common.BytesToAddress(address).Hex(),
		CodeHash: strings.ToLower(common.BytesToHash(codeHash).Hex()), Verified: verified}, nil
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
