package app

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/userauth"
)

func TestBillingServicesKeepHistoryOnWriterWhenUserAuthIsEnabled(t *testing.T) {
	t.Parallel()
	db := new(sql.DB)
	cfg := config.Default()

	dispatcher, reader, err := newBillingServices(cfg, db, nil, nil, slog.Default())
	if err != nil || dispatcher != nil || reader != nil {
		t.Fatalf(
			"disabled dispatcher=%v reader=%v error=%v",
			dispatcher, reader, err,
		)
	}

	cfg.Features.UserAuth = true
	dispatcher, reader, err = newBillingServices(cfg, db, nil, nil, slog.Default())
	if err != nil || dispatcher != nil || reader == nil {
		t.Fatalf(
			"auth dispatcher=%v reader=%v error=%v",
			dispatcher, reader, err,
		)
	}
}

func TestBillingServicesShareWriterLedgerWithEnabledDispatcher(t *testing.T) {
	t.Parallel()
	db := new(sql.DB)
	cfg := config.Default()
	cfg.Features.X402Billing = true
	cfg.Server.PublicURL = "https://explorer.example"
	cfg.Chain.ID = 84532
	cfg.Billing.FacilitatorURL = "https://facilitator.example"
	cfg.Billing.FacilitatorAllowedCIDRs = []string{"203.0.113.0/24"}
	cfg.Billing.Network = "eip155:84532"
	cfg.Billing.Asset = "0x1111111111111111111111111111111111111111"
	cfg.Billing.Recipient = "0x2222222222222222222222222222222222222222"
	cfg.Billing.AssetEIP712Name = "USDC"
	cfg.Billing.AssetEIP712Version = "2"
	cfg.Billing.FingerprintPepper = strings.Repeat("f", 32)
	cfg.Billing.Routes = map[string]config.BillingRouteConfig{
		"listBlocks": {Access: "x402", AmountAtomic: "1"},
	}

	dispatcher, reader, err := newBillingServices(cfg, db, nil, nil, slog.Default())
	if err != nil || dispatcher == nil || reader == nil {
		t.Fatalf(
			"dispatcher=%v reader=%v error=%v",
			dispatcher, reader, err,
		)
	}

	cfg.Features.UserAuth = true
	dispatcher, reader, err = newBillingServices(cfg, db, nil, nil, slog.Default())
	if err == nil || dispatcher != nil || reader != nil ||
		!strings.Contains(err.Error(), "writer user repository") {
		t.Fatalf(
			"missing user writer dispatcher=%v reader=%v error=%v",
			dispatcher, reader, err,
		)
	}
}

func TestBillingResolverIsControlledOnlyByUserAuthFeature(t *testing.T) {
	t.Parallel()
	repository := new(userauth.PostgresRepository)
	cfg := config.Default()
	cfg.Features.X402Billing = true

	resolver, err := billingResolverForConfig(cfg, repository)
	if err != nil || resolver != nil {
		t.Fatalf("feature-off resolver=%T error=%v", resolver, err)
	}

	cfg.Features.UserAuth = true
	resolver, err = billingResolverForConfig(cfg, nil)
	if err == nil || resolver != nil ||
		!strings.Contains(err.Error(), "writer user repository") {
		t.Fatalf("missing writer resolver=%T error=%v", resolver, err)
	}

	resolver, err = billingResolverForConfig(cfg, repository)
	if err != nil || resolver == nil {
		t.Fatalf("feature-on resolver=%T error=%v", resolver, err)
	}
}

func TestBillingUserResolverAssociatesActiveAndDisabledUsers(t *testing.T) {
	t.Parallel()
	var payer common.Address
	payer[0] = 0xab
	payer[19] = 0xcd
	const expectedAddress = "0xab000000000000000000000000000000000000cd"

	for _, status := range []userauth.Status{
		userauth.StatusActive,
		userauth.StatusDisabled,
	} {
		status := status
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			lookup := &stubBillingUserLookup{
				user: userauth.User{ID: "user-" + string(status), Status: status},
			}
			userID, found, err := (billingUserResolver{repository: lookup}).
				UserIDForPayer(context.Background(), payer)
			if err != nil || !found || userID != lookup.user.ID {
				t.Fatalf("user ID=%q found=%t error=%v", userID, found, err)
			}
			if lookup.address != expectedAddress {
				t.Fatalf("lookup address=%q, want %q", lookup.address, expectedAddress)
			}
		})
	}
}

func TestBillingUserResolverTreatsMissingAsAccountlessAndFailsClosed(t *testing.T) {
	t.Parallel()
	var payer common.Address

	missing := &stubBillingUserLookup{err: userauth.ErrUserNotFound}
	userID, found, err := (billingUserResolver{repository: missing}).
		UserIDForPayer(context.Background(), payer)
	if err != nil || found || userID != "" {
		t.Fatalf("missing user ID=%q found=%t error=%v", userID, found, err)
	}

	lookupErr := errors.New("writer unavailable")
	unavailable := &stubBillingUserLookup{err: lookupErr}
	userID, found, err = (billingUserResolver{repository: unavailable}).
		UserIDForPayer(context.Background(), payer)
	if !errors.Is(err, lookupErr) || found || userID != "" {
		t.Fatalf("unavailable user ID=%q found=%t error=%v", userID, found, err)
	}
}

type stubBillingUserLookup struct {
	user    userauth.User
	err     error
	address string
}

func (lookup *stubBillingUserLookup) UserByAddress(
	_ context.Context,
	address string,
) (userauth.User, error) {
	lookup.address = address
	return lookup.user, lookup.err
}
