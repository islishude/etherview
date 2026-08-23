//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestUpgradeableBeaconSharedHistoryFanoutAndReorg(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(98_000), testHash(0), testHash(98_100), "beacon-history-genesis")
	commitCanonical(t, ctx, repository, genesis)

	proxyOne, proxyTwo := integrationContractAddress(0), integrationContractAddress(1)
	beacon := testAddress(9_810)
	implementationA, implementationB, implementationC := testAddress(9_811), testAddress(9_812), testAddress(9_813)
	creation, err := newIntegrationBundle(integrationBundleOptions{
		Number: 1, ParentHash: genesis.Block.Hash(), ExtraData: []byte("beacon-history-create"),
		Transactions: []integrationTransactionOptions{
			{Type: types.DynamicFeeTxType, ContractCreation: true, Data: []byte{0x60, 0xa1}},
			{Type: types.DynamicFeeTxType, ContractCreation: true, Data: []byte{0x60, 0xa2}},
		},
		Withdrawals: []*types.Withdrawal{},
		RawExtra:    map[string]any{"integrationVariant": "beacon-history-create"},
	})
	if err != nil {
		t.Fatalf("build shared BeaconProxy creation block: %v", err)
	}
	oldUpgrade := beaconUpgradeBundle(t, 2, creation.Block.Hash(), beacon, implementationB, "beacon-upgrade-b")
	replacementUpgrade := beaconUpgradeBundle(t, 2, creation.Block.Hash(), beacon, implementationC, "beacon-upgrade-c")

	states := map[string]map[string]proxyContractState{
		creation.Block.Hash().String(): {
			proxyOne.String():        {code: []byte{0x60, 0xb1}, beacon: &beacon},
			proxyTwo.String():        {code: []byte{0x60, 0xb2}, beacon: &beacon},
			beacon.String():          {code: []byte{0x60, 0xba}, beaconImplementation: &implementationA},
			implementationA.String(): {code: []byte{0x60, 0xa1}},
		},
		oldUpgrade.Block.Hash().String(): {
			beacon.String():          {code: []byte{0x60, 0xba}, beaconImplementation: &implementationB},
			implementationB.String(): {code: []byte{0x60, 0xb1}},
		},
		replacementUpgrade.Block.Hash().String(): {
			beacon.String():          {code: []byte{0x60, 0xba}, beaconImplementation: &implementationC},
			implementationC.String(): {code: []byte{0x60, 0xc1}},
		},
	}
	var callMu sync.Mutex
	calls := make(map[string][]string)
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{
		proxyStateEndpoint(t, "beacon-history-a", states, nil, &callMu, calls),
		proxyStateEndpoint(t, "beacon-history-b", states, nil, &callMu, calls),
	}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := enrich.NewPostgresProxyProcessor(db, pool, enrich.ProxyLimits{})
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
		ID: "beacon-history", LeaseDuration: 2 * time.Second, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	commitCanonical(t, ctx, repository, creation)
	creationJob := runDurableProxyBlock(t, ctx, db, queue, worker, creation)
	assertPublishedGeneration(t, ctx, db, creationJob.Job.ID, 1)
	assertPublishedBeaconGeneration(t, ctx, db, creation, beacon, creationJob.Job.ID, true)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM proxy_observations
		WHERE chain_id = 1 AND block_hash = $1 AND beacon_address = $2
		  AND proxy_address IN ($3, $4) AND proxy_kind = 'beacon' AND canonical`, 2,
		creation.Block.Hash().Bytes(), beacon.Bytes(), proxyOne.Bytes(), proxyTwo.Bytes())
	assertSharedBeaconImplementation(t, ctx, db, creation, beacon, implementationA, proxyOne, proxyTwo)
	assertOneEthCall(t, calls[creation.Block.Hash().String()])
	connection, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire Beacon count connection: %v", err)
	}
	defer connection.Close() //nolint:errcheck
	var beaconProxyCount string
	err = connection.Raw(func(driverConnection any) error {
		pgxConnection, ok := driverConnection.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("Beacon count requires pgx stdlib, got %T", driverConnection)
		}
		var countErr error
		beaconProxyCount, countErr = dbgen.New(pgxConnection.Conn()).CountCurrentBeaconProxies(
			ctx, beacon.Bytes(), pgtype.Numeric{Int: common.Big1, Valid: true},
		)
		return countErr
	})
	if err != nil {
		t.Fatalf("count current Beacon proxies: %v", err)
	}
	if beaconProxyCount != "2" {
		rows, queryErr := db.QueryContext(ctx, `
			SELECT encode(observation.proxy_address, 'hex'), observation.proxy_pattern,
			       encode(observation.proxy_code_hash, 'hex'),
			       COALESCE((
			           SELECT encode(code.code_hash, 'hex')
			           FROM contract_code_observations AS code
			           WHERE code.chain_id = observation.chain_id
			             AND code.address = observation.proxy_address
			             AND code.canonical
			           ORDER BY code.block_number DESC, code.observed_at DESC
			           LIMIT 1
			       ), ''),
			       EXISTS (
			           SELECT 1 FROM proxy_detection_evidence AS evidence
			           WHERE evidence.chain_id = observation.chain_id
			             AND evidence.address = observation.proxy_address
			             AND evidence.candidate_kind = 'proxy'
			             AND evidence.canonical
			       )
			FROM proxy_observations AS observation
			WHERE observation.chain_id = 1
			  AND observation.proxy_address IN ($1, $2)
			ORDER BY observation.proxy_address`, proxyOne.Bytes(), proxyTwo.Bytes())
		if queryErr == nil {
			for rows.Next() {
				var address, pattern, observationCodeHash, currentCodeHash string
				var hasNegative bool
				if scanErr := rows.Scan(
					&address, &pattern, &observationCodeHash, &currentCodeHash, &hasNegative,
				); scanErr == nil {
					t.Logf("Beacon count candidate address=%s pattern=%s observation_code=%s current_code=%s negative=%t",
						address, pattern, observationCodeHash, currentCodeHash, hasNegative)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
		t.Fatalf("current Beacon proxy count = %s, want 2", beaconProxyCount)
	}

	commitCanonical(t, ctx, repository, oldUpgrade)
	oldUpgradeJob := runDurableProxyBlock(t, ctx, db, queue, worker, oldUpgrade)
	assertPublishedGeneration(t, ctx, db, oldUpgradeJob.Job.ID, 1)
	assertPublishedBeaconGeneration(t, ctx, db, oldUpgrade, beacon, oldUpgradeJob.Job.ID, true)
	assertProxyUpgradeEvent(t, ctx, db, oldUpgrade, beacon, implementationB, true)
	assertSharedBeaconImplementation(t, ctx, db, oldUpgrade, beacon, implementationB, proxyOne, proxyTwo)
	assertOneEthCall(t, calls[oldUpgrade.Block.Hash().String()])

	applyDerivedReorg(
		t, ctx, repository, creation,
		[]chainbundle.Bundle{oldUpgrade}, []chainbundle.Bundle{replacementUpgrade},
		"shared beacon implementation fork",
	)
	assertPublishedGeneration(t, ctx, db, oldUpgradeJob.Job.ID, 1)
	assertPublishedBeaconGeneration(t, ctx, db, oldUpgrade, beacon, oldUpgradeJob.Job.ID, false)
	assertProxyUpgradeEvent(t, ctx, db, oldUpgrade, beacon, implementationB, false)
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM beacon_implementation_observations
		WHERE chain_id = 1 AND block_hash = $1 AND beacon_address = $2
		  AND implementation_address = $3 AND NOT canonical`, 1,
		oldUpgrade.Block.Hash().Bytes(), beacon.Bytes(), implementationB.Bytes())

	replacementJob := runDurableProxyBlock(t, ctx, db, queue, worker, replacementUpgrade)
	assertPublishedGeneration(t, ctx, db, replacementJob.Job.ID, 1)
	assertPublishedBeaconGeneration(t, ctx, db, replacementUpgrade, beacon, replacementJob.Job.ID, true)
	assertProxyUpgradeEvent(t, ctx, db, replacementUpgrade, beacon, implementationC, true)
	assertSharedBeaconImplementation(t, ctx, db, replacementUpgrade, beacon, implementationC, proxyOne, proxyTwo)
	assertOneEthCall(t, calls[replacementUpgrade.Block.Hash().String()])
	assertOneStateEndpointPerBlock(t, calls)
}

