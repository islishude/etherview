package app

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"

	"github.com/islishude/etherview/internal/billing"
	"github.com/islishude/etherview/internal/billing/x402wire"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/userauth"
)

func newBillingServices(
	cfg config.Config,
	db *sql.DB,
	users *userauth.PostgresRepository,
	observer billing.RequestObserver,
	logger *slog.Logger,
) (*billing.HTTPDispatcher, *billing.PostgresLedger, error) {
	if !cfg.Features.UserAuth && !cfg.Features.X402Billing {
		return nil, nil, nil
	}
	ledger, err := billing.NewPostgresLedger(db, cfg.Chain.ID, cfg.Billing.ReservationTTL)
	if err != nil {
		return nil, nil, err
	}
	if !cfg.Features.X402Billing {
		return nil, ledger, nil
	}
	resolver, err := billingResolverForConfig(cfg, users)
	if err != nil {
		return nil, nil, err
	}
	facilitator, err := x402wire.NewClient(x402wire.ClientOptions{
		BaseURL:          cfg.Billing.FacilitatorURL,
		AllowedCIDRs:     cfg.Billing.FacilitatorAllowedCIDRs,
		Timeout:          cfg.Billing.FacilitatorTimeout,
		MaxResponseBytes: cfg.Billing.FacilitatorMaxResponseBytes,
		Headers:          cfg.Billing.FacilitatorHeaders,
	})
	if err != nil {
		return nil, nil, err
	}
	dispatcher, err := billing.NewHTTPDispatcher(billing.DispatcherOptions{
		Config: cfg, Ledger: ledger, Facilitator: facilitator,
		UserResolver: resolver, Observer: observer, Logger: logger,
	})
	if err != nil {
		return nil, nil, err
	}
	return dispatcher, ledger, nil
}

func billingResolverForConfig(
	cfg config.Config,
	users *userauth.PostgresRepository,
) (billing.PayerUserResolver, error) {
	if !cfg.Features.UserAuth {
		return nil, nil
	}
	if users == nil {
		return nil, errors.New(
			"enabled user authentication and x402 billing require a writer user repository",
		)
	}
	return billingUserResolver{repository: users}, nil
}

type billingUserResolver struct {
	repository billingUserLookup
}

type billingUserLookup interface {
	UserByAddress(context.Context, string) (userauth.User, error)
}

func (resolver billingUserResolver) UserIDForPayer(
	ctx context.Context,
	payer billing.Address,
) (string, bool, error) {
	if resolver.repository == nil {
		return "", false, nil
	}
	user, err := resolver.repository.UserByAddress(ctx, "0x"+hex.EncodeToString(payer[:]))
	if errors.Is(err, userauth.ErrUserNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return user.ID, true, nil
}
