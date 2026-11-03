package config

import (
	"errors"
	"strings"
	"testing"
)

func TestVerificationCredentialRoleScope(t *testing.T) {
	for _, role := range allowedRoles {
		t.Run(role, func(t *testing.T) {
			cfg := Default()
			cfg.Security.APIKeyPepper = strings.Repeat("c", 32)
			reads := 0
			err := applyEnvironmentForRoles(&cfg, func(key string) (string, bool) {
				if key == "ETHERVIEW_API_KEY_PEPPER_FILE" {
					return "/private/pepper", true
				}
				return "", false
			}, func(string) ([]byte, error) { reads++; return nil, errors.New("unavailable secret") }, []string{role})
			owner := role == "api" || role == "verification"
			if owner && (err == nil || reads != 1) {
				t.Fatalf("owner did not load credential: reads=%d err=%v", reads, err)
			}
			if !owner && (err != nil || reads != 0 || cfg.Security.APIKeyPepper != "") {
				t.Fatalf("unrelated role loaded credential: reads=%d err=%v", reads, err)
			}
		})
	}
}
