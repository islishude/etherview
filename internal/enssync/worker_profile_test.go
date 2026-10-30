package enssync

import (
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/vm/runtime"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/enssync/ensfixture"
	"github.com/islishude/etherview/internal/ethrpc"
)

type profileFixtureRPC struct {
	f           *ensfixture.Fixture
	hash        common.Hash
	failStorage bool
}

func (s *profileFixtureRPC) Call(request map[string]json.RawMessage, ref rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	if ref.BlockHash == nil || *ref.BlockHash != s.hash || !ref.RequireCanonical {
		return nil, errors.New("not exact hash")
	}
	var to common.Address
	var input hexutil.Bytes
	if json.Unmarshal(request["to"], &to) != nil || json.Unmarshal(request["data"], &input) != nil {
		return nil, errors.New("invalid call")
	}
	snapshot := s.f.State.Snapshot()
	defer s.f.State.RevertToSnapshot(snapshot)
	output, _, e := runtime.Call(to, input, s.f.Config)
	return output, e
}
func (s *profileFixtureRPC) GetCode(a common.Address, ref rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	if ref.BlockHash == nil || *ref.BlockHash != s.hash {
		return nil, errors.New("not exact hash")
	}
	return s.f.State.GetCode(a), nil
}
func (s *profileFixtureRPC) GetStorageAt(a common.Address, key common.Hash, ref rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	if s.failStorage {
		return nil, errors.New("temporary storage RPC failure")
	}
	if ref.BlockHash == nil || *ref.BlockHash != s.hash {
		return nil, errors.New("not exact hash")
	}
	return s.f.State.GetState(a, key).Bytes(), nil
}
func TestRegistrationProfilesPinnedRealContracts(t *testing.T) {
	f := ensfixture.New(t)
	m := f.Migration()
	hash := common.HexToHash("0x1234")
	server := rpc.NewServer()
	rpcFixture := &profileFixtureRPC{f: f, hash: hash}
	if e := server.RegisterName("eth", rpcFixture); e != nil {
		t.Fatal(e)
	}
	client := rpc.DialInProc(server)
	t.Cleanup(client.Close)
	ep := &ethrpc.Endpoint{Name: "profile", Client: client}
	manifest := Manifest{ParentName: "eth"}
	for profile, c := range map[string]ensfixture.Contract{"registry": m.Registry, "registrar": m.Registrar, "wrapper": m.Wrapper, "resolver": m.Resolver} {
		manifest.Deployments = append(manifest.Deployments, Deployment{Address: c.Address, Profile: profile, CodeHash: crypto.Keccak256Hash(f.State.GetCode(c.Address))})
	}
	w := Worker{options: WorkerOptions{Registry: m.Registry.Address, Manifest: manifest}}
	f.RegisterV1(m, "grace", true, false)
	for _, wrapped := range []bool{false, true} {
		label := "plain"
		if wrapped {
			label = "wrapped"
		}
		f.RegisterV1(m, label, wrapped, false)
		p, _, e := w.registrationProfile(t.Context(), ep, hash, label+".eth", 1, time.Unix(int64(f.Config.Time), 0))
		if e != nil || p.Registrant.Value != f.Owner.Hex() || p.Controller.Value != f.Owner.Hex() || p.ExpirationTime.Value != "1810000000" || p.RegistrationState != "registered" {
			t.Fatalf("%s %+v %v", label, p, e)
		}
	}
	// A registrar transfer changes ownership without reclaiming the registry.
	newOwner := common.HexToAddress("0x123456")
	f.Call(m.Registrar, "transferFrom", f.Owner, newOwner, new(big.Int).SetBytes(crypto.Keccak256([]byte("plain"))))
	p, _, e := w.registrationProfile(t.Context(), ep, hash, "plain.eth", 1, time.Unix(int64(f.Config.Time), 0))
	if e != nil || p.Registrant.Value != newOwner.Hex() || p.Controller.Value != f.Owner.Hex() {
		t.Fatalf("transfer %+v %v", p, e)
	}
	f.Call(m.Registry, "setSubnodeOwner", nameHash("plain.eth"), crypto.Keccak256Hash([]byte("sub")), f.Owner)
	p, _, e = w.v1RegistrationProfile(t.Context(), ep, hash, "sub.plain.eth", 1, time.Unix(int64(f.Config.Time), 0))
	if e != nil || p.Registrant.State != "not_applicable" || p.Controller.Value != f.Owner.Hex() || p.ExpirationTime.State != "not_applicable" {
		t.Fatalf("unwrapped subname %+v %v", p, e)
	}
	f.Call(m.Wrapper, "setSubnodeOwner", nameHash("grace.eth"), "sub", f.Owner, uint32(0), f.Config.Time+500)
	p, _, e = w.v1RegistrationProfile(t.Context(), ep, hash, "sub.grace.eth", 1, time.Unix(int64(f.Config.Time), 0))
	if e != nil || p.Registrant.Value != f.Owner.Hex() || p.ExpirationTime.Value != "1800000500" {
		t.Fatalf("wrapped subname %+v %v", p, e)
	}
	f.Call(m.Registry, "setResolver", nameHash("plain.eth"), newOwner)
	p, _, e = w.v1RegistrationProfile(t.Context(), ep, hash, "plain.eth", 1, time.Unix(int64(f.Config.Time), 0))
	if e != nil || p.Controller.State != "unsupported" || p.Registrant.Value != newOwner.Hex() {
		t.Fatalf("custom permissions %+v %v", p, e)
	}
	partial := w
	partial.options.Manifest.Deployments = nil
	for _, d := range w.options.Manifest.Deployments {
		if d.Profile != "registrar" {
			partial.options.Manifest.Deployments = append(partial.options.Manifest.Deployments, d)
		}
	}
	p, _, e = partial.v1RegistrationProfile(t.Context(), ep, hash, "plain.eth", 1, time.Unix(int64(f.Config.Time), 0))
	if e != nil || p.Controller.State != "unsupported" || p.Registrant.State != "unsupported" {
		t.Fatalf("partial deployment permissions %+v %v", p, e)
	}
	// Historical reviewed deployments do not override the configured registry
	// or the registrar selected by that registry's authoritative parent owner.
	for _, profile := range []string{"registry", "registrar", "wrapper"} {
		w.options.Manifest.Deployments = append(w.options.Manifest.Deployments, Deployment{Profile: profile, Address: newOwner})
	}
	p, _, e = w.v1RegistrationProfile(t.Context(), ep, hash, "grace.eth", 1, time.Unix(int64(f.Config.Time), 0))
	if e != nil || p.Registrant.Value != f.Owner.Hex() {
		t.Fatalf("deployment selection %+v %v", p, e)
	}
	w.options.Manifest.Deployments = w.options.Manifest.Deployments[:len(w.options.Manifest.Deployments)-3]
	f.Call(m.Registry, "setResolver", nameHash("plain.eth"), m.Resolver.Address)
	f.Call(f.Registry, "register", "v2", f.Owner, common.Address{}, common.Address{}, big.NewInt(0), f.Config.Time+100)
	w.options.Manifest.Roots = []RegistryRoot{{Address: f.Registry.Address, Name: "eth"}}
	w.options.Manifest.Deployments = append(w.options.Manifest.Deployments, Deployment{Address: f.Registry.Address, Profile: "v2_registry", CodeHash: crypto.Keccak256Hash(f.State.GetCode(f.Registry.Address))})
	p, _, e = w.registrationProfile(t.Context(), ep, hash, "v2.eth", 1, time.Unix(int64(f.Config.Time), 0))
	if e != nil || p.Registrant.Value != f.Owner.Hex() || p.Controller.State != "multi_account" {
		t.Fatalf("v2 %+v %v", p, e)
	}
	p, _, e = w.registrationProfile(t.Context(), ep, hash, "plain.eth", 1, time.Unix(int64(f.Config.Time), 0))
	if e != nil || p.RegistrationState != "reserved" || p.Registrant.Value != newOwner.Hex() {
		t.Fatalf("premigration %+v %v", p, e)
	}
	// A premigration read must not activate later v1 deployments.
	w.options.Manifest.Deployments = append(w.options.Manifest.Deployments, Deployment{Address: newOwner, Profile: "registry", Number: 2})
	p, _, e = w.registrationProfile(t.Context(), ep, hash, "plain.eth", 1, time.Unix(int64(f.Config.Time), 0))
	if e != nil || p.Registrant.Value != newOwner.Hex() {
		t.Fatalf("future deployment %+v %v", p, e)
	}
	w.options.Manifest.Deployments = w.options.Manifest.Deployments[:len(w.options.Manifest.Deployments)-1]
	f.MigrateV1(m, "wrapped", true, false)
	p, _, e = w.registrationProfile(t.Context(), ep, hash, "wrapped.eth", 1, time.Unix(int64(f.Config.Time), 0))
	if e != nil || p.RegistrationState != "registered" || p.Registrant.Value != f.Owner.Hex() {
		t.Fatalf("migration %+v %v", p, e)
	}
	// Renewal and token regeneration keep the same normalized identity while
	// exact ownership/expiry are recomputed from the new token resource.
	labelID := new(big.Int).SetBytes(crypto.Keccak256([]byte("v2")))
	f.Call(f.Registry, "renew", labelID, f.Config.Time+200)
	p, _, e = w.registrationProfile(t.Context(), ep, hash, "v2.eth", 1, time.Unix(int64(f.Config.Time), 0))
	if e != nil || p.ExpirationTime.Value != "1800000200" {
		t.Fatalf("renew %+v %v", p, e)
	}
	f.Call(f.Registry, "grantRoles", labelID, new(big.Int).Lsh(big.NewInt(1), 20), newOwner)
	p, _, e = w.registrationProfile(t.Context(), ep, hash, "v2.eth", 1, time.Unix(int64(f.Config.Time), 0))
	if e != nil || p.Registrant.Value != f.Owner.Hex() {
		t.Fatalf("regenerated %+v %v", p, e)
	}
	f.RegisterV1(m, "locked-profile", true, true)
	f.MigrateV1(m, "locked-profile", true, true)
	wrapperChild := common.BytesToAddress(f.Call(f.Registry, "getSubregistry", "locked-profile"))
	w.options.Manifest.Implementations = append(w.options.Manifest.Implementations, Implementation{Address: m.WrapperImplementation.Address, Profile: "v2_registry", CodeHash: crypto.Keccak256Hash(f.State.GetCode(m.WrapperImplementation.Address)), ProxyCodeHash: crypto.Keccak256Hash(f.State.GetCode(wrapperChild))})
	f.Call(ensfixture.Contract{Address: wrapperChild, ABI: f.Registry.ABI}, "register", "child", f.Owner, common.Address{}, common.Address{}, big.NewInt(0), f.Config.Time+1000)
	p, _, e = w.registrationProfile(t.Context(), ep, hash, "child.locked-profile.eth", 1, time.Unix(int64(f.Config.Time), 0))
	if e != nil || p.Registrant.Value != f.Owner.Hex() || p.ExpirationTime.Value != "1800001000" {
		t.Fatalf("wrapper registry %+v %v", p, e)
	}
	// An unavailable implementation slot is retryable, not unsupported evidence.
	rpcFixture.failStorage = true
	_, _, e = w.registrationProfile(t.Context(), ep, hash, "child.locked-profile.eth", 1, time.Unix(int64(f.Config.Time), 0))
	if !errors.Is(e, ErrRPCUnavailable) {
		t.Fatalf("implementation RPC failure %v", e)
	}
	rpcFixture.failStorage = false
	w.options.Manifest.Implementations = nil
	p, _, e = w.registrationProfile(t.Context(), ep, hash, "child.locked-profile.eth", 1, time.Unix(int64(f.Config.Time), 0))
	if e != nil || p.Registrant.State != "unsupported" {
		t.Fatalf("unknown implementation %+v %v", p, e)
	}
	f.Config.Time += 201
	p, _, e = w.registrationProfile(t.Context(), ep, hash, "v2.eth", 1, time.Unix(int64(f.Config.Time), 0))
	if e != nil || p.Registrant.Value != "" || p.RegistrationState != "expired" {
		t.Fatalf("expired %+v %v", p, e)
	}
	f.Config.Time = 1_810_000_001
	p, _, e = w.v1RegistrationProfile(t.Context(), ep, hash, "grace.eth", 1, time.Unix(int64(f.Config.Time), 0))
	if e != nil || p.RegistrationState != "grace_period" || p.Registrant.Value != f.Owner.Hex() || p.ExpirationTime.Value != "1810000000" {
		t.Fatalf("wrapped grace %+v %v", p, e)
	}
	f.Config.Time = 1_817_776_001
	p, _, e = w.v1RegistrationProfile(t.Context(), ep, hash, "grace.eth", 1, time.Unix(int64(f.Config.Time), 0))
	if e != nil || p.RegistrationState != "expired" || p.Registrant.Value != "" {
		t.Fatalf("grace end %+v %v", p, e)
	}
	f.Call(m.Registrar, "register", new(big.Int).SetBytes(crypto.Keccak256([]byte("grace"))), newOwner, big.NewInt(1000))
	p, _, e = w.v1RegistrationProfile(t.Context(), ep, hash, "grace.eth", 1, time.Unix(int64(f.Config.Time), 0))
	if e != nil || p.RegistrationState != "registered" || p.Registrant.Value != newOwner.Hex() {
		t.Fatalf("reregistration %+v %v", p, e)
	}

}
