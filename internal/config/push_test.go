package config

import (
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
)

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
