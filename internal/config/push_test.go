package config

import (
	"os"
	"path/filepath"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func TestPushPrivateNetworkConfiguration(t *testing.T) {
	if Default().Push.UnsafeAllowPrivateNetworks {
		t.Fatal("private-network exception must default off")
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("push:\n  unsafe_allow_private_networks: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"all", "maintenance"} {
		loaded, err := LoadForRoles(path, []string{role})
		if err != nil {
			t.Fatal(err)
		}
		if !loaded.Push.UnsafeAllowPrivateNetworks {
			t.Fatal("YAML setting not loaded")
		}
	}
	t.Setenv("ETHERVIEW_PUSH_UNSAFE_ALLOW_PRIVATE_NETWORKS", "false")
	loaded, err := LoadForRoles(path, []string{"maintenance"})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Push.UnsafeAllowPrivateNetworks {
		t.Fatal("environment disable ignored")
	}
	t.Setenv("ETHERVIEW_PUSH_UNSAFE_ALLOW_PRIVATE_NETWORKS", "true")
	loaded, err = LoadForRoles("", []string{"maintenance"})
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Push.UnsafeAllowPrivateNetworks {
		t.Fatal("environment enable ignored")
	}
	t.Setenv("ETHERVIEW_PUSH_UNSAFE_ALLOW_PRIVATE_NETWORKS", "invalid")
	if _, err := LoadForRoles("", []string{"maintenance"}); err == nil {
		t.Fatal("invalid boolean accepted")
	}
}

func TestPushKeysAndRoleScopedSecret(t *testing.T) {
	private, public, e := webpush.GenerateVAPIDKeys()
	if e != nil {
		t.Fatal(e)
	}
	c := Default()
	c.Features.UserAuth = true
	c.Runtime.Roles = []string{"api"}
	c.Push = PushConfig{Enabled: true, PublicKey: public, Contact: "mailto:test@example.com"}
	if e := validatePushConfig(c); e != nil {
		t.Fatal(e)
	}
	c.Runtime.Roles = []string{"maintenance"}
	if e := validatePushConfig(c); e == nil {
		t.Fatal("missing maintenance key accepted")
	}
	c.Push.PrivateKey = private
	if e := validatePushConfig(c); e != nil {
		t.Fatal(e)
	}
	c.Push.PublicKey = "bad"
	if e := validatePushConfig(c); e == nil {
		t.Fatal("invalid public key accepted")
	}
	c.Push.PublicKey = public
	c.Runtime.Roles = []string{"api"}
	c.Push.PrivateKey = ""
	reads := 0
	lookup := func(k string) (string, bool) {
		if k == "ETHERVIEW_PUSH_PRIVATE_KEY_FILE" {
			return "secret", true
		}
		return "", false
	}
	read := func(string) ([]byte, error) { reads++; return []byte(private), nil }
	if e := applySecretEnvironment(&c, lookup, read, true); e != nil {
		t.Fatal(e)
	}
	if reads != 0 || c.Push.PrivateKey != "" {
		t.Fatal("API loaded push private key")
	}
	c.Runtime.Roles = []string{"maintenance"}
	if e := applySecretEnvironment(&c, lookup, read, false); e != nil {
		t.Fatal(e)
	}
	if reads != 1 || c.Push.PrivateKey != private {
		t.Fatal("maintenance did not load push key")
	}
}
