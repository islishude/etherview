package enrich

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	CoreBlockCanonical = "core.block.canonical"
	CoreBlockOrphaned  = "core.block.orphaned"
)

type OutboxDispatcherOptions struct {
	ServiceName    string
	Stages         []StageID
	PollInterval   time.Duration
	RetryBase      time.Duration
	RetryMax       time.Duration
	JobPriority    int
	JobMaxAttempts uint32
	// Wake is a lossy latency hint. Published is invoked only after the
	// PostgreSQL transaction commits and must not block.
	Wake      <-chan struct{}
	Published func()
}

func (options *OutboxDispatcherOptions) defaults() {
	if options.ServiceName == "" {
		options.ServiceName = "enrichment-outbox-dispatcher"
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 500 * time.Millisecond
	}
	if options.RetryBase <= 0 {
		options.RetryBase = time.Second
	}
	if options.RetryMax <= 0 {
		options.RetryMax = 5 * time.Minute
	}
	if options.JobMaxAttempts == 0 {
		options.JobMaxAttempts = DefaultEnrichmentMaxAttempts
	}
}

type OutboxDispatchState string

const (
	OutboxIdle      OutboxDispatchState = "idle"
	OutboxPublished OutboxDispatchState = "published"
	OutboxRetry     OutboxDispatchState = "retry"
)

type OutboxDispatchResult struct {
	State      OutboxDispatchState
	Topic      string
	MessageKey string
	LastError  string
}

type OutboxDispatcher struct {
	db       *sql.DB
	enqueuer JobEnqueuer
	options  OutboxDispatcherOptions
}

func NewOutboxDispatcher(db *sql.DB, enqueuer JobEnqueuer, options OutboxDispatcherOptions) (*OutboxDispatcher, error) {
	if db == nil {
		return nil, errors.New("outbox dispatcher requires a database")
	}
	if enqueuer == nil {
		return nil, errors.New("outbox dispatcher requires a job enqueuer")
	}
	options.defaults()
	if strings.TrimSpace(options.ServiceName) == "" {
		return nil, errors.New("outbox dispatcher service name is empty")
	}
	if options.RetryMax < options.RetryBase {
		return nil, errors.New("outbox maximum retry delay is less than base delay")
	}
	if len(options.Stages) == 0 {
		return nil, errors.New("outbox dispatcher requires at least one enrichment stage")
	}
	seen := make(map[string]struct{}, len(options.Stages))
	stages := make([]StageID, 0, len(options.Stages))
	for _, stage := range options.Stages {
		if err := validateDatabaseStage(stage); err != nil {
			return nil, err
		}
		if _, exists := seen[stage.String()]; exists {
			continue
		}
		seen[stage.String()] = struct{}{}
		stages = append(stages, stage)
	}
	options.Stages = stages
	if int64(options.JobPriority) < minPostgresInteger || int64(options.JobPriority) > maxPostgresInteger {
		return nil, errors.New("outbox job priority exceeds PostgreSQL INTEGER")
	}
	if int64(options.JobMaxAttempts) > maxPostgresInteger {
		return nil, errors.New("outbox job max attempts exceeds PostgreSQL INTEGER")
	}
	if _, err := durationMicroseconds(options.PollInterval); err != nil {
		return nil, fmt.Errorf("outbox poll interval: %w", err)
	}
	if _, err := durationMicroseconds(options.RetryBase); err != nil {
		return nil, fmt.Errorf("outbox retry base: %w", err)
	}
	if _, err := durationMicroseconds(options.RetryMax); err != nil {
		return nil, fmt.Errorf("outbox retry maximum: %w", err)
	}
	return &OutboxDispatcher{db: db, enqueuer: enqueuer, options: options}, nil
}

func (dispatcher *OutboxDispatcher) Name() string {
	if dispatcher == nil {
		return "enrichment-outbox-dispatcher"
	}
	return dispatcher.options.ServiceName
}

func (dispatcher *OutboxDispatcher) Run(ctx context.Context) error {
	if dispatcher == nil || dispatcher.db == nil || dispatcher.enqueuer == nil {
		return errors.New("run nil outbox dispatcher")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := dispatcher.DispatchOne(ctx)
		if err != nil {
			return err
		}
		if result.State == OutboxIdle {
			if err := waitContextOrWake(ctx, dispatcher.options.PollInterval, dispatcher.options.Wake); err != nil {
				return err
			}
		} else if result.State == OutboxPublished && dispatcher.options.Published != nil {
			dispatcher.options.Published()
		}
	}
}

