//go:build integration

package billing

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/x402settlement"
	x402 "github.com/x402-foundation/x402/go/v2"
)

func TestVerificationRetryLeaseAndExternalCredit(t *testing.T) {
	for _, scenario := range []struct{ background, failed bool }{{false, false}, {true, false}, {true, true}} {
		background := scenario.background
		t.Run(fmt.Sprintf("background=%t/failed=%t", background, scenario.failed), func(t *testing.T) {
			db := newBillingPostgres(t)
			user := uuid.NewString()
			payer := topupTestAddress(0x33)
			insertBillingUser(t, db, user, payer)
			accounts := newTestPrepaidLedger(t, db)
			payments, err := NewPostgresLedger(db, 11155111, 2*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			at := time.Now().UTC()
			intent, err := accounts.CreateTopupIntent(t.Context(), CreateTopupIntentInput{UserID: user, Payer: payer, AmountAtomic: "100", ObservedAt: at, PaymentOption: testPaymentOption("eip155:11155111", testReserveInput().Asset, testReserveInput().Recipient)})
			if err != nil {
				t.Fatal(err)
			}
			input := testReserveInput()
			input.ObservedAt = at
			input.Operation = "createBillingTopup"
			input.Method = http.MethodPost
			input.Purpose = "account_topup"
			input.AssetTransferMethod = "eip3009"
			input.PaymentFlow = "authorization"
			input.FingerprintVersion = 2
			input.TopupIntentID = &intent.ID
			input.UserID = &user
			input.ExpectedPayer = payer
			input.Network = intent.Network
			input.Asset = intent.Asset
			input.Recipient = intent.Recipient
			input.AmountAtomic = intent.AmountAtomic
			input.FacilitatorDigest = Digest{0x44}
			input.Reference = &x402settlement.Reference{AuthorizationID: "test-authorization", RequestDigest: hex.EncodeToString(input.Fingerprint[:])}
			first, err := payments.ReserveTopup(t.Context(), input)
			if err != nil || !first.Owned {
				t.Fatal(first, err)
			}
			if err := payments.ReleaseVerification(t.Context(), first.Payment.ID, first.Owner, at.Add(time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			input.ObservedAt = at.Add(2 * time.Millisecond)
			var wg sync.WaitGroup
			results := make(chan Reservation, 2)
			for range 2 {
				wg.Go(func() {
					r, e := payments.ReserveTopup(context.Background(), input)
					if e != nil {
						t.Error(e)
					}
					results <- r
				})
			}
			wg.Wait()
			close(results)
			var winner Reservation
			owned := 0
			for r := range results {
				if r.Owned {
					winner = r
					owned++
				}
			}
			if owned != 1 || winner.Payment.ID != first.Payment.ID || winner.Owner == first.Owner {
				t.Fatalf("retry did not acquire one new lease: %d %+v", owned, winner)
			}
			verified := VerifiedInput{PaymentID: first.Payment.ID, Owner: first.Owner, Payer: payer, UserID: &user, ObservedAt: at.Add(3 * time.Millisecond)}
			if _, e := payments.MarkVerified(t.Context(), verified); e == nil {
				t.Fatal("old verification owner published")
			}
			verified.Owner = winner.Owner
			if _, e := payments.MarkVerified(t.Context(), verified); e != nil {
				t.Fatal(e)
			}
			if e := accounts.BeginTopupSettlement(t.Context(), winner.Payment.ID, winner.Owner, intent.ID, at.Add(4*time.Millisecond)); e != nil {
				t.Fatal(e)
			}
			original := common.HexToHash("0x11")
			external := common.HexToHash("0x22")
			if scenario.failed {
				if e := accounts.MarkTopupSettlementUnknown(t.Context(), winner.Payment.ID, winner.Owner, intent.ID, at.Add(5*time.Millisecond)); e != nil {
					t.Fatal(e)
				}
			} else if e := accounts.MarkTopupSettlementPending(t.Context(), winner.Payment.ID, winner.Owner, intent.ID, original, at.Add(5*time.Millisecond)); e != nil {
				t.Fatal(e)
			}
			result := &x402settlement.Result{Reference: *input.Reference, State: "succeeded", OriginalTransaction: original.Hex(), Evidence: &x402settlement.Evidence{Transaction: external.Hex(), BlockHash: common.HexToHash("0x33").Hex(), BlockNumber: "1", LogIndex: "0", Source: "external", Network: intent.Network, Payer: payer.Hex(), Recipient: intent.Recipient.Hex(), Asset: intent.Asset.Hex(), Amount: "100"}, Response: &x402.SettleResponse{Success: true, Transaction: external.Hex(), Payer: payer.Hex(), Network: x402.Network(intent.Network), Amount: "100"}}
			if scenario.failed {
				result.State = "failed"
				result.TransactionConfirmed = true
				result.Evidence = nil
				result.Response = &x402.SettleResponse{Success: false, Transaction: original.Hex(), Network: x402.Network(intent.Network), ErrorReason: "settlement_rejected"}
			}
			if background {
				dispatcher := &TopupDispatcher{accounts: accounts, network: intent.Network, facilitator: &topupFacilitatorFake{result: result}}
				for range 2 {
					wg.Go(func() {
						if err := dispatcher.reconcileTopups(t.Context()); err != nil {
							t.Error(err)
						}
					})
				}
				wg.Wait()
			} else {
				successes := make(chan bool, 2)
				for range 2 {
					wg.Go(func() {
						_, e := accounts.CreditTopupResult(context.Background(), winner.Payment.ID, winner.Owner, result, [32]byte(input.FacilitatorDigest), at.Add(6*time.Millisecond))
						successes <- e == nil
					})
				}
				wg.Wait()
				close(successes)
				credited := 0
				for ok := range successes {
					if ok {
						credited++
					}
				}
				if credited != 1 {
					t.Fatal("credit count", credited)
				}

			}
			if scenario.failed {
				row, e := payments.Get(t.Context(), winner.Payment.ID)
				if e != nil || row.State != StateFailed || row.TransactionHash == nil || *row.TransactionHash != original {
					t.Fatal("failed result lost original identity", row, e)
				}
				final, e := accounts.TopupIntent(t.Context(), user, intent.ID)
				if e != nil || final.State != TopupIntentFailed || final.TransactionHash == nil || *final.TransactionHash != original {
					t.Fatal(final, e)
				}
				assertBillingAccountAmounts(t, accounts, user, "0", "0", "0")
				assertBillingEntryCount(t, db, "topup", winner.Payment.ID, 0)
				return
			}
			row, e := payments.Get(t.Context(), winner.Payment.ID)
			if e != nil || row.TransactionHash == nil || *row.TransactionHash != original || row.SettlementTransactionHash == nil || *row.SettlementTransactionHash != external {
				t.Fatal(row, e)
			}
			final, e := accounts.TopupIntent(t.Context(), user, intent.ID)
			if e != nil || final.State != TopupIntentCredited || final.SettlementTransactionHash == nil || *final.SettlementTransactionHash != external {
				t.Fatal(final, e)
			}
			assertBillingAccountAmounts(t, accounts, user, "100", "0", "100")
			assertBillingEntryCount(t, db, "topup", winner.Payment.ID, 1)
			retry, e := payments.ReserveTopup(t.Context(), input)
			if e != nil || retry.Owned {
				t.Fatal("settled authorization retried", retry, e)
			}
		})
	}
}
