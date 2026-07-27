//go:build integration

package x402testnet

import (
	"context"
	"database/sql"
	"encoding/hex"
	"math/big"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/billing"
	"github.com/islishude/etherview/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
)

const x402TestnetDatabaseEnvironment = "ETHERVIEW_TEST_DATABASE_URL"

func TestPostgresLedgerVerifierFenceAndFullBinding(t *testing.T) {
	database, writerURL, schema := newX402TestnetPostgres(t)
	ledger, err := billing.NewPostgresLedger(
		database,
		baseSepoliaChainID,
		ledgerReservationTTL,
	)
	if err != nil {
		t.Fatal("create billing ledger")
	}

	resourceDigest := x402TestnetDigest(0x21)
	requirementDigest := x402TestnetDigest(0x41)
	options := LedgerOptions{
		WriterURL:         writerURL,
		ChainID:           baseSepoliaChainID,
		Operation:         "listBlocks",
		ResourceDigest:    [32]byte(resourceDigest),
		RequirementDigest: [32]byte(requirementDigest),
		Network:           baseSepoliaNetwork,
		Asset:             testAsset,
		AmountAtomic:      "1000",
		Recipient:         testRecipient,
		Payer:             testPayer,
	}

	// A matching settled payment from before this invocation must not be
	// accepted as evidence for the current one-shot gate.
	historicalHash := x402TestnetTransactionHash(0x11)
	historical := writeX402TestnetSettledPayment(
		t,
		ledger,
		options,
		0x11,
		time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		historicalHash,
		nil,
	)

	verifier, err := OpenLedger(t.Context(), options)
	if err != nil {
		t.Fatalf("open ledger: %s", ErrorCode(err))
	}
	t.Cleanup(func() {
		if err := verifier.Close(); err != nil {
			t.Errorf("close ledger: %s", ErrorCode(err))
		}
	})
	if verifier.fence.IsZero() || !historical.CreatedAt.Before(verifier.fence) {
		t.Fatal("writer fence did not exclude historical payment")
	}
	assertX402TestnetWriterConnection(t, verifier, schema)
	assertX402TestnetReadOnlyFenceRejected(t, database)

	// An API-key-attributed row with every other binding equal is not an
	// accountless x402 result and therefore must not make the locator
	// ambiguous.
	apiKeyPrefix := insertX402TestnetAPIKey(t, database, verifier.fence)
	writeX402TestnetSettledPayment(
		t,
		ledger,
		options,
		0x22,
		verifier.fence.Add(time.Second),
		x402TestnetTransactionHash(0x22),
		&apiKeyPrefix,
	)

	targetHash := x402TestnetTransactionHash(0x33)
	target := writeX402TestnetSettledPayment(
		t,
		ledger,
		options,
		0x33,
		verifier.fence.Add(10*time.Second),
		targetHash,
		nil,
	)
	evidence, err := verifier.Verify(
		t.Context(),
		x402TestnetTransactionHashString(targetHash),
	)
	if err != nil {
		t.Fatalf("verify unique payment: %s", ErrorCode(err))
	}
	if evidence.PaymentID != target.ID ||
		evidence.UserID != nil ||
		evidence.EventCount != 5 ||
		!evidence.CreatedAt.Equal(target.CreatedAt) ||
		target.SettledAt == nil ||
		!evidence.SettledAt.Equal(*target.SettledAt) {
		t.Fatalf("unexpected ledger evidence: %+v", evidence)
	}

	assertX402TestnetLocatorBindings(
		t,
		verifier,
		x402TestnetTransactionHashString(targetHash),
	)

	// A second accountless payment with the same complete locator binding is
	// ambiguous even when its transaction and fingerprint are different.
	writeX402TestnetSettledPayment(
		t,
		ledger,
		options,
		0x44,
		verifier.fence.Add(20*time.Second),
		x402TestnetTransactionHash(0x44),
		nil,
	)
	if _, err := verifier.Verify(
		t.Context(),
		x402TestnetTransactionHashString(targetHash),
	); ErrorCode(err) != "ledger_payment_not_unique" {
		t.Fatalf("duplicate locator code = %q", ErrorCode(err))
	}
}

func assertX402TestnetLocatorBindings(
	t *testing.T,
	verifier *LedgerVerifier,
	transactionHash string,
) {
	t.Helper()
	tests := []struct {
		name   string
		mutate func(*ledgerExpectation)
	}{
		{
			name: "chain ID",
			mutate: func(expected *ledgerExpectation) {
				expected.chainID++
			},
		},
		{
			name: "operation",
			mutate: func(expected *ledgerExpectation) {
				expected.operation = "getBlock"
			},
		},
		{
			name: "resource digest",
			mutate: func(expected *ledgerExpectation) {
				expected.resourceDigest[0] ^= 0xff
			},
		},
		{
			name: "requirement digest",
			mutate: func(expected *ledgerExpectation) {
				expected.requirementDigest[0] ^= 0xff
			},
		},
		{
			name: "network",
			mutate: func(expected *ledgerExpectation) {
				expected.network = "eip155:8453"
			},
		},
		{
			name: "asset",
			mutate: func(expected *ledgerExpectation) {
				expected.asset[0] ^= 0xff
			},
		},
		{
			name: "amount",
			mutate: func(expected *ledgerExpectation) {
				expected.amountAtomic = "1001"
				expected.amount = pgtype.Numeric{
					Int: big.NewInt(1001), Valid: true,
				}
			},
		},
		{
			name: "recipient",
			mutate: func(expected *ledgerExpectation) {
				expected.recipient[0] ^= 0xff
			},
		},
		{
			name: "payer",
			mutate: func(expected *ledgerExpectation) {
				expected.payer[0] ^= 0xff
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mismatched := *verifier
			test.mutate(&mismatched.options)
			if _, err := mismatched.Verify(
				t.Context(),
				transactionHash,
			); ErrorCode(err) != "ledger_payment_not_found" {
				t.Fatalf("mismatched locator code = %q", ErrorCode(err))
			}
		})
	}
}

