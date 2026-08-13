package mempool

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/islishude/etherview/internal/ethrpc"
)

const (
	cursorVersion      = 1
	maximumCursorBytes = 2048
)

var errSnapshotExpired = errors.New("mempool snapshot expired")

type PostgresOptions struct {
	ChainID uint64
	Enabled bool
	Now     func() time.Time
}

type Postgres struct {
	db      *sql.DB
	chainID uint64
	chain   string
	enabled bool
	now     func() time.Time
}

var _ Store = (*Postgres)(nil)
var _ Reader = (*Postgres)(nil)

func NewPostgres(db *sql.DB, options PostgresOptions) (*Postgres, error) {
	if db == nil {
		return nil, errors.New("mempool database is nil")
	}
	if options.ChainID == 0 {
		return nil, errors.New("mempool chain ID must be greater than zero")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Postgres{
		db: db, chainID: options.ChainID, chain: strconv.FormatUint(options.ChainID, 10),
		enabled: options.Enabled, now: options.Now,
	}, nil
}

func (repository *Postgres) StoreSnapshot(ctx context.Context, snapshot Snapshot) (SnapshotInfo, error) {
	if err := validateSnapshotForStorage(snapshot); err != nil {
		return SnapshotInfo{}, err
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("begin mempool snapshot transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := lockMempool(ctx, tx, repository.chain); err != nil {
		return SnapshotInfo{}, err
	}
	previousSnapshotID, replacementEvidence, err := repository.replacementPredecessor(ctx, tx, snapshot)
	if err != nil {
		return SnapshotInfo{}, err
	}

	var snapshotID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO mempool_snapshots (
			chain_id, endpoint_name, observed_at, expires_at, transaction_count
		) VALUES ($1::numeric, $2, $3, $4, $5)
		RETURNING id`,
		repository.chain, snapshot.Endpoint, snapshot.ObservedAt, snapshot.ExpiresAt, len(snapshot.Transactions),
	).Scan(&snapshotID)
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("insert mempool snapshot: %w", err)
	}

	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO mempool_transactions (
			chain_id, tx_hash, from_address, to_address, nonce, value, gas,
			gas_price, max_fee_per_gas, max_priority_fee_per_gas, tx_type,
			input, raw, first_seen_at, last_seen_at, expires_at, last_endpoint_name
		) VALUES (
			$1::numeric, $2, $3, $4, $5::numeric, $6::numeric, $7::numeric,
			$8::numeric, $9::numeric, $10::numeric, $11::numeric,
			$12, $13::jsonb, $14, $15, $16, $17
		)
		ON CONFLICT (chain_id, tx_hash) DO UPDATE SET
			last_seen_at = GREATEST(mempool_transactions.last_seen_at, EXCLUDED.last_seen_at),
			expires_at = GREATEST(mempool_transactions.expires_at, EXCLUDED.expires_at),
			last_endpoint_name = CASE
				WHEN EXCLUDED.last_seen_at >= mempool_transactions.last_seen_at THEN EXCLUDED.last_endpoint_name
				ELSE mempool_transactions.last_endpoint_name
			END
		WHERE mempool_transactions.from_address = EXCLUDED.from_address
		  AND mempool_transactions.to_address IS NOT DISTINCT FROM EXCLUDED.to_address
		  AND mempool_transactions.nonce = EXCLUDED.nonce
		  AND mempool_transactions.value = EXCLUDED.value
		  AND mempool_transactions.gas = EXCLUDED.gas
		  AND mempool_transactions.gas_price IS NOT DISTINCT FROM EXCLUDED.gas_price
		  AND mempool_transactions.max_fee_per_gas IS NOT DISTINCT FROM EXCLUDED.max_fee_per_gas
		  AND mempool_transactions.max_priority_fee_per_gas IS NOT DISTINCT FROM EXCLUDED.max_priority_fee_per_gas
		  AND mempool_transactions.tx_type IS NOT DISTINCT FROM EXCLUDED.tx_type
		  AND mempool_transactions.input = EXCLUDED.input`)
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("prepare mempool transaction upsert: %w", err)
	}
	defer func() { _ = statement.Close() }()

	for index, transaction := range snapshot.Transactions {
		values, err := transactionStorageValues(transaction)
		if err != nil {
			return SnapshotInfo{}, fmt.Errorf("mempool transaction %d: %w", index, err)
		}
		result, err := statement.ExecContext(ctx,
			repository.chain, values.hash, values.from, values.to,
			transaction.Nonce, transaction.Value, transaction.Gas,
			nullableString(transaction.GasPrice), nullableString(transaction.MaxFeePerGas),
			nullableString(transaction.MaxPriorityFeePerGas), nullableString(transaction.Type),
			values.input, string(transaction.Raw), snapshot.ObservedAt, snapshot.ObservedAt,
			snapshot.ExpiresAt, snapshot.Endpoint,
		)
		if err != nil {
			return SnapshotInfo{}, fmt.Errorf("upsert mempool transaction %d: %w", index, err)
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			return SnapshotInfo{}, fmt.Errorf("mempool transaction %d conflicts with an existing hash identity", index)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mempool_snapshot_transactions (chain_id, snapshot_id, tx_hash)
			VALUES ($1::numeric, $2, $3)`, repository.chain, snapshotID, values.hash); err != nil {
			return SnapshotInfo{}, fmt.Errorf("insert mempool snapshot membership %d: %w", index, err)
		}
	}

	if replacementEvidence {
		if _, err := tx.ExecContext(ctx, `
			WITH previous_slots AS (
				SELECT pending.from_address, pending.nonce, (array_agg(pending.tx_hash))[1] AS tx_hash
				FROM mempool_snapshot_transactions AS member
				JOIN mempool_transactions AS pending
				  ON pending.chain_id = member.chain_id AND pending.tx_hash = member.tx_hash
				WHERE member.chain_id = $1::numeric AND member.snapshot_id = $2
				GROUP BY pending.from_address, pending.nonce
				HAVING count(*) = 1
			), current_slots AS (
				SELECT pending.from_address, pending.nonce, (array_agg(pending.tx_hash))[1] AS tx_hash
				FROM mempool_snapshot_transactions AS member
				JOIN mempool_transactions AS pending
				  ON pending.chain_id = member.chain_id AND pending.tx_hash = member.tx_hash
				WHERE member.chain_id = $1::numeric AND member.snapshot_id = $3
				GROUP BY pending.from_address, pending.nonce
				HAVING count(*) = 1
			)
			INSERT INTO mempool_transaction_replacements (
				chain_id, snapshot_id, replaced_hash, replacement_hash
			)
			SELECT $1::numeric, $3, previous.tx_hash, current.tx_hash
			FROM previous_slots AS previous
			JOIN current_slots AS current
			  ON current.from_address = previous.from_address
			 AND current.nonce = previous.nonce
			WHERE current.tx_hash <> previous.tx_hash`,
			repository.chain, previousSnapshotID, snapshotID,
		); err != nil {
			return SnapshotInfo{}, fmt.Errorf("insert mempool replacement observations: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE mempool_transactions AS pending
			SET expires_at = GREATEST(pending.expires_at, $3)
			FROM mempool_transaction_replacements AS replacement
			WHERE replacement.chain_id = $1::numeric
			  AND replacement.snapshot_id = $2
			  AND pending.chain_id = replacement.chain_id
			  AND pending.tx_hash = replacement.replaced_hash`,
			repository.chain, snapshotID, snapshot.ExpiresAt,
		); err != nil {
			return SnapshotInfo{}, fmt.Errorf("extend replaced mempool transaction retention: %w", err)
		}
	}

	statusResult, err := tx.ExecContext(ctx, `
		INSERT INTO mempool_status (
			chain_id, state, endpoint_name, latest_snapshot_id, transaction_count,
			last_attempt_at, last_success_at, error_code, error_message, updated_at
		) VALUES ($1::numeric, 'complete', $2, $3, $4, $5, $5, NULL, NULL, now())
		ON CONFLICT (chain_id) DO UPDATE SET
			state = EXCLUDED.state,
			endpoint_name = EXCLUDED.endpoint_name,
			latest_snapshot_id = EXCLUDED.latest_snapshot_id,
			transaction_count = EXCLUDED.transaction_count,
			last_attempt_at = EXCLUDED.last_attempt_at,
			last_success_at = EXCLUDED.last_success_at,
			error_code = NULL,
			error_message = NULL,
			updated_at = now()
		WHERE mempool_status.last_attempt_at <= EXCLUDED.last_attempt_at`,
		repository.chain, snapshot.Endpoint, snapshotID, len(snapshot.Transactions), snapshot.ObservedAt,
	)
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("update complete mempool status: %w", err)
	}
	if replacementEvidence {
		rows, rowsErr := statusResult.RowsAffected()
		if rowsErr != nil || rows != 1 {
			return SnapshotInfo{}, fmt.Errorf("%w: replacement snapshot did not become current", ErrCorruptData)
		}
	}
	markerResult, markerErr := tx.ExecContext(ctx, `
		UPDATE mempool_status
		SET last_snapshot_write_id = $2
		WHERE chain_id = $1::numeric`, repository.chain, snapshotID,
	)
	if markerErr != nil {
		return SnapshotInfo{}, fmt.Errorf("record mempool snapshot write continuity: %w", markerErr)
	}
	if rows, rowsErr := markerResult.RowsAffected(); rowsErr != nil || rows != 1 {
		return SnapshotInfo{}, fmt.Errorf("%w: mempool snapshot write continuity was not recorded", ErrCorruptData)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM mempool_snapshots
		WHERE chain_id = $1::numeric AND expires_at <= $2 AND id <> $3`,
		repository.chain, snapshot.ObservedAt, snapshotID,
	); err != nil {
		return SnapshotInfo{}, fmt.Errorf("expire mempool snapshots: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM mempool_transactions AS pending
		WHERE pending.chain_id = $1::numeric
		  AND pending.expires_at <= $2
		  AND NOT EXISTS (
			SELECT 1 FROM mempool_snapshot_transactions AS member
			WHERE member.chain_id = pending.chain_id AND member.tx_hash = pending.tx_hash
		  )`, repository.chain, snapshot.ObservedAt); err != nil {
		return SnapshotInfo{}, fmt.Errorf("expire mempool transactions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SnapshotInfo{}, fmt.Errorf("commit mempool snapshot: %w", err)
	}
	return SnapshotInfo{
		ID: snapshotID, Endpoint: snapshot.Endpoint, ObservedAt: snapshot.ObservedAt,
		ExpiresAt: snapshot.ExpiresAt, TransactionCount: len(snapshot.Transactions),
	}, nil
}

