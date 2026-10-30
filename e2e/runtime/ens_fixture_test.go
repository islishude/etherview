//go:build runtimee2e

package runtimee2e

import (
	"context"
	_ "embed"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/enssync"
	"github.com/islishude/etherview/internal/enssync/ensfixture"
)

// Compiled from ens_ccip_fixture.sol with solc 0.8.30, optimizer runs 200,
// Prague EVM and metadata bytecodeHash=none. It is not an upstream ENS artifact.
//
//go:embed ens_ccip_fixture.hex
var unavailableCCIPCreation string

// Transactions execute real pinned contracts on the SAME Anvil chain consumed
// by core sync. There is no ENS header/log server or second chain identity.
func (h *harness) enqueueENSFixture(ctx context.Context) enssync.Manifest {
	key := deterministicRuntimeKey(h.t, "etherview-runtime-ens-owner")
	real := ensfixture.NewWithOwner(h.t, crypto.PubkeyToAddress(key.PublicKey))
	h.ensOwner = real.Owner.Hex()
	root, universal := real.Universal(common.HexToAddress(h.fixture.authority))
	expiry := uint64(1_900_000_000)
	real.Call(real.Registry, "register", "alice", real.Owner, real.Child, real.Resolver, big.NewInt(0), expiry)
	real.Call(real.ChildContract(), "register", "wallet", real.Owner, common.Address{}, real.Resolver, big.NewInt(0), expiry)
	real.Call(real.ResolverContract(), "setAddress", []byte("\x06wallet\x05alice\x03eth\x00"), big.NewInt(60), common.HexToAddress(h.fixture.authority).Bytes())
	creation, err := hexutil.Decode("0x" + strings.TrimSpace(unavailableCCIPCreation))
	if err != nil {
		h.t.Fatal(err)
	}
	ccip := real.DeployTestCode(creation)
	real.Call(real.Registry, "register", "ccip", real.Owner, common.Address{}, ccip.Address, big.NewInt(0), expiry)
	migration := real.Migration()
	for _, entry := range []struct {
		label           string
		wrapped, locked bool
	}{{"plain", false, false}, {"wrapped", true, false}, {"locked", true, true}} {
		real.RegisterV1(migration, entry.label, entry.wrapped, entry.locked)
		real.MigrateV1(migration, entry.label, entry.wrapped, entry.locked)
		wire := append([]byte{byte(len(entry.label))}, []byte(entry.label)...)
		wire = append(wire, []byte("\x03eth\x00")...)
		real.Call(real.ResolverContract(), "setAddress", wire, big.NewInt(60), real.Owner.Bytes())
	}
	wrapperProxy := common.BytesToAddress(real.Call(real.Registry, "getSubregistry", "locked"))
	var accepted any
	h.rpcCall(ctx, &accepted, "anvil_setBalance", real.Owner.Hex(), "0x3635c9adc5dea00000")
	for i, execution := range real.Executions {
		transaction := types.NewTx(&types.DynamicFeeTx{ChainID: big.NewInt(1), Nonce: uint64(i), Data: execution.Data, To: execution.To, Gas: 30_000_000, GasTipCap: big.NewInt(1_000_000_000), GasFeeCap: big.NewInt(10_000_000_000)})
		hash := h.sendRawTransaction(ctx, transaction, key)
		h.ensTransactions = append(h.ensTransactions, hash)
	}
	m := enssync.Manifest{Registry: root.Address, UniversalResolver: universal.Address, TargetChainID: "1", SourceChainID: "1", Policy: crypto.Keccak256Hash([]byte("runtime-ensv2-policy")), Start: 1, ParentName: "eth", CoinType: "60", Roots: []enssync.RegistryRoot{{Address: root.Address, Name: ""}}}
	for _, entry := range []struct {
		contract ensfixture.Contract
		profile  string
	}{{root, "v2_registry"}, {real.Registry, "v2_registry"}, {real.Factory, "v2_factory"}, {universal, "universal"}, {migration.Registry, "registry"}, {migration.Registrar, "registrar"}, {migration.Wrapper, "wrapper"}, {migration.Resolver, "resolver"}, {migration.Reverse, "reverse"}, {migration.DefaultReverse, "default_reverse"}} {
		m.Deployments = append(m.Deployments, enssync.Deployment{Address: entry.contract.Address, Profile: entry.profile, Number: 1, CodeHash: crypto.Keccak256Hash(entry.contract.Code)})
	}
	m.Implementations = []enssync.Implementation{{Address: real.ChildImplementation.Address, CodeHash: crypto.Keccak256Hash(real.ChildImplementation.Code), ProxyCodeHash: crypto.Keccak256Hash(real.State.GetCode(real.Child)), Profile: "v2_registry"}, {Address: real.ResolverImplementation.Address, CodeHash: crypto.Keccak256Hash(real.ResolverImplementation.Code), ProxyCodeHash: crypto.Keccak256Hash(real.State.GetCode(real.Resolver)), Profile: "v2_resolver"}, {Address: migration.WrapperImplementation.Address, CodeHash: crypto.Keccak256Hash(migration.WrapperImplementation.Code), ProxyCodeHash: crypto.Keccak256Hash(real.State.GetCode(wrapperProxy)), Profile: "v2_registry"}}
	return m
}
func (h *harness) saveENSManifest(ctx context.Context, m enssync.Manifest) {
	// Preserve canonical receipts and the provider's trace envelope for diagnosis.
	for _, method := range []string{"eth_getBlockReceipts", "debug_traceBlockByHash"} {
		var raw json.RawMessage
		args := []any{h.fixture.blockOneHash}
		if method == "debug_traceBlockByHash" {
			args = append(args, map[string]any{"tracer": "callTracer", "tracerConfig": map[string]any{"withLog": true}})
		}
		h.rpcCall(ctx, &raw, method, args...)
		h.writeArtifact(method+".json", raw)
	}
	for _, hash := range h.ensTransactions {
		var receipt struct {
			Status    hexutil.Uint64
			BlockHash common.Hash
		}
		h.rpcCall(ctx, &receipt, "eth_getTransactionReceipt", hash)
		if receipt.Status != 1 || receipt.BlockHash.Hex() != h.fixture.blockOneHash {
			h.t.Fatalf("ENS deployment transaction failed: %s %+v", hash, receipt)
		}
	}
	m.Genesis = common.HexToHash(h.fixture.genesisHash)
	m.StartHash = common.HexToHash(h.fixture.blockOneHash)
	for i := range m.Deployments {
		m.Deployments[i].Hash = m.StartHash
	}
	directory := filepath.Join(h.artifacts, "ens")
	writeENSManifest(h.t, directory, m)
	h.project.Env["ETHERVIEW_ENS_MANIFEST_DIR"] = directory
	h.project.Env["ETHERVIEW_ENS_POLICY_DIGEST"] = m.Policy.Hex()
	h.project.Env["ETHERVIEW_ENS_MANIFEST"] = "/etc/etherview/ens/current.json"
}
func writeENSManifest(t *testing.T, directory string, m enssync.Manifest) {
	t.Helper()
	if e := os.MkdirAll(directory, 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(directory, 0755); e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(directory, "current.json"), raw, 0644); e != nil {
		t.Fatal(e)
	}
}
func TestENSManifestPermissions(t *testing.T) {
	parent := t.TempDir()
	if e := os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	directory := filepath.Join(parent, "ens")
	writeENSManifest(t, directory, enssync.Manifest{})
	for path, mode := range map[string]os.FileMode{parent: 0700, directory: 0755, filepath.Join(directory, "current.json"): 0644} {
		info, e := os.Stat(path)
		if e != nil {
			t.Fatal(e)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("manifest permissions %o want %o", info.Mode().Perm(), mode)
		}
	}
}
