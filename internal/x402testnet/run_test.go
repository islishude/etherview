package x402testnet

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/billing/x402wire"
	x402 "github.com/x402-foundation/x402/go/v2"
)

func TestRunOrdersPreflightWriterRPCPaymentAndReconciliation(t *testing.T) {
	t.Parallel()
	var calls []string
	expectedPayment := validPaymentEvidence()
	ledger := &fakeRunLedger{
		verify: func(_ context.Context, transactionHash string) (LedgerEvidence, error) {
			calls = append(calls, "ledger-verify")
			if transactionHash != runTestTransactionHash {
				t.Fatalf("ledger transaction hash = %q", transactionHash)
			}
			return LedgerEvidence{PaymentID: "10000000-0000-4000-8000-000000000001"}, nil
		},
		close: func() error {
			calls = append(calls, "ledger-close")
			return nil
		},
	}
	completedAt := time.Date(2026, 7, 26, 4, 5, 6, 0, time.UTC)
	report, err := run(context.Background(), validRunConfig(), runDependencies{
		checkServer: func(_ context.Context, options PreflightOptions) error {
			calls = append(calls, "server")
			if options.ExpectedOperation != "listBlocks" ||
				options.ExpectedLedgerChainID != baseSepoliaChainID {
				t.Fatalf("preflight options = %#v", options)
			}
			return nil
		},
		openLedger: func(
			_ context.Context,
			options LedgerOptions,
		) (ledgerSession, error) {
			calls = append(calls, "ledger-open")
			if options.WriterURL != "postgres://private-writer" ||
				options.Network != baseSepoliaNetwork ||
				options.ResourceDigest != expectedPayment.ResourceDigest ||
				options.RequirementDigest !=
					expectedPayment.RequirementDigest {
				t.Fatalf("ledger options = %#v", options)
			}
			return ledger, nil
		},
		checkChain: func(_ context.Context, rpcURL string, chainID uint64) error {
			calls = append(calls, "chain-check")
			if rpcURL != "https://private-rpc.invalid/key" ||
				chainID != baseSepoliaChainID {
				t.Fatalf("chain check = %q %d", rpcURL, chainID)
			}
			return nil
		},
		executePayment: func(
			_ context.Context,
			options HTTPOptions,
		) (PaymentEvidence, error) {
			calls = append(calls, "payment")
			if options.TargetURL == "" || options.MaxFinalBodyBytes != 8<<20 ||
				string(options.PrivateKey) != "private-key" {
				t.Fatalf("payment options = %#v", options)
			}
			return expectedPayment, nil
		},
		verifyChain: func(
			_ context.Context,
			options ChainOptions,
		) (ChainEvidence, error) {
			calls = append(calls, "chain-verify")
			if options.TransactionHash != runTestTransactionHash ||
				options.CallDataPrefixBytes != expectedSettlementCallBytes ||
				zeroDigest(options.CallDataPrefixSHA256) {
				t.Fatalf("chain options = %#v", options)
			}
			return ChainEvidence{
				TransactionHash: runTestTransactionHash,
				BlockHash:       "0x" + strings.Repeat("b", 64),
				BlockNumber:     "123456",
				TransferCount:   1,
			}, nil
		},
		now: func() time.Time { return completedAt },
	})
	if err != nil {
		t.Fatalf("run(): %v", err)
	}
	wantCalls := []string{
		"server", "ledger-open", "chain-check", "payment",
		"ledger-verify", "chain-verify", "ledger-close",
	}
	if !slices.Equal(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	if report.SDKVersion != SDKVersion ||
		report.HarnessRevision != validRunConfig().Revision ||
		report.PaymentID != "10000000-0000-4000-8000-000000000001" ||
		report.TransactionHash != runTestTransactionHash ||
		!report.CompletedAt.Equal(completedAt) {
		t.Fatalf("report = %#v", report)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{
		"private-key", "private-writer", "private-rpc", "Payment-Signature",
	} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("report leaked %q: %s", secret, encoded)
		}
	}
}

func TestRunRefusesToSignBeforeRPCPreflight(t *testing.T) {
	t.Parallel()
	ledger := &fakeRunLedger{}
	paymentCalled := false
	_, err := run(context.Background(), validRunConfig(), runDependencies{
		checkServer: func(context.Context, PreflightOptions) error { return nil },
		openLedger: func(context.Context, LedgerOptions) (ledgerSession, error) {
			return ledger, nil
		},
		checkChain: func(context.Context, string, uint64) error {
			return boundaryError("rpc_chain_mismatch")
		},
		executePayment: func(context.Context, HTTPOptions) (PaymentEvidence, error) {
			paymentCalled = true
			return PaymentEvidence{}, nil
		},
		verifyChain: func(context.Context, ChainOptions) (ChainEvidence, error) {
			t.Fatal("chain verification ran")
			return ChainEvidence{}, nil
		},
		now: time.Now,
	})
	if got := ErrorCode(err); got != "rpc_chain_mismatch" {
		t.Fatalf("ErrorCode() = %q", got)
	}
	if paymentCalled {
		t.Fatal("payment signer ran before RPC preflight succeeded")
	}
	if !ledger.closed {
		t.Fatal("ledger was not closed")
	}
}

