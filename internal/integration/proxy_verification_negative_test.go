//go:build integration

package integration_test

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/etherscan"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
)

func TestPublishedNegativeDetectionShadowsCurrentProxyBindings(t *testing.T) {
	tests := []struct {
		name        string
		candidate   string
		pattern     string
		artifact    string
		addressSeed uint64
	}{
		{
			name: "proxy implementation slot becomes invalid", candidate: "proxy",
			pattern: "erc1967", artifact: "erc1967_proxy", addressSeed: 9_600,
		},
		{
			name: "beacon implementation becomes invalid", candidate: "beacon",
			pattern: "beacon", artifact: "beacon_proxy", addressSeed: 9_620,
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := newMigratedPostgres(t)
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			core, err := store.NewPostgresRepository(db)
			if err != nil {
				t.Fatal(err)
			}
			seed := uint64(96_000 + index*100)
			genesis := testBundle(0, testHash(seed), testHash(0), testHash(seed+10), "negative-shadow-genesis")
			blockOne := testBundle(1, testHash(seed+1), genesis.Block.Hash(), testHash(seed+11), "negative-shadow-one")
			oldTwo := testBundle(2, testHash(seed+2), blockOne.Block.Hash(), testHash(seed+12), "negative-shadow-old-two")
			newTwo := testBundle(2, testHash(seed+22), blockOne.Block.Hash(), testHash(seed+32), "negative-shadow-new-two")
			oldThree := testBundle(3, testHash(seed+3), newTwo.Block.Hash(), testHash(seed+13), "negative-shadow-old-three")
			newThree := testBundle(3, testHash(seed+23), newTwo.Block.Hash(), testHash(seed+33), "negative-shadow-new-three")
			commitCanonical(t, ctx, core, genesis)
			commitCanonical(t, ctx, core, blockOne)
			blockOneRef := mustBlockRef(t, blockOne)

			proxy := testAddress(test.addressSeed)
			implementation := testAddress(test.addressSeed + 1)
			beacon := testAddress(test.addressSeed + 2)
			proxyCode := []byte{0x60, byte(0x81 + index), 0x60, 0x00}
			implementationCode := []byte{0x60, byte(0x91 + index)}
			beaconCode := []byte{0x60, byte(0xa1 + index)}
			proxyCodeHash := common.BytesToHash(crypto.Keccak256(proxyCode))
			implementationCodeHash := common.BytesToHash(crypto.Keccak256(implementationCode))
			beaconCodeHash := common.BytesToHash(crypto.Keccak256(beaconCode))
			generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)
			var proxyImmutable *common.Address
			if test.pattern == "beacon" {
				proxyImmutable = &beacon
			}
			proxyArtifactJob := insertAuthenticatedProxyArtifactFixture(
				t, ctx, db, blockOneRef, generation, compilerDigest, executorDigest,
				proxy, proxyCodeHash, proxyCode, test.artifact, proxyImmutable,
			)
			insertProxyVerificationCode(
				t, ctx, db, blockOneRef, implementation, implementationCodeHash, implementationCode,
			)
			insertProxyVerificationSource(
				t, ctx, db, implementation, implementationCodeHash, "Implementation",
			)
			negativeAddress := proxy
			negativeCode := proxyCode
			negativeCodeHash := proxyCodeHash
			negativeSourceJob := proxyArtifactJob
			states := map[common.Address]proxyVerificationRPCState{
				proxy:          {code: proxyCode, implementation: &implementation},
				implementation: {code: implementationCode},
			}
			if test.pattern == "beacon" {
				beaconArtifactJob := insertAuthenticatedProxyArtifactFixture(
					t, ctx, db, blockOneRef, generation, compilerDigest, executorDigest,
					beacon, beaconCodeHash, beaconCode, "upgradeable_beacon", nil,
				)
				states[proxy] = proxyVerificationRPCState{code: proxyCode, beacon: &beacon}
				states[beacon] = proxyVerificationRPCState{
					code: beaconCode, beaconImplementation: &implementation,
				}
				negativeAddress = beacon
				negativeCode = beaconCode
				negativeCodeHash = beaconCodeHash
				negativeSourceJob = beaconArtifactJob
			}
			publishAuthenticatedProxyState(t, ctx, db, blockOneRef, states)

			repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{})
			if err != nil {
				t.Fatal(err)
			}
			service, err := verify.NewService(repository, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			backend, err := etherscan.NewPostgresBackend(db, etherscan.PostgresOptions{
				ChainID: 1, Verification: service,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := backend.Execute(ctx, etherscan.Request{
				Module: "contract", Action: "verifyproxycontract",
				Values: url.Values{
					"address": {proxy.Hex()}, "expectedimplementation": {implementation.Hex()},
				},
			})
			if err != nil {
				t.Fatalf("submit exact %s binding: %v", test.pattern, err)
			}
			guid, _ := result.(string)
			lease, found, err := repository.Claim(ctx, "negative-shadow-completion", time.Minute)
			if err != nil || !found || lease.Job.ID != guid {
				t.Fatalf("claim exact %s binding: lease=%+v found=%t error=%v", test.pattern, lease, found, err)
			}

			commitCanonical(t, ctx, core, oldTwo)
			oldTwoRef := mustBlockRef(t, oldTwo)
			publishProxyVerificationInteractionCoverage(t, ctx, db, oldTwoRef)
			insertProxyVerificationReplayTarget(
				t, ctx, db, oldTwoRef, negativeAddress, test.candidate, negativeSourceJob,
			)
			publishAuthenticatedProxyReplaySources(
				t, ctx, db, oldTwoRef,
				&proxyVerificationRPCService{
					blockHash: oldTwoRef.Hash,
					states: map[common.Address]proxyVerificationRPCState{
						negativeAddress: {code: negativeCode},
					},
				},
				negativeSourceJob,
			)
			assertRowCount(t, ctx, db, `
				SELECT count(*)
				FROM proxy_detection_evidence AS evidence
				JOIN published_block_stage_results AS published
				  ON published.chain_id = evidence.chain_id
				 AND published.block_hash = evidence.block_hash
				 AND published.stage = 'proxy'
				 AND published.stage_version = evidence.stage_version
				 AND published.durable_job_id = evidence.durable_job_id
				 AND published.job_generation = evidence.job_generation
				 AND published.state = 'complete'
				WHERE evidence.chain_id = 1 AND evidence.address = $1
				  AND evidence.block_hash = $2 AND evidence.code_hash = $3
				  AND evidence.candidate_kind = $4 AND evidence.canonical`,
				1, negativeAddress.Bytes(), oldTwoRef.Hash.Bytes(), negativeCodeHash.Bytes(), test.candidate,
			)
			if completeErr := repository.CompleteProxyV2(ctx, lease); !errors.Is(completeErr, verify.ErrTargetNotCanonical) {
				t.Fatalf("complete %s binding under newer negative error = %v", test.pattern, completeErr)
			}

			applyDerivedReorg(
				t, ctx, core, blockOne, []chainbundle.Bundle{oldTwo},
				[]chainbundle.Bundle{newTwo}, "remove published negative proxy evidence",
			)
			newTwoRef := mustBlockRef(t, newTwo)
			publishEmptyProxyVerificationCoverage(t, ctx, db, newTwoRef)
			if err := repository.CompleteProxyV2(ctx, lease); err != nil {
				t.Fatalf("complete %s binding after negative reorg: %v", test.pattern, err)
			}
			assertProxyVerificationSource(t, ctx, backend, proxy, implementation, true)

			commitCanonical(t, ctx, core, oldThree)
			oldThreeRef := mustBlockRef(t, oldThree)
			publishProxyVerificationInteractionCoverage(t, ctx, db, oldThreeRef)
			insertProxyVerificationReplayTarget(
				t, ctx, db, oldThreeRef, negativeAddress, test.candidate, negativeSourceJob,
			)
			publishAuthenticatedProxyReplaySources(
				t, ctx, db, oldThreeRef,
				&proxyVerificationRPCService{
					blockHash: oldThreeRef.Hash,
					states: map[common.Address]proxyVerificationRPCState{
						negativeAddress: {code: negativeCode},
					},
				},
				negativeSourceJob,
			)
			assertProxyVerificationSource(t, ctx, backend, proxy, common.Address{}, false)
			before := proxyVerificationJobCount(t, ctx, db)
			if _, err := backend.Execute(ctx, etherscan.Request{
				Module: "contract", Action: "verifyproxycontract",
				Values: url.Values{
					"address": {proxy.Hex()}, "expectedimplementation": {implementation.Hex()},
				},
			}); !errors.Is(err, etherscan.ErrProxyVerificationTargetUnavailable) {
				t.Fatalf("%s admission under newer negative error = %v", test.pattern, err)
			}
			if got := proxyVerificationJobCount(t, ctx, db); got != before {
				t.Fatalf("negative-shadowed %s admission created a job: before=%d after=%d", test.pattern, before, got)
			}

			applyDerivedReorg(
				t, ctx, core, newTwo, []chainbundle.Bundle{oldThree},
				[]chainbundle.Bundle{newThree}, "restore binding after negative evidence reorg",
			)
			newThreeRef := mustBlockRef(t, newThree)
			publishEmptyProxyVerificationCoverage(t, ctx, db, newThreeRef)
			assertProxyVerificationSource(t, ctx, backend, proxy, implementation, true)
			reused, err := backend.Execute(ctx, etherscan.Request{
				Module: "contract", Action: "verifyproxycontract",
				Values: url.Values{
					"address": {proxy.Hex()}, "expectedimplementation": {implementation.Hex()},
				},
			})
			if err != nil || reused != guid {
				t.Fatalf("%s binding after negative reorg = %#v, error=%v, want %s", test.pattern, reused, err, guid)
			}
		})
	}
}
