//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

var integrationDiamondABI = mustIntegrationDiamondABI(`[
  {"type":"function","name":"facets","stateMutability":"view","inputs":[],"outputs":[{"name":"facets_","type":"tuple[]","components":[{"name":"facetAddress","type":"address"},{"name":"functionSelectors","type":"bytes4[]"}]}]},
  {"type":"function","name":"facetFunctionSelectors","stateMutability":"view","inputs":[{"name":"facet","type":"address"}],"outputs":[{"name":"functionSelectors_","type":"bytes4[]"}]},
  {"type":"function","name":"facetAddresses","stateMutability":"view","inputs":[],"outputs":[{"name":"facetAddresses_","type":"address[]"}]},
  {"type":"function","name":"facetAddress","stateMutability":"view","inputs":[{"name":"selector","type":"bytes4"}],"outputs":[{"name":"facetAddress_","type":"address"}]},
  {"type":"function","name":"supportsInterface","stateMutability":"view","inputs":[{"name":"interfaceId","type":"bytes4"}],"outputs":[{"name":"supported","type":"bool"}]},
  {"type":"event","name":"DiamondCut","anonymous":false,"inputs":[
    {"name":"diamondCut","type":"tuple[]","indexed":false,"components":[
      {"name":"facetAddress","type":"address"},
      {"name":"action","type":"uint8"},
      {"name":"functionSelectors","type":"bytes4[]"}
    ]},
    {"name":"init","type":"address","indexed":false},
    {"name":"calldata","type":"bytes","indexed":false}
  ]}
]`)

type integrationDiamondFacet struct {
	FacetAddress      common.Address
	FunctionSelectors [][4]byte
}

type integrationDiamondCut struct {
	FacetAddress      common.Address
	Action            uint8
	FunctionSelectors [][4]byte
}

type integrationDiamondState struct {
	code   map[common.Address][]byte
	facets []integrationDiamondFacet
}

type integrationDiamondService struct {
	diamond common.Address
	states  map[string]integrationDiamondState
	mu      sync.Mutex
	calls   map[string]int
}

func (service *integrationDiamondService) GetCode(
	_ context.Context,
	address common.Address,
	blockReference rpc.BlockNumberOrHash,
) (hexutil.Bytes, error) {
	blockHash, err := service.record("eth_getCode", blockReference)
	if err != nil {
		return nil, err
	}
	return hexutil.Bytes(common.CopyBytes(service.states[blockHash].code[address])), nil
}

func (service *integrationDiamondService) GetStorageAt(
	_ context.Context,
	_ common.Address,
	_ common.Hash,
	blockReference rpc.BlockNumberOrHash,
) (hexutil.Bytes, error) {
	if _, err := service.record("eth_getStorageAt", blockReference); err != nil {
		return nil, err
	}
	return make(hexutil.Bytes, common.HashLength), nil
}

