//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/billing"
)

func TestCLIAdminBillingInspectionAndReconciliationUseWriter(t *testing.T) {
	t.Setenv("ETHERVIEW_FEATURE_USER_AUTH", "false")
	t.Setenv("ETHERVIEW_FEATURE_X402_BILLING", "false")

	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	if _, err := db.ExecContext(
		ctx, `INSERT INTO chains (chain_id) VALUES (1), (2)`,
	); err != nil {
		t.Fatalf("insert billing chains: %v", err)
	}

	ledger, err := billing.NewPostgresLedger(db, 1, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	settledPaymentID := createCLIUnknownBillingPayment(
		t, ctx, ledger, 0x11,
	)
	failedPaymentID := createCLIUnknownBillingPayment(
		t, ctx, ledger, 0x22,
	)
	otherLedger, err := billing.NewPostgresLedger(
		db, 2, 2*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	otherPaymentID := createCLIUnknownBillingPayment(
		t, ctx, otherLedger, 0x33,
	)

	var schema string
	if err := db.QueryRowContext(
		ctx, `SELECT current_schema()`,
	).Scan(&schema); err != nil {
		t.Fatalf("read integration schema: %v", err)
	}
	databaseURL := isolatedDatabaseURL(t, schema)
	configPath := filepath.Join(t.TempDir(), "etherview-billing-admin.yaml")
	configBody := fmt.Sprintf(
		"chain:\n  id: 1\ndatabase:\n  url: %s\n  read_url: %s\n"+
			"features:\n  user_auth: false\n  x402_billing: false\n",
		strconv.Quote(databaseURL),
		strconv.Quote("postgres://127.0.0.1:1/unreachable"),
	)
	if err := os.WriteFile(
		configPath, []byte(configBody), 0o600,
	); err != nil {
		t.Fatalf("write billing admin config: %v", err)
	}

	runner := newCLIRunner()
	code, stdout, stderr := runner.run(
		ctx, "admin", "billing", "inspect",
		"--id", settledPaymentID, "--config", configPath,
	)
	inspection := decodeCLIAdminBillingOutput(
		t, code, stdout, stderr,
	)
	if inspection.Status != "inspected" ||
		inspection.Outcome != nil ||
		inspection.Payment.ID != settledPaymentID ||
		inspection.Payment.State != "settling" ||
		inspection.Payment.FailureCode == nil ||
		*inspection.Payment.FailureCode != "settlement_unknown" ||
		len(inspection.Events) != 5 {
		t.Fatalf("inspection = %+v", inspection)
	}
	assertCLIAdminBillingEventsOrdered(t, inspection.Events)
	for _, forbidden := range []string{
		"fingerprint", "resource_digest", "requirement_digest",
		"facilitator_digest", "reservation_owner", databaseURL,
	} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("billing inspect output exposes %q: %s", forbidden, stdout)
		}
	}

	transactionHash := "0x" + strings.Repeat("44", 32)
	code, stdout, stderr = runner.run(
		ctx, "admin", "billing", "reconcile",
		"--id", settledPaymentID, "--outcome", "settled",
		"--transaction-hash", transactionHash, "--config", configPath,
	)
	reconciled := decodeCLIAdminBillingOutput(
		t, code, stdout, stderr,
	)
	if reconciled.Status != "reconciled" ||
		reconciled.Outcome == nil ||
		*reconciled.Outcome != "settled" ||
		reconciled.Payment.State != "settled" ||
		reconciled.Payment.TransactionHash == nil ||
		*reconciled.Payment.TransactionHash != transactionHash ||
		len(reconciled.Events) != 0 {
		t.Fatalf("settled reconciliation = %+v", reconciled)
	}
	code, stdout, stderr = runner.run(
		ctx, "admin", "billing", "inspect",
		"--id", settledPaymentID, "--config", configPath,
	)
	inspection = decodeCLIAdminBillingOutput(t, code, stdout, stderr)
	if len(inspection.Events) != 6 ||
		inspection.Events[5].Actor != "operator" {
		t.Fatalf("settled inspection = %+v", inspection)
	}
	assertCLIAdminBillingEventsOrdered(t, inspection.Events)

	code, stdout, stderr = runner.run(
		ctx, "admin", "billing", "reconcile",
		"--id", failedPaymentID, "--outcome", "failed",
		"--config", configPath,
	)
	reconciled = decodeCLIAdminBillingOutput(
		t, code, stdout, stderr,
	)
	if reconciled.Outcome == nil || *reconciled.Outcome != "failed" ||
		reconciled.Payment.State != "failed" ||
		reconciled.Payment.FailureCode == nil ||
		*reconciled.Payment.FailureCode != "operator_reconciled_failed" ||
		len(reconciled.Events) != 0 {
		t.Fatalf("failed reconciliation = %+v", reconciled)
	}

	code, stdout, stderr = runner.run(
		ctx, "admin", "billing", "inspect",
		"--id", otherPaymentID, "--config", configPath,
	)
	if code != 1 || stdout != "" ||
		stderr != "etherview: billing payment was not found\n" {
		t.Fatalf(
			"cross-chain inspect code=%d stdout=%q stderr=%q",
			code, stdout, stderr,
		)
	}
}

