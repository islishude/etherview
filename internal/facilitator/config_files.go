package facilitator

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// resolvedConfig is startup-only; it is never serialized or retained by Service.
type resolvedConfig struct {
	database []byte
	token    []byte
	rpc      []string
	signers  map[string]signerFiles
}
type signerFiles struct{ key, password []byte }

func (r *resolvedConfig) clear() {
	clear(r.database)
	clear(r.token)
	for _, f := range r.signers {
		clear(f.key)
		clear(f.password)
	}
	clear(r.rpc)
}
func readConfigFile(path string, limit int64, field string) ([]byte, error) {
	return readBoundedConfigFile(path, limit, field, false)
}
func readBoundedConfigFile(path string, limit int64, field string, allowEmpty bool) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, configError("file_unavailable", field)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, configError("file_unavailable", field)
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || (!allowEmpty && len(b) == 0) || int64(len(b)) > limit {
		clear(b)
		return nil, configError("invalid_file", field)
	}
	return b, nil
}
func resolveConfig(c Config) (_ *resolvedConfig, err error) {
	if err = c.Validate(); err != nil {
		return nil, err
	}
	r := &resolvedConfig{signers: map[string]signerFiles{}}
	defer func() {
		if err != nil {
			r.clear()
		}
	}()
	r.database, err = resolveCredential("X402_DATABASE_URL", c.Database.URLFile, 8192, "database.url_file", os.LookupEnv)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(r.database)) == 0 {
		return nil, configError("invalid_file", "database.url_file")
	}
	r.token, err = resolveCredential("X402_BEARER_TOKEN", c.Auth.BearerTokenFile, 4096, "auth.bearer_token_file", os.LookupEnv)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(r.token)) < 32 {
		return nil, configError("invalid_bearer_token", "auth.bearer_token_file")
	}
	if !c.Server.AllowInsecureHTTP {
		cert, e := readConfigFile(c.Server.TLSCertFile, 1<<20, "server.tls_cert_file")
		if e != nil {
			return nil, e
		}
		defer clear(cert)
		key, e := readConfigFile(c.Server.TLSKeyFile, 1<<20, "server.tls_key_file")
		if e != nil {
			return nil, e
		}
		defer clear(key)
		if _, e = tls.X509KeyPair(cert, key); e != nil {
			return nil, configError("invalid_tls", "server")
		}
	}
	ids := make([]string, 0, len(c.Signers))
	for id := range c.Signers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for i, id := range ids {
		s := c.Signers[id]
		if s.Type != "keystore" {
			continue
		}
		field := fmt.Sprintf("signers[%d]", i)
		key, e := readConfigFile(s.KeystoreFile, 1<<20, field+".keystore_file")
		if e != nil {
			return nil, e
		}
		r.signers[id] = signerFiles{key: key}
		password, e := readConfigFile(s.PasswordFile, 4096, field+".password_file")
		if e != nil {
			return nil, e
		}
		r.signers[id] = signerFiles{key: key, password: password}
	}
	for i, ch := range c.Chains {
		raw := ch.RPC.URL
		field := fmt.Sprintf("chains[%d].rpc", i)
		if ch.RPC.URLFile != "" {
			b, e := readConfigFile(ch.RPC.URLFile, 8192, field+".url_file")
			if e != nil {
				return nil, e
			}
			raw = strings.TrimSpace(string(b))
			clear(b)
		}
		if e := validateRPCURL(raw, ch.RPC.AllowInsecureHTTP, field); e != nil {
			return nil, e
		}
		r.rpc = append(r.rpc, raw)
	}
	return r, nil
}

// CheckConfig checks configuration, credential environment and bounded local files without external calls
// or decrypting a signing key. It does not establish dependency readiness.
func CheckConfig(c Config) error {
	r, err := resolveConfig(c)
	if err != nil {
		return err
	}
	r.clear()
	return nil
}
