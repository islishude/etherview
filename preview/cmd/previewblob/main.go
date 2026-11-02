// previewblob prepares a signed Preview blob transaction without contacting a node.
// Private input arrives on stdin; stdout is consumed by the CLI's private journal.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/kzg4844"
	"github.com/holiman/uint256"
)

type input struct {
	ChainID string `json:"chainId"`
	Key     string `json:"key"`
	To      string `json:"to"`
	Nonce   uint64 `json:"nonce"`
	Fee     string `json:"fee"`
	Tip     string `json:"tip"`
	BlobFee string `json:"blobFee"`
	Count   int    `json:"count"`
	Marker  byte   `json:"marker"`
}
type blobInfo struct {
	VersionedHash string `json:"versionedHash"`
	SHA256        string `json:"sha256"`
	Commitment    string `json:"commitment"`
}
type output struct {
	Raw   string     `json:"raw"`
	Hash  string     `json:"hash"`
	Blobs []blobInfo `json:"blobs"`
}

func amount(s string) (*uint256.Int, error) {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.Sign() <= 0 || n.BitLen() > 256 {
		return nil, errors.New("invalid positive fee")
	}
	return uint256.MustFromBig(n), nil
}
func prepare(in input) (output, error) {
	var out output
	if in.ChainID != "48815" || !common.IsHexAddress(in.To) || in.Count < 1 || in.Count > 2 {
		return out, errors.New("invalid Preview blob request")
	}
	key, err := crypto.HexToECDSA(in.Key)
	if err != nil {
		return out, errors.New("invalid signing key")
	}
	fee, err := amount(in.Fee)
	if err != nil {
		return out, err
	}
	tip, err := amount(in.Tip)
	if err != nil {
		return out, err
	}
	blobFee, err := amount(in.BlobFee)
	if err != nil {
		return out, err
	}
	if tip.Cmp(fee) > 0 {
		return out, errors.New("tip exceeds fee")
	}
	blobs := make([]kzg4844.Blob, in.Count)
	commitments := make([]kzg4844.Commitment, in.Count)
	var proofs []kzg4844.Proof
	for i := range blobs {
		// Each field element remains canonical, while runs have identifiable content.
		for j := 31; j < len(blobs[i]); j += 32 {
			blobs[i][j] = in.Marker + byte(i)
		}
		commitments[i], err = kzg4844.BlobToCommitment(&blobs[i])
		if err != nil {
			return out, errors.New("commitment failed")
		}
		cells, proofErr := kzg4844.ComputeCellProofs(&blobs[i])
		if proofErr != nil {
			return out, errors.New("cell proofs failed")
		}
		proofs = append(proofs, cells...)
		sum := sha256.Sum256(blobs[i][:])
		hash := kzg4844.CalcBlobHashV1(sha256.New(), &commitments[i])
		out.Blobs = append(out.Blobs, blobInfo{VersionedHash: common.Hash(hash).Hex(), SHA256: hex.EncodeToString(sum[:]), Commitment: "0x" + hex.EncodeToString(commitments[i][:])})
	}
	sidecar := types.NewBlobTxSidecar(types.BlobSidecarVersion1, blobs, commitments, proofs)
	tx := types.NewTx(&types.BlobTx{ChainID: uint256.NewInt(48815), Nonce: in.Nonce, GasTipCap: tip, GasFeeCap: fee, Gas: 21000, To: common.HexToAddress(in.To), Value: new(uint256.Int), BlobFeeCap: blobFee, BlobHashes: sidecar.BlobHashes(), Sidecar: sidecar})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(big.NewInt(48815)), key)
	if err != nil {
		return out, errors.New("signing failed")
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		return out, errors.New("encoding failed")
	}
	out.Raw, out.Hash = "0x"+hex.EncodeToString(raw), signed.Hash().Hex()
	return out, nil
}
func run(r io.Reader, w io.Writer) error {
	data, err := io.ReadAll(io.LimitReader(r, 8193))
	if err != nil || len(data) > 8192 {
		return errors.New("input exceeds limit or is unreadable")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var in input
	if err := decoder.Decode(&in); err != nil {
		return errors.New("invalid input")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing input")
	}
	out, err := prepare(in)
	if err != nil {
		return err
	}
	return json.NewEncoder(w).Encode(out)
}
func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "previewblob:", err)
		os.Exit(1)
	}
}
