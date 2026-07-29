package app

import (
	"context"
	"errors"
	"slices"

	"github.com/islishude/etherview/internal/billing/x402wire"
	"github.com/islishude/etherview/internal/config"
)

const doctorBillingCheckFailed = "x402_facilitator_check_failed"

type billingSupportChecker interface {
	CheckSupported(context.Context, string) error
}

type billingSupportCheckerFactory func(
	x402wire.ClientOptions,
) (billingSupportChecker, error)

// Doctor performs role-scoped external capability checks after the CLI has
// validated the complete runnable configuration. Feature-off and non-API
// roles never construct a facilitator client or make a network request.
func (b *Backend) Doctor(
	ctx context.Context,
	cfg config.Config,
	roles []string,
) error {
	return checkBillingFacilitator(
		ctx,
		cfg,
		roles,
		func(options x402wire.ClientOptions) (billingSupportChecker, error) {
			return x402wire.NewClient(options)
		},
	)
}

func checkBillingFacilitator(
	ctx context.Context,
	cfg config.Config,
	roles []string,
	factory billingSupportCheckerFactory,
) error {
	if !cfg.Features.X402Billing || !slices.Contains(roles, "api") {
		return nil
	}
	if factory == nil {
		return errors.New(doctorBillingCheckFailed)
	}
	checker, err := factory(x402wire.ClientOptions{
		BaseURL:          cfg.Billing.FacilitatorURL,
		AllowedCIDRs:     cfg.Billing.FacilitatorAllowedCIDRs,
		Timeout:          cfg.Billing.FacilitatorTimeout,
		MaxResponseBytes: cfg.Billing.FacilitatorMaxResponseBytes,
		Headers:          cfg.Billing.FacilitatorHeaders,
	})
	if err != nil {
		return stableDoctorBillingError(err)
	}
	if checker == nil {
		return errors.New(doctorBillingCheckFailed)
	}
	return stableDoctorBillingError(
		checker.CheckSupported(ctx, cfg.Billing.Network),
	)
}

func stableDoctorBillingError(err error) error {
	if err == nil {
		return nil
	}
	if boundary, ok := errors.AsType[*x402wire.BoundaryError](err); ok {
		switch boundary.Code {
		case x402wire.CodeFacilitatorConfigInvalid,
			x402wire.CodeFacilitatorUnavailable,
			x402wire.CodeFacilitatorUnsupported:
			return errors.New(boundary.Code)
		}
	}
	return errors.New(doctorBillingCheckFailed)
}