func (repository *Postgres) replacementPredecessor(
	ctx context.Context,
	tx *sql.Tx,
	snapshot Snapshot,
) (int64, bool, error) {
	var state string
	var endpoint sql.NullString
	var snapshotID sql.NullInt64
	var lastSnapshotWriteID sql.NullInt64
	var lastAttempt time.Time
	err := tx.QueryRowContext(ctx, `
		SELECT state, endpoint_name, latest_snapshot_id, last_snapshot_write_id, last_attempt_at
		FROM mempool_status
		WHERE chain_id = $1::numeric
		FOR UPDATE`, repository.chain,
	).Scan(&state, &endpoint, &snapshotID, &lastSnapshotWriteID, &lastAttempt)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("query replacement predecessor status: %w", err)
	}
	if state != string(StateComplete) || !endpoint.Valid || !snapshotID.Valid ||
		!lastSnapshotWriteID.Valid || lastSnapshotWriteID.Int64 != snapshotID.Int64 ||
		!snapshot.ObservedAt.After(lastAttempt) || endpoint.String != snapshot.Endpoint {
		return 0, false, nil
	}

	var previousEndpoint string
	var previousObservedAt, previousExpiresAt time.Time
	err = tx.QueryRowContext(ctx, `
		SELECT endpoint_name, observed_at, expires_at
		FROM mempool_snapshots
		WHERE chain_id = $1::numeric AND id = $2`, repository.chain, snapshotID.Int64,
	).Scan(&previousEndpoint, &previousObservedAt, &previousExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, fmt.Errorf("%w: replacement predecessor snapshot is missing", ErrCorruptData)
	}
	if err != nil {
		return 0, false, fmt.Errorf("query replacement predecessor snapshot: %w", err)
	}
	if previousEndpoint != endpoint.String || !previousObservedAt.Equal(lastAttempt) {
		return 0, false, fmt.Errorf("%w: replacement predecessor status differs from its snapshot", ErrCorruptData)
	}
	if !previousExpiresAt.After(snapshot.ObservedAt) {
		return 0, false, nil
	}
	return snapshotID.Int64, true, nil
}

