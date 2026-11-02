package main

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestPrepare(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	in := input{ChainID: "48815", Key: hex.EncodeToString(crypto.FromECDSA(key)), To: common.Address{1}.Hex(), Fee: "2000000000", Tip: "1000000000", BlobFee: "1000000000", Count: 2, Marker: 7}
	out, err := prepare(in)
	if err != nil {
		t.Fatal(err)
	}
	var tx types.Transaction
	if err := tx.UnmarshalBinary(common.FromHex(out.Raw)); err != nil {
		t.Fatal(err)
	}
	if tx.Hash().Hex() != out.Hash || tx.Type() != types.BlobTxType || len(tx.BlobHashes()) != 2 || tx.BlobTxSidecar().Version != types.BlobSidecarVersion1 {
		t.Fatal("wrong transaction identity or sidecar")
	}
	from, err := types.Sender(types.LatestSignerForChainID(big.NewInt(48815)), &tx)
	if err != nil || from != crypto.PubkeyToAddress(key.PublicKey) {
		t.Fatal("wrong signer")
	}
	if out.Blobs[0].SHA256 == out.Blobs[1].SHA256 || out.Blobs[0].VersionedHash != tx.BlobHashes()[0].Hex() {
		t.Fatal("wrong blob identities")
	}
	in.ChainID = "1"
	if _, err = prepare(in); err == nil {
		t.Fatal("accepted external chain")
	}
}
func TestInputBounds(t *testing.T) {
	for _, s := range []string{`{"key":"secret","unknown":1}`, `{}`, `{}` + strings.Repeat(" ", 8192) + `{}`} {
		var out bytes.Buffer
		if err := run(strings.NewReader(s), &out); err == nil || strings.Contains(err.Error(), "secret") || out.Len() != 0 {
			t.Fatal("accepted or leaked bad input")
		}
	}
}
