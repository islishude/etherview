package app

import (
	"database/sql"
	"testing"

	"github.com/islishude/etherview/internal/config"
)

func TestBillingServicesRetainWriterHistoryWithoutRequestDispatcher(t *testing.T) {
	t.Parallel()
	db := new(sql.DB)
	cfg := config.Default()

	reader, err := newBillingServices(cfg, db)
	if err != nil || reader != nil {
		t.Fatalf("disabled reader=%v error=%v", reader, err)
	}

	for _, feature := range []string{"user-auth", "api-billing"} {
		t.Run(feature, func(t *testing.T) {
			configured := cfg
			if feature == "user-auth" {
				configured.Features.UserAuth = true
			} else {
				configured.Features.APIBilling = true
			}
			reader, err := newBillingServices(configured, db)
			if err != nil || reader == nil {
				t.Fatalf("configured reader=%v error=%v", reader, err)
			}
		})
	}
}