func TestRunPreservesUnknownAuthorizationBoundaryWithoutReconciliation(t *testing.T) {
	t.Parallel()
	ledger := &fakeRunLedger{
		verify: func(context.Context, string) (LedgerEvidence, error) {
			t.Fatal("ledger verification ran after unknown payment")
			return LedgerEvidence{}, nil
		},
	}
	chainVerifyCalled := false
	_, err := run(context.Background(), validRunConfig(), runDependencies{
		checkServer: func(context.Context, PreflightOptions) error { return nil },
		openLedger: func(context.Context, LedgerOptions) (ledgerSession, error) {
			return ledger, nil
		},
		checkChain: func(context.Context, string, uint64) error { return nil },
		executePayment: func(context.Context, HTTPOptions) (PaymentEvidence, error) {
			return PaymentEvidence{},
				boundaryError("outcome_unknown_after_authorization")
		},
		verifyChain: func(context.Context, ChainOptions) (ChainEvidence, error) {
			chainVerifyCalled = true
			return ChainEvidence{}, nil
		},
		now: time.Now,
	})
	if got := ErrorCode(err); got != "outcome_unknown_after_authorization" {
		t.Fatalf("ErrorCode() = %q", got)
	}
	if chainVerifyCalled {
		t.Fatal("chain verification ran after unknown payment")
	}
	if !ledger.closed {
		t.Fatal("ledger was not closed")
	}
}

func TestRunFailsClosedOnEvidenceDrift(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*PaymentEvidence)
	}{
		{
			name: "amount",
			mutate: func(payment *PaymentEvidence) {
				payment.AmountAtomic = "2"
			},
		},
		{
			name: "resource digest",
			mutate: func(payment *PaymentEvidence) {
				payment.ResourceDigest[0] ^= 0xff
			},
		},
		{
			name: "requirement digest",
			mutate: func(payment *PaymentEvidence) {
				payment.RequirementDigest[0] ^= 0xff
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ledger := &fakeRunLedger{}
			payment := validPaymentEvidence()
			test.mutate(&payment)
			_, err := run(context.Background(), validRunConfig(), runDependencies{
				checkServer: func(context.Context, PreflightOptions) error { return nil },
				openLedger: func(context.Context, LedgerOptions) (ledgerSession, error) {
					return ledger, nil
				},
				checkChain: func(context.Context, string, uint64) error { return nil },
				executePayment: func(context.Context, HTTPOptions) (PaymentEvidence, error) {
					return payment, nil
				},
				verifyChain: func(context.Context, ChainOptions) (ChainEvidence, error) {
					return ChainEvidence{}, errors.New("must not run")
				},
				now: time.Now,
			})
			if got := ErrorCode(err); got != codePaidReconciliationIncomplete {
				t.Fatalf("ErrorCode() = %q", got)
			}
			if !ledger.closed {
				t.Fatal("ledger was not closed")
			}
		})
	}
}

func TestRunMarksEveryPostPaymentFailureAsUnsafeToRetry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		ledger      *fakeRunLedger
		verifyChain func(context.Context, ChainOptions) (ChainEvidence, error)
		now         func() time.Time
	}{
		{
			name: "ledger unavailable",
			ledger: &fakeRunLedger{
				verify: func(context.Context, string) (LedgerEvidence, error) {
					return LedgerEvidence{}, errors.New("hostile writer detail")
				},
			},
			verifyChain: func(context.Context, ChainOptions) (ChainEvidence, error) {
				t.Fatal("chain verification ran after ledger failure")
				return ChainEvidence{}, nil
			},
			now: time.Now,
		},
		{
			name: "chain panic",
			ledger: &fakeRunLedger{
				verify: func(context.Context, string) (LedgerEvidence, error) {
					return LedgerEvidence{
						PaymentID: "10000000-0000-4000-8000-000000000001",
					}, nil
				},
			},
			verifyChain: func(context.Context, ChainOptions) (ChainEvidence, error) {
				panic("hostile RPC body")
			},
			now: time.Now,
		},
		{
			name: "chain unavailable",
			ledger: &fakeRunLedger{
				verify: func(context.Context, string) (LedgerEvidence, error) {
					return LedgerEvidence{
						PaymentID: "10000000-0000-4000-8000-000000000001",
					}, nil
				},
			},
			verifyChain: func(context.Context, ChainOptions) (ChainEvidence, error) {
				return ChainEvidence{}, boundaryError("chain_unavailable")
			},
			now: time.Now,
		},
		{
			name: "report clock panic",
			ledger: &fakeRunLedger{
				verify: func(context.Context, string) (LedgerEvidence, error) {
					return LedgerEvidence{
						PaymentID: "10000000-0000-4000-8000-000000000001",
					}, nil
				},
			},
			verifyChain: func(context.Context, ChainOptions) (ChainEvidence, error) {
				return ChainEvidence{
					TransactionHash: runTestTransactionHash,
					BlockHash:       "0x" + strings.Repeat("b", 64),
					BlockNumber:     "123456",
					TransferCount:   1,
				}, nil
			},
			now: func() time.Time {
				panic("hostile clock detail")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := run(
				context.Background(),
				validRunConfig(),
				runDependencies{
					checkServer: func(
						context.Context,
						PreflightOptions,
					) error {
						return nil
					},
					openLedger: func(
						context.Context,
						LedgerOptions,
					) (ledgerSession, error) {
						return test.ledger, nil
					},
					checkChain: func(
						context.Context,
						string,
						uint64,
					) error {
						return nil
					},
					executePayment: func(
						context.Context,
						HTTPOptions,
					) (PaymentEvidence, error) {
						return validPaymentEvidence(), nil
					},
					verifyChain: test.verifyChain,
					now:         test.now,
				},
			)
			if got := ErrorCode(err); got != codePaidReconciliationIncomplete {
				t.Fatalf(
					"ErrorCode() = %q, want %q",
					got,
					codePaidReconciliationIncomplete,
				)
			}
			if strings.Contains(err.Error(), "hostile") {
				t.Fatalf("post-payment error leaked hostile detail: %q", err)
			}
			if !test.ledger.closed {
				t.Fatal("ledger was not closed")
			}
		})
	}
}

