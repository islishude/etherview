package etherscan

import (
	"bytes"
	"testing"
)

func TestVyperPublishedArtifactReadValidation(t *testing.T) {
	hash := bytes.Repeat([]byte{1}, 32)
	record := verifiedContractRecord{CodeHash: hash, TargetCodeHash: hash, SourceAddress: bytes.Repeat([]byte{2}, 20), Language: "vyper", CompilerVersion: "0.4.3", MatchKind: "partial", ContractName: "A", Sources: []byte(`{"A.vy":{"content":"@external\ndef value() -> uint256: return 42"}}`), Settings: []byte(`{"optimize":"codesize"}`)}
	if _, err := validateVerifiedContractRecord(record, hash); err != nil {
		t.Fatal(err)
	}
	settings, err := sourceSettings(record.Settings)
	if err != nil || settings.optimized != "1" || settings.runs != "0" {
		t.Fatalf("settings=%+v err=%v", settings, err)
	}
	record.Language = "unknown"
	if _, err := validateVerifiedContractRecord(record, hash); err == nil {
		t.Fatal("unknown language accepted")
	}
}
