package defi

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/deficontract"
)

type callFixture struct {
	t       *testing.T
	hash    common.Hash
	answers map[string][]byte
	seen    int
}

func key(a common.Address, sig string) string {
	return a.Hex() + ":" + hex.EncodeToString(crypto.Keccak256([]byte(sig))[:4])
}
func (f *callFixture) CallContext(_ context.Context, result any, method string, args ...any) error {
	f.seen++
	if method == "eth_getCode" {
		selector := args[1].(rpc.BlockNumberOrHash)
		if selector.BlockHash == nil || *selector.BlockHash != f.hash || !selector.RequireCanonical {
			f.t.Fatal("unbound code")
		}
		*(result.(*string)) = "0x01"
		return nil
	}
	if method != "eth_call" {
		return errors.New("unexpected method")
	}
	selector, ok := args[1].(rpc.BlockNumberOrHash)
	if !ok || selector.BlockHash == nil || *selector.BlockHash != f.hash || !selector.RequireCanonical {
		f.t.Fatal("unbound RPC call")
	}
	call := args[0].(map[string]any)
	data := call["data"].(hexutil.Bytes)
	a := call["to"].(common.Address)
	answer, ok := f.answers[a.Hex()+":"+hex.EncodeToString(data[:4])]
	if !ok {
		return errors.New("private error https://secret.invalid/?key=credential")
	}
	*(result.(*string)) = hexutil.Encode(answer)
	return nil
}
func words(values ...int64) []byte {
	var result []byte
	for _, v := range values {
		result = append(result, uintWord(big.NewInt(v))...)
	}
	return result
}
func TestV2UsesExactBalancesAndAuthenticatedFactory(t *testing.T) {
	factory, pair, owner, t0, t1 := common.HexToAddress("0x1"), common.HexToAddress("0x2"), common.HexToAddress("0x3"), common.HexToAddress("0x4"), common.HexToAddress("0x5")
	hash := common.HexToHash("0xa")
	f := &callFixture{t: t, hash: hash, answers: map[string][]byte{key(pair, "factory()"): addrWord(factory), key(pair, "token0()"): addrWord(t0), key(pair, "token1()"): addrWord(t1), key(factory, "getPair(address,address)"): addrWord(pair), key(pair, "balanceOf(address)"): words(3), key(pair, "totalSupply()"): words(7), key(pair, "getReserves()"): words(10, 20, 1), key(t0, "decimals()"): words(18), key(t1, "decimals()"): words(6)}}
	d := Deployment{ID: "v2", Protocol: deficontract.UniswapV2, Contracts: []Contract{{Role: "factory", Address: factory}}}
	c := Candidate{Contract: pair, Owner: owner, Kind: "liquidity"}
	p, e := Observe(t.Context(), &State{RPC: f, Hash: hash}, d, c, "42")
	if e != nil || len(p.Assets) != 2 || p.Assets[0].Amount != "4" || p.Assets[1].Amount != "8" || p.BlockHash != hash.Hex() {
		t.Fatal(p, e)
	}
	f.answers[key(pair, "factory()")] = addrWord(owner)
	_, e = Observe(t.Context(), &State{RPC: f, Hash: hash}, d, c, "42")
	if e != deficontract.ErrUnsupported {
		t.Fatal("accepted spoofed pair", e)
	}
}
func TestAaveDebtDoesNotBecomeAnAsset(t *testing.T) {
	pool, provider, debt, asset, owner := common.HexToAddress("0x1"), common.HexToAddress("0x2"), common.HexToAddress("0x3"), common.HexToAddress("0x4"), common.HexToAddress("0x5")
	hash := common.HexToHash("0xa")
	tokens := append(append(addrWord(owner), make([]byte, 32)...), addrWord(debt)...)
	f := &callFixture{t: t, hash: hash, answers: map[string][]byte{key(debt, "UNDERLYING_ASSET_ADDRESS()"): addrWord(asset), key(debt, "POOL()"): addrWord(pool), key(provider, "getReserveTokensAddresses(address)"): tokens, key(debt, "balanceOf(address)"): words(101), key(asset, "decimals()"): words(18)}}
	d := Deployment{ID: "aave", Protocol: deficontract.AaveV3, ReceiptContracts: []Contract{{Role: "receipt", Address: debt, CodeHash: crypto.Keccak256Hash([]byte{1})}}, Contracts: []Contract{{Role: "pool", Address: pool}, {Role: "data_provider", Address: provider}}}
	p, e := Observe(t.Context(), &State{RPC: f, Hash: hash}, d, Candidate{Contract: debt, Owner: owner, Kind: "lending"}, "10")
	if e != nil || len(p.Assets) != 0 || len(p.Debts) != 1 || p.Debts[0].Amount != "101" || p.Kind != "borrow" {
		t.Fatal(p, e)
	}
	d.ReceiptContracts = nil
	p, e = Observe(t.Context(), &State{RPC: f, Hash: hash}, d, Candidate{Contract: debt, Owner: owner, Kind: "lending"}, "10")
	if !errors.Is(e, deficontract.ErrUnsupported) || len(p.Debts) != 0 {
		t.Fatal("unreviewed receipt was valued", p, e)
	}
}
func TestLidoWrappedAmountAndRPCFailure(t *testing.T) {
	steth, wrapped, owner := common.HexToAddress("0x1"), common.HexToAddress("0x2"), common.HexToAddress("0x3")
	hash := common.HexToHash("0xa")
	f := &callFixture{t: t, hash: hash, answers: map[string][]byte{key(wrapped, "balanceOf(address)"): words(10), key(wrapped, "getStETHByWstETH(uint256)"): words(12), key(steth, "decimals()"): words(18)}}
	d := Deployment{ID: "lido", Protocol: deficontract.Lido, Contracts: []Contract{{Role: "steth", Address: steth}, {Role: "wsteth", Address: wrapped}}}
	c := Candidate{Contract: wrapped, Owner: owner, Kind: "staking"}
	p, e := Observe(t.Context(), &State{RPC: f, Hash: hash}, d, c, "10")
	if e != nil || p.ReceiptAmount != "10" || len(p.Assets) != 1 || p.Assets[0].Amount != "12" {
		t.Fatal(p, e)
	}
	delete(f.answers, key(wrapped, "balanceOf(address)"))
	_, e = Observe(t.Context(), &State{RPC: f, Hash: hash}, d, c, "10")
	if e != deficontract.ErrUnavailable {
		t.Fatal("nested error escaped", e)
	}
}
func TestPackedV4Ticks(t *testing.T) {
	v := new(big.Int).Lsh(big.NewInt(0xffffff), 8)
	v.Or(v, new(big.Int).Lsh(big.NewInt(887272), 32))
	if packedTick(v, 8) != -1 || packedTick(v, 32) != 887272 {
		t.Fatal("incorrect signed packed ticks")
	}
}

