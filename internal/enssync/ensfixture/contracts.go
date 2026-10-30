// Package ensfixture executes pinned real ENSv2 creation bytecode for tests.
// It never connects to a chain or substitutes hand-written events for EVM logs.
package ensfixture

import (
	"embed"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm/runtime"
	"github.com/ethereum/go-ethereum/crypto"
)

//go:embed testdata/*.json
var files embed.FS

type Contract struct {
	Address common.Address
	ABI     abi.ABI
	Code    []byte
}

// Execution is a replayable transaction against the same pinned contracts.
// Nil To denotes creation; all executions use Fixture.Owner as sender.
type Execution struct {
	To   *common.Address
	Data []byte
}
type Fixture struct {
	Registry, ChildImplementation, ResolverImplementation, Factory, LabelStore Contract
	Owner, Child, Resolver                                                     common.Address
	State                                                                      *state.StateDB
	Config                                                                     *runtime.Config
	Logs                                                                       []*types.Log
	Executions                                                                 []Execution
	next                                                                       uint64
	t                                                                          testing.TB
}
type grant struct {
	Account    common.Address
	RoleBitmap *big.Int
}

func New(t testing.TB) *Fixture {
	t.Helper()
	return NewWithOwner(t, common.HexToAddress("0x000000000000000000000000000000000000cafe"))
}

// NewWithOwner supports replay with authentic signed transactions on a local chain.
func NewWithOwner(t testing.TB, owner common.Address) *Fixture {
	t.Helper()
	db, e := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	if e != nil {
		t.Fatal(e)
	}
	f := &Fixture{t: t, Owner: owner, State: db}
	f.Config = &runtime.Config{State: db, Origin: f.Owner, GasLimit: 30_000_000, BlockNumber: big.NewInt(1), Time: 1_800_000_000}
	f.LabelStore = f.deploy("LabelStore", common.Address{})
	roles, _ := new(big.Int).SetString(strings.Repeat("1", 64), 16)
	f.Registry = f.deploy("ETHRegistry", f.LabelStore.Address, f.Owner, roles)
	f.ChildImplementation = f.deploy("UserRegistryImpl", f.LabelStore.Address, common.Address{}, f.Owner)
	f.ResolverImplementation = f.deploy("PermissionedResolverImpl", f.Owner)
	f.Factory = f.deploy("VerifiableFactory")
	init, e := f.ChildImplementation.ABI.Pack("initialize", []grant{{f.Owner, roles}})
	if e != nil {
		t.Fatal(e)
	}
	out := f.Call(f.Factory, "deployProxy", f.ChildImplementation.Address, big.NewInt(1), init)
	f.Child = common.BytesToAddress(out)
	init, e = f.ResolverImplementation.ABI.Pack("initialize", []grant{{f.Owner, roles}}, [][]byte{})
	if e != nil {
		t.Fatal(e)
	}
	out = f.Call(f.Factory, "deployProxy", f.ResolverImplementation.Address, big.NewInt(2), init)
	f.Resolver = common.BytesToAddress(out)
	return f
}
func (f *Fixture) begin() common.Hash {
	f.next++
	hash := crypto.Keccak256Hash(new(big.Int).SetUint64(f.next).Bytes())
	f.State.SetTxContext(hash, int(f.next), uint32(f.next))
	return hash
}
func (f *Fixture) end(hash common.Hash) {
	f.Logs = append(f.Logs, f.State.GetLogs(hash, 1, common.Hash{}, f.Config.Time)...)
}
func (f *Fixture) deploy(name string, args ...any) Contract {
	f.t.Helper()
	raw, e := files.ReadFile("testdata/" + name + ".json")
	if e != nil {
		f.t.Fatal(e)
	}
	var artifact struct {
		ABI      json.RawMessage
		Bytecode string
		Revision string
	}
	if e = json.Unmarshal(raw, &artifact); e != nil {
		f.t.Fatal(e)
	}
	if artifact.Revision != "4f47cbcc11462fb413196e6b5c0f8090247a6c08" && artifact.Revision != "9b034936a42f462fc04bc0a929a419ede5e18d59" {
		f.t.Fatal("unexpected contract revision")
	}
	parsed, e := abi.JSON(strings.NewReader(string(artifact.ABI)))
	if e != nil {
		f.t.Fatal(e)
	}
	data, e := parsed.Constructor.Inputs.Pack(args...)
	if e != nil {
		f.t.Fatal(e)
	}
	code, e := hexutil.Decode(artifact.Bytecode)
	if e != nil {
		f.t.Fatal(e)
	}
	hash := f.begin()
	input := append(code, data...)
	f.Executions = append(f.Executions, Execution{Data: append([]byte(nil), input...)})
	runtimeCode, address, _, e := runtime.Create(input, f.Config)
	if e != nil {
		for _, definition := range parsed.Errors {
			if len(runtimeCode) >= 4 && string(definition.ID[:4]) == string(runtimeCode[:4]) {
				f.t.Fatalf("deploy %s: %s (%x)", name, definition.Name, runtimeCode)
			}
		}
		f.t.Fatalf("deploy %s: %v (%x)", name, e, runtimeCode)
	}
	f.end(hash)
	return Contract{address, parsed, runtimeCode}
}