func (repository *Postgres) StoreFailure(ctx context.Context, failure Failure) error {
	if failure.State != StateUnavailable && failure.State != StateFailed {
		return errors.New("mempool failure state must be unavailable or failed")
	}
	if failure.Code == "" || len(failure.Code) > 64 || failure.ObservedAt.IsZero() {
		return errors.New("mempool failure code and observation time are required")
	}
	if len(failure.Endpoint) > 128 {
		return errors.New("mempool failure endpoint is too long")
	}
	failure.Message = boundedMessage(failure.Message)
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin mempool failure transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := lockMempool(ctx, tx, repository.chain); err != nil {
		return err
	}
	var endpoint any
	if failure.Endpoint != "" {
		endpoint = failure.Endpoint
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO mempool_status (
			chain_id, state, endpoint_name, latest_snapshot_id, transaction_count,
			last_attempt_at, last_success_at, error_code, error_message, updated_at
		) VALUES ($1::numeric, $2, $3, NULL, NULL, $4, NULL, $5, $6, now())
		ON CONFLICT (chain_id) DO UPDATE SET
			state = EXCLUDED.state,
			endpoint_name = EXCLUDED.endpoint_name,
			last_attempt_at = EXCLUDED.last_attempt_at,
			error_code = EXCLUDED.error_code,
			error_message = EXCLUDED.error_message,
			updated_at = now()
		WHERE mempool_status.last_attempt_at <= EXCLUDED.last_attempt_at`,
		repository.chain, string(failure.State), endpoint, failure.ObservedAt, failure.Code, failure.Message,
	)
	if err != nil {
		return fmt.Errorf("update failed mempool status: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mempool failure status: %w", err)
	}
	return nil
}

func (repository *Postgres) Pending(ctx context.Context, encodedCursor string, limit int) (Page, error) {
	if !repository.enabled {
		return Page{}, CapabilityError{State: StateUnavailable, Code: "feature_disabled"}
	}
	if limit <= 0 || limit > 100 {
		return Page{}, fmt.Errorf("pending transaction limit %d is outside 1..100", limit)
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return Page{}, fmt.Errorf("begin stable mempool query: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	status, err := repository.readStatus(ctx, tx)
	if err != nil {
		return Page{}, err
	}
	if status.state != StateComplete {
		return Page{}, CapabilityError{State: status.state, Code: status.errorCode, LastAttemptAt: status.lastAttemptAt}
	}

	snapshotID := status.snapshotID
	var boundary *pendingCursor
	if encodedCursor != "" {
		cursor, err := decodePendingCursor(encodedCursor)
		if err != nil || cursor.ChainID != repository.chain || cursor.SnapshotID <= 0 {
			return Page{}, ErrInvalidCursor
		}
		if _, err := ethrpc.ParseHash(cursor.BeforeHash); err != nil || cursor.BeforeFirstSeen.IsZero() {
			return Page{}, ErrInvalidCursor
		}
		snapshotID = cursor.SnapshotID
		boundary = &cursor
	}
	snapshot, err := repository.readSnapshot(ctx, tx, snapshotID)
	if err != nil {
		if (errors.Is(err, sql.ErrNoRows) || errors.Is(err, errSnapshotExpired)) && encodedCursor != "" {
			return Page{}, ErrInvalidCursor
		}
		if errors.Is(err, errSnapshotExpired) {
			return Page{}, CapabilityError{
				State: StateUnavailable, Code: "snapshot_expired", LastAttemptAt: status.lastAttemptAt,
			}
		}
		if errors.Is(err, sql.ErrNoRows) {
			return Page{}, fmt.Errorf("%w: latest mempool snapshot is missing", ErrCorruptData)
		}
		return Page{}, err
	}
	rows, err := repository.pendingRows(ctx, tx, snapshot, boundary, limit+1)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close() //nolint:errcheck
	items := make([]Transaction, 0, limit+1)
	for rows.Next() {
		transaction, err := repository.scanPending(rows, snapshot)
		if err != nil {
			return Page{}, err
		}
		items = append(items, transaction)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("iterate pending transaction page: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Page{}, fmt.Errorf("commit stable mempool query: %w", err)
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	page := Page{Items: items, Snapshot: snapshot}
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		page.NextCursor, err = encodePendingCursor(pendingCursor{
			Version: cursorVersion, ChainID: repository.chain, SnapshotID: snapshot.ID,
			BeforeFirstSeen: last.FirstSeenAt, BeforeHash: last.Hash,
		})
		if err != nil {
			return Page{}, err
		}
	}
	return page, nil
}

func (repository *Postgres) Lookup(ctx context.Context, value string) (Detail, error) {
	if !repository.enabled {
		return Detail{}, CapabilityError{State: StateUnavailable, Code: "feature_disabled"}
	}
	hash, err := ethrpc.ParseHash(value)
	if err != nil {
		return Detail{}, errors.New("invalid mempool transaction hash")
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return Detail{}, fmt.Errorf("begin mempool detail query: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	status, err := repository.readStatus(ctx, tx)
	if err != nil {
		return Detail{}, err
	}

	var currentCapability *CapabilityError
	if status.state == StateComplete {
		snapshot, snapshotErr := repository.readSnapshot(ctx, tx, status.snapshotID)
		switch {
		case snapshotErr == nil:
			transaction, lookupErr := repository.lookupPending(ctx, tx, snapshot, hash.Bytes())
			if lookupErr == nil {
				if err := tx.Commit(); err != nil {
					return Detail{}, fmt.Errorf("commit pending transaction detail query: %w", err)
				}
				return Detail{Kind: DetailPending, Transaction: transaction}, nil
			}
			if !errors.Is(lookupErr, sql.ErrNoRows) {
				return Detail{}, lookupErr
			}
		case errors.Is(snapshotErr, errSnapshotExpired):
			currentCapability = &CapabilityError{
				State: StateUnavailable, Code: "snapshot_expired", LastAttemptAt: status.lastAttemptAt,
			}
		case errors.Is(snapshotErr, sql.ErrNoRows):
			return Detail{}, fmt.Errorf("%w: latest mempool snapshot is missing", ErrCorruptData)
		default:
			return Detail{}, snapshotErr
		}
	} else {
		currentCapability = &CapabilityError{
			State: status.state, Code: status.errorCode, LastAttemptAt: status.lastAttemptAt,
		}
	}

	replaced, err := repository.lookupReplaced(ctx, tx, hash.Bytes())
	if err == nil {
		if commitErr := tx.Commit(); commitErr != nil {
			return Detail{}, fmt.Errorf("commit replaced transaction detail query: %w", commitErr)
		}
		return replaced, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Detail{}, err
	}
	if currentCapability != nil {
		return Detail{}, *currentCapability
	}
	if err := tx.Commit(); err != nil {
		return Detail{}, fmt.Errorf("commit absent mempool transaction detail query: %w", err)
	}
	return Detail{}, ErrNotFound
}

func (repository *Postgres) lookupPending(
	ctx context.Context,
	tx *sql.Tx,
	snapshot SnapshotInfo,
	hash []byte,
) (Transaction, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT
			pending.tx_hash, pending.from_address, pending.to_address,
			pending.nonce::text, pending.value::text, pending.gas::text,
			pending.gas_price::text, pending.max_fee_per_gas::text,
			pending.max_priority_fee_per_gas::text, pending.tx_type::text,
			pending.input, pending.raw, pending.first_seen_at,
			pending.last_seen_at, pending.expires_at,
			predecessor.replaced_hash
		FROM mempool_snapshot_transactions AS member
		JOIN mempool_transactions AS pending
		  ON pending.chain_id = member.chain_id AND pending.tx_hash = member.tx_hash
		LEFT JOIN LATERAL (
			SELECT replacement.replaced_hash
			FROM mempool_transaction_replacements AS replacement
			JOIN mempool_snapshots AS evidence
			  ON evidence.chain_id = replacement.chain_id AND evidence.id = replacement.snapshot_id
			WHERE replacement.chain_id = pending.chain_id
			  AND replacement.replacement_hash = pending.tx_hash
			  AND evidence.observed_at <= $4
			  AND evidence.expires_at > $5
			ORDER BY evidence.observed_at DESC, evidence.id DESC
			LIMIT 1
		) AS predecessor ON TRUE
		WHERE member.chain_id = $1::numeric
		  AND member.snapshot_id = $2
		  AND member.tx_hash = $3`,
		repository.chain, snapshot.ID, hash, snapshot.ObservedAt, repository.now().UTC(),
	)
	return repository.scanPending(row, snapshot)
}