type outboxMessage struct {
	ID          int64
	ChainID     string
	Topic       string
	MessageKey  string
	Payload     json.RawMessage
	Attempts    int64
	Generation  int64
	BlockHash   Word
	BlockNumber uint64
}

type coreOutboxPayload struct {
	BlockHash   string `json:"block_hash"`
	BlockNumber string `json:"block_number"`
}

type dispatchAudit struct {
	Outcome           string   `json:"outcome"`
	JobsCreated       int      `json:"jobs_created"`
	JobsExisting      int      `json:"jobs_existing"`
	Stages            []string `json:"stages,omitempty"`
	Replayed          bool     `json:"replayed"`
	JournalsCanonical *bool    `json:"journals_canonical,omitempty"`
}

// DispatchOne locks at most one core outbox row. Enqueue and publish are in the
// same transaction when using PostgresJobQueue on the same database. With a
// generic enqueuer, crash recovery remains safe because enqueue is idempotent
// and published_at is written only after every stage succeeds.
func (dispatcher *OutboxDispatcher) DispatchOne(ctx context.Context) (OutboxDispatchResult, error) {
	if dispatcher == nil || dispatcher.db == nil || dispatcher.enqueuer == nil {
		return OutboxDispatchResult{}, errors.New("dispatch using nil outbox dispatcher")
	}
	tx, err := dispatcher.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return OutboxDispatchResult{}, fmt.Errorf("begin enrichment outbox dispatch: %w", err)
	}
	defer tx.Rollback()
	message, found, err := claimOutboxMessage(ctx, tx)
	if err != nil {
		return OutboxDispatchResult{}, err
	}
	if !found {
		return OutboxDispatchResult{State: OutboxIdle}, nil
	}
	if _, err := tx.ExecContext(ctx, "SAVEPOINT enrichment_dispatch_jobs"); err != nil {
		return OutboxDispatchResult{}, fmt.Errorf("create enrichment dispatch savepoint: %w", err)
	}
	audit, processErr := dispatcher.processMessage(ctx, tx, message)
	if processErr != nil {
		if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT enrichment_dispatch_jobs"); err != nil {
			return OutboxDispatchResult{}, fmt.Errorf("rollback partial enrichment dispatch: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT enrichment_dispatch_jobs"); err != nil {
			return OutboxDispatchResult{}, fmt.Errorf("release failed enrichment dispatch savepoint: %w", err)
		}
		delay := dispatcher.retryDelay(message.Attempts)
		microseconds, _ := durationMicroseconds(delay)
		reason := truncateOutboxError(processErr.Error())
		result, updateErr := tx.ExecContext(ctx, retryOutboxSQL, message.ID, reason, microseconds)
		if updateErr != nil {
			return OutboxDispatchResult{}, fmt.Errorf("record enrichment outbox retry: %w", updateErr)
		}
		if err := requireSingleUpdate(result, "retry enrichment outbox message"); err != nil {
			return OutboxDispatchResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return OutboxDispatchResult{}, fmt.Errorf("commit enrichment outbox retry: %w", err)
		}
		return OutboxDispatchResult{
			State: OutboxRetry, Topic: message.Topic, MessageKey: message.MessageKey, LastError: reason,
		}, nil
	}
	if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT enrichment_dispatch_jobs"); err != nil {
		return OutboxDispatchResult{}, fmt.Errorf("release enrichment dispatch savepoint: %w", err)
	}
	encodedAudit, err := json.Marshal(audit)
	if err != nil {
		return OutboxDispatchResult{}, fmt.Errorf("encode enrichment outbox audit: %w", err)
	}
	result, err := tx.ExecContext(ctx, publishOutboxSQL, message.ID, string(encodedAudit))
	if err != nil {
		return OutboxDispatchResult{}, fmt.Errorf("publish enrichment outbox message: %w", err)
	}
	if err := requireSingleUpdate(result, "publish enrichment outbox message"); err != nil {
		return OutboxDispatchResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return OutboxDispatchResult{}, fmt.Errorf("commit enrichment outbox dispatch: %w", err)
	}
	return OutboxDispatchResult{State: OutboxPublished, Topic: message.Topic, MessageKey: message.MessageKey}, nil
}