// DeployTestCode supplements the pinned protocol with an explicit custom test contract.
func (f *Fixture) DeployTestCode(creation []byte) Contract {
	f.t.Helper()
	hash := f.begin()
	f.Executions = append(f.Executions, Execution{Data: append([]byte(nil), creation...)})
	code, address, _, err := runtime.Create(creation, f.Config)
	if err != nil {
		f.t.Fatalf("custom fixture creation: %v", err)
	}
	f.end(hash)
	return Contract{Address: address, Code: code}
}

func (f *Fixture) Call(contract Contract, method string, args ...any) []byte {
	f.t.Helper()
	input, e := contract.ABI.Pack(method, args...)
	if e != nil {
		f.t.Fatal(e)
	}
	hash := f.begin()
	f.Executions = append(f.Executions, Execution{To: &contract.Address, Data: append([]byte(nil), input...)})
	f.State.SetNonce(f.Owner, f.State.GetNonce(f.Owner)+1, tracing.NonceChangeEoACall)
	out, _, e := runtime.Call(contract.Address, input, f.Config)
	if e != nil {
		f.t.Fatalf("%s: %v (%x)", method, e, out)
	}
	f.end(hash)
	return out
}
func (f *Fixture) ChildContract() Contract {
	return Contract{f.Child, f.ChildImplementation.ABI, f.State.GetCode(f.Child)}
}
func (f *Fixture) ResolverContract() Contract {
	return Contract{f.Resolver, f.ResolverImplementation.ABI, f.State.GetCode(f.Resolver)}
}

// Universal deploys the real v2 root and Universal Resolver and gives the
// fixture's ETH registry its reviewed DNS path. Reverse data remains a real
// permissioned-resolver record, exercised through Universal Resolver bytecode.
func (f *Fixture) Universal(address common.Address) (root, universal Contract) {
	roles, _ := new(big.Int).SetString(strings.Repeat("1", 64), 16)
	root = f.deploy("ETHRegistry", f.LabelStore.Address, f.Owner, roles)
	namer := f.deploy("ContractNamer")
	gateway := f.deploy("DNSSECGatewayProvider", f.Owner, []string{})
	universal = f.deploy("UniversalResolverV2", root.Address, gateway.Address, namer.Address)
	expiry := uint64(1_900_000_000)
	f.Call(root, "register", "eth", f.Owner, f.Registry.Address, common.Address{}, big.NewInt(0), expiry)
	f.Call(root, "register", "reverse", f.Owner, common.Address{}, f.Resolver, big.NewInt(0), expiry)
	name := strings.ToLower(strings.TrimPrefix(address.Hex(), "0x")) + ".addr.reverse"
	var wire []byte
	for label := range strings.SplitSeq(name, ".") {
		wire = append(wire, byte(len(label)))
		wire = append(wire, []byte(label)...)
	}
	wire = append(wire, 0)
	f.Call(f.ResolverContract(), "setName", wire, "wallet.alice.eth")
	return root, universal
}
