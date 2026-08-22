package derivedverify

import (
	"encoding/json"
	"testing"

	"github.com/islishude/etherview/internal/verify"
)

func TestClassifyTraceRequiresUniqueCreationAndRuntimeMatch(t *testing.T) {
	t.Parallel()
	first := restoredCandidate(t, "First", "0x6001", "0x6002")
	second := restoredCandidate(t, "Second", "0x6001", "0x6002")
	for _, test := range []struct {
		name       string
		candidates []verify.CandidateArtifact
		creation   []byte
		runtime    []byte
		want       string
		unique     bool
	}{
		{name: "matched", candidates: []verify.CandidateArtifact{first}, creation: []byte{0x60, 0x01}, runtime: []byte{0x60, 0x02}, want: "matched", unique: true},
		{name: "pending runtime", candidates: []verify.CandidateArtifact{first}, creation: []byte{0x60, 0x01}, want: "pending_runtime"},
		{name: "no creation match", candidates: []verify.CandidateArtifact{first}, creation: []byte{0x60, 0x03}, runtime: []byte{0x60, 0x02}, want: "no_match"},
		{name: "runtime mismatch", candidates: []verify.CandidateArtifact{first}, creation: []byte{0x60, 0x01}, runtime: []byte{0x60, 0x04}, want: "runtime_mismatch"},
		{name: "ambiguous FQN", candidates: []verify.CandidateArtifact{first, second}, creation: []byte{0x60, 0x01}, runtime: []byte{0x60, 0x02}, want: "ambiguous"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			status, unique, err := classifyTrace(test.candidates, traceCandidate{
				CreationCode: test.creation, RuntimeCode: test.runtime,
			})
			if err != nil || status != test.want || unique != test.unique {
				t.Fatalf("status=%q unique=%t error=%v", status, unique, err)
			}
		})
	}
}

func restoredCandidate(
	t *testing.T,
	name, creation, runtime string,
) verify.CandidateArtifact {
	t.Helper()
	candidate, err := verify.RestoreCandidateArtifact(verify.CandidateArtifact{
		FileName: "Factory.sol", ContractName: name,
		Language: verify.LanguageSolidity, CompilerVersion: "0.8.30+commit.73712a01",
		CreationBytecode: creation, RuntimeBytecode: runtime,
		ABI: json.RawMessage(`[]`), CompilationArtifacts: json.RawMessage(`{}`),
		CreationCodeArtifacts: json.RawMessage(`{}`), RuntimeCodeArtifacts: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}
