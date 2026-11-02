package facilitator

import (
	"context"
	"crypto/ecdsa"
	"encoding/asn1"
	"errors"
	"math/big"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type digestSigner interface {
	Address() common.Address
	Sign(context.Context, []byte) ([]byte, error)
}
type localSigner struct{ key *ecdsa.PrivateKey }

func (s *localSigner) Address() common.Address { return crypto.PubkeyToAddress(s.key.PublicKey) }
func (s *localSigner) Sign(ctx context.Context, digest []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return crypto.Sign(digest, s.key)
}

type kmsAPI interface {
	GetPublicKey(context.Context, *kms.GetPublicKeyInput, ...func(*kms.Options)) (*kms.GetPublicKeyOutput, error)
	Sign(context.Context, *kms.SignInput, ...func(*kms.Options)) (*kms.SignOutput, error)
}
type kmsSigner struct {
	client kmsAPI
	arn    string
	public *ecdsa.PublicKey
}

func (s *kmsSigner) Address() common.Address { return crypto.PubkeyToAddress(*s.public) }
func (s *kmsSigner) Sign(ctx context.Context, digest []byte) ([]byte, error) {
	if len(digest) != 32 {
		return nil, errors.New("invalid_digest")
	}
	out, err := s.client.Sign(ctx, &kms.SignInput{KeyId: aws.String(s.arn), Message: digest, MessageType: kmstypes.MessageTypeDigest, SigningAlgorithm: kmstypes.SigningAlgorithmSpecEcdsaSha256})
	if err != nil || out == nil || aws.ToString(out.KeyId) != s.arn {
		return nil, errors.New("kms_sign_failed")
	}
	var rs struct{ R, S *big.Int }
	rest, err := asn1.Unmarshal(out.Signature, &rs)
	n := crypto.S256().Params().N
	if err != nil || len(rest) != 0 || rs.R == nil || rs.S == nil || rs.R.Sign() <= 0 || rs.S.Sign() <= 0 || rs.R.Cmp(n) >= 0 || rs.S.Cmp(n) >= 0 {
		return nil, errors.New("invalid_kms_signature")
	}
	if rs.S.Cmp(new(big.Int).Rsh(new(big.Int).Set(n), 1)) > 0 {
		rs.S.Sub(n, rs.S)
	}
	sig := make([]byte, 65)
	rs.R.FillBytes(sig[:32])
	rs.S.FillBytes(sig[32:64])
	for v := range 2 {
		sig[64] = byte(v)
		pub, e := crypto.SigToPub(digest, sig)
		if e == nil && crypto.PubkeyToAddress(*pub) == s.Address() {
			return sig, nil
		}
	}
	return nil, errors.New("kms_signature_address_mismatch")
}

func newKMSSigner(ctx context.Context, c SignerConfig, client kmsAPI) (digestSigner, error) {
	out, err := client.GetPublicKey(ctx, &kms.GetPublicKeyInput{KeyId: aws.String(c.KeyARN)})
	if err != nil || out == nil || out.KeySpec != kmstypes.KeySpecEccSecgP256k1 || out.KeyUsage != kmstypes.KeyUsageTypeSignVerify || aws.ToString(out.KeyId) != c.KeyARN {
		return nil, errors.New("kms_key_invalid")
	}
	// crypto/x509 does not support secp256k1. Decode SPKI and validate both OIDs.
	var spki struct {
		Algorithm struct {
			Algorithm  asn1.ObjectIdentifier
			Parameters asn1.ObjectIdentifier
		}
		Public asn1.BitString
	}
	rest, err := asn1.Unmarshal(out.PublicKey, &spki)
	if err != nil || len(rest) != 0 || !spki.Algorithm.Algorithm.Equal(asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}) || !spki.Algorithm.Parameters.Equal(asn1.ObjectIdentifier{1, 3, 132, 0, 10}) || spki.Public.BitLength != 520 {
		return nil, errors.New("kms_public_key_invalid")
	}
	pub, err := crypto.UnmarshalPubkey(spki.Public.Bytes)
	if err != nil {
		return nil, errors.New("kms_public_key_invalid")
	}
	s := &kmsSigner{client: client, arn: c.KeyARN, public: pub}
	if s.Address() != common.HexToAddress(c.Address) {
		return nil, errors.New("signer_address_mismatch")
	}
	return s, nil
}

func loadSigner(ctx context.Context, c SignerConfig, files signerFiles) (digestSigner, error) {
	if c.Type == "aws_kms" {
		cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(c.Region))
		if err != nil {
			return nil, errors.New("kms_config_failed")
		}
		return newKMSSigner(ctx, c, kms.NewFromConfig(cfg))
	}
	key, err := keystore.DecryptKey(files.key, strings.TrimRight(string(files.password), "\r\n"))
	if err != nil {
		return nil, errors.New("keystore_unlock_failed")
	}
	s := &localSigner{key: key.PrivateKey}
	if s.Address() != common.HexToAddress(c.Address) {
		return nil, errors.New("signer_address_mismatch")
	}
	return s, nil
}
