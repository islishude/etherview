package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/billing/x402wire"
	"github.com/islishude/etherview/internal/config"
)

type doctorSupportChecker struct {
	network string
	err     error
}

func (checker *doctorSupportChecker) CheckSupported(
	_ context.Context,
	network string,
) error {
	checker.network = network
	return checker.err
}

func TestBillingDoctorIsRoleAndFeatureScoped(t *testing.T) {
	for _, test := range []struct {
		name    string
		enabled bool
		roles   []string
	}{
		{name: "feature off", roles: []string{"api"}},
		{name: "non api", enabled: true, roles: []string{"sync"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Features.X402Billing = test.enabled
			called := false
			err := checkBillingFacilitator(
				context.Background(),
				cfg,
				test.roles,
				func(x402wire.ClientOptions) (billingSupportChecker, error) {
					called = true
					return nil, errors.New("must not construct")
				},
			)
			if err != nil || called {
				t.Fatalf("err=%v factory called=%v", err, called)
			}
		})
	}
}

func TestBillingDoctorUsesRestrictedClientConfiguration(t *testing.T) {
	cfg := config.Default()
	cfg.Features.X402Billing = true
	cfg.Billing.FacilitatorURL = "https://facilitator.example"
	cfg.Billing.FacilitatorAllowedCIDRs = []string{"192.0.2.0/24"}
	cfg.Billing.FacilitatorTimeout = 7
	cfg.Billing.FacilitatorMaxResponseBytes = 1234
	cfg.Billing.FacilitatorHeaders = map[string]string{"Authorization": "opaque"}
	cfg.Billing.Network = "eip155:84532"
	checker := &doctorSupportChecker{}
	var captured x402wire.ClientOptions
	err := checkBillingFacilitator(
		context.Background(),
		cfg,
		[]string{"api"},
		func(options x402wire.ClientOptions) (billingSupportChecker, error) {
			captured = options
			return checker, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if captured.BaseURL != cfg.Billing.FacilitatorURL ||
		strings.Join(captured.AllowedCIDRs, ",") != "192.0.2.0/24" ||
		captured.Timeout != cfg.Billing.FacilitatorTimeout ||
		captured.MaxResponseBytes != 1234 ||
		captured.Headers["Authorization"] != "opaque" ||
		checker.network != "eip155:84532" {
		t.Fatalf("options=%+v network=%q", captured, checker.network)
	}
}

func TestBillingDoctorReturnsOnlyClosedErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "supported unavailable",
			err: &x402wire.BoundaryError{
				Phase: x402wire.PhaseSupported,
				Class: x402wire.FailureUnavailable,
				Code:  x402wire.CodeFacilitatorUnavailable,
			},
			want: x402wire.CodeFacilitatorUnavailable,
		},
		{
			name: "hostile generic",
			err:  errors.New("https://user:secret@facilitator.example"),
			want: doctorBillingCheckFailed,
		},
		{
			name: "hostile boundary code",
			err: &x402wire.BoundaryError{
				Phase: x402wire.PhaseSupported,
				Class: x402wire.FailureUnavailable,
				Code:  "secret-value",
			},
			want: doctorBillingCheckFailed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checker := &doctorSupportChecker{err: test.err}
			cfg := config.Default()
			cfg.Features.X402Billing = true
			cfg.Billing.Network = "eip155:84532"
			err := checkBillingFacilitator(
				context.Background(),
				cfg,
				[]string{"api"},
				func(x402wire.ClientOptions) (billingSupportChecker, error) {
					return checker, nil
				},
			)
			if err == nil || err.Error() != test.want {
				t.Fatalf("error=%v want=%q", err, test.want)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("error leaked hostile input: %v", err)
			}
		})
	}
}