func writeX402TestnetSettledPayment(
	t *testing.T,
	ledger *billing.PostgresLedger,
	options LedgerOptions,
	identity byte,
	observedAt time.Time,
	transactionHash common.Hash,
	apiKeyPrefix *string,
) billing.Payment {
	t.Helper()
	asset, ok := parseAddress(options.Asset)
	if !ok {
		t.Fatal("parse test asset")
	}
	recipient, ok := parseAddress(options.Recipient)
	if !ok {
		t.Fatal("parse test recipient")
	}
	payer, ok := parseAddress(options.Payer)
	if !ok {
		t.Fatal("parse test payer")
	}
	reservation, err := ledger.Reserve(t.Context(), billing.ReserveInput{
		Fingerprint:       x402TestnetDigest(identity),
		Operation:         options.Operation,
		ResourceDigest:    billing.Digest(options.ResourceDigest),
		RequirementDigest: billing.Digest(options.RequirementDigest),
		Network:           options.Network,
		Asset:             asset,
		AmountAtomic:      options.AmountAtomic,
		Recipient:         recipient,
		APIKeyPrefix:      apiKeyPrefix,
		FacilitatorDigest: x402TestnetDigest(identity + 0x80),
		ObservedAt:        observedAt,
	})
	if err != nil || !reservation.Owned || reservation.Owner == "" {
		t.Fatal("reserve testnet payment")
	}
	verified, err := ledger.MarkVerified(t.Context(), billing.VerifiedInput{
		PaymentID:    reservation.Payment.ID,
		Owner:        reservation.Owner,
		Payer:        payer,
		APIKeyPrefix: apiKeyPrefix,
		ObservedAt:   observedAt.Add(time.Second),
	})
	if err != nil || verified.State != billing.StateVerified {
		t.Fatal("verify testnet payment")
	}
	handled, err := ledger.StartHandler(
		t.Context(),
		reservation.Payment.ID,
		reservation.Owner,
		observedAt.Add(2*time.Second),
	)
	if err != nil || handled.State != billing.StateVerified ||
		handled.HandlerStartedAt == nil {
		t.Fatal("start testnet handler")
	}
	settling, err := ledger.BeginSettlement(
		t.Context(),
		reservation.Payment.ID,
		reservation.Owner,
		observedAt.Add(3*time.Second),
	)
	if err != nil || settling.State != billing.StateSettling {
		t.Fatal("begin testnet settlement")
	}
	settled, err := ledger.MarkSettled(
		t.Context(),
		reservation.Payment.ID,
		reservation.Owner,
		transactionHash,
		observedAt.Add(4*time.Second),
	)
	if err != nil || settled.State != billing.StateSettled {
		t.Fatal("settle testnet payment")
	}
	inspection, err := ledger.Inspect(t.Context(), settled.ID)
	if err != nil || len(inspection.Events) != 5 {
		t.Fatal("inspect five-event settlement")
	}
	return inspection.Payment
}

func assertX402TestnetWriterConnection(
	t *testing.T,
	verifier *LedgerVerifier,
	wantSchema string,
) {
	t.Helper()
	postgresStore, ok := verifier.store.(*postgresLedgerVerificationStore)
	if !ok {
		t.Fatal("ledger verifier did not use PostgreSQL writer store")
	}
	var (
		inRecovery bool
		readOnly   string
		schema     string
	)
	if err := postgresStore.database.QueryRowContext(
		t.Context(),
		`SELECT pg_is_in_recovery(),
		        current_setting('transaction_read_only'),
		        current_schema()`,
	).Scan(&inRecovery, &readOnly, &schema); err != nil {
		t.Fatal("inspect writer connection")
	}
	if inRecovery || readOnly != "off" || schema != wantSchema {
		t.Fatalf(
			"unexpected writer state: recovery=%t read_only=%q schema_match=%t",
			inRecovery,
			readOnly,
			schema == wantSchema,
		)
	}
}

