package verify

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

func TestRunnerFrameRoundTripAndChecksum(t *testing.T) {
	t.Parallel()
	compiler := []byte("compiler executable")
	frame := RunnerFrame{
		Compiler: compiler,
		Input:    []byte(`{"language":"Solidity"}`),
		Version:  "0.8.30+commit.73712a01",
		Digest:   sha256.Sum256(compiler),
	}
	var encoded bytes.Buffer
	if err := WriteRunnerFrame(&encoded, frame); err != nil {
		t.Fatal(err)
	}
	decoded, err := ReadRunnerFrame(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.Compiler, frame.Compiler) ||
		!bytes.Equal(decoded.Input, frame.Input) ||
		decoded.Version != frame.Version || decoded.Digest != frame.Digest {
		t.Fatalf("decoded frame = %#v", decoded)
	}
	tampered := append([]byte(nil), encoded.Bytes()...)
	tampered[len(tampered)-len(frame.Input)-1] ^= 1
	if _, err := ReadRunnerFrame(bytes.NewReader(tampered)); err == nil {
		t.Fatal("expected compiler checksum rejection")
	}
}
