package config

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

func validateBytecodeDB(c BytecodeDBConfig, verification bool) error {
	u, err := url.Parse(c.BaseURL)
	if err != nil || c.BaseURL != strings.TrimSpace(c.BaseURL) || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || len(c.BaseURL) > 4096 || unsafeURLPath(u.EscapedPath()) {
		return errors.New("bytecode_db.base_url must be an absolute HTTPS URL without credentials, query, fragment or traversal")
	}
	if c.Enabled && !verification {
		return errors.New("bytecode_db.enabled requires features.verification")
	}
	if c.Timeout < 100*time.Millisecond || c.Timeout > 2*time.Minute || c.MaxResponseBytes < 1 || c.MaxResponseBytes > 64<<20 || c.MaxConcurrency < 1 || c.MaxConcurrency > 16 || c.RequestInterval < 100*time.Millisecond || c.RequestInterval > time.Hour || c.Cooldown < time.Minute || c.Cooldown > 30*24*time.Hour {
		return errors.New("bytecode_db bounds are invalid")
	}
	return nil
}
