//go:build runtimee2e

package runtimee2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm/runtime"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/islishude/etherview/internal/enssync"
	"github.com/islishude/etherview/internal/enssync/ensfixture"
)

// This is an isolated local RPC over pinned real v2 EVM contracts. Its synthetic
// finalized header uses the official source identity for exercising that path;
// it never connects to Mainnet or supplies deployment/activation evidence.
type ensLocalV2RPC struct {
	mu       sync.Mutex
	fixture  *ensfixture.Fixture
	header   *types.Header
	genesis  *types.Header
	listener net.Listener
	server   *http.Server
	calls    uint64
	offline  bool
}

func startENSLocalV2RPC(t *testing.T, address common.Address, directory string) *ensLocalV2RPC {
	t.Helper()
	// Only public fixture manifests are mounted into the non-root application.
	// Keep diagnostics private, but allow container UID 65532 to traverse this
	// dedicated bind-mount root on Linux (MkdirTemp defaults to 0700).
	if e := os.MkdirAll(directory, 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(directory, 0755); e != nil {
		t.Fatal(e)
	}
	real := ensfixture.New(t)
	root, universal := real.Universal(address)
	expiry := uint64(1_900_000_000)
	real.Call(real.Registry, "register", "alice", real.Owner, real.Child, real.Resolver, big.NewInt(0), expiry)
	real.Call(real.ChildContract(), "register", "wallet", real.Owner, common.Address{}, real.Resolver, big.NewInt(0), expiry)
	real.Call(real.ResolverContract(), "setAddress", []byte("\x06wallet\x05alice\x03eth\x00"), big.NewInt(60), address.Bytes())
	genesis := core.DefaultGenesisBlock().ToBlock().Header()
	header := &types.Header{Number: big.NewInt(1), ParentHash: genesis.Hash(), Time: real.Config.Time, GasLimit: 30_000_000, Difficulty: big.NewInt(0), Root: real.State.IntermediateRoot(real.Config.ChainConfig.Rules(real.Config.BlockNumber, true, real.Config.Time)), Extra: []byte("isolated-real-ensv2")}
	for i, log := range real.Logs {
		log.BlockHash = header.Hash()
		log.BlockNumber = 1
		log.Index = uint(i)
	}
	service := &ensLocalV2RPC{fixture: real, header: header, genesis: genesis}
	server := rpc.NewServer()
	if e := server.RegisterName("eth", service); e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp4", "0.0.0.0:0")
	if e != nil {
		t.Fatal(e)
	}
	service.listener = listener
	service.server = &http.Server{Handler: server, ReadHeaderTimeout: time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	go func() { _ = service.server.Serve(listener) }()
	m := enssync.Manifest{Registry: root.Address, UniversalResolver: universal.Address, TargetChainID: "1", SourceChainID: "1", Genesis: genesis.Hash(), Namespace: enscontract.Official, Policy: crypto.Keccak256Hash([]byte("runtime-ensv2-policy")), Start: 1, StartHash: header.Hash(), ParentName: "eth", CoinType: "60", Roots: []enssync.RegistryRoot{{Address: root.Address, Name: ""}}}
	for _, entry := range []struct {
		contract ensfixture.Contract
		profile  string
	}{{root, "v2_registry"}, {real.Registry, "v2_registry"}, {real.Factory, "v2_factory"}, {universal, "universal"}} {
		m.Deployments = append(m.Deployments, enssync.Deployment{Address: entry.contract.Address, Profile: entry.profile, Number: 1, Hash: header.Hash(), CodeHash: crypto.Keccak256Hash(entry.contract.Code)})
	}
	m.Implementations = []enssync.Implementation{{Address: real.ChildImplementation.Address, CodeHash: crypto.Keccak256Hash(real.ChildImplementation.Code), ProxyCodeHash: crypto.Keccak256Hash(real.State.GetCode(real.Child)), Profile: "v2_registry"}, {Address: real.ResolverImplementation.Address, CodeHash: crypto.Keccak256Hash(real.ResolverImplementation.Code), ProxyCodeHash: crypto.Keccak256Hash(real.State.GetCode(real.Resolver)), Profile: "v2_resolver"}}
	raw, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(directory, "official.json"), raw, 0644); e != nil {
		t.Fatal(e)
	}
	return service
}

func TestENSLocalV2ManifestPermissions(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(parent, "ens")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	service := startENSLocalV2RPC(t, common.HexToAddress("0xcafe"), directory)
	t.Cleanup(func() { _ = service.server.Close() })
	for path, mode := range map[string]os.FileMode{parent: 0700, directory: 0755, filepath.Join(directory, "official.json"): 0644} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("%s permissions = %o, want %o", path, info.Mode().Perm(), mode)
		}
	}
}