func (service *integrationDiamondService) Call(
	_ context.Context,
	request map[string]any,
	blockReference rpc.BlockNumberOrHash,
) (hexutil.Bytes, error) {
	blockHash, err := service.record("eth_call", blockReference)
	if err != nil {
		return nil, err
	}
	addressText, ok := request["to"].(string)
	if !ok || !common.IsHexAddress(addressText) {
		return nil, errors.New("Diamond integration call target is invalid")
	}
	if common.HexToAddress(addressText) != service.diamond {
		return nil, errors.New("execution reverted")
	}
	data, err := proxyVerificationCallData(request["data"])
	if err != nil || len(data) < 4 {
		return nil, errors.New("Diamond integration call data is invalid")
	}
	method, err := integrationDiamondABI.MethodById(data[:4])
	if err != nil {
		return nil, errors.New("execution reverted")
	}
	state := service.states[blockHash]
	var output []byte
	switch method.Name {
	case "facets":
		output, err = method.Outputs.Pack(state.facets)
	case "facetAddresses":
		addresses := make([]common.Address, len(state.facets))
		for index := range state.facets {
			addresses[index] = state.facets[index].FacetAddress
		}
		output, err = method.Outputs.Pack(addresses)
	case "facetFunctionSelectors":
		values, decodeErr := method.Inputs.Unpack(data[4:])
		if decodeErr != nil || len(values) != 1 {
			return nil, errors.New("Diamond integration facet input is invalid")
		}
		facet, ok := values[0].(common.Address)
		if !ok {
			return nil, errors.New("Diamond integration facet address is invalid")
		}
		selectors := make([][4]byte, 0)
		for _, row := range state.facets {
			if row.FacetAddress == facet {
				selectors = append(selectors, row.FunctionSelectors...)
				break
			}
		}
		output, err = method.Outputs.Pack(selectors)
	case "facetAddress":
		values, decodeErr := method.Inputs.Unpack(data[4:])
		if decodeErr != nil || len(values) != 1 {
			return nil, errors.New("Diamond integration selector input is invalid")
		}
		selector, ok := values[0].([4]byte)
		if !ok {
			return nil, errors.New("Diamond integration selector is invalid")
		}
		facet := common.Address{}
		for _, row := range state.facets {
			if slices.Contains(row.FunctionSelectors, selector) {
				facet = row.FacetAddress
				break
			}
		}
		output, err = method.Outputs.Pack(facet)
	case "supportsInterface":
		values, decodeErr := method.Inputs.Unpack(data[4:])
		if decodeErr != nil || len(values) != 1 {
			return nil, errors.New("Diamond integration interface input is invalid")
		}
		interfaceID, ok := values[0].([4]byte)
		if !ok {
			return nil, errors.New("Diamond integration interface ID is invalid")
		}
		output, err = method.Outputs.Pack(interfaceID == [4]byte{0x48, 0xe2, 0xb0, 0x93})
	default:
		return nil, errors.New("execution reverted")
	}
	if err != nil {
		return nil, fmt.Errorf("encode Diamond integration %s result: %w", method.Name, err)
	}
	return hexutil.Bytes(output), nil
}

func (service *integrationDiamondService) record(
	method string,
	blockReference rpc.BlockNumberOrHash,
) (string, error) {
	if blockReference.BlockHash == nil || !blockReference.RequireCanonical {
		return "", errors.New("Diamond integration RPC was not exact-block canonical")
	}
	hash := blockReference.BlockHash.String()
	if _, exists := service.states[hash]; !exists {
		return "", errors.New("Diamond integration RPC block is unknown")
	}
	service.mu.Lock()
	service.calls[hash+":"+method]++
	service.mu.Unlock()
	return hash, nil
}

