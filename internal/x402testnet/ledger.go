package x402testnet

import (
	"context"
	"database/sql"
	"encoding/hex"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/apiops"
	"github.com/islishude/etherview/internal/billing"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
)

const ledgerReservationTTL = 2 * time.Minute

var maximumTestnetUint256 = new(big.Int).Sub(
	new(big.Int).Lsh(big.NewInt(1), 256),
	big.NewInt(1),
)

type LedgerOptions struct {
	WriterURL         string
	ChainID           uint64
	Operation         string
	ResourceDigest    [32]byte
	RequirementDigest [32]byte
	Network           string
	Asset             string
	AmountAtomic      string
	Recipient         string
	Payer             string
}

type LedgerEvidence struct {
	PaymentID  string
	UserID     *string
	CreatedAt  time.Time
	SettledAt  time.Time
	EventCount int
}

// LedgerVerifier owns only a small writer pool and an immutable timestamp
// fence. OpenLedger releases the connection used to obtain the fence before it
// returns, so the paid HTTP request never runs while a database transaction,
// row lock, or pinned connection is held.
type LedgerVerifier struct {
	store   ledgerVerificationStore
	options ledgerExpectation
	fence   time.Time
}

type ledgerExpectation struct {
	chainID           uint64
	operation         string
	resourceDigest    [32]byte
	requirementDigest [32]byte
	network           string
	asset             common.Address
	amountAtomic      string
	amount            pgtype.Numeric
	recipient         common.Address
	payer             common.Address
}

type ledgerVerificationStore interface {
	find(context.Context, ledgerExpectation, time.Time) ([]string, error)
	inspect(context.Context, string) (billing.Inspection, error)
	close() error
}

type postgresLedgerVerificationStore struct {
	database *sql.DB
	ledger   *billing.PostgresLedger
}

func OpenLedger(
	ctx context.Context,
	options LedgerOptions,
) (*LedgerVerifier, error) {
	expected, ok := parseLedgerExpectation(options)
	if !ok || !validWriterDatabaseURL(options.WriterURL) {
		return nil, boundaryError("ledger_configuration_invalid")
	}
	config, err := pgx.ParseConfig(options.WriterURL)
	if err != nil {
		return nil, boundaryError("ledger_configuration_invalid")
	}
	if config.RuntimeParams == nil {
		config.RuntimeParams = make(map[string]string)
	}
	config.RuntimeParams["application_name"] = "etherview-x402-testnet"
	config.RuntimeParams["default_transaction_read_only"] = "off"
	config.RuntimeParams["statement_timeout"] = "10000"
	config.ConnectTimeout = 10 * time.Second

	database := stdlib.OpenDB(*config)
	database.SetMaxOpenConns(2)
	database.SetMaxIdleConns(1)
	database.SetConnMaxIdleTime(time.Minute)
	database.SetConnMaxLifetime(5 * time.Minute)
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		return nil, boundaryError("ledger_unavailable")
	}
	if err := store.CheckSchema(ctx, database); err != nil {
		_ = database.Close()
		return nil, boundaryError("ledger_schema_invalid")
	}

	var writer dbgen.GetX402TestnetWriterFenceRow
	if err := dbaccess.WithQueries(
		ctx,
		database,
		func(queries *dbgen.Queries) error {
			var queryErr error
			writer, queryErr = queries.GetX402TestnetWriterFence(ctx)
			return queryErr
		},
	); err != nil {
		_ = database.Close()
		return nil, boundaryError("ledger_writer_check_failed")
	}
	fence, err := writerFence(
		writer.InRecovery,
		writer.TransactionReadOnly,
		writer.CreatedAtFence,
	)
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	ledger, err := billing.NewPostgresLedger(
		database,
		options.ChainID,
		ledgerReservationTTL,
	)
	if err != nil {
		_ = database.Close()
		return nil, boundaryError("ledger_configuration_invalid")
	}
	return &LedgerVerifier{
		store: &postgresLedgerVerificationStore{
			database: database,
			ledger:   ledger,
		},
		options: expected,
		fence:   fence,
	}, nil
}

func writerFence(
	inRecovery bool,
	transactionReadOnly string,
	createdAtFence pgtype.Timestamptz,
) (time.Time, error) {
	if inRecovery || transactionReadOnly != "off" {
		return time.Time{}, boundaryError("ledger_writer_required")
	}
	if !createdAtFence.Valid ||
		createdAtFence.InfinityModifier != pgtype.Finite ||
		createdAtFence.Time.IsZero() {
		return time.Time{}, boundaryError("ledger_writer_check_failed")
	}
	return createdAtFence.Time.UTC(), nil
}

