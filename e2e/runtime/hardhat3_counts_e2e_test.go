//go:build runtimee2e && hardhat3e2e

package runtimee2e

import (
	"fmt"
	"testing"
)

func TestHardhat3VerificationCounts(t *testing.T) {
	// Observed in both architectures of CI run 34689671531: nine Solidity
	// address jobs, twelve Vyper address jobs, one Yul and one derived result.
	complete := hardhatProxySnapshot{AddressJobs: 21, CompilerResults: 23, VyperJobs: 12, VyperResults: 12}
	t.Run("complete CI snapshot", func(t *testing.T) {
		if !complete.hasExpectedVerificationCounts(12) {
			t.Fatal("complete six-version Vyper snapshot was rejected")
		}
	})
	t.Run("obsolete totals", func(t *testing.T) {
		old := complete
		old.AddressJobs, old.CompilerResults = 11, 13
		if old.hasExpectedVerificationCounts(12) {
			t.Fatal("accepted totals from the old single-version fixture")
		}
	})
	t.Run("different matrix cardinality", func(t *testing.T) {
		expanded := hardhatProxySnapshot{AddressJobs: 23, CompilerResults: 25, VyperJobs: 14, VyperResults: 14}
		if !expanded.hasExpectedVerificationCounts(14) {
			t.Fatal("expanded matrix totals were rejected")
		}
		if expanded.hasExpectedVerificationCounts(12) {
			t.Fatal("extra jobs/results were accepted for a smaller matrix")
		}
	})
	for _, field := range []struct {
		name   string
		change func(*hardhatProxySnapshot, int64)
	}{
		{"address jobs", func(s *hardhatProxySnapshot, delta int64) { s.AddressJobs += delta }},
		{"compiler results", func(s *hardhatProxySnapshot, delta int64) { s.CompilerResults += delta }},
		{"Vyper jobs", func(s *hardhatProxySnapshot, delta int64) { s.VyperJobs += delta }},
		{"Vyper results", func(s *hardhatProxySnapshot, delta int64) { s.VyperResults += delta }},
	} {
		for _, delta := range []int64{-1, 1} {
			t.Run(fmt.Sprintf("%s/%+d", field.name, delta), func(t *testing.T) {
				incomplete := complete
				field.change(&incomplete, delta)
				if incomplete.hasExpectedVerificationCounts(12) {
					t.Fatal("accepted a missing or duplicate job/result")
				}
			})
		}
	}
}
