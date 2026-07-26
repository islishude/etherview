package x402testnet

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"time"

	"github.com/islishude/etherview/internal/apiops"
	"github.com/islishude/etherview/internal/billing/x402wire"
	x402 "github.com/x402-foundation/x402/go/v2"
)

const (
	testnetRequestTimeout = 30 * time.Second
	maxChallengeBodyBytes = int64(64 << 10)
)

type ledgerSession interface {
	Verify(context.Context, string) (LedgerEvidence, error)
	Close() error
}

type runDependencies struct {
	checkServer    func(context.Context, PreflightOptions) error
	openLedger     func(context.Context, LedgerOptions) (ledgerSession, error)
	checkChain     func(context.Context, string, uint64) error
	executePayment func(context.Context, HTTPOptions) (PaymentEvidence, error)
	verifyChain    func(context.Context, ChainOptions) (ChainEvidence, error)
	now            func() time.Time
}

func productionRunDependencies() runDependencies {
	return runDependencies{
		checkServer: CheckServer,
		openLedger: func(
			ctx context.Context,
			options LedgerOptions,
		) (ledgerSession, error) {
			return OpenLedger(ctx, options)
		},
		checkChain:     CheckChain,
		executePayment: ExecutePayment,
		verifyChain:    VerifyChain,
		now:            time.Now,
	}
}

// Run executes one non-repeatable, real-payment Base Sepolia conformance pass.
// Callers must never retry an error: ExecutePayment reports a stable unknown
// boundary once an authorization may have left the process.
func Run(ctx context.Context, cfg Config) (Report, error) {
	return run(ctx, cfg, productionRunDependencies())
}