func (verifier *LedgerVerifier) Verify(
	ctx context.Context,
	transactionHash string,
) (LedgerEvidence, error) {
	if verifier == nil || verifier.store == nil || verifier.fence.IsZero() {
		return LedgerEvidence{}, boundaryError("ledger_configuration_invalid")
	}
	expectedHash, ok := parseTransactionHash(transactionHash)
	if !ok {
		return LedgerEvidence{}, boundaryError("ledger_transaction_hash_invalid")
	}
	paymentIDs, err := verifier.store.find(ctx, verifier.options, verifier.fence)
	if err != nil {
		return LedgerEvidence{}, boundaryError("ledger_lookup_failed")
	}
	switch len(paymentIDs) {
	case 0:
		return LedgerEvidence{}, boundaryError("ledger_payment_not_found")
	case 1:
	default:
		return LedgerEvidence{}, boundaryError("ledger_payment_not_unique")
	}
	inspection, err := verifier.store.inspect(ctx, paymentIDs[0])
	if err != nil {
		return LedgerEvidence{}, boundaryError("ledger_inspection_failed")
	}
	if !matchesLedgerPayment(
		inspection.Payment,
		paymentIDs[0],
		verifier.options,
		verifier.fence,
		expectedHash,
	) {
		return LedgerEvidence{}, boundaryError("ledger_payment_mismatch")
	}
	if !matchesSettledEventChain(
		inspection.Events,
		paymentIDs[0],
		expectedHash,
	) {
		return LedgerEvidence{}, boundaryError("ledger_event_mismatch")
	}
	return LedgerEvidence{
		PaymentID:  inspection.Payment.ID,
		UserID:     cloneLedgerString(inspection.Payment.UserID),
		CreatedAt:  inspection.Payment.CreatedAt,
		SettledAt:  *inspection.Payment.SettledAt,
		EventCount: len(inspection.Events),
	}, nil
}

func (verifier *LedgerVerifier) Close() error {
	if verifier == nil || verifier.store == nil {
		return nil
	}
	if err := verifier.store.close(); err != nil {
		return boundaryError("ledger_close_failed")
	}
	return nil
}

func (store *postgresLedgerVerificationStore) find(
	ctx context.Context,
	expected ledgerExpectation,
	fence time.Time,
) ([]string, error) {
	var rows []pgtype.UUID
	err := dbaccess.WithQueries(
		ctx,
		store.database,
		func(queries *dbgen.Queries) error {
			var queryErr error
			rows, queryErr = queries.FindX402TestnetBillingPayments(
				ctx,
				dbgen.FindX402TestnetBillingPaymentsParams{
					ChainID: pgtype.Numeric{
						Int:   new(big.Int).SetUint64(expected.chainID),
						Valid: true,
					},
					Operation:         expected.operation,
					ResourceDigest:    expected.resourceDigest[:],
					RequirementDigest: expected.requirementDigest[:],
					Network:           expected.network,
					Asset:             expected.asset[:],
					AmountAtomic:      expected.amount,
					Recipient:         expected.recipient[:],
					Payer:             expected.payer[:],
					CreatedAtFence:    pgtype.Timestamptz{Time: fence, Valid: true},
				},
			)
			return queryErr
		},
	)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(rows))
	for _, row := range rows {
		if !row.Valid {
			return nil, billing.ErrIntegrity
		}
		result = append(result, uuid.UUID(row.Bytes).String())
	}
	return result, nil
}

func (store *postgresLedgerVerificationStore) inspect(
	ctx context.Context,
	paymentID string,
) (billing.Inspection, error) {
	return store.ledger.Inspect(ctx, paymentID)
}

func (store *postgresLedgerVerificationStore) close() error {
	return store.database.Close()
}

func parseLedgerExpectation(
	options LedgerOptions,
) (ledgerExpectation, bool) {
	var result ledgerExpectation
	operation, ok := apiops.Lookup(options.Operation)
	if !ok || !operation.BillingEligible || operation.Method != "GET" ||
		options.ChainID != baseSepoliaChainID ||
		options.Network != baseSepoliaNetwork ||
		zeroDigest(options.ResourceDigest) ||
		zeroDigest(options.RequirementDigest) {
		return result, false
	}
	asset, ok := parseExpectedAddress(options.Asset)
	if !ok {
		return result, false
	}
	recipient, ok := parseExpectedAddress(options.Recipient)
	if !ok {
		return result, false
	}
	payer, ok := parseExpectedAddress(options.Payer)
	if !ok {
		return result, false
	}
	amount, ok := parseAmount(options.AmountAtomic)
	if !ok {
		return result, false
	}
	return ledgerExpectation{
		chainID:           options.ChainID,
		operation:         options.Operation,
		resourceDigest:    options.ResourceDigest,
		requirementDigest: options.RequirementDigest,
		network:           options.Network,
		asset:             asset,
		amountAtomic:      options.AmountAtomic,
		amount:            pgtype.Numeric{Int: amount, Valid: true},
		recipient:         recipient,
		payer:             payer,
	}, true
}

