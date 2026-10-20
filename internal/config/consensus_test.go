package config

import (
	"errors"
	"testing"
)

func TestConsensusOptionalValidationAndRoleSecrets(t *testing.T) {
	for _, role := range []string{"api", "sync", "enrich", "all"} {
		t.Run(role, func(t *testing.T) {
			cfg := Default()
			cfg.Runtime.Roles = []string{role}
			cfg.Consensus.Enabled = true
			cfg.Consensus.URL = "http://beacon.internal:5052"
			cfg.Consensus.Headers = map[string]string{"Authorization": "Bearer secret"}
			if err := validateConsensusConfig(cfg); err != nil {
				t.Fatal(err)
			}
			env := map[string]string{"ETHERVIEW_CONSENSUS_URL_FILE": "/only-enrich", "ETHERVIEW_CONSENSUS_HEADERS_FILE": "/only-enrich"}
			lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
			calls := 0
			read := func(string) ([]byte, error) { calls++; return nil, errors.New("secret_file_unavailable") }
			err := applyConsensusEnvironment(&cfg, lookup, read)
			worker := role == "enrich" || role == "all"
			if worker != (err != nil) || worker != (calls > 0) {
				t.Fatalf("role=%s calls=%d err=%v", role, calls, err)
			}
		})
	}
	cfg := Default()
	cfg.Consensus.Blobs.Enabled = true
	if validateConsensusConfig(cfg) == nil {
		t.Fatal("blobs accepted without consensus")
	}
	cfg.Consensus.Enabled = true
	cfg.Consensus.URL = "http://beacon.internal"
	cfg.Consensus.Blobs.Endpoint = "http://archive.internal"
	cfg.Consensus.Blobs.Bucket = cfg.Adapters.S3Bucket
	if validateConsensusConfig(cfg) == nil {
		t.Fatal("shared cache bucket accepted")
	}
	cfg.Consensus.Blobs.Bucket = "durable-blobs"
	if err := validateConsensusConfig(cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Consensus.Headers = map[string]string{"Authorization": "secret\r\nInjected: true"}
	if validateConsensusConfig(cfg) == nil {
		t.Fatal("header injection accepted")
	}
	cfg.Consensus.Headers = nil
	cfg.Consensus.URL = "http://user:secret@beacon.internal"
	if validateConsensusConfig(cfg) == nil {
		t.Fatal("URL credentials accepted")
	}
}
