//go:build integration

package billing

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/testdbguard"
)

func TestPrepaidConcurrentCreditAndUsageCommit(t *testing.T) {
	db := newBillingPostgres(t)
	user := uuid.NewString()
	payer := common.HexToAddress("0x00000000000000000000000000000000000000a9")
	insertBillingUser(t, db, user, payer)
	ledger := newTestPrepaidLedger(t, db)
	base := testReserveInput().ObservedAt
	if _, err := ledger.EnsureAccount(t.Context(), user, base); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Adjust(t.Context(), AdjustmentInput{UserID: user, Direction: "credit", AmountAtomic: "100", Reason: "concurrency fixture", ObservedAt: base}); err != nil {
		t.Fatal(err)
	}
	insertPrepaidAPIKey(t, db, "cccccccccc", user, base)
	operations := map[string]func(context.Context) error{}
	for i := byte(1); i <= 4; i++ {
		var resource Digest
		resource[31] = i
		reservation, err := ledger.ReserveUsage(t.Context(), ReserveUsageInput{UserID: user, APIKeyPrefix: "cccccccccc", Method: http.MethodGet, Operation: "etherview.account.balance", Resource: resource, AmountAtomic: "10", ObservedAt: base.Add(time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		operations[fmt.Sprintf("commit-%d", i)] = func(ctx context.Context) error {
			_, err := ledger.CommitUsage(ctx, CommitUsageInput{ChargeID: reservation.Charge.ID, Owner: reservation.Owner, Response: resource, ResponseBytes: 1, ObservedAt: base.Add(2 * time.Second)})
			return err
		}
	}
	operations["credit"] = func(ctx context.Context) error {
		_, err := ledger.Adjust(ctx, AdjustmentInput{UserID: user, Direction: "credit", AmountAtomic: "25", Reason: "concurrent credit", ObservedAt: base.Add(2 * time.Second)})
		return err
	}
	if err := testdbguard.Concurrent(t.Context(), operations); err != nil {
		t.Fatal(err)
	}
	assertBillingAccountAmounts(t, ledger, user, "125", "40", "85")
}
