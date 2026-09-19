package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"

	"github.com/ethereum/go-ethereum/common"
	dbgen "github.com/islishude/etherview/internal/db/gen"
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
	Observer  OutboxObserver
}

type OutboxObserver interface {
	RecordEnrichmentOutbox(OutboxTransition)
}

type OutboxTransition struct {
	Component    string
	ID           int64
	ChainID      string
	Topic        string
	BlockHash    common.Hash
	BlockNumber  uint64
	Attempt      int64
	Generation   int64
	Result       string
	Code         string
	RetryAfter   time.Duration
	Duration     time.Duration
	JobsCreated  int
	JobsExisting int
	Stages       []string
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
	Transition *OutboxTransition
}

type OutboxDispatcher struct {
	db       dbaccess.Database
	enqueuer JobEnqueuer
	options  OutboxDispatcherOptions
}

func NewOutboxDispatcher(db dbaccess.Database, enqueuer JobEnqueuer, options OutboxDispatcherOptions) (*OutboxDispatcher, error) {
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
		} else {
			if result.Transition != nil && dispatcher.options.Observer != nil {
				dispatcher.options.Observer.RecordEnrichmentOutbox(*result.Transition)
			}
			if result.State == OutboxPublished && dispatcher.options.Published != nil {
				dispatcher.options.Published()
			}
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
	BlockHash   common.Hash
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
	tx, err := dispatcher.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return OutboxDispatchResult{}, fmt.Errorf("begin enrichment outbox dispatch: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
	message, found, err := claimOutboxMessage(ctx, tx)
	if err != nil {
		return OutboxDispatchResult{}, err
	}
	if !found {
		return OutboxDispatchResult{State: OutboxIdle}, nil
	}
	startedAt := time.Now()
	decodedMessage := message
	_ = decodedMessage.decode()
	if err := dbgen.New(tx).EnrichSavepointDispatchJobs(ctx); err != nil {
		return OutboxDispatchResult{}, fmt.Errorf("create enrichment dispatch savepoint: %w", err)
	}
	audit, processErr := dispatcher.processMessage(ctx, tx, message)
	if processErr != nil {
		if err := dbgen.New(tx).EnrichRollbackDispatchJobs(ctx); err != nil {
			return OutboxDispatchResult{}, fmt.Errorf("rollback partial enrichment dispatch: %w", err)
		}
		if err := dbgen.New(tx).EnrichReleaseDispatchJobs(ctx); err != nil {
			return OutboxDispatchResult{}, fmt.Errorf("release failed enrichment dispatch savepoint: %w", err)
		}
		delay := dispatcher.retryDelay(message.Attempts)
		microseconds, _ := durationMicroseconds(delay)
		reason := truncateOutboxError(processErr.Error())
		result, updateErr := dbgen.New(tx).EnrichLegacyRetryOutbox(ctx, new(reason), microseconds, message.ID)
		if updateErr != nil {
			return OutboxDispatchResult{}, fmt.Errorf("record enrichment outbox retry: %w", updateErr)
		}
		if err := requireSingleUpdate(result, "retry enrichment outbox message"); err != nil {
			return OutboxDispatchResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return OutboxDispatchResult{}, fmt.Errorf("commit enrichment outbox retry: %w", err)
		}
		return OutboxDispatchResult{
			State: OutboxRetry, Topic: message.Topic, MessageKey: message.MessageKey, LastError: reason,
			Transition: &OutboxTransition{
				Component: dispatcher.Name(), ID: message.ID, ChainID: message.ChainID,
				Topic: message.Topic, BlockHash: decodedMessage.BlockHash, BlockNumber: decodedMessage.BlockNumber,
				Attempt: message.Attempts + 1, Generation: message.Generation,
				Result: "retry", Code: "dispatch_failed", RetryAfter: delay, Duration: time.Since(startedAt),
			},
		}, nil
	}
	if err := dbgen.New(tx).EnrichReleaseDispatchJobs(ctx); err != nil {
		return OutboxDispatchResult{}, fmt.Errorf("release enrichment dispatch savepoint: %w", err)
	}
	encodedAudit, err := json.Marshal(audit)
	if err != nil {
		return OutboxDispatchResult{}, fmt.Errorf("encode enrichment outbox audit: %w", err)
	}
	result, err := dbgen.New(tx).EnrichLegacyPublishOutbox(ctx, []byte(string(encodedAudit)), message.ID)
	if err != nil {
		return OutboxDispatchResult{}, fmt.Errorf("publish enrichment outbox message: %w", err)
	}
	if err := requireSingleUpdate(result, "publish enrichment outbox message"); err != nil {
		return OutboxDispatchResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OutboxDispatchResult{}, fmt.Errorf("commit enrichment outbox dispatch: %w", err)
	}
	return OutboxDispatchResult{
		State: OutboxPublished, Topic: message.Topic, MessageKey: message.MessageKey,
		Transition: &OutboxTransition{
			Component: dispatcher.Name(), ID: message.ID, ChainID: message.ChainID,
			Topic: message.Topic, BlockHash: decodedMessage.BlockHash, BlockNumber: decodedMessage.BlockNumber,
			Attempt: message.Attempts, Generation: message.Generation,
			Result: audit.Outcome, Duration: time.Since(startedAt),
			JobsCreated: audit.JobsCreated, JobsExisting: audit.JobsExisting,
			Stages: append([]string(nil), audit.Stages...),
		},
	}, nil
}

func claimOutboxMessage(ctx context.Context, tx pgx.Tx) (outboxMessage, bool, error) {
	var message outboxMessage
	var payload []byte
	err := func() error {

		queryRow, err := dbgen.New(tx).EnrichLegacyClaimOutbox(ctx)
		if err != nil {
			return err
		}
		message.ID = queryRow.ID
		message.ChainID = queryRow.ChainID
		message.Topic = queryRow.Topic
		message.MessageKey = queryRow.MessageKey
		payload = queryRow.Payload
		message.Attempts = int64(queryRow.Attempts)
		message.Generation = queryRow.Generation
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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

func (dispatcher *OutboxDispatcher) processMessage(ctx context.Context, tx pgx.Tx, message outboxMessage) (dispatchAudit, error) {
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

func (dispatcher *OutboxDispatcher) processCanonical(ctx context.Context, tx pgx.Tx, message outboxMessage) (dispatchAudit, error) {
	var canonical bool
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(message.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(message.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).EnrichLegacyCanonicalBlock(ctx, queryValue0, queryValue1, message.BlockHash[:])
		if err != nil {
			return err
		}
		canonical = queryRow
		return nil
	}(); err != nil {
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

func (dispatcher *OutboxDispatcher) enqueue(ctx context.Context, tx pgx.Tx, request EnqueueRequest) (EnqueueResult, error) {
	if queue, ok := dispatcher.enqueuer.(*PostgresJobQueue); ok && queue.db == dispatcher.db {
		return queue.enqueueTx(ctx, tx, request)
	}
	return dispatcher.enqueuer.Enqueue(ctx, request)
}

func (*OutboxDispatcher) processOrphan(ctx context.Context, tx pgx.Tx, message outboxMessage) (dispatchAudit, error) {
	var canonical bool
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(message.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(message.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).EnrichLegacyCanonicalBlock(ctx, queryValue0, queryValue1, message.BlockHash[:])
		if err != nil {
			return err
		}
		canonical = queryRow
		return nil
	}(); err != nil {
		return dispatchAudit{}, fmt.Errorf("check orphan outbox block: %w", err)
	}
	if canonical {
		return dispatchAudit{Outcome: "stale_orphan_skipped", Replayed: false}, nil
	}
	var journalsNonCanonical bool
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(message.ChainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).EnrichLegacyOrphanJournals(ctx, queryValue0, message.BlockHash[:])
		if err != nil {
			return err
		}
		journalsNonCanonical = queryRow
		return nil
	}(); err != nil {
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

func requireSingleUpdate(result int64, operation string) error {
	affected := result
	if affected != 1 {
		return fmt.Errorf("%s: affected %d rows, want 1", operation, affected)
	}
	return nil
}