func beaconUpgradeBundle(
	t *testing.T,
	number uint64,
	parent common.Hash,
	beacon, implementation common.Address,
	variant string,
) chainbundle.Bundle {
	t.Helper()
	bundle, err := newIntegrationBundle(integrationBundleOptions{
		Number: number, ParentHash: parent, ExtraData: []byte(variant),
		Transactions: []integrationTransactionOptions{{
			Type: types.DynamicFeeTxType, To: &beacon, Data: implementation.Bytes(),
			Logs: []*types.Log{{
				Address: beacon,
				Topics: []common.Hash{
					enrich.SignatureHash("Upgraded(address)"),
					common.BytesToHash(implementation.Bytes()),
				},
				Data: []byte{},
			}},
		}},
		Withdrawals: []*types.Withdrawal{},
		RawExtra:    map[string]any{"integrationVariant": variant},
	})
	if err != nil {
		t.Fatalf("build Beacon upgrade block: %v", err)
	}
	return bundle
}

func assertPublishedBeaconGeneration(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block chainbundle.Bundle,
	beacon common.Address,
	jobID string,
	canonical bool,
) {
	t.Helper()
	assertRowCount(t, ctx, db, `
		SELECT count(*)
		FROM beacon_observation_generations AS witness
		JOIN beacon_implementation_observations AS observation
		  ON observation.chain_id = witness.chain_id
		 AND observation.beacon_address = witness.beacon_address
		 AND observation.block_hash = witness.observation_block_hash
		 AND observation.stage_version = witness.observation_stage_version
		JOIN published_block_stage_results AS published
		  ON published.chain_id = witness.chain_id
		 AND published.block_hash = witness.observation_block_hash
		 AND published.stage = 'proxy'
		 AND published.stage_version = witness.observation_stage_version
		 AND published.durable_job_id = witness.durable_job_id
		 AND published.job_generation = witness.job_generation
		WHERE witness.chain_id = 1
		  AND witness.beacon_address = $1
		  AND witness.observation_block_hash = $2
		  AND witness.durable_job_id = $3
		  AND witness.job_generation = $4
		  AND observation.canonical = $5`, 1,
		beacon.Bytes(), block.Block.Hash().Bytes(), jobID, 1, canonical)
}