func TestDiamondProxyHistoryReorgAndHistoricalABI(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	genesis := testBundle(0, testHash(92_000), testHash(0), testHash(92_100), "diamond-genesis")
	commitCanonical(t, ctx, repository, genesis)
	diamond := integrationContractAddress(0)
	facetA, facetB, facetC := testAddress(92_201), testAddress(92_202), testAddress(92_203)
	init, falseEmitter := testAddress(92_204), testAddress(92_205)
	diamondRuntime := []byte{0x60, 0x25, 0x35}
	facetRuntimeA := []byte{0x60, 0xa1}
	facetRuntimeB := []byte{0x60, 0xb2}
	facetRuntimeC := []byte{0x60, 0xc3}

	setValueSelector := enrich.SignatureSelector("setValue(uint256)")
	transientSelector := enrich.SignatureSelector("transientValue()")
	immutableSelectors := [][4]byte{
		enrich.SignatureSelector("facets()"),
		enrich.SignatureSelector("facetFunctionSelectors(address)"),
		enrich.SignatureSelector("facetAddresses()"),
		enrich.SignatureSelector("facetAddress(bytes4)"),
		enrich.SignatureSelector("supportsInterface(bytes4)"),
	}
	blockOne := integrationDiamondBlockOne(
		t, genesis.Block.Hash(), diamond, facetA, init, falseEmitter,
		setValueSelector, immutableSelectors,
	)
	oldTwo := integrationDiamondBlockTwo(
		t, blockOne.Block.Hash(), diamond, facetB, init,
		setValueSelector, transientSelector, "diamond-old-two",
	)
	newTwo := integrationDiamondBlockTwo(
		t, blockOne.Block.Hash(), diamond, facetC, init,
		setValueSelector, transientSelector, "diamond-new-two",
	)

	states := map[string]integrationDiamondState{
		blockOne.Block.Hash().String(): integrationDiamondRPCState(
			diamond, facetA, diamondRuntime, facetRuntimeA,
			setValueSelector, immutableSelectors,
		),
		oldTwo.Block.Hash().String(): integrationDiamondRPCState(
			diamond, facetB, diamondRuntime, facetRuntimeB,
			setValueSelector, immutableSelectors,
		),
		newTwo.Block.Hash().String(): integrationDiamondRPCState(
			diamond, facetC, diamondRuntime, facetRuntimeC,
			setValueSelector, immutableSelectors,
		),
	}
	blockOneState := states[blockOne.Block.Hash().String()]
	blockOneState.code[falseEmitter] = []byte{0x60, 0xfa}
	states[blockOne.Block.Hash().String()] = blockOneState
	service := &integrationDiamondService{
		diamond: diamond, states: states, calls: make(map[string]int),
	}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name:     "diamond-state",
		Client:   newIntegrationRPCClient(t, "eth", service),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewPostgresProxyProcessorWithOptions(
		db, pool, enrich.ProxyLimits{}, enrich.ProxyDetectionOptions{
			Enabled: true, DiamondEnabled: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
		ID: "diamond-history", LeaseDuration: 2 * time.Second, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	commitCanonical(t, ctx, repository, blockOne)
	runDurableProxyBlock(t, ctx, db, queue, worker, blockOne)
	assertDiamondSnapshot(t, ctx, db, blockOne, diamond, facetA, true)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM diamond_loupe_snapshots
		WHERE chain_id = 1 AND block_hash = $1 AND diamond_address = $2
		  AND detection_state = 'candidate' AND completeness = 'unknown'
		  AND validation = 'interface-only' AND canonical`, 1,
		blockOne.Block.Hash().Bytes(), falseEmitter.Bytes())

	commitCanonical(t, ctx, repository, oldTwo)
	runDurableProxyBlock(t, ctx, db, queue, worker, oldTwo)
	assertDiamondSnapshot(t, ctx, db, oldTwo, diamond, facetB, true)

	applyDerivedReorg(
		t, ctx, repository, blockOne,
		[]chainbundle.Bundle{oldTwo}, []chainbundle.Bundle{newTwo},
		"Diamond facet fork",
	)
	runDurableProxyBlock(t, ctx, db, queue, worker, newTwo)
	assertDiamondSnapshot(t, ctx, db, oldTwo, diamond, facetB, false)
	assertDiamondSnapshot(t, ctx, db, newTwo, diamond, facetC, true)
	assertDiamondHistoryAfterReorg(
		t, ctx, db, oldTwo, newTwo, diamond, facetC, init,
		setValueSelector, transientSelector,
	)

	// A Diamond is never projected through the legacy singular implementation.
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_observations
		WHERE chain_id = 1 AND proxy_address = $1`, 0, diamond.Bytes())
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_detection_evidence
		WHERE chain_id = 1 AND address = $1 AND candidate_kind = 'proxy_v2'
		  AND detection_state = 'confirmed' AND canonical`, 2, diamond.Bytes())

	ref := mustBlockRef(t, newTwo)
	hashA := crypto.Keccak256Hash(facetRuntimeA)
	hashC := crypto.Keccak256Hash(facetRuntimeC)
	const facetABI = `[
	  {"type":"function","name":"setValue","inputs":[{"name":"value","type":"uint256"}]},
	  {"type":"function","name":"inactiveNoise","inputs":[]}
	]`
	insertVerifiedContractFixture(
		t, ctx, db, facetA.Bytes(), hashA.Bytes(), 1, nil,
		"0.8.30", "FacetA", facetABI, `{"FacetA.sol":"contract FacetA {}"}`, `{}`,
	)
	insertVerifiedContractFixture(
		t, ctx, db, facetC.Bytes(), hashC.Bytes(), 1, nil,
		"0.8.30", "FacetC", facetABI, `{"FacetC.sol":"contract FacetC {}"}`, `{}`,
	)
	publishABIStateDiff(t, ctx, db, ref, map[common.Address][]byte{diamond: diamondRuntime})
	abiProcessor, err := enrich.NewPostgresABIProcessor(db)
	if err != nil {
		t.Fatal(err)
	}
	result, err := abiProcessor.Process(ctx, abiIntegrationJob(t, ref))
	if err != nil || result.State != enrich.ResultComplete {
		t.Fatalf("process Diamond historical ABI: result=%+v err=%v", result, err)
	}
	assertDiamondHistoricalDecoding(
		t, ctx, db, newTwo, diamond, facetA, facetC, setValueSelector,
	)

	service.mu.Lock()
	defer service.mu.Unlock()
	for _, block := range []chainbundle.Bundle{blockOne, oldTwo, newTwo} {
		prefix := block.Block.Hash().String() + ":"
		if service.calls[prefix+"eth_getCode"] == 0 ||
			service.calls[prefix+"eth_call"] == 0 ||
			service.calls[prefix+"eth_getStorageAt"] == 0 {
			t.Fatalf("Diamond exact-block RPC calls for %s = %+v", block.Block.Hash(), service.calls)
		}
	}
}

func integrationDiamondBlockOne(
	t *testing.T,
	parent common.Hash,
	diamond, facet, init, falseEmitter common.Address,
	selector [4]byte,
	immutableSelectors [][4]byte,
) chainbundle.Bundle {
	t.Helper()
	data := integrationDiamondCutData(t, []integrationDiamondCut{
		{FacetAddress: facet, Action: 0, FunctionSelectors: [][4]byte{selector}},
		{FacetAddress: diamond, Action: 0, FunctionSelectors: immutableSelectors},
	}, init, []byte{})
	bundle, err := newIntegrationBundle(integrationBundleOptions{
		Number: 1, ParentHash: parent, ExtraData: []byte("diamond-one"),
		Transactions: []integrationTransactionOptions{{
			Type: types.DynamicFeeTxType, ContractCreation: true,
			Data: []byte{0x60, 0x25, 0x35},
			Logs: []*types.Log{
				{
					Address: diamond,
					Topics:  []common.Hash{enrich.SignatureHash("DiamondCut((address,uint8,bytes4[])[],address,bytes)")},
					Data:    data,
				},
				{
					Address: falseEmitter,
					Topics:  []common.Hash{enrich.SignatureHash("DiamondCut((address,uint8,bytes4[])[],address,bytes)")},
					Data:    data,
				},
			},
		}},
		Withdrawals: []*types.Withdrawal{},
		RawExtra:    map[string]any{"integrationVariant": "diamond-one"},
	})
	if err != nil {
		t.Fatalf("build Diamond creation block: %v", err)
	}
	return bundle
}

func integrationDiamondBlockTwo(
	t *testing.T,
	parent common.Hash,
	diamond, replacement, init common.Address,
	selector, transient [4]byte,
	variant string,
) chainbundle.Bundle {
	t.Helper()
	cutData := integrationDiamondCutData(t, []integrationDiamondCut{
		{FacetAddress: replacement, Action: 1, FunctionSelectors: [][4]byte{selector}},
		{FacetAddress: replacement, Action: 0, FunctionSelectors: [][4]byte{transient}},
		{FacetAddress: common.Address{}, Action: 2, FunctionSelectors: [][4]byte{transient}},
	}, init, []byte{0xca, 0xfe})
	setInput := append(append([]byte(nil), selector[:]...), abiUintWord(7)...)
	bundle, err := newIntegrationBundle(integrationBundleOptions{
		Number: 2, ParentHash: parent, ExtraData: []byte(variant),
		Transactions: []integrationTransactionOptions{
			{Type: types.DynamicFeeTxType, To: &diamond, Data: setInput},
			{
				Type: types.DynamicFeeTxType, To: &diamond, Data: []byte{0xca, 0xfe},
				Logs: []*types.Log{{
					Address: diamond,
					Topics:  []common.Hash{enrich.SignatureHash("DiamondCut((address,uint8,bytes4[])[],address,bytes)")},
					Data:    cutData,
				}},
			},
			{Type: types.DynamicFeeTxType, To: &diamond, Data: setInput},
		},
		Withdrawals: []*types.Withdrawal{},
		RawExtra:    map[string]any{"integrationVariant": variant},
	})
	if err != nil {
		t.Fatalf("build Diamond replacement block: %v", err)
	}
	return bundle
}

func integrationDiamondRPCState(
	diamond, facet common.Address,
	diamondRuntime, facetRuntime []byte,
	selector [4]byte,
	immutableSelectors [][4]byte,
) integrationDiamondState {
	return integrationDiamondState{
		code: map[common.Address][]byte{
			diamond: common.CopyBytes(diamondRuntime),
			facet:   common.CopyBytes(facetRuntime),
		},
		facets: []integrationDiamondFacet{
			{FacetAddress: diamond, FunctionSelectors: append([][4]byte(nil), immutableSelectors...)},
			{FacetAddress: facet, FunctionSelectors: [][4]byte{selector}},
		},
	}
}

func integrationDiamondCutData(
	t *testing.T,
	cuts []integrationDiamondCut,
	init common.Address,
	calldata []byte,
) []byte {
	t.Helper()
	definition := integrationDiamondABI.Events["DiamondCut"]
	data, err := definition.Inputs.Pack(cuts, init, calldata)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertDiamondSnapshot(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block chainbundle.Bundle,
	diamond, externalFacet common.Address,
	canonical bool,
) {
	t.Helper()
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM diamond_loupe_snapshots AS snapshot
		WHERE snapshot.chain_id = 1 AND snapshot.block_hash = $1
		  AND snapshot.diamond_address = $2
		  AND snapshot.detection_state = 'confirmed'
		  AND snapshot.completeness = 'complete'
		  AND snapshot.validation = 'full'
		  AND snapshot.standard_diamond_cut = 'absent'
		  AND snapshot.canonical = $3`, 1,
		block.Block.Hash().Bytes(), diamond.Bytes(), canonical)
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM diamond_loupe_snapshots AS snapshot
		JOIN diamond_loupe_facets AS facet ON facet.snapshot_id = snapshot.id
		WHERE snapshot.chain_id = 1 AND snapshot.block_hash = $1
		  AND snapshot.diamond_address = $2
		  AND facet.facet_address = $3 AND facet.facet_kind = 'facet'
		  AND facet.code_exists AND facet.code_hash IS NOT NULL`, 1,
		block.Block.Hash().Bytes(), diamond.Bytes(), externalFacet.Bytes())
}

func assertDiamondHistoryAfterReorg(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	oldBlock, newBlock chainbundle.Bundle,
	diamond, currentFacet, init common.Address,
	selector, transient [4]byte,
) {
	t.Helper()
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM diamond_cut_events
		WHERE chain_id = 1 AND block_hash = $1 AND NOT canonical`, 1,
		oldBlock.Block.Hash().Bytes())
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM diamond_cut_events
		WHERE chain_id = 1 AND block_hash = $1 AND canonical`, 1,
		newBlock.Block.Hash().Bytes())
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM diamond_loupe_facets AS facet
		JOIN diamond_loupe_snapshots AS snapshot ON snapshot.id = facet.snapshot_id
		WHERE snapshot.chain_id = 1 AND facet.facet_address = $1`, 0, init.Bytes())
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM diamond_selector_changes
		WHERE chain_id = 1 AND facet_address = $1`, 0, init.Bytes())

	var facetBytes []byte
	err := db.QueryRowContext(ctx, `
		SELECT facet_address
		FROM canonical_diamond_selector_intervals
		WHERE chain_id = 1 AND diamond_address = $1 AND selector = $2
		  AND valid_to_block_number IS NULL`, diamond.Bytes(), selector[:]).Scan(&facetBytes)
	if err != nil {
		t.Fatalf("query current Diamond selector interval: %v", err)
	}
	if common.BytesToAddress(facetBytes) != currentFacet {
		t.Fatalf("current Diamond selector facet=%x want=%s", facetBytes, currentFacet)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM canonical_diamond_selector_intervals
		WHERE chain_id = 1 AND diamond_address = $1 AND selector = $2
		  AND valid_to_block_number IS NOT NULL`, 1, diamond.Bytes(), transient[:])
}