func (repository *Postgres) lookupReplaced(ctx context.Context, tx *sql.Tx, hash []byte) (Detail, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT
			pending.tx_hash, pending.from_address, pending.to_address,
			pending.nonce::text, pending.value::text, pending.gas::text,
			pending.gas_price::text, pending.max_fee_per_gas::text,
			pending.max_priority_fee_per_gas::text, pending.tx_type::text,
			pending.input, pending.raw, pending.first_seen_at,
			pending.last_seen_at, pending.expires_at,
			predecessor.replaced_hash,
			replacement.replacement_hash, evidence.observed_at,
			evidence.expires_at, evidence.endpoint_name
		FROM mempool_transaction_replacements AS replacement
		JOIN mempool_snapshots AS evidence
		  ON evidence.chain_id = replacement.chain_id AND evidence.id = replacement.snapshot_id
		JOIN mempool_transactions AS pending
		  ON pending.chain_id = replacement.chain_id AND pending.tx_hash = replacement.replaced_hash
		LEFT JOIN LATERAL (
			SELECT earlier.replaced_hash
			FROM mempool_transaction_replacements AS earlier
			JOIN mempool_snapshots AS earlier_evidence
			  ON earlier_evidence.chain_id = earlier.chain_id AND earlier_evidence.id = earlier.snapshot_id
			WHERE earlier.chain_id = pending.chain_id
			  AND earlier.replacement_hash = pending.tx_hash
			  AND earlier_evidence.observed_at <= evidence.observed_at
			  AND earlier_evidence.expires_at > $3
			ORDER BY earlier_evidence.observed_at DESC, earlier_evidence.id DESC
			LIMIT 1
		) AS predecessor ON TRUE
		WHERE replacement.chain_id = $1::numeric
		  AND replacement.replaced_hash = $2
		  AND evidence.expires_at > $3
		ORDER BY evidence.observed_at DESC, evidence.id DESC
		LIMIT 1`, repository.chain, hash, repository.now().UTC())

	var hashBytes, from, to, input, raw, replaces, replacement []byte
	var nonce, value, gas string
	var gasPrice, maxFee, priorityFee, txType sql.NullString
	var firstSeen, lastSeen, storedExpires, replacedAt, evidenceExpires time.Time
	var endpoint string
	if err := row.Scan(
		&hashBytes, &from, &to, &nonce, &value, &gas, &gasPrice, &maxFee,
		&priorityFee, &txType, &input, &raw, &firstSeen, &lastSeen, &storedExpires,
		&replaces, &replacement, &replacedAt, &evidenceExpires, &endpoint,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Detail{}, sql.ErrNoRows
		}
		return Detail{}, fmt.Errorf("query replaced mempool transaction: %w", err)
	}
	if storedExpires.Before(evidenceExpires) {
		return Detail{}, fmt.Errorf("%w: replaced transaction retention is shorter than its evidence", ErrCorruptData)
	}
	transaction, err := repository.decodeStoredTransaction(
		hashBytes, from, to, input, raw, nonce, value, gas,
		gasPrice, maxFee, priorityFee, txType,
		firstSeen.UTC(), lastSeen.UTC(), evidenceExpires.UTC(), SnapshotInfo{
			Endpoint: endpoint, ObservedAt: lastSeen.UTC(), ExpiresAt: evidenceExpires.UTC(),
		},
	)
	if err != nil {
		return Detail{}, err
	}
	if replaces != nil {
		value, err := fixedHash(replaces)
		if err != nil {
			return Detail{}, err
		}
		transaction.ReplacesHash = &value
	}
	replacementHash, err := fixedHash(replacement)
	if err != nil {
		return Detail{}, err
	}
	return Detail{
		Kind: DetailReplaced, Transaction: transaction,
		ReplacementHash: replacementHash, ReplacedAt: replacedAt.UTC(),
	}, nil
}

type statusRecord struct {
	state         State
	snapshotID    int64
	errorCode     string
	lastAttemptAt time.Time
}

func (repository *Postgres) readStatus(ctx context.Context, tx *sql.Tx) (statusRecord, error) {
	var state string
	var snapshotID sql.NullInt64
	var errorCode sql.NullString
	var lastAttempt time.Time
	err := tx.QueryRowContext(ctx, `
		SELECT state, latest_snapshot_id, error_code, last_attempt_at
		FROM mempool_status
		WHERE chain_id = $1::numeric`, repository.chain,
	).Scan(&state, &snapshotID, &errorCode, &lastAttempt)
	if errors.Is(err, sql.ErrNoRows) {
		return statusRecord{state: StatePending, errorCode: "not_observed"}, nil
	}
	if err != nil {
		return statusRecord{}, fmt.Errorf("query mempool capability status: %w", err)
	}
	parsed := State(state)
	if parsed != StatePending && parsed != StateComplete && parsed != StateUnavailable && parsed != StateFailed {
		return statusRecord{}, fmt.Errorf("%w: invalid mempool state %q", ErrCorruptData, state)
	}
	if parsed == StateComplete && !snapshotID.Valid {
		return statusRecord{}, fmt.Errorf("%w: complete mempool status has no snapshot", ErrCorruptData)
	}
	if (parsed == StateUnavailable || parsed == StateFailed) && !errorCode.Valid {
		return statusRecord{}, fmt.Errorf("%w: failed mempool status has no error code", ErrCorruptData)
	}
	return statusRecord{
		state: parsed, snapshotID: snapshotID.Int64, errorCode: errorCode.String,
		lastAttemptAt: lastAttempt.UTC(),
	}, nil
}

func (repository *Postgres) readSnapshot(ctx context.Context, tx *sql.Tx, snapshotID int64) (SnapshotInfo, error) {
	var snapshot SnapshotInfo
	err := tx.QueryRowContext(ctx, `
		SELECT id, endpoint_name, observed_at, expires_at, transaction_count
		FROM mempool_snapshots
		WHERE chain_id = $1::numeric AND id = $2`,
		repository.chain, snapshotID,
	).Scan(&snapshot.ID, &snapshot.Endpoint, &snapshot.ObservedAt, &snapshot.ExpiresAt, &snapshot.TransactionCount)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SnapshotInfo{}, sql.ErrNoRows
		}
		return SnapshotInfo{}, fmt.Errorf("query mempool snapshot: %w", err)
	}
	if snapshot.ID <= 0 || snapshot.Endpoint == "" || snapshot.TransactionCount < 0 || !snapshot.ExpiresAt.After(snapshot.ObservedAt) {
		return SnapshotInfo{}, fmt.Errorf("%w: invalid mempool snapshot metadata", ErrCorruptData)
	}
	snapshot.ObservedAt = snapshot.ObservedAt.UTC()
	snapshot.ExpiresAt = snapshot.ExpiresAt.UTC()
	if !snapshot.ExpiresAt.After(repository.now().UTC()) {
		return SnapshotInfo{}, errSnapshotExpired
	}
	return snapshot, nil
}

func (repository *Postgres) pendingRows(ctx context.Context, tx *sql.Tx, snapshot SnapshotInfo, cursor *pendingCursor, limit int) (*sql.Rows, error) {
	const selectSQL = `
		SELECT
			pending.tx_hash, pending.from_address, pending.to_address,
			pending.nonce::text, pending.value::text, pending.gas::text,
			pending.gas_price::text, pending.max_fee_per_gas::text,
			pending.max_priority_fee_per_gas::text, pending.tx_type::text,
			pending.input, pending.raw, pending.first_seen_at,
			pending.last_seen_at, pending.expires_at,
			predecessor.replaced_hash
		FROM mempool_snapshot_transactions AS member
		JOIN mempool_transactions AS pending
		  ON pending.chain_id = member.chain_id AND pending.tx_hash = member.tx_hash
		LEFT JOIN LATERAL (
			SELECT replacement.replaced_hash
			FROM mempool_transaction_replacements AS replacement
			JOIN mempool_snapshots AS evidence
			  ON evidence.chain_id = replacement.chain_id AND evidence.id = replacement.snapshot_id
			WHERE replacement.chain_id = pending.chain_id
			  AND replacement.replacement_hash = pending.tx_hash
			  AND evidence.observed_at <= $3
			  AND evidence.expires_at > $4
			ORDER BY evidence.observed_at DESC, evidence.id DESC
			LIMIT 1
		) AS predecessor ON TRUE
		WHERE member.chain_id = $1::numeric AND member.snapshot_id = $2`
	var rows *sql.Rows
	var err error
	if cursor == nil {
		rows, err = tx.QueryContext(ctx, selectSQL+`
			ORDER BY pending.first_seen_at DESC, pending.tx_hash DESC
			LIMIT $5`, repository.chain, snapshot.ID, snapshot.ObservedAt, repository.now().UTC(), limit)
	} else {
		hash, parseErr := ethrpc.ParseHash(cursor.BeforeHash)
		if parseErr != nil {
			return nil, ErrInvalidCursor
		}
		rows, err = tx.QueryContext(ctx, selectSQL+`
			AND (pending.first_seen_at, pending.tx_hash) < ($5, $6)
			ORDER BY pending.first_seen_at DESC, pending.tx_hash DESC
			LIMIT $7`, repository.chain, snapshot.ID, snapshot.ObservedAt, repository.now().UTC(), cursor.BeforeFirstSeen, hash.Bytes(), limit)
	}
	if err != nil {
		return nil, fmt.Errorf("query pending transaction page: %w", err)
	}
	return rows, nil
}

type rowScanner interface{ Scan(...any) error }

func (repository *Postgres) scanPending(scanner rowScanner, snapshot SnapshotInfo) (Transaction, error) {
	var hash, from, to, input, raw, replaces []byte
	var nonce, value, gas string
	var gasPrice, maxFee, priorityFee, txType sql.NullString
	var firstSeen, lastSeen, expires time.Time
	if err := scanner.Scan(
		&hash, &from, &to, &nonce, &value, &gas, &gasPrice, &maxFee,
		&priorityFee, &txType, &input, &raw, &firstSeen, &lastSeen, &expires, &replaces,
	); err != nil {
		return Transaction{}, fmt.Errorf("scan pending transaction: %w", err)
	}
	transaction, err := repository.decodeStoredTransaction(
		hash, from, to, input, raw, nonce, value, gas,
		gasPrice, maxFee, priorityFee, txType,
		firstSeen.UTC(), lastSeen.UTC(), expires.UTC(), snapshot,
	)
	if err != nil {
		return Transaction{}, err
	}
	// The membership is an immutable observation. A later poll may update the
	// transaction's global last-seen fields, so expose the pinned snapshot time.
	transaction.LastSeenAt = snapshot.ObservedAt
	transaction.ExpiresAt = snapshot.ExpiresAt
	if replaces != nil {
		replacesHash, err := fixedHash(replaces)
		if err != nil {
			return Transaction{}, err
		}
		transaction.ReplacesHash = &replacesHash
	}
	return transaction, nil
}

func (repository *Postgres) decodeStoredTransaction(
	hashBytes, fromBytes, toBytes, inputBytes, raw []byte,
	nonce, value, gas string,
	gasPrice, maxFee, priorityFee, txType sql.NullString,
	firstSeen, lastSeen, expires time.Time,
	snapshot SnapshotInfo,
) (Transaction, error) {
	hash, err := fixedHash(hashBytes)
	if err != nil {
		return Transaction{}, err
	}
	from, err := fixedAddress(fromBytes)
	if err != nil {
		return Transaction{}, err
	}
	var to *string
	if toBytes != nil {
		value, err := fixedAddress(toBytes)
		if err != nil {
			return Transaction{}, err
		}
		to = &value
	}
	for _, quantity := range []string{nonce, value, gas} {
		if !canonicalDecimal(quantity) {
			return Transaction{}, fmt.Errorf("%w: invalid pending quantity", ErrCorruptData)
		}
	}
	for _, quantity := range []sql.NullString{gasPrice, maxFee, priorityFee, txType} {
		if quantity.Valid && !canonicalDecimal(quantity.String) {
			return Transaction{}, fmt.Errorf("%w: invalid optional pending quantity", ErrCorruptData)
		}
	}
	if firstSeen.IsZero() || lastSeen.Before(firstSeen) || !expires.After(lastSeen) {
		return Transaction{}, fmt.Errorf("%w: invalid pending observation timestamps", ErrCorruptData)
	}
	input := hexutil.Encode(inputBytes)
	normalized, err := pendingTransaction(raw, repository.chainID, snapshot.Endpoint, firstSeen, snapshot.ExpiresAt)
	if err != nil {
		return Transaction{}, fmt.Errorf("%w: %v", ErrCorruptData, err)
	}
	if !strings.EqualFold(normalized.Hash, hash) || normalized.From != from || !equalOptionalString(normalized.To, to) ||
		normalized.Nonce != nonce || normalized.Value != value || normalized.Gas != gas || normalized.Input != input ||
		!equalOptionalNull(normalized.GasPrice, gasPrice) || !equalOptionalNull(normalized.MaxFeePerGas, maxFee) ||
		!equalOptionalNull(normalized.MaxPriorityFeePerGas, priorityFee) || !equalOptionalNull(normalized.Type, txType) {
		return Transaction{}, fmt.Errorf("%w: raw pending transaction differs from indexed fields", ErrCorruptData)
	}
	normalized.Raw = append(json.RawMessage(nil), raw...)
	normalized.FirstSeenAt = firstSeen
	normalized.LastSeenAt = lastSeen
	normalized.ExpiresAt = expires
	return normalized, nil
}

type transactionValues struct {
	hash  []byte
	from  []byte
	to    []byte
	input []byte
}

func transactionStorageValues(transaction Transaction) (transactionValues, error) {
	hash, err := ethrpc.ParseHash(transaction.Hash)
	if err != nil {
		return transactionValues{}, errors.New("invalid transaction hash")
	}
	hashBytes := hash.Bytes()
	from, err := ethrpc.ParseAddress(transaction.From)
	if err != nil {
		return transactionValues{}, errors.New("invalid transaction sender")
	}
	fromBytes := from.Bytes()
	var toBytes []byte
	if transaction.To != nil {
		to, err := ethrpc.ParseAddress(*transaction.To)
		if err != nil {
			return transactionValues{}, errors.New("invalid transaction recipient")
		}
		toBytes = to.Bytes()
	}
	input, err := ethrpc.ParseData(transaction.Input)
	if err != nil {
		return transactionValues{}, errors.New("invalid transaction input")
	}
	inputBytes := []byte(input)
	return transactionValues{hash: hashBytes, from: fromBytes, to: toBytes, input: inputBytes}, nil
}

func validateSnapshotForStorage(snapshot Snapshot) error {
	if snapshot.Endpoint == "" || len(snapshot.Endpoint) > 128 || snapshot.ObservedAt.IsZero() || !snapshot.ExpiresAt.After(snapshot.ObservedAt) {
		return errors.New("mempool snapshot metadata is invalid")
	}
	seen := make(map[string]struct{}, len(snapshot.Transactions))
	seenSlots := make(map[string]struct{}, len(snapshot.Transactions))
	for index, transaction := range snapshot.Transactions {
		values, err := transactionStorageValues(transaction)
		if err != nil {
			return fmt.Errorf("transaction %d: %w", index, err)
		}
		for _, quantity := range []string{transaction.Nonce, transaction.Value, transaction.Gas} {
			if !canonicalDecimal(quantity) {
				return fmt.Errorf("transaction %d has an invalid quantity", index)
			}
		}
		for _, quantity := range []*string{transaction.GasPrice, transaction.MaxFeePerGas, transaction.MaxPriorityFeePerGas, transaction.Type} {
			if quantity != nil && !canonicalDecimal(*quantity) {
				return fmt.Errorf("transaction %d has an invalid optional quantity", index)
			}
		}
		if !json.Valid(transaction.Raw) {
			return fmt.Errorf("transaction %d raw JSON is invalid", index)
		}
		key := strings.ToLower(transaction.Hash)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("transaction %d duplicates hash", index)
		}
		seen[key] = struct{}{}
		slot := string(values.from) + "\x00" + transaction.Nonce
		if _, duplicate := seenSlots[slot]; duplicate {
			return fmt.Errorf("transaction %d conflicts with another transaction in sender and nonce slot", index)
		}
		seenSlots[slot] = struct{}{}
	}
	return nil
}

func lockMempool(ctx context.Context, tx *sql.Tx, chain string) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('etherview:mempool:' || $1))`, chain); err != nil {
		return fmt.Errorf("lock mempool snapshot state: %w", err)
	}
	return nil
}

