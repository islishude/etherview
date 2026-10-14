//go:build runtimee2e && hardhat3e2e

package runtimee2e

import (
	"fmt"
	"testing"
)

func TestHardhat3VerificationCounts(t *testing.T) {
	complete := hardhatProxySnapshot{AddressJobs: 9, CompilerResults: 11}
	if !complete.hasExpectedVerificationCounts() {
		t.Fatal("complete Hardhat snapshot rejected")
	}
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
				if incomplete.hasExpectedVerificationCounts() {
					t.Fatal("accepted a missing or duplicate job/result")
				}
			})
		}
	}
}
