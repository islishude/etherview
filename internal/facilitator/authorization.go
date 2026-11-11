package facilitator

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/x402-foundation/x402/go/v2/mechanisms/evm"
	exact "github.com/x402-foundation/x402/go/v2/mechanisms/evm/exact/facilitator"
)

type authorizationProof struct {
	method                          string
	payer, recipient, token, target common.Address
	amount, after, before, nonce    *big.Int
	nonceBytes                      common.Hash
	calldata                        [][]byte
}

// Local authorization checks deliberately do not compare the expiry with wall
// time: a previously executed payment is checked against its execution block.
func proofFor(in paymentRequest) (*authorizationProof, error) {
	bad := errors.New("invalid_authorization")
	chain, ok := networkID(in.PaymentRequirements.Network)
	if !ok {
		return nil, bad
	}
	p := &authorizationProof{token: common.HexToAddress(in.PaymentRequirements.Asset), recipient: common.HexToAddress(in.PaymentRequirements.PayTo)}
	p.method, _ = defaultString(in.PaymentRequirements.Extra, "assetTransferMethod", "eip3009")
	var digest, sig []byte
	var err error
	switch p.method {
	case "eip3009":
		payload, e := evm.PayloadFromMap(in.PaymentPayload.Payload)
		if e != nil {
			return nil, bad
		}
		a, e := exact.ParseEIP3009Authorization(payload.Authorization)
		if e != nil {
			return nil, bad
		}
		name, nok := in.PaymentRequirements.Extra["name"].(string)
		version, vok := in.PaymentRequirements.Extra["version"].(string)
		if !nok || !vok || name == "" || version == "" {
			return nil, bad
		}
		p.payer = a.From
		p.amount = a.Value
		p.after = a.ValidAfter
		p.before = a.ValidBefore
		p.nonceBytes = a.Nonce
		p.target = p.token
		if a.To != p.recipient {
			return nil, bad
		}
		digest, err = evm.HashEIP3009Authorization(payload.Authorization, chain, p.token.Hex(), name, version)
		if err != nil {
			return nil, bad
		}
		sig, err = evm.HexToBytes(payload.Signature)
		if err != nil || len(sig) != 65 {
			return nil, bad
		}
		v := sig[64]
		if v < 27 {
			v += 27
		}
		var r, s [32]byte
		copy(r[:], sig[:32])
		copy(s[:], sig[32:64])
		data, e := packAuthorization(evm.TransferWithAuthorizationVRSABI, "transferWithAuthorization", a.From, a.To, a.Value, a.ValidAfter, a.ValidBefore, a.Nonce, v, r, s)
		if e != nil {
			return nil, bad
		}
		p.calldata = append(p.calldata, data)
		data, e = packAuthorization(evm.TransferWithAuthorizationBytesABI, "transferWithAuthorization", a.From, a.To, a.Value, a.ValidAfter, a.ValidBefore, a.Nonce, sig)
		if e != nil {
			return nil, bad
		}
		p.calldata = append(p.calldata, data)
	case "permit2":
		payload, e := evm.Permit2PayloadFromMap(in.PaymentPayload.Payload)
		if e != nil {
			return nil, bad
		}
		a, e := exact.BuildPermit2SettleArgs(payload)
		if e != nil {
			return nil, bad
		}
		if !strings.EqualFold(payload.Permit2Authorization.Spender, evm.X402ExactPermit2ProxyAddress) || a.Permit.Permitted.Token != p.token || a.Witness.To != p.recipient {
			return nil, bad
		}
		p.payer = a.Owner
		p.amount = a.Permit.Permitted.Amount
		p.after = a.Witness.ValidAfter
		p.before = a.Permit.Deadline
		p.nonce = a.Permit.Nonce
		p.target = common.HexToAddress(evm.X402ExactPermit2ProxyAddress)
		digest, err = evm.HashPermit2Authorization(payload.Permit2Authorization, chain)
		if err != nil {
			return nil, bad
		}
		sig = a.Signature
		data, e := packAuthorization(evm.X402ExactPermit2ProxySettleABI, "settle", a.Permit, a.Owner, a.Witness, a.Signature)
		if e != nil {
			return nil, bad
		}
		p.calldata = append(p.calldata, data)
	default:
		return nil, bad
	}
	for _, n := range []*big.Int{p.amount, p.after, p.before} {
		if n == nil || n.Sign() < 0 || n.BitLen() > 256 {
			return nil, bad
		}
	}
	if p.amount.Sign() == 0 || p.amount.String() != in.PaymentRequirements.Amount || p.before.Cmp(p.after) <= 0 || len(sig) != 65 {
		return nil, bad
	}
	normalized := bytes.Clone(sig)
	if normalized[64] >= 27 {
		normalized[64] -= 27
	}
	if !crypto.ValidateSignatureValues(normalized[64], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:64]), true) {
		return nil, bad
	}
	pub, e := crypto.SigToPub(digest, normalized)
	if e != nil || crypto.PubkeyToAddress(*pub) != p.payer {
		return nil, bad
	}
	return p, nil
}

func packAuthorization(raw []byte, method string, args ...any) ([]byte, error) {
	a, err := abi.JSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	return a.Pack(method, args...)
}

func (p *authorizationProof) consumed(ctx context.Context, ch *chainRuntime, block common.Hash) (bool, error) {
	var data []byte
	to := p.token
	if p.method == "eip3009" {
		data = append(crypto.Keccak256([]byte("authorizationState(address,bytes32)"))[:4], common.LeftPadBytes(p.payer.Bytes(), 32)...)
		data = append(data, p.nonceBytes.Bytes()...)
	} else {
		to = common.HexToAddress(evm.PERMIT2Address)
		word := new(big.Int).Rsh(new(big.Int).Set(p.nonce), 8)
		data = append(crypto.Keccak256([]byte("nonceBitmap(address,uint256)"))[:4], common.LeftPadBytes(p.payer.Bytes(), 32)...)
		data = append(data, common.LeftPadBytes(word.Bytes(), 32)...)
	}
	out, err := ch.adapter.client.CallContractAtHash(ctx, ethereum.CallMsg{To: &to, Data: data}, block)
	if err != nil {
		return false, err
	}
	if len(out) != 32 {
		return false, errors.New("invalid_nonce_state")
	}
	n := new(big.Int).SetBytes(out)
	if p.method == "eip3009" {
		if n.BitLen() > 1 {
			return false, errors.New("invalid_nonce_state")
		}
		return n.Sign() != 0, nil
	}
	return n.Bit(int(new(big.Int).And(p.nonce, big.NewInt(255)).Int64())) == 1, nil
}

func (p *authorizationProof) matchesTransaction(tx *types.Transaction, chain *big.Int) bool {
	if tx.To() == nil || *tx.To() != p.target || tx.ChainId().Cmp(chain) != 0 || tx.Value().Sign() != 0 {
		return false
	}
	for _, data := range p.calldata {
		if bytes.Equal(data, tx.Data()) {
			return true
		}
	}
	return false
}
