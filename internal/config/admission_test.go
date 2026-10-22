package config

import "testing"

func TestAdmissionConfigurationBoundsAndEnvironment(t *testing.T) {
	cfg := Default()
	env := map[string]string{"ETHERVIEW_INGRESS_RATE": "50", "ETHERVIEW_INGRESS_BURST": "100", "ETHERVIEW_SSE_MAX_CONNECTIONS": "30", "ETHERVIEW_SSE_MAX_CONNECTIONS_PER_CLIENT": "3"}
	if err := applyNumericEnvironment(&cfg, func(key string) (string, bool) { value, ok := env[key]; return value, ok }); err != nil {
		t.Fatal(err)
	}
	if cfg.Security.IngressRate != 50 || cfg.Security.IngressBurst != 100 || cfg.Security.SSEMaxConnections != 30 || cfg.Security.SSEMaxConnectionsPerClient != 3 {
		t.Fatalf("%+v", cfg.Security)
	}
	if err := validateFeatureSecurityConfig(cfg); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*SecurityConfig){
		func(s *SecurityConfig) { s.IngressRate = 0 }, func(s *SecurityConfig) { s.IngressBurst = 49 },
		func(s *SecurityConfig) { s.SSEMaxConnections = 0 }, func(s *SecurityConfig) { s.SSEMaxConnectionsPerClient = 0 }, func(s *SecurityConfig) { s.SSEMaxConnectionsPerClient = 31 },
	} {
		invalid := cfg
		mutate(&invalid.Security)
		if err := validateFeatureSecurityConfig(invalid); err == nil {
			t.Fatalf("invalid limits accepted: %+v", invalid.Security)
		}
	}
}
