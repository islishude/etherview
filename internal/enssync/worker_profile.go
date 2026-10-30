package enssync

import (
	"context"
	"embed"
	"encoding/json"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enscontract"
	"github.com/islishude/etherview/internal/ethrpc"
)

// Getter subsets of the revisions recorded in ensfixture/testdata. Dynamic
// registry proxies must match a configured implementation before these apply.
//
//go:embed profileabi/*.json
var profileABIs embed.FS

func profileCall(ctx context.Context, endpoint *ethrpc.Endpoint, hash common.Hash, address common.Address, contract, method string, args ...any) ([]any, error) {
	raw, e := profileABIs.ReadFile("profileabi/" + contract + ".json")
	if e != nil {
		return nil, ErrUnsupportedDeployment
	}
	a, e := abi.JSON(strings.NewReader(string(raw)))
	if e != nil {
		return nil, ErrUnsupportedDeployment
	}
	input, e := a.Pack(method, args...)
	if e != nil {
		return nil, ErrInvalidInput
	}
	var output hexutil.Bytes
	e = endpoint.CallContext(ctx, &output, "eth_call", map[string]any{"to": address, "data": hexutil.Bytes(input)}, gethrpc.BlockNumberOrHashWithHash(hash, true))
	if e != nil {
		return nil, ErrRPCUnavailable
	}
	if len(output) > 1024 {
		return nil, ErrInvalidEvent
	}
	result, e := a.Unpack(method, output)
	if e != nil {
		return nil, ErrInvalidEvent
	}
	return result, nil
}
func profileValue(value string) enscontract.Field {
	return enscontract.Field{State: "value", Value: value}
}
func profileAbsent() enscontract.Field { return enscontract.Field{State: "not_applicable"} }
func (w *Worker) registrationProfile(ctx context.Context, ep *ethrpc.Endpoint, hash common.Hash, name string, number uint64, at time.Time) (enscontract.RegistrationProfile, []common.Address, error) {
	// Prefer the longest explicitly bound registry root. A reverse parent hint
	// never selects a path. Every traversed child is verified at this exact hash.
	var root *RegistryRoot
	for i := range w.options.Manifest.Roots {
		r := &w.options.Manifest.Roots[i]
		if (r.Name == "" || strings.HasSuffix(name, "."+r.Name)) && (root == nil || len(r.Name) > len(root.Name)) {
			root = r
		}
	}
	if root != nil {
		return w.v2RegistrationProfile(ctx, ep, hash, name, *root, number, at)
	}
	return w.v1RegistrationProfile(ctx, ep, hash, name, number, at)
}
func (w *Worker) v1RegistrationProfile(ctx context.Context, ep *ethrpc.Endpoint, hash common.Hash, name string, number uint64, at time.Time) (enscontract.RegistrationProfile, []common.Address, error) {
	p := enscontract.PendingProfile()
	var registrar, wrapper, registry, legacyRegistry common.Address
	registryCount := 0
	for _, d := range w.options.Manifest.Deployments {
		if d.Number > number {
			continue
		}
		if d.Profile == "registry" {
			legacyRegistry = d.Address
			registryCount++
			if d.Address == w.options.Registry {
				registry = d.Address
			}
		}
	}
	if registry == (common.Address{}) && registryCount == 1 {
		registry = legacyRegistry
	}
	if registry == (common.Address{}) {
		p.RegistrationState = "unsupported"
		p.Registrant.State = "unsupported"
		p.Controller.State = "unsupported"
		p.ExpirationTime.State = "unsupported"
		return p, nil, nil
	}
	node := nameHash(name)
	owner, e := profileCall(ctx, ep, hash, registry, "ENSRegistry", "owner", node)
	if e != nil {
		return p, nil, e
	}
	manager := owner[0].(common.Address)
	for _, d := range w.options.Manifest.Deployments {
		if d.Number <= number && d.Profile == "wrapper" && d.Address == manager {
			wrapper = d.Address
		}
	}
	p.Controller = profileAbsent()
	p.Registrant = profileAbsent()
	p.ExpirationTime = profileAbsent()
	p.RegistrationState = "not_applicable"
	if manager != (common.Address{}) {
		p.Controller = profileValue(manager.Hex())
		p.Controller.Model = "registry_manager"
	}
	resolver, e := profileCall(ctx, ep, hash, registry, "ENSRegistry", "resolver", node)
	if e != nil {
		return p, nil, e
	}
	supportedResolver := resolver[0].(common.Address) == (common.Address{})
	for _, d := range w.options.Manifest.Deployments {
		if d.Number <= number && d.Profile == "resolver" && d.Address == resolver[0].(common.Address) {
			supportedResolver = true
		}
	}
	labels := strings.Split(name, ".")
	second := len(labels) == len(strings.Split(w.options.Manifest.ParentName, "."))+1 && strings.HasSuffix(name, "."+w.options.Manifest.ParentName)
	var expiry *big.Int
	if second {
		parentOwner, err := profileCall(ctx, ep, hash, registry, "ENSRegistry", "owner", nameHash(w.options.Manifest.ParentName))
		if err != nil {
			return p, nil, err
		}
		for _, d := range w.options.Manifest.Deployments {
			if d.Number <= number && d.Profile == "registrar" && d.Address == parentOwner[0].(common.Address) {
				registrar = d.Address
			}
		}
		if registrar == (common.Address{}) {
			p.Registrant.State = "unsupported"
			p.ExpirationTime.State = "unsupported"
			p.RegistrationState = "unsupported"
			if !supportedResolver && p.Controller.State == "value" {
				p.Controller = enscontract.Field{State: "unsupported"}
			}
			return p, nil, nil
		}
		base, e := profileCall(ctx, ep, hash, registrar, "BaseRegistrarImplementation", "baseNode")
		if e != nil {
			return p, nil, e
		}
		if common.Hash(base[0].([32]byte)) != nameHash(w.options.Manifest.ParentName) {
			return p, nil, ErrSourceIdentity
		}
		id := new(big.Int).SetBytes(crypto.Keccak256([]byte(labels[0])))
		expires, e := profileCall(ctx, ep, hash, registrar, "BaseRegistrarImplementation", "nameExpires", id)
		if e != nil {
			return p, nil, e
		}
		expiry = expires[0].(*big.Int)
		p.ExpirationTime = profileValue(expiry.String())
		p.RegistrationState = "registered"
		if expiry.Sign() == 0 {
			p.RegistrationState = "unregistered"
			p.ExpirationTime = profileAbsent()
			p.Controller = profileAbsent()
			return p, nil, nil
		}
		if expiry.Cmp(big.NewInt(at.Unix())) <= 0 {
			grace, e := profileCall(ctx, ep, hash, registrar, "BaseRegistrarImplementation", "GRACE_PERIOD")
			if e != nil {
				return p, nil, e
			}
			p.RegistrationState = "expired"
			if new(big.Int).Add(expiry, grace[0].(*big.Int)).Cmp(big.NewInt(at.Unix())) > 0 {
				p.RegistrationState = "grace_period"
			}
			// ownerOf is deliberately unavailable during registrar expiry. Never
			// substitute an old event owner; renewal will produce a fresh observation.
			p.Controller = profileAbsent()
			if p.RegistrationState == "grace_period" {
				p.Registrant.State = "pending"
				if manager == wrapper && wrapper != (common.Address{}) {
					data, e := profileCall(ctx, ep, hash, wrapper, "NameWrapper", "getData", new(big.Int).SetBytes(node.Bytes()))
					if e != nil {
						return p, nil, e
					}
					if data[0].(common.Address) != (common.Address{}) {
						p.Registrant = profileValue(data[0].(common.Address).Hex())
					}
				}
			}
			return p, nil, nil
		}
		owner, e := profileCall(ctx, ep, hash, registrar, "BaseRegistrarImplementation", "ownerOf", id)
		if e != nil {
			return p, nil, e
		}
		p.Registrant = profileValue(owner[0].(common.Address).Hex())
	}
	for _, d := range w.options.Manifest.Deployments {
		if d.Number <= number && d.Profile == "wrapper" && d.Address.Hex() == p.Registrant.Value {
			wrapper = d.Address
		}
	}
	if wrapper != (common.Address{}) && (manager == wrapper || p.Registrant.Value == wrapper.Hex()) {
		data, e := profileCall(ctx, ep, hash, wrapper, "NameWrapper", "getData", new(big.Int).SetBytes(node.Bytes()))
		if e != nil {
			return p, nil, e
		}
		wrappedOwner := data[0].(common.Address)
		wrappedExpiry := data[2].(uint64)
		if wrappedOwner == (common.Address{}) {
			p.Registrant = profileAbsent()
			p.Controller = profileAbsent()
			p.RegistrationState = "expired"
		} else {
			p.Registrant = profileValue(wrappedOwner.Hex())
			p.Controller = profileValue(wrappedOwner.Hex())
			p.Controller.Model = "wrapped_manager"
			p.RegistrationState = "registered"
		}
		if !second {
			p.ExpirationTime = profileValue(new(big.Int).SetUint64(wrappedExpiry).String())
		}
	}
	if !supportedResolver && p.Controller.State == "value" {
		p.Controller = enscontract.Field{State: "unsupported"}
	}
	return p, nil, nil
}

