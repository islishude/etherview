package facilitator

import (
	"context"
	"crypto/ecdsa"
	"encoding/asn1"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
)

type fakeKMS struct {
	key   *ecdsa.PrivateKey
	high  bool
	fail  bool
	wrong bool
}

func (f *fakeKMS) GetPublicKey(_ context.Context, in *kms.GetPublicKeyInput, _ ...func(*kms.Options)) (*kms.GetPublicKeyOutput, error) {
	var spki struct {
		Algorithm struct{ Algorithm, Parameters asn1.ObjectIdentifier }
		Public    asn1.BitString
	}
	spki.Algorithm.Algorithm = asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}
	spki.Algorithm.Parameters = asn1.ObjectIdentifier{1, 3, 132, 0, 10}
	spki.Public = asn1.BitString{Bytes: crypto.FromECDSAPub(&f.key.PublicKey), BitLength: 520}
	b, _ := asn1.Marshal(spki)
	return &kms.GetPublicKeyOutput{KeyId: in.KeyId, PublicKey: b, KeySpec: kmstypes.KeySpecEccSecgP256k1, KeyUsage: kmstypes.KeyUsageTypeSignVerify}, nil
}
func (f *fakeKMS) Sign(ctx context.Context, in *kms.SignInput, _ ...func(*kms.Options)) (*kms.SignOutput, error) {
	if f.fail {
		return nil, errors.New("denied")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if in.MessageType != kmstypes.MessageTypeDigest || in.SigningAlgorithm != kmstypes.SigningAlgorithmSpecEcdsaSha256 {
		return nil, errors.New("wrong_sign_request")
	}
	key := f.key
	if f.wrong {
		key, _ = crypto.GenerateKey()
	}
	sig, err := crypto.Sign(in.Message, key)
	if err != nil {
		return nil, err
	}
	r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:64])
	if f.high {
		s.Sub(crypto.S256().Params().N, s)
	}
	b, _ := asn1.Marshal(struct{ R, S *big.Int }{r, s})
	return &kms.SignOutput{Signature: b, KeyId: in.KeyId}, nil
}
func TestKMSSignatureRecovery(t *testing.T) {
	key, _ := crypto.GenerateKey()
	cfg := SignerConfig{KeyARN: "arn:aws:kms:us-east-1:123456789012:key/test", Address: crypto.PubkeyToAddress(key.PublicKey).Hex()}
	for _, high := range []bool{false, true} {
		client := &fakeKMS{key: key, high: high}
		signer, err := newKMSSigner(t.Context(), cfg, client)
		if err != nil {
			t.Fatal(err)
		}
		digest := crypto.Keccak256([]byte("transaction digest"))
		sig, err := signer.Sign(t.Context(), digest)
		if err != nil {
			t.Fatal(err)
		}
		pub, err := crypto.SigToPub(digest, sig)
		if err != nil || crypto.PubkeyToAddress(*pub) != signer.Address() {
			t.Fatal("wrong recovered address")
		}
		if new(big.Int).SetBytes(sig[32:64]).Cmp(new(big.Int).Rsh(new(big.Int).Set(crypto.S256().Params().N), 1)) > 0 {
			t.Fatal("high S")
		}
		txSigner := types.LatestSignerForChainID(big.NewInt(31337))
		tx := types.NewTx(&types.DynamicFeeTx{ChainID: big.NewInt(31337), Nonce: 7, Gas: 21000, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2)})
		txSig, err := signer.Sign(t.Context(), txSigner.Hash(tx).Bytes())
		if err != nil {
			t.Fatal(err)
		}
		signed, err := tx.WithSignature(txSigner, txSig)
		if err != nil {
			t.Fatal(err)
		}
		sender, err := types.Sender(txSigner, signed)
		if err != nil || sender != signer.Address() {
			t.Fatal("KMS transaction sender mismatch", err)
		}
		client.wrong = true
		if _, err = signer.Sign(t.Context(), digest); err == nil {
			t.Fatal("accepted another key")
		}
		client.wrong = false
		client.fail = true
		if _, err = signer.Sign(t.Context(), digest); err == nil {
			t.Fatal("accepted denied signing")
		}
		client.fail = false
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err = signer.Sign(ctx, digest); err == nil {
			t.Fatal("ignored cancellation")
		}
	}
	cfg.Address = "0x0000000000000000000000000000000000000001"
	if _, err := newKMSSigner(t.Context(), cfg, &fakeKMS{key: key}); err == nil {
		t.Fatal("accepted incorrect expected address")
	}
}
func TestKeystoreSigner(t *testing.T) {
	key, _ := crypto.GenerateKey()
	data, err := keystore.EncryptKey(&keystore.Key{Id: uuid.New(), Address: crypto.PubkeyToAddress(key.PublicKey), PrivateKey: key}, "test-password", keystore.LightScryptN, keystore.LightScryptP)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := SignerConfig{Type: "keystore", Address: crypto.PubkeyToAddress(key.PublicKey).Hex(), KeystoreFile: filepath.Join(dir, "key"), PasswordFile: filepath.Join(dir, "password")}
	if err = os.WriteFile(cfg.KeystoreFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(cfg.PasswordFile, []byte("test-password\n"), 0600); err != nil {
		t.Fatal(err)
	}
	signer, err := loadSigner(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signer.Sign(t.Context(), crypto.Keccak256([]byte("test")))
	if err != nil || len(sig) != 65 {
		t.Fatal("sign failed")
	}
	if err = os.WriteFile(cfg.PasswordFile, []byte("wrong"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = loadSigner(t.Context(), cfg); err == nil {
		t.Fatal("accepted wrong password")
	}
}
