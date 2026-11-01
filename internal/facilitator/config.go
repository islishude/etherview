// Package facilitator implements the isolated, authenticated payment settlement service.
package facilitator

import (
	"bytes"
	"errors"
	"io"
	"math/big"
	"net/url"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"gopkg.in/yaml.v3"
)

// Config contains only file references for secrets, never secret values.
type Config struct {
	Listen          string                  `yaml:"listen"`
	DatabaseURLFile string                  `yaml:"database_url_file"`
	BearerTokenFile string                  `yaml:"bearer_token_file"`
	TLSCertFile     string                  `yaml:"tls_cert_file"`
	TLSKeyFile      string                  `yaml:"tls_key_file"`
	UnsafeLocalHTTP bool                    `yaml:"unsafe_local_http"`
	Signers         map[string]SignerConfig `yaml:"signers"`
	Chains          []ChainConfig           `yaml:"chains"`
}

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
	RPCURLFile    string        `yaml:"rpc_url_file"`
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
		return c, errors.New("config_unavailable")
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return c, errors.New("invalid_config")
	}
	d := yaml.NewDecoder(bytes.NewReader(body))
	d.KnownFields(true)
	if d.Decode(&c) != nil {
		return c, errors.New("invalid_config")
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		return c, errors.New("invalid_config")
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
	bad := errors.New("invalid_config")
	if c.Listen == "" || c.DatabaseURLFile == "" || c.BearerTokenFile == "" || len(c.Chains) == 0 || len(c.Chains) > 32 || len(c.Signers) == 0 || len(c.Signers) > 32 {
		return bad
	}
	if !c.UnsafeLocalHTTP && (c.TLSCertFile == "" || c.TLSKeyFile == "") {
		return bad
	}
	for id, s := range c.Signers {
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
	for _, ch := range c.Chains {
		if _, ok := networkID(ch.Network); !ok || seen[ch.Network] {
			return bad
		}
		seen[ch.Network] = true
		if _, ok := c.Signers[ch.Signer]; !ok {
			return bad
		}
		fee, ok := new(big.Int).SetString(ch.MaxFeePerGas, 10)
		if !ok || fee.Sign() <= 0 || fee.BitLen() > 256 || ch.MaxGas < 21000 || ch.MaxGas > 30000000 || ch.Confirmations == 0 || ch.Confirmations > 256 || ch.RPCURLFile == "" || len(ch.Tokens) == 0 || len(ch.Tokens) > 32 || len(ch.Recipients) == 0 || len(ch.Recipients) > 32 {
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

func secretFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("secret_unavailable")
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(b) == 0 || int64(len(b)) > limit {
		return nil, errors.New("invalid_secret")
	}
	return b, nil
}

func rpcURL(path string, local bool) (string, error) {
	b, err := secretFile(path, 8192)
	if err != nil {
		return "", err
	}
	raw := strings.TrimSpace(string(b))
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Fragment != "" || (u.Scheme != "https" && (!local || u.Scheme != "http")) {
		return "", errors.New("invalid_rpc_url")
	}
	return raw, nil
}
