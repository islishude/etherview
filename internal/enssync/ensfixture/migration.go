package ensfixture

import (
	"math/big"
	"slices"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// MigrationContracts joins real pinned v1 and v2 deployments in one EVM.
// Every migration uses the token's actual transfer entrypoint.
type MigrationContracts struct {
	Registry, Registrar, Wrapper, Resolver, Reverse, DefaultReverse             Contract
	Root, Universal, Mirror, Graveyard, Unlocked, Locked, WrapperImplementation Contract
}

type MigrationData struct {
	Label                        string
	Owner, Subregistry, Resolver common.Address
}

func node(name string) common.Hash {
	var result common.Hash
	labels := strings.Split(name, ".")
	for _, label := range slices.Backward(labels) {
		result = crypto.Keccak256Hash(result.Bytes(), crypto.Keccak256([]byte(label)))
	}
	return result
}

func (f *Fixture) Migration() MigrationContracts {
	f.t.Helper()
	var m MigrationContracts
	legacy := f.deploy("LegacyENSRegistry")
	m.Registry = f.deploy("ENSRegistry", legacy.Address)
	m.Reverse = f.deploy("ReverseRegistrar", m.Registry.Address)
	f.Call(m.Registry, "setSubnodeOwner", common.Hash{}, crypto.Keccak256Hash([]byte("reverse")), f.Owner)
	f.Call(m.Registry, "setSubnodeOwner", node("reverse"), crypto.Keccak256Hash([]byte("addr")), m.Reverse.Address)
	m.Registrar = f.deploy("BaseRegistrarImplementation", m.Registry.Address, node("eth"))
	metadata := f.deploy("StaticMetadataService", "https://example.invalid/{id}")
	m.Wrapper = f.deploy("NameWrapper", m.Registry.Address, m.Registrar.Address, metadata.Address)
	f.Call(m.Registry, "setSubnodeOwner", common.Hash{}, crypto.Keccak256Hash([]byte("eth")), m.Registrar.Address)
	f.Call(m.Registrar, "addController", f.Owner)
	f.Call(m.Registrar, "addController", m.Wrapper.Address)
	f.Call(m.Wrapper, "setController", f.Owner, true)
	m.DefaultReverse = f.deploy("DefaultReverseRegistrar")
	m.Resolver = f.deploy("PublicResolver", m.Registry.Address, m.Wrapper.Address, common.Address{}, m.Reverse.Address)
	f.Call(m.Reverse, "setDefaultResolver", m.Resolver.Address)
	namer := f.deploy("ContractNamer")
	gateway := f.deploy("DNSSECGatewayProvider", f.Owner, []string{})
	roles, _ := new(big.Int).SetString(strings.Repeat("1", 64), 16)
	m.Root = f.deploy("ETHRegistry", f.LabelStore.Address, f.Owner, roles)
	m.Universal = f.deploy("UniversalResolverV2", m.Root.Address, gateway.Address, namer.Address)
	m.Mirror = f.deploy("ENSV1Resolver", gateway.Address, namer.Address, m.Registry.Address)
	f.Call(m.Root, "register", "eth", f.Owner, f.Registry.Address, common.Address{}, big.NewInt(0), uint64(1_900_000_000))
	f.Call(m.Root, "register", "reverse", f.Owner, common.Address{}, m.Mirror.Address, big.NewInt(0), uint64(1_900_000_000))
	m.Graveyard = f.deploy("Graveyard", m.Wrapper.Address, namer.Address)
	f.Call(m.Registrar, "addController", m.Graveyard.Address)
	m.Unlocked = f.deploy("UnlockedMigrationController", m.Wrapper.Address, m.Graveyard.Address, f.Registry.Address, namer.Address)
	upgrades := f.deploy("RegistryUpgradeSet", f.Owner)
	resolvers := f.deploy("PublicResolverSet", f.Owner)
	publicV2 := f.deploy("PublicResolverV2", m.Wrapper.Address, m.Root.Address, namer.Address)
	m.WrapperImplementation = f.deploy("WrapperRegistryImpl", m.Wrapper.Address, m.Graveyard.Address, f.Factory.Address, m.Mirror.Address, upgrades.Address, f.LabelStore.Address, common.Address{}, resolvers.Address, publicV2.Address, namer.Address)
	m.Locked = f.deploy("LockedMigrationController_Implementation", m.Wrapper.Address, m.Graveyard.Address, f.Registry.Address, f.Factory.Address, m.WrapperImplementation.Address, resolvers.Address, publicV2.Address, namer.Address)
	f.Call(f.Registry, "grantRootRoles", roles, m.Unlocked.Address)
	f.Call(f.Registry, "grantRootRoles", roles, m.Locked.Address)
	return m
}

func (f *Fixture) RegisterV1(m MigrationContracts, label string, wrapped, locked bool) common.Hash {
	f.t.Helper()
	duration := big.NewInt(10_000_000)
	if wrapped {
		fuses := uint16(0)
		if locked {
			fuses = 1
		}
		f.Call(m.Wrapper, "registerAndWrapETH2LD", label, f.Owner, duration, m.Resolver.Address, fuses)
	} else {
		f.Call(m.Registrar, "register", new(big.Int).SetBytes(crypto.Keccak256([]byte(label))), f.Owner, duration)
		f.Call(m.Registry, "setResolver", node(label+".eth"), m.Resolver.Address)
	}
	// Premigration reserves the v2 slot while preserving v1 resolution.
	f.Call(f.Registry, "register", label, common.Address{}, common.Address{}, m.Mirror.Address, big.NewInt(0), f.Config.Time+10_000_000)
	f.callSignature(m.Resolver, "setAddr(bytes32,address)", node(label+".eth"), f.Owner)
	return node(label + ".eth")
}

func (f *Fixture) MigrateV1(m MigrationContracts, label string, wrapped, locked bool) {
	f.t.Helper()
	dataType, e := abi.NewType("tuple", "", []abi.ArgumentMarshaling{{Name: "label", Type: "string"}, {Name: "owner", Type: "address"}, {Name: "subregistry", Type: "address"}, {Name: "resolver", Type: "address"}})
	if e != nil {
		f.t.Fatal(e)
	}
	data, e := (abi.Arguments{{Type: dataType}}).Pack(MigrationData{label, f.Owner, common.Address{}, f.Resolver})
	if e != nil {
		f.t.Fatal(e)
	}
	if wrapped {
		controller := m.Unlocked.Address
		if locked {
			controller = m.Locked.Address
		}
		f.Call(m.Wrapper, "safeTransferFrom", f.Owner, controller, new(big.Int).SetBytes(node(label+".eth").Bytes()), big.NewInt(1), data)
	} else {
		f.callSignature(m.Registrar, "safeTransferFrom(address,address,uint256,bytes)", f.Owner, m.Unlocked.Address, new(big.Int).SetBytes(crypto.Keccak256([]byte(label))), data)
	}
}
func (f *Fixture) callSignature(c Contract, signature string, args ...any) {
	f.t.Helper()
	for key, m := range c.ABI.Methods {
		if m.Sig == signature {
			f.Call(c, key, args...)
			return
		}
	}
	f.t.Fatalf("missing method %s", signature)
}
