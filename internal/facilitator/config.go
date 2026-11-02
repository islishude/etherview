// Package facilitator implements the isolated, authenticated payment settlement service.
package facilitator

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"gopkg.in/yaml.v3"
)

// Config declares ordinary settings and explicit secret file references.
type Config struct {
	Server   ServerConfig            `yaml:"server"`
	Database DatabaseConfig          `yaml:"database"`
	Auth     AuthConfig              `yaml:"auth"`
	Signers  map[string]SignerConfig `yaml:"signers"`
	Chains   []ChainConfig           `yaml:"chains"`
}
type ServerConfig struct {
	Listen            string `yaml:"listen"`
	TLSCertFile       string `yaml:"tls_cert_file"`
	TLSKeyFile        string `yaml:"tls_key_file"`
	AllowInsecureHTTP bool   `yaml:"allow_insecure_http"`
}
type DatabaseConfig struct {
	URLFile string `yaml:"url_file"`
}
type AuthConfig struct {
	BearerTokenFile string `yaml:"bearer_token_file"`
}
type RPCConfig struct {
	URL               string `yaml:"url"`
	URLFile           string `yaml:"url_file"`
	AllowInsecureHTTP bool   `yaml:"allow_insecure_http"`
}

// ConfigError exposes only a closed error code and a schema-owned field path.
type ConfigError struct{ Code, Field string }

func (e *ConfigError) Error() string       { return e.Code + ": " + e.Field }
func configError(code, field string) error { return &ConfigError{Code: code, Field: field} }

type SignerConfig struct {
	Type         string `yaml:"type"`
	Address      string `yaml:"address"`
	KeystoreFile string `yaml:"keystore_file"`
	PasswordFile string `yaml:"password_file"`
	Region       string `yaml:"region"`
	KeyARN       string `yaml:"key_arn"`
}

type ChainConfig struct {
	Network       string        `yaml:"network"`
	RPC           RPCConfig     `yaml:"rpc"`
	Signer        string        `yaml:"signer"`
	Tokens        []TokenConfig `yaml:"tokens"`
	Recipients    []string      `yaml:"recipients"`
	MaxGas        uint64        `yaml:"max_gas"`
	MaxFeePerGas  string        `yaml:"max_fee_per_gas"`
	Confirmations uint64        `yaml:"confirmations"`
}

type TokenConfig struct {
	Address string   `yaml:"address"`
	Methods []string `yaml:"methods"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	f, err := os.Open(path)
	if err != nil {
		return c, configError("config_unavailable", "config")
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return c, configError("invalid_config", "config")
	}
	d := yaml.NewDecoder(bytes.NewReader(body))
	d.KnownFields(true)
	if d.Decode(&c) != nil {
		return c, configError("invalid_config", "config")
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		return c, configError("invalid_config", "config")
	}
	return c, c.Validate()
}

func networkID(network string) (*big.Int, bool) {
	if !strings.HasPrefix(network, "eip155:") {
		return nil, false
	}
	raw := strings.TrimPrefix(network, "eip155:")
	n, ok := new(big.Int).SetString(raw, 10)
	return n, ok && n.Sign() > 0 && n.BitLen() <= 256 && n.String() == raw
}

func validAddress(raw string) bool {
	return len(raw) == 42 && strings.HasPrefix(raw, "0x") && common.IsHexAddress(raw) && common.HexToAddress(raw) != (common.Address{})
}

func (c Config) Validate() error {
	bad := configError("invalid_config", "config")
	if c.Server.Listen == "" || len(c.Chains) == 0 || len(c.Chains) > 32 || len(c.Signers) == 0 || len(c.Signers) > 32 {
		return bad
	}
	if !c.Server.AllowInsecureHTTP && (c.Server.TLSCertFile == "" || c.Server.TLSKeyFile == "") {
		return configError("invalid_config", "server")
	}
	_, port, err := net.SplitHostPort(c.Server.Listen)
	number, portErr := strconv.ParseUint(port, 10, 16)
	if err != nil || portErr != nil || number == 0 {
		return configError("invalid_config", "server.listen")
	}
	if c.Server.AllowInsecureHTTP && (c.Server.TLSCertFile != "" || c.Server.TLSKeyFile != "") {
		return configError("conflicting_config", "server")
	}

	for id, s := range c.Signers {
		bad := configError("invalid_config", "signers")
		if id == "" || !validAddress(s.Address) {
			return bad
		}
		switch s.Type {
		case "keystore":
			if s.KeystoreFile == "" || s.PasswordFile == "" || s.Region != "" || s.KeyARN != "" {
				return bad
			}
		case "aws_kms":
			parts := strings.Split(s.KeyARN, ":")
			if len(parts) != 6 || parts[0] != "arn" || parts[2] != "kms" || parts[3] != s.Region || s.Region == "" || !strings.HasPrefix(parts[5], "key/") || s.KeystoreFile != "" || s.PasswordFile != "" {
				return bad
			}
		default:
			return bad
		}
	}
	seen := map[string]bool{}
	for i, ch := range c.Chains {
		bad := configError("invalid_config", fmt.Sprintf("chains[%d]", i))
		if err := ch.RPC.validate(fmt.Sprintf("chains[%d].rpc", i)); err != nil {
			return err
		}
		if _, ok := networkID(ch.Network); !ok || seen[ch.Network] {
			return bad
		}
		seen[ch.Network] = true
		if _, ok := c.Signers[ch.Signer]; !ok {
			return bad
		}
		fee, ok := new(big.Int).SetString(ch.MaxFeePerGas, 10)
		if !ok || fee.Sign() <= 0 || fee.BitLen() > 256 || ch.MaxGas < 21000 || ch.MaxGas > 30000000 || ch.Confirmations == 0 || ch.Confirmations > 256 || len(ch.Tokens) == 0 || len(ch.Tokens) > 32 || len(ch.Recipients) == 0 || len(ch.Recipients) > 32 {
			return bad
		}
		addresses := map[common.Address]bool{}
		for _, t := range ch.Tokens {
			a := common.HexToAddress(t.Address)
			if !validAddress(t.Address) || addresses[a] || len(t.Methods) == 0 || len(t.Methods) > 2 {
				return bad
			}
			addresses[a] = true
			methods := map[string]bool{}
			for _, m := range t.Methods {
				if (m != "eip3009" && m != "permit2") || methods[m] {
					return bad
				}
				methods[m] = true
			}
		}
		addresses = map[common.Address]bool{}
		for _, r := range ch.Recipients {
			a := common.HexToAddress(r)
			if !validAddress(r) || addresses[a] {
				return bad
			}
			addresses[a] = true
		}
	}
	return nil
}

func (r RPCConfig) validate(field string) error {
	if (r.URL == "") == (r.URLFile == "") {
		return configError("invalid_source", field)
	}
	if r.URL != "" {
		return validateRPCURL(r.URL, r.AllowInsecureHTTP, field+".url")
	}
	return nil
}
func validateRPCURL(raw string, local bool, field string) error {
	u, err := url.Parse(raw)
	if len(raw) > 8192 || strings.TrimSpace(raw) != raw || err != nil || u.Hostname() == "" || u.Fragment != "" || (u.Scheme != "https" && (!local || u.Scheme != "http")) {
		return configError("invalid_rpc_url", field)
	}
	return nil
}