func parseExpectedAddress(value string) (common.Address, bool) {
	// Operator expectations use their exact EIP-55 representation. RPC wire
	// addresses are parsed separately because Ethereum JSON-RPC DATA is
	// canonical lowercase hexadecimal.
	if _, ok := canonicalAddress(value); !ok {
		return common.Address{}, false
	}
	return parseAddress(value)
}

func parseAddress(value string) (common.Address, bool) {
	var result common.Address
	if len(value) != 42 || value[:2] != "0x" {
		return result, false
	}
	decoded, err := hex.DecodeString(value[2:])
	if err != nil || len(decoded) != len(result) {
		return result, false
	}
	copy(result[:], decoded)
	var zero common.Address
	return result, result != zero
}

func parseTransactionHash(value string) (common.Hash, bool) {
	// x402 settlement and JSON-RPC hashes share the canonical lowercase DATA
	// form; accepting another spelling would weaken exact reconciliation.
	var result common.Hash
	if len(value) != 66 || value[:2] != "0x" {
		return result, false
	}
	decoded, err := hex.DecodeString(value[2:])
	if err != nil || len(decoded) != len(result) ||
		hex.EncodeToString(decoded) != value[2:] {
		return result, false
	}
	copy(result[:], decoded)
	var zero common.Hash
	return result, result != zero
}

func parseAmount(value string) (*big.Int, bool) {
	amount, ok := new(big.Int).SetString(value, 10)
	if !ok || amount.Sign() <= 0 ||
		amount.Cmp(maximumTestnetUint256) > 0 ||
		amount.String() != value {
		return nil, false
	}
	return amount, true
}

func matchesLedgerPayment(
	payment billing.Payment,
	paymentID string,
	expected ledgerExpectation,
	fence time.Time,
	transactionHash common.Hash,
) bool {
	return payment.ID == paymentID &&
		payment.ChainID == expected.chainID &&
		payment.Operation == expected.operation &&
		payment.Method == "GET" &&
		payment.Network == expected.network &&
		payment.Asset == expected.asset &&
		payment.AmountAtomic == expected.amountAtomic &&
		payment.Recipient == expected.recipient &&
		payment.Payer != nil &&
		*payment.Payer == expected.payer &&
		payment.APIKeyPrefix == nil &&
		payment.FailureCode == nil &&
		!payment.CreatedAt.Before(fence) &&
		payment.State == billing.StateSettled &&
		payment.TransactionHash != nil &&
		*payment.TransactionHash == transactionHash &&
		payment.SettledAt != nil
}

func zeroDigest(value [32]byte) bool {
	var zero [32]byte
	return value == zero
}

func matchesSettledEventChain(
	events []billing.PaymentEvent,
	paymentID string,
	transactionHash common.Hash,
) bool {
	expected := [...]struct {
		from *billing.State
		to   billing.State
		code string
	}{
		{nil, billing.StateReserved, "payment_reserved"},
		{statePointer(billing.StateReserved), billing.StateVerified, "payment_verified"},
		{statePointer(billing.StateVerified), billing.StateVerified, "handler_started"},
		{statePointer(billing.StateVerified), billing.StateSettling, "settlement_started"},
		{statePointer(billing.StateSettling), billing.StateSettled, "payment_settled"},
	}
	if len(events) != len(expected) {
		return false
	}
	for index, event := range events {
		want := expected[index]
		if event.PaymentID != paymentID ||
			event.Actor != billing.ActorRuntime ||
			event.ToState != want.to ||
			event.Code != want.code ||
			!sameStatePointer(event.FromState, want.from) {
			return false
		}
		if index == len(events)-1 {
			if event.TransactionHash == nil ||
				*event.TransactionHash != transactionHash {
				return false
			}
		} else if event.TransactionHash != nil {
			return false
		}
	}
	return true
}

//go:fix inline
func statePointer(value billing.State) *billing.State {
	return new(value)
}

func sameStatePointer(left, right *billing.State) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func cloneLedgerString(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
