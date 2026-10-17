//go:build runtimee2e && verificatione2e

package runtimee2e

import (
	"context"
	"testing"
)

type vyperApplicationSnapshot struct {
	Jobs             int64                  `json:"jobs"`
	Results          int64                  `json:"results"`
	SucceededJobs    int64                  `json:"succeeded_jobs"`
	SucceededResults int64                  `json:"succeeded_results"`
	Cases            []vyperApplicationCase `json:"cases"`
}

type vyperApplicationCase struct {
	Version        string `json:"version"`
	Protocol       string `json:"protocol"`
	CompilerDigest string `json:"compiler_digest"`
	ExecutorDigest string `json:"executor_digest"`
	ABI            string `json:"abi_sha256"`
	Sources        string `json:"sources_sha256"`
	Constructor    string `json:"constructor"`
	CreationMatch  string `json:"creation_match"`
	RuntimeMatch   string `json:"runtime_match"`
}

func TestVyperProductionE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), verificationE2ETimeout)
	defer cancel()
	root := repositoryRoot(t)
	assertNativeImageArchitecture(t, ctx, dockerCommand(), valueOrDefault("IMAGE", "etherview:local"))
	imageID := verificationImageID(t, ctx)
	results, ok := runVerificationTopologies(t, ctx, func(t *testing.T, mode string) vyperApplicationSnapshot {
		h := startVerificationTopology(t, ctx, root, mode, "vyper", "e2e/runtime/vyper.compose.yaml", nil)
		cases := verifyVyperProduction(t, ctx, h, createVerificationAPIKey(t, ctx, h))
		result := vyperApplicationSnapshot{Cases: cases}
		if err := h.db.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE language='vyper' AND kind='address' AND status='succeeded') FROM verification_jobs`).Scan(&result.Jobs, &result.SucceededJobs); err != nil {
			t.Fatal(err)
		}
		if err := h.db.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE language='vyper' AND outcome_kind='verification_success') FROM verification_results`).Scan(&result.Results, &result.SucceededResults); err != nil {
			t.Fatal(err)
		}
		if !result.complete() {
			t.Fatalf("incomplete Vyper persistence: %#v", result)
		}
		return result
	})
	if ok {
		writeApplicationResult(t, ctx, root, "vyper", imageID, results)
	}
}

func (s vyperApplicationSnapshot) complete() bool {
	if s.Jobs != vyperProductionJobCount || s.Results != vyperProductionJobCount || s.SucceededJobs != s.Jobs || s.SucceededResults != s.Results || len(s.Cases) != int(vyperProductionJobCount) {
		return false
	}
	seen := make(map[string]bool)
	for _, c := range s.Cases {
		key := c.Version + "/" + c.Protocol
		if seen[key] {
			return false
		}
		seen[key] = true
	}
	for _, version := range vyperProductionVersions {
		for _, protocol := range vyperProductionProtocols {
			if !seen[version+"/"+protocol] {
				return false
			}
		}
	}
	return true
}

func TestVyperVerificationCounts(t *testing.T) {
	complete := vyperApplicationSnapshot{Jobs: vyperProductionJobCount, Results: vyperProductionJobCount, SucceededJobs: vyperProductionJobCount, SucceededResults: vyperProductionJobCount}
	for _, version := range vyperProductionVersions {
		for _, protocol := range vyperProductionProtocols {
			complete.Cases = append(complete.Cases, vyperApplicationCase{Version: version, Protocol: protocol})
		}
	}
	if !complete.complete() {
		t.Fatal("complete Vyper snapshot rejected")
	}
	for _, mutate := range []func(*vyperApplicationSnapshot){
		func(s *vyperApplicationSnapshot) { s.Jobs-- }, func(s *vyperApplicationSnapshot) { s.Jobs++ },
		func(s *vyperApplicationSnapshot) { s.Results-- }, func(s *vyperApplicationSnapshot) { s.Results++ },
		func(s *vyperApplicationSnapshot) { s.SucceededJobs-- }, func(s *vyperApplicationSnapshot) { s.SucceededResults-- },
		func(s *vyperApplicationSnapshot) { s.Cases = s.Cases[:len(s.Cases)-1] },
		func(s *vyperApplicationSnapshot) { s.Cases[0] = s.Cases[1] },
		func(s *vyperApplicationSnapshot) { s.Cases[0].Version = "unknown" },
		func(s *vyperApplicationSnapshot) { s.Cases[0].Protocol = "unknown" },
	} {
		value := complete
		value.Cases = append([]vyperApplicationCase(nil), complete.Cases...)
		mutate(&value)
		if value.complete() {
			t.Fatal("accepted missing, extra, wrong-language or duplicate Vyper results")
		}
	}
}
