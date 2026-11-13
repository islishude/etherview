package defi

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/deficontract"
)

var errReverted = errors.New("defi_execution_reverted")

type RPC interface {
	CallContext(context.Context, any, string, ...any) error
}
type State struct {
	RPC   RPC
	Hash  common.Hash
	Calls int
	cache map[string][]byte
}

func (s *State) call(ctx context.Context, address common.Address, signature string, args ...[]byte) ([]byte, error) {
	if s.RPC == nil || s.Hash == (common.Hash{}) || address == (common.Address{}) || s.Calls >= 256 {
		return nil, deficontract.ErrUnavailable
	}
	input := append([]byte{}, crypto.Keccak256([]byte(signature))[:4]...)
	for _, arg := range args {
		if len(arg) != 32 {
			return nil, deficontract.ErrInput
		}
		input = append(input, arg...)
	}
	key := s.Hash.Hex() + address.Hex() + hex.EncodeToString(input)
	if cached, ok := s.cache[key]; ok {
		return cached, nil
	}
	s.Calls++
	var result string
	if e := s.RPC.CallContext(ctx, &result, "eth_call", map[string]any{"to": address, "data": hexutil.Bytes(input), "gas": hexutil.Uint64(2_000_000)}, rpc.BlockNumberOrHashWithHash(s.Hash, true)); e != nil {
		var rpcError rpc.Error
		if errors.As(e, &rpcError) && rpcError.ErrorCode() == 3 {
			return nil, errReverted
		}
		return nil, deficontract.ErrUnavailable
	}
	if len(result) > 2+65536*2 || !strings.HasPrefix(result, "0x") || len(result)%64 != 2 {
		return nil, deficontract.ErrIntegrity
	}
	data, e := hex.DecodeString(result[2:])
	if e != nil {
		return nil, deficontract.ErrIntegrity
	}
	if s.cache != nil && len(s.cache) < 512 && len(data) <= 4096 {
		s.cache[key] = data
	}
	return data, nil
}
func word(data []byte, index int) (*big.Int, error) {
	if index < 0 || index >= len(data)/32 {
		return nil, deficontract.ErrIntegrity
	}
	return new(big.Int).SetBytes(data[index*32 : (index+1)*32]), nil
}
func addressWord(data []byte, index int) (common.Address, error) {
	v, e := word(data, index)
	if e != nil || v.BitLen() > 160 {
		return common.Address{}, deficontract.ErrIntegrity
	}
	return common.BigToAddress(v), nil
}
func signedWord(data []byte, index int) (int32, error) {
	v, e := word(data, index)
	if e != nil {
		return 0, e
	}
	if v.Bit(255) == 1 {
		v.Sub(v, q256)
	}
	min := new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 23))
	max := new(big.Int).Neg(min)
	if v.Cmp(min) < 0 || v.Cmp(max) >= 0 {
		return 0, deficontract.ErrIntegrity
	}
	return int32(v.Int64()), nil
}
func uintWord(v *big.Int) []byte { return v.FillBytes(make([]byte, 32)) }
func intWord(v int32) []byte {
	x := big.NewInt(int64(v))
	if v < 0 {
		x.Add(x, q256)
	}
	return uintWord(x)
}
func addrWord(v common.Address) []byte { return common.LeftPadBytes(v[:], 32) }
func (s *State) uint(ctx context.Context, a common.Address, sig string, args ...[]byte) (*big.Int, error) {
	data, e := s.call(ctx, a, sig, args...)
	if e != nil {
		return nil, e
	}
	if len(data) != 32 {
		return nil, deficontract.ErrIntegrity
	}
	return word(data, 0)
}
func (s *State) address(ctx context.Context, a common.Address, sig string, args ...[]byte) (common.Address, error) {
	data, e := s.call(ctx, a, sig, args...)
	if e != nil {
		return common.Address{}, e
	}
	if len(data) != 32 {
		return common.Address{}, deficontract.ErrIntegrity
	}
	return addressWord(data, 0)
}
func (s *State) Verify(ctx context.Context, d Deployment) error {
	for _, c := range d.Contracts {
		if e := s.code(ctx, c.Address, c.CodeHash); e != nil {
			return e
		}
		if c.Implementation != (common.Address{}) {
			var raw string
			if s.RPC.CallContext(ctx, &raw, "eth_getStorageAt", c.Address, c.ImplementationSlot, rpc.BlockNumberOrHashWithHash(s.Hash, true)) != nil {
				return deficontract.ErrUnavailable
			}
			decoded, e := hexutil.Decode(raw)
			if e != nil || len(decoded) != 32 || new(big.Int).SetBytes(decoded).BitLen() > 160 || common.BytesToAddress(decoded) != c.Implementation {
				return deficontract.ErrUnsupported
			}
			if e = s.code(ctx, c.Implementation, c.ImplementationCodeHash); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *State) code(ctx context.Context, a common.Address, hash common.Hash) error {
	var raw string
	if s.RPC.CallContext(ctx, &raw, "eth_getCode", a, rpc.BlockNumberOrHashWithHash(s.Hash, true)) != nil {
		return deficontract.ErrUnavailable
	}
	if len(raw) > 2+49152*2 {
		return deficontract.ErrIntegrity
	}
	b, e := hexutil.Decode(raw)
	if e != nil || len(b) == 0 || crypto.Keccak256Hash(b) != hash {
		return deficontract.ErrUnsupported
	}
	return nil
}
func (s *State) asset(ctx context.Context, a common.Address, amount *big.Int) deficontract.Asset {
	asset := deficontract.Asset{Kind: "erc20", Address: a.Hex(), Amount: amount.String()}
	if a == (common.Address{}) {
		asset.Kind = "native"
		asset.Address = ""
		return asset
	}
	// Optional display precision must not turn an exact quantity into a failure.
	decimals, e := s.uint(ctx, a, "decimals()")
	if e == nil && decimals.IsUint64() && decimals.Uint64() <= 255 {
		n := int(decimals.Uint64())
		asset.Decimals = &n
	}
	return asset
}