type cliAdminBillingOutput struct {
	Status  string  `json:"status"`
	Outcome *string `json:"outcome"`
	Payment struct {
		ID              string  `json:"id"`
		State           string  `json:"state"`
		FailureCode     *string `json:"failure_code"`
		TransactionHash *string `json:"transaction_hash"`
	} `json:"payment"`
	Events []struct {
		ID    string `json:"id"`
		Actor string `json:"actor"`
	} `json:"events"`
}

func decodeCLIAdminBillingOutput(
	t *testing.T,
	code int,
	stdout, stderr string,
) cliAdminBillingOutput {
	t.Helper()
	var output cliAdminBillingOutput
	if err := json.Unmarshal(
		[]byte(stdout), &output,
	); code != 0 || err != nil || stderr != "" {
		t.Fatalf(
			"admin billing output code=%d output=%+v stdout=%q stderr=%q error=%v",
			code, output, stdout, stderr, err,
		)
	}
	return output
}

func assertCLIAdminBillingEventsOrdered(
	t *testing.T,
	events []struct {
		ID    string `json:"id"`
		Actor string `json:"actor"`
	},
) {
	t.Helper()
	var previous int64
	for index, event := range events {
		identifier, err := strconv.ParseInt(event.ID, 10, 64)
		if err != nil || identifier <= previous {
			t.Fatalf(
				"event %d id=%q previous=%d error=%v",
				index, event.ID, previous, err,
			)
		}
		previous = identifier
	}
}

func createCLIUnknownBillingPayment(
	t *testing.T,
	ctx context.Context,
	ledger *billing.PostgresLedger,
	identity byte,
) string {
	t.Helper()
	// Keep every fixture transition safely before the operator command's
	// real-time reconciliation timestamp. A fixed wall-clock date can move
	// into the future for developers in other time zones and would correctly
	// trip the ledger's monotonic event-time fence.
	observedAt := time.Now().UTC().
		Add(-10 * time.Minute).
		Truncate(time.Microsecond).
		Add(time.Duration(identity) * time.Second)
	var (
		fingerprint       billing.Digest
		resourceDigest    billing.Digest
		requirementDigest billing.Digest
		facilitatorDigest billing.Digest
		asset             common.Address
		recipient         common.Address
		payer             common.Address
	)
	fingerprint[31] = identity
	resourceDigest[31] = identity + 1
	requirementDigest[31] = identity + 2
	facilitatorDigest[31] = identity + 3
	asset[19] = 0x11
	recipient[19] = 0x22
	payer[19] = 0x33
	reservation, err := ledger.Reserve(ctx, billing.ReserveInput{
		Fingerprint: fingerprint, Operation: "listBlocks",
		ResourceDigest: resourceDigest, RequirementDigest: requirementDigest,
		Network: "eip155:84532", Asset: asset, AmountAtomic: "1000",
		Recipient: recipient, FacilitatorDigest: facilitatorDigest,
		ObservedAt: observedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.MarkVerified(ctx, billing.VerifiedInput{
		PaymentID: reservation.Payment.ID, Owner: reservation.Owner,
		Payer: payer, ObservedAt: observedAt.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.StartHandler(
		ctx, reservation.Payment.ID, reservation.Owner,
		observedAt.Add(2*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.BeginSettlement(
		ctx, reservation.Payment.ID, reservation.Owner,
		observedAt.Add(3*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.MarkSettlementUnknown(
		ctx, reservation.Payment.ID, reservation.Owner,
		observedAt.Add(4*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	return reservation.Payment.ID
}
