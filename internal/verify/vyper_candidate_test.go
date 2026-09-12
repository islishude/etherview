package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVyperFixtureChecksums(t *testing.T) {
	const root = "testdata/compiler/vyper"
	raw, err := os.ReadFile(root + "/SHA256SUMS")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || filepath.Base(fields[1]) != fields[1] || seen[fields[1]] {
			t.Fatal("invalid fixture checksum manifest")
		}
		contents, err := os.ReadFile(filepath.Join(root, fields[1]))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(contents)
		if hex.EncodeToString(sum[:]) != fields[0] {
			t.Fatalf("fixture checksum changed: %s", fields[1])
		}
		seen[fields[1]] = true
	}
	entries, err := filepath.Glob(root + "/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(seen) {
		t.Fatal("fixture manifest is incomplete")
	}
}

func TestVyperPinnedCandidateFixtures(t *testing.T) {
	for _, name := range []string{"plain", "immutable", "no_metadata", "module", "interface", "dispatch", "module_immutable", "module_type", "builtin", "abi_interface", "filename"} {
		t.Run(name, func(t *testing.T) {
			first, err := os.ReadFile("testdata/compiler/vyper/" + name + ".input.output.json")
			if err != nil {
				t.Fatal(err)
			}
			second, err := os.ReadFile("testdata/compiler/vyper/" + name + ".modified.output.json")
			if err != nil {
				t.Fatal(err)
			}
			candidates, err := ExtractCandidatesV2(first, second, LanguageVyper, VyperCompilerVersion)
			if err != nil {
				t.Fatal(err)
			}
			if len(candidates) != 1 {
				t.Fatalf("candidates=%d", len(candidates))
			}
			candidate := candidates[0]
			pair := BytecodePair{Creation: candidate.CreationBytecode, Runtime: candidate.RuntimeBytecode}
			if name == "immutable" || name == "module_immutable" || name == "module_type" {
				word := strings.Repeat("00", 12) + strings.Repeat("11", 20)
				pair.Creation += word
				pair.Runtime = pair.Runtime[:len(pair.Runtime)-64] + word
			}
			matches, err := VerifyCandidateArtifacts(candidates, pair, "", true)
			if err != nil || len(matches) != 1 {
				t.Fatalf("match=%v err=%v", matches, err)
			}
			if matches[0].Runtime.MatchType != VerificationMatchPartial {
				t.Fatal("metadata-free runtime claimed full")
			}
			expected := VerificationMatchFull
			if name == "no_metadata" {
				expected = VerificationMatchPartial
			}
			if matches[0].Creation == nil || matches[0].Creation.MatchType != expected {
				t.Fatal("incorrect creation evidence")
			}
			pair.Runtime += "00"
			matches, err = VerifyCandidateArtifacts(candidates, pair, "", true)
			if err != nil || len(matches) != 0 {
				t.Fatalf("unexpected suffix accepted: %v", err)
			}
		})
	}
}

func TestVyperInputBoundaries(t *testing.T) {
	valid := `{"language":"Vyper","sources":{"A.vy":{"content":"@external\ndef value() -> uint256: return 42"}},"settings":{}}`
	for _, input := range []string{
		strings.Replace(valid, `"settings":{}`, `"settings":{"experimentalCodegen":true}`, 1),
		strings.Replace(valid, `"settings":{}`, `"settings":{"optimize":true}`, 1),
		strings.Replace(valid, `"settings":{}`, `"settings":{"search_paths":["/etc"]}`, 1),
		strings.Replace(valid, `"content":`, `"urls":`, 1),
		strings.Replace(valid, `"language":"Vyper"`, `"language":"Vyper","language":"Vyper"`, 1),
	} {
		if _, err := PrepareVyperStandardJSON(json.RawMessage(input), "A.vy", VyperCompilerVersion, 1<<20); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	for _, target := range []string{"", "../A.vy", "/A.vy", "A.sol", "B.vy"} {
		if _, err := PrepareVyperStandardJSON(json.RawMessage(valid), target, VyperCompilerVersion, 1<<20); err == nil {
			t.Fatalf("accepted target %s", target)
		}
	}
	if _, err := PrepareVyperStandardJSON(json.RawMessage(valid), "A.vy", VyperCompilerVersion, 1<<20); err != nil {
		t.Fatal(err)
	}
}

func TestVyperImmutableLayoutRejectsUndeclaredRanges(t *testing.T) {
	for _, raw := range []string{
		`{"code_layout":{"a":{"offset":32,"length":32,"type":"address"}}}`,
		`{"code_layout":{"a":{"bogus":0,"length":32,"type":"address"}}}`,
		`{"code_layout":{"a":{"offset":null,"length":32,"type":"address"}}}`,
		`{"code_layout":{"a":{"offset":0,"length":32,"type":"address"},"b":{"offset":0,"length":32,"type":"address"}}}`,
		`{"code_layout":{"a":{"offset":0,"length":31,"type":"address"}}}`,
		`{"code_layout":{"a":{"offset":18446744073709551615,"length":32,"type":"address"}}}`,
	} {
		if _, _, err := vyperImmutableLayout(json.RawMessage(raw), 42); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
