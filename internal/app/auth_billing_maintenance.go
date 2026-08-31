package app

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"

	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/billing"
	"github.com/islishude/etherview/internal/components"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/userauth"
)

const maximumAuthBillingMaintenanceBatch = 1_000

type authBillingMaintenanceSweep func(context.Context, time.Time, int) error

type authBillingHousekeeperOptions struct {
	ServiceName   string
	FailureCode   string
	Interval      time.Duration
	RetryInterval time.Duration
	Batch         int
	Now           func() time.Time
}

func (options *authBillingHousekeeperOptions) defaults() {
	if options.RetryInterval <= 0 {
		options.RetryInterval = min(options.Interval, 30*time.Second)
	}
	if options.Now == nil {
		options.Now = time.Now
	}
}

// authBillingHousekeeper runs bounded, best-effort writer maintenance under
// the shared component supervisor. Cleanup is a storage concern: a transient
// writer failure is retried without changing readiness or exposing the nested
// database error.
type authBillingHousekeeper struct {
	sweep   authBillingMaintenanceSweep
	logger  *slog.Logger
	options authBillingHousekeeperOptions
}

func newAuthBillingHousekeeper(
	sweep authBillingMaintenanceSweep,
	logger *slog.Logger,
	options authBillingHousekeeperOptions,
) (*authBillingHousekeeper, error) {
	if sweep == nil {
		return nil, errors.New("auth/billing housekeeper requires a sweep")
	}
	options.defaults()
	options.ServiceName = strings.TrimSpace(options.ServiceName)
	options.FailureCode = strings.TrimSpace(options.FailureCode)
	if options.ServiceName == "" || len(options.ServiceName) > 128 ||
		options.FailureCode == "" || len(options.FailureCode) > 128 ||
		options.Interval <= 0 || options.RetryInterval <= 0 ||
		options.RetryInterval > options.Interval ||
		options.Batch < 1 || options.Batch > maximumAuthBillingMaintenanceBatch ||
		options.Now == nil {
		return nil, errors.New("auth/billing housekeeper options are invalid")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &authBillingHousekeeper{
		sweep: sweep, logger: logger, options: options,
	}, nil
}

func (housekeeper *authBillingHousekeeper) Name() string {
	if housekeeper == nil || housekeeper.options.ServiceName == "" {
		return "auth-billing-housekeeper"
	}
	return housekeeper.options.ServiceName
}

func (housekeeper *authBillingHousekeeper) Run(ctx context.Context) error {
	if housekeeper == nil || housekeeper.sweep == nil {
		return errors.New("run nil auth/billing housekeeper")
	}
	delay := time.Duration(0)
	retry := housekeeper.options.RetryInterval
	for {
		if err := waitForHousekeeping(ctx, delay); err != nil {
			return err
		}
		err := housekeeper.sweep(
			ctx, housekeeper.options.Now().UTC(), housekeeper.options.Batch,
		)
		if err == nil {
			delay = housekeeper.options.Interval
			retry = housekeeper.options.RetryInterval
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		housekeeper.logger.WarnContext(
			ctx,
			"bounded PostgreSQL housekeeping failed",
			"error_code",
			housekeeper.options.FailureCode,
		)
		delay = retry
		if retry < housekeeper.options.Interval {
			if retry >= housekeeper.options.Interval/2 {
				retry = housekeeper.options.Interval
			} else {
				retry *= 2
			}
		}
	}
}

func waitForHousekeeping(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func registerAuthBillingHousekeepers(
	registry *components.Registry,
	writer *sql.DB,
	cfg config.Config,
	logger *slog.Logger,
) error {
	if !cfg.Features.UserAuth && !cfg.Features.APIBilling {
		return nil
	}
	if registry == nil {
		return errors.New("register auth/billing housekeepers: nil component registry")
	}
	if writer == nil {
		return errors.New("register auth/billing housekeepers: nil writer database")
	}
	common := authBillingHousekeeperOptions{
		Interval: cfg.Maintenance.Interval,
		Batch:    maximumAuthBillingMaintenanceBatch,
	}
	if cfg.Features.UserAuth {
		repository, err := userauth.NewPostgresRepository(writer, cfg.Chain.ID)
		if err != nil {
			return err
		}
		options := common
		options.ServiceName = "user-auth-cleanup"
		options.FailureCode = "user_auth_cleanup_failed"
		housekeeper, err := newAuthBillingHousekeeper(
			func(ctx context.Context, observedAt time.Time, limit int) error {
				_, cleanupErr := repository.Cleanup(ctx, observedAt, limit)
				return cleanupErr
			},
			logger,
			options,
		)
		if err != nil {
			return err
		}
		if err := registry.Register(
			components.RoleMaintenance,
			"47-user-auth-cleanup",
			func() (components.Service, error) { return housekeeper, nil },
		); err != nil {
			return err
		}
	}
	if cfg.Features.APIBilling {
		ledger, err := billing.NewPostgresLedger(
			writer, cfg.Chain.ID, cfg.Billing.ReservationTTL,
		)
		if err != nil {
			return err
		}
		options := common
		options.ServiceName = "x402-billing-expiry"
		options.FailureCode = "x402_billing_expiry_failed"
		housekeeper, err := newAuthBillingHousekeeper(
			func(ctx context.Context, observedAt time.Time, limit int) error {
				_, expireErr := ledger.Expire(ctx, observedAt, limit)
				return expireErr
			},
			logger,
			options,
		)
		if err != nil {
			return err
		}
		if err := registry.Register(
			components.RoleMaintenance,
			"48-x402-billing-expiry",
			func() (components.Service, error) { return housekeeper, nil },
		); err != nil {
			return err
		}
	}
	if cfg.Features.APIBilling {
		ledger, err := billing.NewPrepaidLedger(writer, billing.PrepaidOptions{
			ChainID: cfg.Chain.ID, Network: cfg.Billing.Network,
			Asset:     ethcommon.HexToAddress(cfg.Billing.Asset),
			Recipient: ethcommon.HexToAddress(cfg.Billing.Recipient),
			TopupTTL:  cfg.Billing.TopupIntentTTL,
			UsageTTL:  cfg.Billing.UsageReservationTTL,
		})
		if err != nil {
			return err
		}
		options := common
		options.ServiceName = "prepaid-billing-expiry"
		options.FailureCode = "prepaid_billing_expiry_failed"
		housekeeper, err := newAuthBillingHousekeeper(
			func(ctx context.Context, observedAt time.Time, limit int) error {
				_, expireErr := ledger.Expire(ctx, observedAt, limit)
				return expireErr
			},
			logger,
			options,
		)
		if err != nil {
			return err
		}
		if err := registry.Register(
			components.RoleMaintenance,
			"49-prepaid-billing-expiry",
			func() (components.Service, error) { return housekeeper, nil },
		); err != nil {
			return err
		}
	}
	return nil
}