func (s *ensLocalV2RPC) containerURL() string {
	return fmt.Sprintf("http://host.docker.internal:%d", s.listener.Addr().(*net.TCPAddr).Port)
}
func (s *ensLocalV2RPC) available() error {
	s.calls++
	if s.offline {
		return errors.New("isolated source offline")
	}
	return nil
}
func (s *ensLocalV2RPC) ChainId() (hexutil.Uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return 1, s.available()
}
func (s *ensLocalV2RPC) GetBlockByNumber(number string, _ bool) (*types.Header, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.available(); e != nil {
		return nil, e
	}
	switch number {
	case "0x0":
		return s.genesis, nil
	case "0x1", "finalized":
		return s.header, nil
	}
	return nil, errors.New("unknown fixture block")
}
func (s *ensLocalV2RPC) GetBlockByHash(hash common.Hash, _ bool) (*types.Header, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.available(); e != nil {
		return nil, e
	}
	if hash != s.header.Hash() {
		return nil, errors.New("wrong exact block")
	}
	return s.header, nil
}
func (s *ensLocalV2RPC) exact(block rpc.BlockNumberOrHash) error {
	if e := s.available(); e != nil {
		return e
	}
	if block.BlockHash == nil || !block.RequireCanonical || *block.BlockHash != s.header.Hash() {
		return errors.New("state was not pinned")
	}
	return nil
}
func (s *ensLocalV2RPC) GetCode(address common.Address, block rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.exact(block); e != nil {
		return nil, e
	}
	return s.fixture.State.GetCode(address), nil
}
func (s *ensLocalV2RPC) GetStorageAt(address common.Address, slot common.Hash, block rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.exact(block); e != nil {
		return nil, e
	}
	return s.fixture.State.GetState(address, slot).Bytes(), nil
}
func (s *ensLocalV2RPC) GetLogs(filter map[string]json.RawMessage) ([]*types.Log, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.available(); e != nil {
		return nil, e
	}
	var hash common.Hash
	if json.Unmarshal(filter["blockHash"], &hash) != nil || hash != s.header.Hash() || len(filter) != 1 {
		return nil, errors.New("logs were not pinned")
	}
	return s.fixture.Logs, nil
}
func (s *ensLocalV2RPC) Call(call map[string]json.RawMessage, block rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.exact(block); e != nil {
		return nil, e
	}
	var to common.Address
	var data hexutil.Bytes
	if json.Unmarshal(call["to"], &to) != nil || json.Unmarshal(call["data"], &data) != nil {
		return nil, errors.New("invalid call")
	}
	snapshot := s.fixture.State.Snapshot()
	defer s.fixture.State.RevertToSnapshot(snapshot)
	output, _, e := runtime.Call(to, data, s.fixture.Config)
	if e != nil {
		return nil, ensFixtureRevert(output)
	}
	return output, nil
}

type ensFixtureRevert []byte

func (ensFixtureRevert) Error() string    { return "execution reverted" }
func (ensFixtureRevert) ErrorCode() int   { return 3 }
func (e ensFixtureRevert) ErrorData() any { return hexutil.Encode(e) }
