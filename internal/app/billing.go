package app

import (
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/billing"
	"github.com/islishude/etherview/internal/billing/x402wire"
	"github.com/islishude/etherview/internal/config"
)

func newBillingServices(
	cfg config.Config,
	db *sql.DB,
) (*billing.PostgresLedger, error) {
	if !cfg.Features.UserAuth && !cfg.Features.APIBilling && !cfg.Features.X402Topups {
		return nil, nil
	}
	ledger, err := billing.NewPostgresLedger(db, cfg.Chain.ID, cfg.Billing.ReservationTTL)
	if err != nil {
		return nil, err
	}
	return ledger, nil
}

func newTopupDispatcher(
	cfg config.Config,
	payments *billing.PostgresLedger,
	accounts *billing.PrepaidLedger,
	logger *slog.Logger,
	observer billing.PrepaidObserver,
) (*billing.TopupDispatcher, error) {
	if !cfg.Features.X402Topups {
		return nil, nil
	}
	if payments == nil || accounts == nil {
		return nil, errors.New("enabled x402 top-ups require payment and prepaid ledgers")
	}
	facilitator, err := x402wire.NewClient(x402wire.ClientOptions{
		BaseURL:          cfg.Billing.FacilitatorURL,
		UnsafeAllowHTTP:  cfg.Billing.FacilitatorUnsafeAllowHTTP,
		AllowedCIDRs:     cfg.Billing.FacilitatorAllowedCIDRs,
		Timeout:          cfg.Billing.FacilitatorTimeout,
		MaxResponseBytes: cfg.Billing.FacilitatorMaxResponseBytes,
		Headers:          cfg.Billing.FacilitatorHeaders,
	})
	if err != nil {
		return nil, err
	}
	codec, err := x402wire.NewCodec(cfg.Billing.MaxPaymentHeaderBytes)
	if err != nil {
		return nil, err
	}
	return billing.NewTopupDispatcher(billing.TopupDispatcherOptions{
		Payments: payments, Accounts: accounts, Facilitator: facilitator,
		Codec: codec, FingerprintPepper: []byte(cfg.Billing.FingerprintPepper),
		PublicOrigin: cfg.Server.PublicURL, Methods: cfg.Billing.AssetTransferMethods,
		MaxTimeoutSeconds: int(cfg.Billing.RequirementMaxTimeout / time.Second),
		AssetName:         cfg.Billing.AssetEIP712Name,
		AssetVersion:      cfg.Billing.AssetEIP712Version, Logger: logger,
		Observer: observer,
	})
}

func newPrepaidServices(
	cfg config.Config,
	db *sql.DB,
	logger *slog.Logger,
	observer billing.PrepaidObserver,
) (*billing.PrepaidLedger, *billing.UsageDispatcher, error) {
	if !cfg.Features.APIBilling {
		return nil, nil, nil
	}
	asset := common.HexToAddress(cfg.Billing.Asset)
	recipient := common.HexToAddress(cfg.Billing.Recipient)
	ledger, err := billing.NewPrepaidLedger(db, billing.PrepaidOptions{
		ChainID: cfg.Chain.ID, Network: cfg.Billing.Network,
		Asset: asset, Recipient: recipient,
		TopupTTL: cfg.Billing.TopupIntentTTL,
		UsageTTL: cfg.Billing.UsageReservationTTL,
	})
	if err != nil {
		return nil, nil, err
	}
	prices := make(map[string]string, len(cfg.Billing.Operations))
	for operation, operationConfig := range cfg.Billing.Operations {
		prices[operation] = operationConfig.AmountAtomic
	}
	dispatcher, err := billing.NewUsageDispatcher(billing.UsageDispatcherOptions{
		Ledger: ledger, Prices: prices,
		MaxBodyBytes:   cfg.Billing.MaxBufferedResponseBytes,
		MaxHeaderBytes: cfg.Billing.MaxCapturedHeaderBytes,
		Logger:         logger,
		Observer:       observer,
	})
	if err != nil {
		return nil, nil, err
	}
	return ledger, dispatcher, nil
}