func TestV3ZeroLiquidityRetainsRecordedPrincipal(t *testing.T) {
	manager, factory, pool, owner, t0, t1 := common.HexToAddress("0x1"), common.HexToAddress("0x2"), common.HexToAddress("0x3"), common.HexToAddress("0x4"), common.HexToAddress("0x5"), common.HexToAddress("0x6")
	hash := common.HexToHash("0xa")
	position := append(words(0, 0), addrWord(t0)...)
	position = append(position, addrWord(t1)...)
	position = append(position, words(3000)...)
	position = append(position, intWord(-60)...)
	position = append(position, intWord(60)...)
	position = append(position, words(0, 0, 0, 7, 0)...)
	slot := append(uintWord(q96), words(0, 0, 0, 0, 0, 1)...)
	f := &callFixture{t: t, hash: hash, answers: map[string][]byte{key(manager, "ownerOf(uint256)"): addrWord(owner), key(manager, "positions(uint256)"): position, key(manager, "factory()"): addrWord(factory), key(factory, "getPool(address,address,uint24)"): addrWord(pool), key(pool, "slot0()"): slot, key(pool, "ticks(int24)"): words(0, 0, 0, 0, 0, 0, 0, 0), key(pool, "feeGrowthGlobal0X128()"): words(0), key(pool, "feeGrowthGlobal1X128()"): words(0)}}
	d := Deployment{ID: "v3", Protocol: deficontract.UniswapV3, Contracts: []Contract{{Role: "position_manager", Address: manager}, {Role: "factory", Address: factory}}}
	p, e := Observe(t.Context(), &State{RPC: f, Hash: hash}, d, Candidate{Contract: manager, Owner: owner, TokenID: "1", Kind: "liquidity-nft"}, "1")
	if e != nil || p.State != "available" || p.Liquidity != "0" || len(p.Claimable) != 2 || p.Claimable[0].Amount != "7" || p.UnsettledFees[0].Amount != "0" {
		t.Fatal(p, e)
	}
	f.answers[key(manager, "ownerOf(uint256)")] = addrWord(t0)
	p, e = Observe(t.Context(), &State{RPC: f, Hash: hash}, d, Candidate{Contract: manager, Owner: owner, TokenID: "1", Kind: "liquidity-nft"}, "1")
	if e != nil || p.State != "closed" {
		t.Fatal("transferred NFT retained", p, e)
	}
}
func TestV4NativeCurrencyAndUnknownHook(t *testing.T) {
	manager, pool, view, owner, token := common.HexToAddress("0x1"), common.HexToAddress("0x2"), common.HexToAddress("0x3"), common.HexToAddress("0x4"), common.HexToAddress("0x5")
	hash := common.HexToHash("0xa")
	info := new(big.Int).Lsh(big.NewInt(0x1000000-60), 8)
	info.Or(info, new(big.Int).Lsh(big.NewInt(60), 32))
	poolInfo := append(addrWord(common.Address{}), addrWord(token)...)
	poolInfo = append(poolInfo, words(3000, 60, 0)...)
	poolInfo = append(poolInfo, uintWord(info)...)
	f := &callFixture{t: t, hash: hash, answers: map[string][]byte{key(manager, "ownerOf(uint256)"): addrWord(owner), key(manager, "poolManager()"): addrWord(pool), key(view, "poolManager()"): addrWord(pool), key(manager, "getPoolAndPositionInfo(uint256)"): poolInfo, key(view, "getSlot0(bytes32)"): append(uintWord(q96), words(0, 0, 3000)...), key(view, "getPositionInfo(bytes32,bytes32)"): words(1000000, 0, 0), key(view, "getFeeGrowthInside(bytes32,int24,int24)"): append(uintWord(q128), uintWord(q128)...)}}
	d := Deployment{ID: "v4", Protocol: deficontract.UniswapV4, Contracts: []Contract{{Role: "position_manager", Address: manager}, {Role: "pool_manager", Address: pool}, {Role: "state_view", Address: view}}}
	c := Candidate{Contract: manager, Owner: owner, TokenID: "1", Kind: "liquidity-nft"}
	p, e := Observe(t.Context(), &State{RPC: f, Hash: hash}, d, c, "1")
	if e != nil || len(p.Assets) != 2 || p.Assets[0].Kind != "native" || p.UnsettledFees[0].Amount != "1000000" {
		t.Fatal(p, e)
	}
	copy(poolInfo[4*32:5*32], addrWord(owner))
	p, e = Observe(t.Context(), &State{RPC: f, Hash: hash}, d, c, "1")
	if e != nil || p.State != "unsupported" || len(p.Assets) != 0 {
		t.Fatal("invented hook assets", p, e)
	}
}
func TestCurveReviewedPoolAndCycle(t *testing.T) {
	registry, pool, lp, owner, t0, t1 := common.HexToAddress("0x1"), common.HexToAddress("0x2"), common.HexToAddress("0x3"), common.HexToAddress("0x4"), common.HexToAddress("0x5"), common.HexToAddress("0x6")
	hash := common.HexToHash("0xa")
	coins := append(addrWord(t0), addrWord(t1)...)
	coins = append(coins, make([]byte, 6*32)...)
	f := &callFixture{t: t, hash: hash, answers: map[string][]byte{key(registry, "get_pool_from_lp_token(address)"): addrWord(pool), key(registry, "get_lp_token(address)"): addrWord(lp), key(lp, "balanceOf(address)"): words(10), key(lp, "totalSupply()"): words(100), key(registry, "get_coins(address)"): coins, key(registry, "get_balances(address)"): words(1000, 2000, 0, 0, 0, 0, 0, 0), key(registry, "get_base_pool(address)"): words(0)}}
	d := Deployment{ID: "curve", Protocol: deficontract.Curve, Contracts: []Contract{{Role: "metaregistry", Address: registry}}, PoolCodeHashes: []common.Hash{crypto.Keccak256Hash([]byte{1})}}
	c := Candidate{Contract: lp, Owner: owner, Kind: "liquidity"}
	p, e := Observe(t.Context(), &State{RPC: f, Hash: hash}, d, c, "1")
	if e != nil || len(p.Assets) != 2 || p.Assets[0].Amount != "100" || p.Assets[1].Amount != "200" {
		t.Fatal(p, e)
	}
	copy(coins[32:64], addrWord(t0))
	_, e = Observe(t.Context(), &State{RPC: f, Hash: hash}, d, c, "1")
	if !errors.Is(e, deficontract.ErrIntegrity) {
		t.Fatal("duplicated coin valued twice", e)
	}
	copy(coins[32:64], addrWord(t1))
	f.answers[key(registry, "get_base_pool(address)")] = addrWord(pool)
	copy(coins[:32], addrWord(lp))
	_, e = Observe(t.Context(), &State{RPC: f, Hash: hash}, d, c, "1")
	if e != deficontract.ErrUnsupported {
		t.Fatal("accepted registry cycle", e)
	}
	d.PoolCodeHashes = nil
	_, e = Observe(t.Context(), &State{RPC: f, Hash: hash}, d, c, "1")
	if e != deficontract.ErrUnsupported {
		t.Fatal("unknown accounting accepted", e)
	}
}
func TestLidoWithdrawalPendingAndClaimableAreDistinct(t *testing.T) {
	queue, steth, owner := common.HexToAddress("0x1"), common.HexToAddress("0x2"), common.HexToAddress("0x3")
	hash := common.HexToHash("0xa")
	status := append(words(32, 1, 10, 9), addrWord(owner)...)
	status = append(status, words(1, 0, 0)...)
	f := &callFixture{t: t, hash: hash, answers: map[string][]byte{key(queue, "getWithdrawalStatus(uint256[])"): status, key(queue, "getLastCheckpointIndex()"): words(1), key(queue, "findCheckpointHints(uint256[],uint256,uint256)"): words(32, 1, 1), key(queue, "getClaimableEther(uint256[],uint256[])"): words(32, 1, 8)}}
	d := Deployment{ID: "lido", Protocol: deficontract.Lido, Contracts: []Contract{{Role: "withdrawal_queue", Address: queue}, {Role: "steth", Address: steth}}}
	c := Candidate{Contract: queue, Owner: owner, TokenID: "1", Kind: "withdrawal"}
	p, e := Observe(t.Context(), &State{RPC: f, Hash: hash}, d, c, "1")
	if e != nil || p.State != "pending" || len(p.Claimable) != 0 || p.Assets[0].Amount != "10" {
		t.Fatal(p, e)
	}
	copy(status[6*32:7*32], words(1))
	p, e = Observe(t.Context(), &State{RPC: f, Hash: hash}, d, c, "1")
	if e != nil || p.State != "claimable" || len(p.Assets) != 0 || p.Claimable[0].Amount != "8" {
		t.Fatal(p, e)
	}
	copy(status[7*32:], words(1))
	p, e = Observe(t.Context(), &State{RPC: f, Hash: hash}, d, c, "1")
	if e != nil || p.State != "closed" {
		t.Fatal(p, e)
	}
}