type pendingCursor struct {
	Version         int       `json:"v"`
	ChainID         string    `json:"chain_id"`
	SnapshotID      int64     `json:"snapshot_id"`
	BeforeFirstSeen time.Time `json:"before_first_seen"`
	BeforeHash      string    `json:"before_hash"`
}

func encodePendingCursor(cursor pendingCursor) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode mempool cursor: %w", err)
	}
	if len(encoded) > maximumCursorBytes {
		return "", ErrInvalidCursor
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodePendingCursor(encoded string) (pendingCursor, error) {
	if encoded == "" || len(encoded) > base64.RawURLEncoding.EncodedLen(maximumCursorBytes) {
		return pendingCursor{}, ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 || len(raw) > maximumCursorBytes {
		return pendingCursor{}, ErrInvalidCursor
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var cursor pendingCursor
	if err := decoder.Decode(&cursor); err != nil {
		return pendingCursor{}, ErrInvalidCursor
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return pendingCursor{}, ErrInvalidCursor
	}
	if cursor.Version != cursorVersion {
		return pendingCursor{}, ErrInvalidCursor
	}
	return cursor, nil
}

func fixedHash(value []byte) (string, error) {
	if len(value) != common.HashLength {
		return "", fmt.Errorf("%w: pending hash has %d bytes", ErrCorruptData, len(value))
	}
	return common.BytesToHash(value).Hex(), nil
}

func fixedAddress(value []byte) (string, error) {
	if len(value) != common.AddressLength {
		return "", fmt.Errorf("%w: pending address has %d bytes", ErrCorruptData, len(value))
	}
	return checksumAddress(common.BytesToAddress(value)), nil
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func equalOptionalString(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalOptionalNull(value *string, stored sql.NullString) bool {
	return value == nil && !stored.Valid || value != nil && stored.Valid && *value == stored.String
}
