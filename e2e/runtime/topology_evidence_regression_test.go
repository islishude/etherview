//go:build runtimee2e

package runtimee2e

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type topologyTestSnapshot struct {
	Count  int64
	Nested struct{ OK bool }
}

func TestTopologyEvidenceModes(t *testing.T) {
	for input, expected := range map[string][]string{
		"": {"monolith", "distributed"}, "monolith": {"monolith"}, "distributed": {"distributed"},
	} {
		got, err := selectedTopologyModes(input)
		if err != nil || !reflect.DeepEqual(got, expected) {
			t.Fatalf("%q: %v, %v", input, got, err)
		}
	}
	for _, invalid := range []string{"split", "both", "MONOLITH", " distributed "} {
		if _, err := selectedTopologyModes(invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
}

func TestTopologyEvidencePair(t *testing.T) {
	identity := topologyEvidence{Suite: "hardhat3", Arch: "amd64", Run: "123", Revision: "abc", Image: "sha256:" + strings.Repeat("a", 64), Client: "sha256:" + strings.Repeat("b", 64)}
	marshal := func(v any) []byte {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	snapshot := topologyTestSnapshot{Count: 9007199254740993}
	snapshot.Nested.OK = true
	pair := func() []topologyEvidence {
		a, b := identity, identity
		a.Mode = "monolith"
		b.Mode = "distributed"
		a.Snapshot = marshal(snapshot)
		b.Snapshot = marshal(snapshot)
		return []topologyEvidence{a, b}
	}
	validate := func(p []topologyEvidence) error {
		records := make([][]byte, len(p))
		for i := range p {
			records[i] = marshal(p[i])
		}
		return validateTopologyPair[topologyTestSnapshot](records, identity)
	}
	if err := validate(pair()); err != nil {
		t.Fatal(err)
	}
	rebuilt := pair()
	rebuilt[1].Image = "sha256:" + strings.Repeat("c", 64)
	rebuilt[1].Client = "sha256:" + strings.Repeat("d", 64)
	if err := validate(rebuilt); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func([]topologyEvidence) []topologyEvidence{
		"missing":   func(p []topologyEvidence) []topologyEvidence { return p[:1] },
		"extra":     func(p []topologyEvidence) []topologyEvidence { return append(p, p[0]) },
		"duplicate": func(p []topologyEvidence) []topologyEvidence { p[1] = p[0]; return p },
		"arch":      func(p []topologyEvidence) []topologyEvidence { p[1].Arch = "arm64"; return p },
		"suite":     func(p []topologyEvidence) []topologyEvidence { p[1].Suite = "foundry"; return p },
		"run":       func(p []topologyEvidence) []topologyEvidence { p[1].Run = "old"; return p },
		"revision":  func(p []topologyEvidence) []topologyEvidence { p[1].Revision = "old"; return p },
		"image":     func(p []topologyEvidence) []topologyEvidence { p[1].Image = "different"; return p },
		"client":    func(p []topologyEvidence) []topologyEvidence { p[1].Client = "different"; return p },
		"parity": func(p []topologyEvidence) []topologyEvidence {
			s := snapshot
			s.Count++
			p[1].Snapshot = marshal(s)
			return p
		},
		"missing field": func(p []topologyEvidence) []topologyEvidence {
			p[1].Snapshot = []byte(`{"Count":9007199254740993}`)
			return p
		},
		"missing nested field": func(p []topologyEvidence) []topologyEvidence {
			p[1].Snapshot = []byte(`{"Count":9007199254740993,"Nested":{}}`)
			return p
		},
		"null": func(p []topologyEvidence) []topologyEvidence { p[1].Snapshot = []byte(`null`); return p },
		"empty": func(p []topologyEvidence) []topologyEvidence {
			p[1].Snapshot = marshal(topologyTestSnapshot{})
			return p
		},
		"unknown": func(p []topologyEvidence) []topologyEvidence {
			p[1].Snapshot = []byte(`{"Count":1,"extra":true}`)
			return p
		},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			if err := validate(change(pair())); err == nil {
				t.Fatal("accepted invalid evidence")
			}
		})
	}
	for _, invalid := range [][]byte{append(marshal(pair()[0]), []byte(` {}`)...), []byte(`{"unexpected":true}`)} {
		if err := validateTopologyPair[topologyTestSnapshot]([][]byte{invalid, marshal(pair()[1])}, identity); err == nil {
			t.Fatal("accepted invalid JSON")
		}
	}
}
