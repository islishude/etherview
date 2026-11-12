package config

import (
	"strings"
	"testing"
	"time"
)

func TestPaymentBudgetValidation(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{"default", func(*Config) {}, ""},
		{"zero", func(c *Config) { c.Server.PaymentRequestTimeout = 0 }, "payment_request_timeout"},
		{"unbounded", func(c *Config) { c.Server.PaymentRequestTimeout = 3*time.Minute + time.Second }, "at most 3m"},
		{"short client", func(c *Config) { c.Billing.FacilitatorTimeout = 27 * time.Second }, "at least 30s"},
		{"serial calls", func(c *Config) { c.Billing.FacilitatorTimeout = 31 * time.Second }, "twice billing.facilitator_timeout"},
		{"short payment", func(c *Config) { c.Server.PaymentRequestTimeout = 74 * time.Second }, "twice billing.facilitator_timeout"},
		{"short write", func(c *Config) { c.Server.WriteTimeout = 94 * time.Second }, "server.write_timeout"},
		{"body read allowance", func(c *Config) { c.Server.ReadTimeout = 21 * time.Second }, "server.write_timeout"},
		{"larger bounded", func(c *Config) {
			c.Billing.FacilitatorTimeout = time.Minute
			c.Server.PaymentRequestTimeout = 135 * time.Second
			c.Server.WriteTimeout = 155 * time.Second
		}, ""},
		{"disabled billing", func(c *Config) {
			c.Features.X402Topups = false
			c.Server.WriteTimeout = time.Second
			c.Billing.FacilitatorTimeout = time.Second
		}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := Default()
			c.Features.X402Topups = true
			tt.change(&c)
			err := validateServerConfig(c)
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v; want %s", err, tt.want)
			}
		})
	}
}

func TestPaymentBudgetEnvironment(t *testing.T) {
	c := Default()
	err := applyDurationEnvironment(&c, func(key string) (string, bool) {
		if key == "ETHERVIEW_SERVER_PAYMENT_REQUEST_TIMEOUT" {
			return "90s", true
		}
		return "", false
	})
	if err != nil || c.Server.PaymentRequestTimeout != 90*time.Second || c.Server.RequestTimeout != 20*time.Second {
		t.Fatalf("server=%+v error=%v", c.Server, err)
	}
}