func run(
	ctx context.Context,
	cfg Config,
	dependencies runDependencies,
) (report Report, resultErr error) {
	paymentSucceeded := false
	defer func() {
		if recover() == nil {
			return
		}
		report = Report{}
		if paymentSucceeded {
			resultErr = boundaryError(codePaidReconciliationIncomplete)
			return
		}
		resultErr = boundaryError(CodeFailed)
	}()
	if dependencies.checkServer == nil || dependencies.openLedger == nil ||
		dependencies.checkChain == nil || dependencies.executePayment == nil ||
		dependencies.verifyChain == nil || dependencies.now == nil {
		return Report{}, boundaryError("testnet_dependencies_invalid")
	}
	spec, ok := apiops.Lookup(cfg.ExpectedOperation)
	if !ok || !spec.BillingEligible || spec.Method != "GET" ||
		spec.MaxResponseBytes <= 0 {
		return Report{}, boundaryError("testnet_configuration_invalid")
	}
	expectedRequirement, err := x402wire.NewRequirement(
		x402wire.RequirementOptions{
			Network:            baseSepoliaNetwork,
			Asset:              cfg.ExpectedAsset,
			Amount:             cfg.ExpectedAmountAtomic,
			PayTo:              cfg.ExpectedRecipient,
			MaxTimeoutSeconds:  int(cfg.ExpectedMaxTimeoutSeconds),
			AssetEIP712Name:    cfg.ExpectedAssetEIP712Name,
			AssetEIP712Version: cfg.ExpectedAssetEIP712Version,
			Resource: x402.ResourceInfo{
				URL:         cfg.ExpectedResourceURL,
				MimeType:    testnetResourceMimeType,
				ServiceName: testnetResourceService,
			},
		},
	)
	if err != nil {
		return Report{}, boundaryError("testnet_configuration_invalid")
	}
	resourceDigest := expectedRequirement.ResourceDigest()
	requirementDigest := expectedRequirement.RequirementDigest()
	preflight := PreflightOptions{
		TargetURL: cfg.TargetURL, ExpectedOperation: cfg.ExpectedOperation,
		ExpectedAccess: cfg.ExpectedAccess, ExpectedAsset: cfg.ExpectedAsset,
		ExpectedAssetDecimals: int(cfg.ExpectedAssetDecimals),
		ExpectedAssetName:     cfg.ExpectedAssetEIP712Name,
		ExpectedAssetVersion:  cfg.ExpectedAssetEIP712Version,
		ExpectedAmountAtomic:  cfg.ExpectedAmountAtomic,
		ExpectedRecipient:     cfg.ExpectedRecipient,
		ExpectedLedgerChainID: cfg.LedgerChainID,
	}
	if err := dependencies.checkServer(ctx, preflight); err != nil {
		return Report{}, err
	}

	ledger, err := dependencies.openLedger(ctx, LedgerOptions{
		WriterURL: cfg.WriterDatabaseURL, ChainID: cfg.LedgerChainID,
		Operation: cfg.ExpectedOperation, Network: baseSepoliaNetwork,
		Asset: cfg.ExpectedAsset, AmountAtomic: cfg.ExpectedAmountAtomic,
		Recipient:         cfg.ExpectedRecipient,
		Payer:             cfg.ExpectedPayer,
		ResourceDigest:    resourceDigest,
		RequirementDigest: requirementDigest,
	})
	if err != nil {
		return Report{}, err
	}
	defer ledger.Close() //nolint:errcheck

	// Refuse to create an authorization until the independently configured
	// reconciliation RPC confirms Base Sepolia.
	if err := dependencies.checkChain(
		ctx, cfg.RPCURL, cfg.LedgerChainID,
	); err != nil {
		return Report{}, err
	}
	payment, err := dependencies.executePayment(ctx, HTTPOptions{
		TargetURL: cfg.TargetURL, ExpectedResourceURL: cfg.ExpectedResourceURL,
		Network: baseSepoliaNetwork, Asset: cfg.ExpectedAsset,
		AmountAtomic: cfg.ExpectedAmountAtomic,
		Recipient:    cfg.ExpectedRecipient, ExpectedPayer: cfg.ExpectedPayer,
		PrivateKey:            cfg.PrivateKey,
		AssetEIP712Name:       cfg.ExpectedAssetEIP712Name,
		AssetEIP712Version:    cfg.ExpectedAssetEIP712Version,
		MaxTimeoutSeconds:     int(cfg.ExpectedMaxTimeoutSeconds),
		Timeout:               testnetRequestTimeout,
		MaxPaymentHeaderBytes: x402wire.DefaultMaxHeaderBytes,
		MaxChallengeBodyBytes: maxChallengeBodyBytes,
		MaxFinalBodyBytes:     spec.MaxResponseBytes,
	})
	if err != nil {
		return Report{}, err
	}
	paymentSucceeded = true
	if payment.StatusCode < 200 || payment.StatusCode >= 300 ||
		!sameText(payment.Payer, cfg.ExpectedPayer) ||
		payment.Network != baseSepoliaNetwork ||
		!sameText(payment.Asset, cfg.ExpectedAsset) ||
		payment.AmountAtomic != cfg.ExpectedAmountAtomic ||
		!sameText(payment.Recipient, cfg.ExpectedRecipient) ||
		payment.ResourceDigest != resourceDigest ||
		payment.RequirementDigest != requirementDigest ||
		payment.CallDataPrefixBytes != expectedSettlementCallBytes ||
		zeroDigest(payment.CallDataPrefixSHA256) ||
		payment.FinalBodyBytes <= 0 ||
		zeroDigest(payment.FinalBodySHA256) ||
		payment.TransactionHash == "" {
		return Report{}, boundaryError(codePaidReconciliationIncomplete)
	}

	ledgerEvidence, err := ledger.Verify(ctx, payment.TransactionHash)
	if err != nil {
		return Report{}, boundaryError(codePaidReconciliationIncomplete)
	}
	chainEvidence, err := dependencies.verifyChain(ctx, ChainOptions{
		RPCURL: cfg.RPCURL, ChainID: cfg.LedgerChainID,
		TransactionHash: payment.TransactionHash,
		Asset:           cfg.ExpectedAsset, AmountAtomic: cfg.ExpectedAmountAtomic,
		Recipient: cfg.ExpectedRecipient, Payer: cfg.ExpectedPayer,
		CallDataPrefixBytes:  payment.CallDataPrefixBytes,
		CallDataPrefixSHA256: payment.CallDataPrefixSHA256,
	})
	if err != nil {
		return Report{}, boundaryError(codePaidReconciliationIncomplete)
	}
	if ledgerEvidence.PaymentID == "" ||
		!sameText(chainEvidence.TransactionHash, payment.TransactionHash) ||
		chainEvidence.BlockHash == "" || chainEvidence.BlockNumber == "" ||
		chainEvidence.TransferCount <= 0 {
		return Report{}, boundaryError(codePaidReconciliationIncomplete)
	}
	return Report{
		SDKVersion: SDKVersion, HarnessRevision: cfg.Revision,
		Operation: cfg.ExpectedOperation, Network: baseSepoliaNetwork,
		Payer: payment.Payer, Asset: payment.Asset,
		Recipient: payment.Recipient, AmountAtomic: payment.AmountAtomic,
		PaymentID:         ledgerEvidence.PaymentID,
		TransactionHash:   payment.TransactionHash,
		ReceiptBlock:      chainEvidence.BlockNumber,
		ReceiptBlockHash:  chainEvidence.BlockHash,
		ResponseBodyBytes: payment.FinalBodyBytes,
		ResponseBodySHA256: hex.EncodeToString(
			payment.FinalBodySHA256[:],
		),
		CompletedAt: dependencies.now().UTC(),
	}, nil
}

func sameText(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare(
		[]byte(lowerASCII(left)), []byte(lowerASCII(right)),
	) == 1
}

func lowerASCII(value string) string {
	buffer := []byte(value)
	for index, character := range buffer {
		if character >= 'A' && character <= 'Z' {
			buffer[index] = character + ('a' - 'A')
		}
	}
	return string(buffer)
}