func (w *Worker) verifiedProfileContract(ctx context.Context, ep *ethrpc.Endpoint, hash common.Hash, address common.Address, profile string) error {
	s := rpcSession{endpoint: ep}
	for _, d := range w.options.Manifest.Deployments {
		if d.Address == address && d.Profile == profile {
			return s.verifyCode(ctx, d, hash)
		}
	}
	code, e := s.code(ctx, address, hash)
	if e != nil {
		return e
	}
	for _, impl := range w.options.Manifest.Implementations {
		if impl.Profile != profile || crypto.Keccak256Hash(code) != impl.ProxyCodeHash {
			continue
		}
		d := Deployment{Address: address, CodeHash: impl.ProxyCodeHash, Implementation: impl.Address, ImplementationCodeHash: impl.CodeHash}
		if err := s.verifyCode(ctx, d, hash); err != ErrUnsupportedDeployment {
			return err
		}
	}
	return ErrUnsupportedDeployment
}
func (w *Worker) v2RegistrationProfile(ctx context.Context, ep *ethrpc.Endpoint, hash common.Hash, name string, root RegistryRoot, number uint64, at time.Time) (enscontract.RegistrationProfile, []common.Address, error) {
	p := enscontract.PendingProfile()
	remaining := name
	if root.Name != "" {
		remaining = strings.TrimSuffix(name, "."+root.Name)
	}
	labels := strings.Split(remaining, ".")
	if len(labels) > 32 {
		return p, nil, ErrCollectionLimit
	}
	registry := root.Address
	watches := []common.Address{}
	for i, labelText := range slices.Backward(labels) {
		if e := w.verifiedProfileContract(ctx, ep, hash, registry, "v2_registry"); e != nil {
			if e == ErrUnsupportedDeployment {
				p.RegistrationState = "unsupported"
				p.Registrant.State = "unsupported"
				p.Controller.State = "unsupported"
				p.ExpirationTime.State = "unsupported"
				return p, watches, nil
			}
			return p, watches, e
		}
		watches = append(watches, registry)
		if i > 0 {
			child, e := profileCall(ctx, ep, hash, registry, "ETHRegistry", "getSubregistry", labelText)
			if e != nil {
				return p, watches, e
			}
			registry = child[0].(common.Address)
			if registry == (common.Address{}) {
				p.RegistrationState = "unregistered"
				p.Registrant = profileAbsent()
				p.Controller = profileAbsent()
				p.ExpirationTime = profileAbsent()
				return p, watches, nil
			}
			continue
		}
		label := new(big.Int).SetBytes(crypto.Keccak256([]byte(labelText)))
		token, e := profileCall(ctx, ep, hash, registry, "ETHRegistry", "getTokenId", label)
		if e != nil {
			return p, watches, e
		}
		id := token[0].(*big.Int)
		owner, e := profileCall(ctx, ep, hash, registry, "ETHRegistry", "getOwner", id)
		if e != nil {
			return p, watches, e
		}
		expiry, e := profileCall(ctx, ep, hash, registry, "ETHRegistry", "getExpiry", id)
		if e != nil {
			return p, watches, e
		}
		status, e := profileCall(ctx, ep, hash, registry, "ETHRegistry", "getStatus", id)
		if e != nil {
			return p, watches, e
		}
		p.Registrant = profileAbsent()
		p.Controller = profileAbsent()
		p.ExpirationTime = profileValue(new(big.Int).SetUint64(expiry[0].(uint64)).String())
		switch status[0].(uint8) {
		case 0:
			p.RegistrationState = "unregistered"
		case 1:
			p.RegistrationState = "reserved"
		case 2:
			p.RegistrationState = "registered"
		default:
			return p, watches, ErrUnsupportedDeployment
		}
		if p.RegistrationState == "reserved" {
			prior, _, e := w.v1RegistrationProfile(ctx, ep, hash, name, number, at)
			if e != nil {
				return p, watches, e
			}
			p.Registrant = prior.Registrant
			p.Controller = prior.Controller
		}
		if p.RegistrationState == "registered" && owner[0].(common.Address) != (common.Address{}) {
			p.Registrant = profileValue(owner[0].(common.Address).Hex())
			p.Controller = enscontract.Field{State: "multi_account", Model: "permissioned_registry"}
		}
		if p.RegistrationState == "registered" {
			resolver, e := profileCall(ctx, ep, hash, registry, "ETHRegistry", "getResolver", labelText)
			if e != nil {
				return p, watches, e
			}
			address := resolver[0].(common.Address)
			if address != (common.Address{}) {
				watches = append(watches, address)
				if e := w.verifiedProfileContract(ctx, ep, hash, address, "v2_resolver"); e != nil {
					if e != ErrUnsupportedDeployment {
						return p, watches, e
					}
					p.Controller = enscontract.Field{State: "unsupported"}
				}
			}
		}
		if expiry[0].(uint64) == 0 {
			p.ExpirationTime = profileAbsent()
		}
		if expiry[0].(uint64) > 0 && expiry[0].(uint64) <= uint64(at.Unix()) {
			p.RegistrationState = "expired"
			p.Registrant = profileAbsent()
			p.Controller = profileAbsent()
		}
	}
	return p, watches, nil
}
func (w *Worker) publishProfile(ctx context.Context, ep *ethrpc.Endpoint, job dbgen.EnsSyncJob, data dbgen.GetENSSyncWorkerJobRow, number uint64) error {
	profile, watches, e := w.registrationProfile(ctx, ep, common.BytesToHash(job.TargetHash), data.LookupKey, number, data.BlockTime.Time)
	if e != nil {
		return w.retry(ctx, job, "profile_unavailable")
	}
	raw, e := json.Marshal(profile)
	if e != nil {
		return ErrInvalidInput
	}
	now := time.Now()
	observation := dbgen.PublishENSSyncObservationParams{SourceID: job.SourceID, CandidateID: job.CandidateID, JobID: job.ID, Generation: job.Generation, LeaseToken: job.LeaseToken, Deployment: w.options.Registry.Bytes(), Endpoint: ep.Name, Evidence: "resolver", Outcome: "resolved", Name: &data.LookupKey, ProfileData: raw, ObservedAt: stamp(now), ValidUntil: stamp(now.Add(w.options.Freshness)), RefreshAt: stamp(now.Add(w.options.Freshness / 2)), RetainUntil: stamp(now.Add(w.options.Retention))}
	// Dependency watches are committed before the final fenced publication. A
	// rejected old target remains visible to the scheduler for replacement.
	addresses := make([][]byte, 0, len(watches))
	for _, a := range watches {
		addresses = append(addresses, a.Bytes())
	}
	if len(addresses) > 0 {
		if _, e = dbgen.New(w.repo.db).WatchENSSyncJob(ctx, dbgen.WatchENSSyncJobParams{SourceID: job.SourceID, JobID: job.ID, LeaseToken: job.LeaseToken, Addresses: addresses}); e != nil {
			return e
		}
	}
	if e = w.bindResultDependencies(ctx, job, data.LookupKey, &observation); e != nil {
		return e
	}
	_, e = w.repo.Publish(ctx, observation)
	return collectionError(e)
}
