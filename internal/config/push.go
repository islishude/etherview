package config

import (
	"crypto/ecdh"
	"encoding/base64"
	"errors"
	"net/url"
	"slices"
	"strings"
)

func validatePushConfig(c Config) error {
	if !c.Push.Enabled {
		return nil
	}
	if !c.Features.UserAuth {
		return errors.New("push requires user authentication")
	}
	public, e := base64.RawURLEncoding.DecodeString(c.Push.PublicKey)
	if e != nil {
		return errors.New("push public key is invalid")
	}
	if _, e = ecdh.P256().NewPublicKey(public); e != nil {
		return errors.New("push public key is invalid")
	}
	contact, e := url.Parse(c.Push.Contact)
	if e != nil {
		return errors.New("push contact must be mailto or HTTPS")
	}
	allowedContact := contact.Scheme == "mailto" && strings.Contains(contact.Opaque, "@") || contact.Scheme == "https" && contact.Host != ""
	if !allowedContact {
		return errors.New("push contact must be mailto or HTTPS")
	}
	roles, _ := NormalizeRoles(c.Runtime.Roles)
	if slices.Contains(roles, "maintenance") {
		raw, e := base64.RawURLEncoding.DecodeString(c.Push.PrivateKey)
		if e != nil {
			return errors.New("push private key is invalid")
		}
		private, e := ecdh.P256().NewPrivateKey(raw)
		if e != nil || base64.RawURLEncoding.EncodeToString(private.PublicKey().Bytes()) != c.Push.PublicKey {
			return errors.New("push private key must match public key")
		}
	}
	return nil
}
