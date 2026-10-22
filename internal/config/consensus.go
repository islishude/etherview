package config

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

type ConsensusConfig struct {
	Enabled          bool              `yaml:"enabled"`
	URL              string            `yaml:"url"`
	Headers          map[string]string `yaml:"headers"`
	PollInterval     time.Duration     `yaml:"poll_interval"`
	RequestTimeout   time.Duration     `yaml:"request_timeout"`
	WorkerCount      int               `yaml:"worker_count"`
	MaxResponseBytes int64             `yaml:"max_response_bytes"`
	Blobs            BlobArchiveConfig `yaml:"blobs"`
}

type BlobArchiveConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Endpoint     string `yaml:"endpoint"`
	Bucket       string `yaml:"bucket"`
	Region       string `yaml:"region"`
	AccessKey    string `yaml:"access_key"`
	SecretKey    string `yaml:"secret_key"`
	SessionToken string `yaml:"session_token"`
	PathStyle    bool   `yaml:"path_style"`
}

func DefaultConsensusConfig() ConsensusConfig {
	return ConsensusConfig{PollInterval: 12 * time.Second, RequestTimeout: 10 * time.Second, WorkerCount: 2, MaxResponseBytes: 32 << 20}
}

func validateConsensusConfig(c Config) error {
	v := c.Consensus
	if v.Blobs.Enabled && !v.Enabled {
		return errors.New("consensus blobs require consensus.enabled")
	}
	if !v.Enabled {
		return nil
	}
	roles, err := NormalizeRoles(c.Runtime.Roles)
	if err != nil {
		return err
	}
	if v.PollInterval < time.Second || v.PollInterval > time.Hour || v.RequestTimeout < time.Second || v.RequestTimeout > time.Minute || v.WorkerCount < 1 || v.WorkerCount > 16 || v.MaxResponseBytes < 1<<20 || v.MaxResponseBytes > 128<<20 {
		return errors.New("consensus limits are outside supported bounds")
	}
	if slices.Contains(roles, "enrich") {
		u, parseErr := url.Parse(v.URL)
		if parseErr != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("consensus.url must be an HTTP(S) URL without credentials, query or fragment")
		}
		if len(v.Headers) > 16 {
			return errors.New("consensus headers exceed limit")
		}
		for k, value := range v.Headers {
			if !validConsensusHeader(k, value) {
				return errors.New("consensus authentication header is invalid")
			}
		}
	}
	if v.Blobs.Enabled && (slices.Contains(roles, "api") || slices.Contains(roles, "enrich")) {
		b := v.Blobs
		u, parseErr := url.Parse(b.Endpoint)
		if parseErr != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || b.Bucket == "" || b.Bucket == c.Adapters.S3Bucket {
			return errors.New("blob archive requires an HTTP(S) object endpoint and a dedicated bucket")
		}
		if (b.AccessKey == "") != (b.SecretKey == "") || (b.SessionToken != "" && b.AccessKey == "") {
			return errors.New("blob archive credentials are incomplete")
		}
	}
	return nil
}

func validConsensusHeader(k, v string) bool {
	if k == "" || len(k) > 128 || len(v) > 4096 || strings.ContainsAny(v, "\r\n") {
		return false
	}
	for _, c := range k {
		valid := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)
		if !valid {
			return false
		}
	}
	switch http.CanonicalHeaderKey(k) {
	case "Host", "Content-Length", "Transfer-Encoding", "Connection", "Accept", "Accept-Encoding":
		return false
	}
	return true
}

func applyConsensusEnvironment(c *Config, lookup func(string) (string, bool), readFile func(string) ([]byte, error)) error {
	v := &c.Consensus
	if err := setBool(lookup, "CONSENSUS_ENABLED", &v.Enabled); err != nil {
		return err
	}
	if err := setBool(lookup, "CONSENSUS_BLOBS_ENABLED", &v.Blobs.Enabled); err != nil {
		return err
	}
	roles, err := NormalizeRoles(c.Runtime.Roles)
	if err != nil {
		return err
	}
	if err := setDuration(lookup, "CONSENSUS_POLL_INTERVAL", &v.PollInterval); err != nil {
		return err
	}
	if err := setDuration(lookup, "CONSENSUS_REQUEST_TIMEOUT", &v.RequestTimeout); err != nil {
		return err
	}
	if err := setInt(lookup, "CONSENSUS_WORKER_COUNT", &v.WorkerCount); err != nil {
		return err
	}
	if err := setInt64(lookup, "CONSENSUS_MAX_RESPONSE_BYTES", &v.MaxResponseBytes); err != nil {
		return err
	}
	if v.Enabled && slices.Contains(roles, "enrich") {
		address, e := lookupValueOrFile("CONSENSUS_URL", lookup, readFile)
		if e != nil {
			return e
		}
		if address != "" {
			v.URL = address
		}
		headers, e := lookupValueOrFile("CONSENSUS_HEADERS", lookup, readFile)
		if e != nil {
			return e
		}
		if headers != "" {
			if len(headers) > 16<<10 || json.Unmarshal([]byte(headers), &v.Headers) != nil {
				return errors.New("consensus headers must be a bounded string map")
			}
		}
	}
	if v.Blobs.Enabled && (slices.Contains(roles, "api") || slices.Contains(roles, "enrich")) {
		for key, target := range map[string]*string{"ENDPOINT": &v.Blobs.Endpoint, "BUCKET": &v.Blobs.Bucket, "REGION": &v.Blobs.Region, "ACCESS_KEY": &v.Blobs.AccessKey, "SECRET_KEY": &v.Blobs.SecretKey, "SESSION_TOKEN": &v.Blobs.SessionToken} {
			value, e := lookupValueOrFile("CONSENSUS_BLOBS_"+key, lookup, readFile)
			if e != nil {
				return e
			}
			if value != "" {
				*target = value
			}
		}
		return setBool(lookup, "CONSENSUS_BLOBS_PATH_STYLE", &v.Blobs.PathStyle)
	}
	return nil
}
