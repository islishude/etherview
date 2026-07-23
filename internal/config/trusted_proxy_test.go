package config

import (
	"strings"
	"testing"
)

func TestTrustedProxyConfigurationRequiresCanonicalIPOrCIDR(t *testing.T) {
	t.Parallel()
	valid := Default()
	valid.Security.TrustedProxies = []string{
		"192.0.2.10",
		"10.0.0.0/8",
		"2001:db8::1",
		"2001:db8::/32",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("canonical trusted proxies failed validation: %v", err)
	}

	for _, value := range []string{
		"",
		" proxy.example",
		"proxy.example",
		"192.0.2.1:443",
		"192.0.2.1/24",
		"2001:0DB8::1",
		"fe80::1%eth0",
		"::ffff:192.0.2.1",
	} {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			cfg.Security.TrustedProxies = []string{value}
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), "security.trusted_proxies[0]") {
				t.Fatalf("non-canonical trusted proxy %q error=%v", value, err)
			}
		})
	}
}