func assertX402TestnetReadOnlyFenceRejected(
	t *testing.T,
	database *sql.DB,
) {
	t.Helper()
	transaction, err := database.BeginTx(
		t.Context(),
		&sql.TxOptions{ReadOnly: true},
	)
	if err != nil {
		t.Fatal("begin read-only transaction")
	}
	defer transaction.Rollback() //nolint:errcheck
	var (
		inRecovery bool
		readOnly   string
		fence      time.Time
	)
	if err := transaction.QueryRowContext(
		t.Context(),
		`SELECT pg_is_in_recovery(),
		        current_setting('transaction_read_only'),
		        clock_timestamp()`,
	).Scan(&inRecovery, &readOnly, &fence); err != nil {
		t.Fatal("inspect read-only transaction")
	}
	if readOnly != "on" {
		t.Fatalf("PostgreSQL read-only transaction reported %q", readOnly)
	}
	if _, err := writerFence(
		inRecovery,
		readOnly,
		pgtype.Timestamptz{Time: fence, Valid: true},
	); ErrorCode(err) != "ledger_writer_required" {
		t.Fatalf("read-only writer fence code = %q", ErrorCode(err))
	}
}

func insertX402TestnetAPIKey(
	t *testing.T,
	database *sql.DB,
	createdAt time.Time,
) string {
	t.Helper()
	const prefix = "aaaaaaaaaa"
	digest := x402TestnetDigest(0xa1)
	if _, err := database.ExecContext(
		t.Context(),
		`INSERT INTO api_keys (
		    prefix, digest, name, rate_per_second, burst, created_at
		) VALUES ($1, $2, 'x402-testnet-integration', 1, 1, $3)`,
		prefix,
		digest[:],
		createdAt,
	); err != nil {
		t.Fatal("insert API key fixture")
	}
	return prefix
}

func x402TestnetDigest(identity byte) billing.Digest {
	var digest billing.Digest
	for index := range digest {
		digest[index] = identity + byte(index)
	}
	return digest
}

func x402TestnetTransactionHash(identity byte) common.Hash {
	var transactionHash common.Hash
	transactionHash[0] = 0xa5
	transactionHash[len(transactionHash)-1] = identity
	return transactionHash
}

func x402TestnetTransactionHashString(
	transactionHash common.Hash,
) string {
	return "0x" + hex.EncodeToString(transactionHash[:])
}

func newX402TestnetPostgres(
	t *testing.T,
) (*sql.DB, string, string) {
	t.Helper()
	rawURL := strings.TrimSpace(os.Getenv(x402TestnetDatabaseEnvironment))
	if rawURL == "" {
		t.Skipf("%s is not set", x402TestnetDatabaseEnvironment)
	}
	adminConfig, err := pgx.ParseConfig(rawURL)
	if err != nil {
		t.Fatalf("parse %s", x402TestnetDatabaseEnvironment)
	}
	adminConfig.RuntimeParams = cloneX402TestnetRuntimeParams(
		adminConfig.RuntimeParams,
	)
	adminConfig.RuntimeParams["application_name"] = "etherview-x402-testnet-admin"
	adminDatabase := stdlib.OpenDB(*adminConfig)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := adminDatabase.PingContext(ctx); err != nil {
		_ = adminDatabase.Close()
		t.Fatalf("connect to %s", x402TestnetDatabaseEnvironment)
	}

	schema := "etherview_x402_testnet_it_" +
		strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminDatabase.ExecContext(
		ctx,
		`CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize(),
	); err != nil {
		_ = adminDatabase.Close()
		t.Fatal("create isolated schema")
	}
	writerURL := x402TestnetWriterURL(t, rawURL, schema)
	testConfig, err := pgx.ParseConfig(writerURL)
	if err != nil {
		t.Fatal("parse isolated writer URL")
	}
	testConfig.RuntimeParams = cloneX402TestnetRuntimeParams(
		testConfig.RuntimeParams,
	)
	testConfig.RuntimeParams["application_name"] = "etherview-x402-testnet-it"
	database := stdlib.OpenDB(*testConfig)
	database.SetMaxOpenConns(6)
	database.SetMaxIdleConns(3)
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		_, _ = adminDatabase.ExecContext(
			ctx,
			`DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`,
		)
		_ = adminDatabase.Close()
		t.Fatal("connect isolated schema")
	}
	t.Cleanup(func() {
		_ = database.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(),
			15*time.Second,
		)
		defer cleanupCancel()
		_, _ = adminDatabase.ExecContext(
			cleanupCtx,
			`DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`,
		)
		_ = adminDatabase.Close()
	})
	if err := store.RunMigrations(ctx, database); err != nil {
		t.Fatal("run isolated migrations")
	}
	if _, err := database.ExecContext(
		ctx,
		`INSERT INTO chains (chain_id) VALUES ($1::numeric)`,
		baseSepoliaChainID,
	); err != nil {
		t.Fatal("insert Base Sepolia chain")
	}
	return database, writerURL, schema
}

func x402TestnetWriterURL(
	t *testing.T,
	rawURL string,
	schema string,
) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil ||
		(parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatalf("%s must be a PostgreSQL URL", x402TestnetDatabaseEnvironment)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func cloneX402TestnetRuntimeParams(
	source map[string]string,
) map[string]string {
	cloned := make(map[string]string, len(source)+2)
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
