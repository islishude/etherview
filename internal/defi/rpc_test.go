package defi

import (
	"context"
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/deficontract"
)

type rpcFunction func(context.Context, any, string, ...any) error

func (f rpcFunction) CallContext(ctx context.Context, out any, method string, args ...any) error {
	return f(ctx, out, method, args...)
}

func TestVerifyProxyImplementationAtExactBlock(t *testing.T) {
	proxy, implementation := common.HexToAddress("0x1"), common.HexToAddress("0x2")
	block, slot := common.HexToHash("0xa"), common.HexToHash("0xb")
	contract := Contract{Role: "pool", Address: proxy, CodeHash: crypto.Keccak256Hash([]byte{1}), Implementation: implementation, ImplementationCodeHash: crypto.Keccak256Hash([]byte{2}), ImplementationSlot: slot}
	for _, scenario := range []string{"reviewed", "proxy_changed", "implementation_changed", "implementation_code_changed", "rpc_unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			client := rpcFunction(func(_ context.Context, out any, method string, args ...any) error {
				selector := args[len(args)-1].(rpc.BlockNumberOrHash)
				if selector.BlockHash == nil || *selector.BlockHash != block || !selector.RequireCanonical {
					t.Fatal("unbound implementation read")
				}
				if scenario == "rpc_unavailable" {
					return errors.New("private upstream error")
				}
				switch method {
				case "eth_getCode":
					address := args[0].(common.Address)
					code := byte(1)
					if address == implementation {
						code = 2
					}
					if scenario == "proxy_changed" && address == proxy || scenario == "implementation_code_changed" && address == implementation {
						code = 3
					}
					*(out.(*string)) = hexutil.Encode([]byte{code})
				case "eth_getStorageAt":
					if args[0].(common.Address) != proxy || args[1].(common.Hash) != slot {
						t.Fatal("wrong implementation slot")
					}
					address := implementation
					if scenario == "implementation_changed" {
						address = common.HexToAddress("0x3")
					}
					*(out.(*string)) = hexutil.Encode(common.LeftPadBytes(address[:], 32))
				default:
					t.Fatalf("unexpected RPC %s", method)
				}
				return nil
			})
			err := (&State{RPC: client, Hash: block}).Verify(t.Context(), Deployment{Contracts: []Contract{contract}})
			expected := deficontract.ErrUnsupported
			if scenario == "reviewed" {
				expected = nil
			}
			if scenario == "rpc_unavailable" {
				expected = deficontract.ErrUnavailable
			}
			if !errors.Is(err, expected) {
				t.Fatalf("result=%v expected=%v", err, expected)
			}
		})
	}
}

type executionRevert struct{}

func (executionRevert) Error() string  { return "execution reverted" }
func (executionRevert) ErrorCode() int { return 3 }

func TestBurnedNFTIsClosedButRPCFailureIsNot(t *testing.T) {
	manager, owner := common.HexToAddress("0x1"), common.HexToAddress("0x2")
	for _, upstream := range []error{executionRevert{}, errors.New("network failure")} {
		client := rpcFunction(func(context.Context, any, string, ...any) error { return upstream })
		position, err := Observe(t.Context(), &State{RPC: client, Hash: common.HexToHash("0xa")}, Deployment{Protocol: deficontract.UniswapV3, Contracts: []Contract{{Role: "position_manager", Address: manager}}}, Candidate{Contract: manager, Owner: owner, TokenID: "1", Kind: "liquidity-nft"}, "1")
		_, burned := upstream.(executionRevert)
		if burned && (err != nil || position.State != "closed") {
			t.Fatal("burned NFT remains active", position, err)
		}
		if !burned && (!errors.Is(err, deficontract.ErrUnavailable) || position.State == "closed") {
			t.Fatal("RPC failure was treated as absence", position, err)
		}
	}
}