func claimOutboxMessage(ctx context.Context, tx *sql.Tx) (outboxMessage, bool, error) {
	var message outboxMessage
	var payload []byte
	err := tx.QueryRowContext(ctx, claimOutboxSQL).Scan(
		&message.ID,
		&message.ChainID,
		&message.Topic,
		&message.MessageKey,
		&payload,
		&message.Attempts,
		&message.Generation,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return outboxMessage{}, false, nil
	}
	if err != nil {
		return outboxMessage{}, false, fmt.Errorf("claim enrichment outbox message: %w", err)
	}
	message.Payload = append(json.RawMessage(nil), payload...)
	return message, true, nil
}

func (message *outboxMessage) decode() error {
	if message.ID <= 0 || message.Attempts < 0 || message.Attempts > maxPostgresInteger || message.Generation <= 0 {
		return errors.New("outbox identity or attempts are out of range")
	}
	if err := validateChainID(message.ChainID); err != nil {
		return err
	}
	if message.Topic != CoreBlockCanonical && message.Topic != CoreBlockOrphaned {
		return fmt.Errorf("unsupported core outbox topic %q", message.Topic)
	}
	var payload coreOutboxPayload
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		return fmt.Errorf("decode core outbox payload: %w", err)
	}
	hash, err := ParseWord(payload.BlockHash)
	if err != nil {
		return fmt.Errorf("decode core outbox block hash: %w", err)
	}
	number, err := strconv.ParseUint(payload.BlockNumber, 10, 64)
	if err != nil || strconv.FormatUint(number, 10) != payload.BlockNumber {
		return errors.New("decode core outbox block number: not a canonical uint64")
	}
	if !strings.EqualFold(message.MessageKey, hash.String()) {
		return errors.New("core outbox message key does not match block hash")
	}
	message.BlockHash = hash
	message.BlockNumber = number
	return nil
}

func (dispatcher *OutboxDispatcher) processMessage(ctx context.Context, tx *sql.Tx, message outboxMessage) (dispatchAudit, error) {
	if err := message.decode(); err != nil {
		return dispatchAudit{}, err
	}
	switch message.Topic {
	case CoreBlockCanonical:
		return dispatcher.processCanonical(ctx, tx, message)
	case CoreBlockOrphaned:
		return dispatcher.processOrphan(ctx, tx, message)
	default:
		return dispatchAudit{}, fmt.Errorf("unsupported core outbox topic %q", message.Topic)
	}
}

func (dispatcher *OutboxDispatcher) processCanonical(ctx context.Context, tx *sql.Tx, message outboxMessage) (dispatchAudit, error) {
	var canonical bool
	if err := tx.QueryRowContext(
		ctx,
		canonicalBlockSQL,
		message.ChainID,
		strconv.FormatUint(message.BlockNumber, 10),
		message.BlockHash[:],
	).Scan(&canonical); err != nil {
		return dispatchAudit{}, fmt.Errorf("check canonical outbox block: %w", err)
	}
	if !canonical {
		return dispatchAudit{Outcome: "stale_canonical_skipped", Replayed: false}, nil
	}
	audit := dispatchAudit{Outcome: "enrichment_enqueued", Replayed: true}
	for _, stage := range dispatcher.options.Stages {
		request := EnqueueRequest{
			Stage: stage, ChainID: message.ChainID, BlockHash: message.BlockHash, BlockNumber: message.BlockNumber,
			Payload: append(json.RawMessage(nil), message.Payload...), Priority: dispatcher.options.JobPriority,
			MaxAttempts: dispatcher.options.JobMaxAttempts,
			Replay: ReplaySource{
				Kind: "canonical-attach",
				Key:  fmt.Sprintf("%d:%d", message.ID, message.Generation),
			},
		}
		result, err := dispatcher.enqueue(ctx, tx, request)
		if err != nil {
			return dispatchAudit{}, fmt.Errorf("enqueue stage %s for block %s: %w", stage, message.BlockHash, err)
		}
		audit.Stages = append(audit.Stages, stage.String())
		if result.Created {
			audit.JobsCreated++
		} else {
			audit.JobsExisting++
		}
	}
	return audit, nil
}