func assertDiamondHistoricalDecoding(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block chainbundle.Bundle,
	diamond, beforeFacet, afterFacet common.Address,
	selector [4]byte,
) {
	t.Helper()
	for _, expected := range []struct {
		transaction int
		facet       common.Address
	}{
		{transaction: 0, facet: beforeFacet},
		{transaction: 2, facet: afterFacet},
	} {
		transaction := block.Block.Transactions()[expected.transaction]
		var status, source, signature string
		var sourceAddress []byte
		err := db.QueryRowContext(ctx, `
			SELECT status, source, signature, source_address
			FROM abi_decodings
			WHERE chain_id = 1 AND block_hash = $1
			  AND transaction_hash = $2
			  AND object_kind = 'transaction_calldata'`,
			block.Block.Hash().Bytes(), transaction.Hash().Bytes(),
		).Scan(&status, &source, &signature, &sourceAddress)
		if err != nil {
			t.Fatalf("query Diamond transaction %d decoding: %v", expected.transaction, err)
		}
		if status != "decoded" || source != "diamond_facet" ||
			signature != "setValue(uint256)" ||
			common.BytesToAddress(sourceAddress) != expected.facet {
			t.Fatalf(
				"Diamond transaction %d decoding=%s/%s/%s/%x want facet=%s",
				expected.transaction, status, source, signature, sourceAddress, expected.facet,
			)
		}
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM contract_abis
		WHERE chain_id = 1 AND block_hash = $1 AND address = $2
		  AND source = 'diamond_facet'
		  AND source_address IN ($3, $4)
		  AND selector_scope <> decode(repeat('00', 32), 'hex')
		  AND jsonb_array_length(abi) = 1
		  AND abi->0->>'name' = 'setValue'`, 2,
		block.Block.Hash().Bytes(), diamond.Bytes(), beforeFacet.Bytes(), afterFacet.Bytes())
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM contract_abis
		WHERE chain_id = 1 AND block_hash = $1 AND address = $2
		  AND source = 'diamond_facet'
		  AND EXISTS (
		      SELECT 1 FROM jsonb_array_elements(abi) AS entry
		      WHERE entry->>'name' = 'inactiveNoise'
		  )`, 0, block.Block.Hash().Bytes(), diamond.Bytes())
	if selector == ([4]byte{}) {
		t.Fatal("Diamond historical selector fixture is zero")
	}
}

func mustIntegrationDiamondABI(document string) gethabi.ABI {
	parsed, err := gethabi.JSON(strings.NewReader(document))
	if err != nil {
		panic(err)
	}
	return parsed
}