func assertSharedBeaconImplementation(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	tip chainbundle.Bundle,
	beacon, implementation, proxyOne, proxyTwo common.Address,
) {
	t.Helper()
	reference := mustBlockRef(t, tip)
	assertRowCount(t, ctx, db, `
		WITH linked_proxy AS (
		    SELECT DISTINCT ON (proxy.proxy_address)
		           proxy.proxy_address, proxy.beacon_address
		    FROM proxy_observations AS proxy
		    JOIN canonical_blocks AS canonical
		      ON canonical.chain_id = proxy.chain_id
		     AND canonical.number = proxy.block_number
		     AND canonical.block_hash = proxy.block_hash
		    JOIN proxy_observation_generations AS witness
		      ON witness.chain_id = proxy.chain_id
		     AND witness.proxy_address = proxy.proxy_address
		     AND witness.observation_block_hash = proxy.block_hash
		     AND witness.observation_stage_version = proxy.stage_version
		    JOIN published_block_stage_results AS published
		      ON published.chain_id = witness.chain_id
		     AND published.block_hash = witness.observation_block_hash
		     AND published.stage = 'proxy'
		     AND published.stage_version = witness.observation_stage_version
		     AND published.durable_job_id = witness.durable_job_id
		     AND published.job_generation = witness.job_generation
		    WHERE proxy.chain_id = 1 AND proxy.block_number <= $1::numeric
		      AND proxy.beacon_address = $2 AND proxy.canonical
		      AND proxy.proxy_address IN ($3, $4)
		    ORDER BY proxy.proxy_address, proxy.block_number DESC, proxy.block_hash DESC
		), current_beacon AS (
		    SELECT observation.implementation_address
		    FROM beacon_implementation_observations AS observation
		    JOIN canonical_blocks AS canonical
		      ON canonical.chain_id = observation.chain_id
		     AND canonical.number = observation.block_number
		     AND canonical.block_hash = observation.block_hash
		    JOIN beacon_observation_generations AS witness
		      ON witness.chain_id = observation.chain_id
		     AND witness.beacon_address = observation.beacon_address
		     AND witness.observation_block_hash = observation.block_hash
		     AND witness.observation_stage_version = observation.stage_version
		    JOIN published_block_stage_results AS published
		      ON published.chain_id = witness.chain_id
		     AND published.block_hash = witness.observation_block_hash
		     AND published.stage = 'proxy'
		     AND published.stage_version = witness.observation_stage_version
		     AND published.durable_job_id = witness.durable_job_id
		     AND published.job_generation = witness.job_generation
		    WHERE observation.chain_id = 1
		      AND observation.beacon_address = $2
		      AND observation.block_number <= $1::numeric
		      AND observation.canonical
		    ORDER BY observation.block_number DESC, observation.block_hash DESC
		    LIMIT 1
		)
		SELECT count(*)
		FROM linked_proxy
		CROSS JOIN current_beacon
		WHERE current_beacon.implementation_address = $5`, 2,
		reference.Number, beacon.Bytes(), proxyOne.Bytes(), proxyTwo.Bytes(), implementation.Bytes())
}