type fakeRunLedger struct {
	verify func(context.Context, string) (LedgerEvidence, error)
	close  func() error
	closed bool
}

func (ledger *fakeRunLedger) Verify(
	ctx context.Context,
	transactionHash string,
) (LedgerEvidence, error) {
	if ledger.verify == nil {
		return LedgerEvidence{}, errors.New("unexpected ledger verification")
	}
	return ledger.verify(ctx, transactionHash)
}

func (ledger *fakeRunLedger) Close() error {
	ledger.closed = true
	if ledger.close != nil {
		return ledger.close()
	}
	return nil
}

const runTestTransactionHash = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func validPaymentEvidence() PaymentEvidence {
	requirement, err := x402wire.NewRequirement(x402wire.RequirementOptions{
		Network: baseSepoliaNetwork, Asset: runTestAsset, Amount: "1",
		PayTo: runTestRecipient, MaxTimeoutSeconds: 60,
		AssetEIP712Name: "Test USD", AssetEIP712Version: "2",
		AssetTransferMethod: x402wire.TransferMethodEIP3009,
		PaymentFlow:         x402wire.PaymentFlowAuthorization,
		Resource: x402.ResourceInfo{
			URL:         "https://explorer.example/api/v1/blocks?limit=1",
			MimeType:    testnetResourceMimeType,
			ServiceName: testnetResourceService,
		},
	})
	if err != nil {
		panic(err)
	}
	body := []byte(httpTestNativeBody)
	callDataDigest := sha256.Sum256([]byte("settlement-call-prefix"))
	return PaymentEvidence{
		StatusCode: http.StatusOK, Payer: runTestPayer,
		Network: baseSepoliaNetwork, Asset: runTestAsset,
		AmountAtomic: "1", Recipient: runTestRecipient,
		TransactionHash:      runTestTransactionHash,
		ResourceDigest:       requirement.ResourceDigest(),
		RequirementDigest:    requirement.RequirementDigest(),
		CallDataPrefixBytes:  expectedSettlementCallBytes,
		CallDataPrefixSHA256: callDataDigest,
		FinalBodyBytes:       int64(len(body)),
		FinalBodySHA256:      sha256.Sum256(body),
	}
}

func validRunConfig() Config {
	return Config{
		Revision: "revision-test", TargetURL: "https://explorer.example/api/v1/blocks?limit=1",
		ExpectedResourceURL: "https://explorer.example/api/v1/blocks?limit=1",
		ExpectedOperation:   "listBlocks", ExpectedAccess: "x402",
		ExpectedAsset: runTestAsset, ExpectedAssetDecimals: 6,
		ExpectedAssetEIP712Name: "Test USD", ExpectedAssetEIP712Version: "2",
		ExpectedAmountAtomic: "1", ExpectedRecipient: runTestRecipient,
		ExpectedPayer: runTestPayer, ExpectedMaxTimeoutSeconds: 60,
		LedgerChainID: baseSepoliaChainID, PrivateKey: []byte("private-key"),
		RPCURL:            "https://private-rpc.invalid/key",
		WriterDatabaseURL: "postgres://private-writer",
	}
}

const (
	runTestAsset     = "0x1111111111111111111111111111111111111111"
	runTestRecipient = "0x2222222222222222222222222222222222222222"
	runTestPayer     = "0x3333333333333333333333333333333333333333"
)