func (dispatcher *OutboxDispatcher) enqueue(ctx context.Context, tx *sql.Tx, request EnqueueRequest) (EnqueueResult, error) {
	if queue, ok := dispatcher.enqueuer.(*PostgresJobQueue); ok && queue.db == dispatcher.db {
		return queue.enqueueTx(ctx, tx, request)
	}
	return dispatcher.enqueuer.Enqueue(ctx, request)
}

func (*OutboxDispatcher) processOrphan(ctx context.Context, tx *sql.Tx, message outboxMessage) (dispatchAudit, error) {
	var canonical bool
	if err := tx.QueryRowContext(
		ctx,
		canonicalBlockSQL,
		message.ChainID,
		strconv.FormatUint(message.BlockNumber, 10),
		message.BlockHash[:],
	).Scan(&canonical); err != nil {
		return dispatchAudit{}, fmt.Errorf("check orphan outbox block: %w", err)
	}
	if canonical {
		return dispatchAudit{Outcome: "stale_orphan_skipped", Replayed: false}, nil
	}
	var journalsNonCanonical bool
	if err := tx.QueryRowContext(ctx, orphanJournalsSQL, message.ChainID, message.BlockHash[:]).Scan(&journalsNonCanonical); err != nil {
		return dispatchAudit{}, fmt.Errorf("check orphaned block journals: %w", err)
	}
	if !journalsNonCanonical {
		return dispatchAudit{}, errors.New("orphaned block still has canonical journal entries")
	}
	journalsCanonical := false
	return dispatchAudit{
		Outcome: "orphan_acknowledged", JobsCreated: 0, JobsExisting: 0,
		Replayed: false, JournalsCanonical: &journalsCanonical,
	}, nil
}

func (dispatcher *OutboxDispatcher) retryDelay(previousAttempts int64) time.Duration {
	delay := dispatcher.options.RetryBase
	for attempt := int64(0); attempt < previousAttempts && delay < dispatcher.options.RetryMax; attempt++ {
		if delay > dispatcher.options.RetryMax/2 {
			return dispatcher.options.RetryMax
		}
		delay *= 2
	}
	if delay > dispatcher.options.RetryMax {
		return dispatcher.options.RetryMax
	}
	return delay
}

func truncateOutboxError(value string) string {
	const maximum = 4096
	value = strings.ToValidUTF8(value, "�")
	if len(value) <= maximum {
		return value
	}
	end := maximum
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

func requireSingleUpdate(result sql.Result, operation string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: read affected rows: %w", operation, err)
	}
	if affected != 1 {
		return fmt.Errorf("%s: affected %d rows, want 1", operation, affected)
	}
	return nil
}

const claimOutboxSQL = `
SELECT id, chain_id::text, topic, message_key, payload, attempts, generation
FROM transactional_outbox
WHERE published_at IS NULL
  AND available_at <= clock_timestamp()
  AND topic IN ('core.block.canonical', 'core.block.orphaned')
ORDER BY available_at, id
FOR UPDATE SKIP LOCKED
LIMIT 1`

const canonicalBlockSQL = `
SELECT EXISTS (
    SELECT 1
    FROM canonical_blocks
    WHERE chain_id = $1::numeric
      AND number = $2::numeric
      AND block_hash = $3
)`

const orphanJournalsSQL = `
SELECT NOT EXISTS (
    SELECT 1
    FROM block_journals
    WHERE chain_id = $1::numeric
      AND block_hash = $2
      AND canonical
)`

const publishOutboxSQL = `
UPDATE transactional_outbox
SET published_at = clock_timestamp(),
    last_error = NULL,
    payload = jsonb_set(payload, '{_etherview_dispatch}', $2::jsonb, true)
WHERE id = $1 AND published_at IS NULL`

const retryOutboxSQL = `
UPDATE transactional_outbox
SET attempts = LEAST(attempts + 1, 2147483647),
    last_error = $2,
    available_at = clock_timestamp() + ($3 * INTERVAL '1 microsecond')
WHERE id = $1 AND published_at IS NULL`
