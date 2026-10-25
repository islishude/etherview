package config

import (
	"errors"
	"net/url"
	"regexp"
)

var turnstileKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

func validateTurnstileConfig(c Config) error {
	if !c.Turnstile.Enabled() {
		return nil
	}
	var errs []error
	if !c.Features.UserAuth {
		errs = append(errs, errors.New("turnstile requires features.user_auth"))
	}
	if !turnstileKeyPattern.MatchString(c.Turnstile.SiteKey) {
		errs = append(errs, errors.New("turnstile.site_key must be a valid public key"))
	}
	if c.Turnstile.SecretKey != "" && !turnstileKeyPattern.MatchString(c.Turnstile.SecretKey) {
		errs = append(errs, errors.New("turnstile secret key is invalid"))
	}
	u, err := url.Parse(c.Server.PublicURL)
	if err != nil || u.Hostname() == "" {
		errs = append(errs, errors.New("turnstile requires server.public_url"))
	}
	return errors.Join(errs...)
}
