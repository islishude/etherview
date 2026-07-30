//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/etherscan"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
)

func TestProxyVerificationIsDurableIdempotentAndUpgradeSafe(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(95_000), testHash(0), testHash(95_100), "proxy-verification-genesis")
	blockOne := testBundle(1, testHash(95_001), genesis.Block.Hash(), testHash(95_101), "proxy-verification-one")
	commitCanonical(t, ctx, core, genesis)
	commitCanonical(t, ctx, core, blockOne)
	blockOneRef := mustBlockRef(t, blockOne)

	proxy := testAddress(9_500)
	implementationOne := testAddress(9_501)
	implementationTwo := testAddress(9_502)
	implementationThree := testAddress(9_503)
	proxyCode := []byte{0x60, 0x01}
	implementationCodeOne := []byte{0x60, 0x02}
	implementationCodeTwo := []byte{0x60, 0x03}
	implementationCodeThree := []byte{0x60, 0x04}
	proxyHash := common.BytesToHash(crypto.Keccak256(proxyCode))
	implementationHashOne := common.BytesToHash(crypto.Keccak256(implementationCodeOne))
	implementationHashTwo := common.BytesToHash(crypto.Keccak256(implementationCodeTwo))
	implementationHashThree := common.BytesToHash(crypto.Keccak256(implementationCodeThree))

	insertProxyVerificationCode(t, ctx, db, blockOneRef, proxy, proxyHash, proxyCode)
	insertProxyVerificationCode(t, ctx, db, blockOneRef, implementationOne, implementationHashOne, implementationCodeOne)
	insertProxyVerificationObservation(
		t, ctx, db, blockOneRef, proxy, proxyHash, implementationOne, implementationHashOne,
	)
	insertProxyVerificationSource(t, ctx, db, proxy, proxyHash, "Proxy")
	insertProxyVerificationSource(t, ctx, db, implementationOne, implementationHashOne, "Implementation")

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

	submit := func(expected common.Address) (string, error) {
		result, executeErr := backend.Execute(ctx, etherscan.Request{
			Module: "contract", Action: "verifyproxycontract",
			Values: url.Values{
				"address":                {proxy.Hex()},
				"expectedimplementation": {expected.Hex()},
			},
		})
		if executeErr != nil {
			return "", executeErr
		}
		guid, _ := result.(string)
		return guid, nil
	}

	before := proxyVerificationJobCount(t, ctx, db)
	if _, err := submit(implementationTwo); !errors.Is(err, etherscan.ErrProxyExpectedImplementationMismatch) {
		t.Fatalf("wrong expected implementation error = %v", err)
	}
	if got := proxyVerificationJobCount(t, ctx, db); got != before {
		t.Fatalf("wrong expected implementation created a job: before=%d after=%d", before, got)
	}
	guid, err := submit(implementationOne)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := submit(implementationOne)
	if err != nil || duplicate != guid {
		t.Fatalf("duplicate submission = %q, error=%v, want %q", duplicate, err, guid)
	}
	completeProxyVerification(t, ctx, repository, guid)

	status, err := backend.Execute(ctx, etherscan.Request{
		Module: "contract", Action: "checkproxyverification",
		Values: url.Values{"guid": {guid}},
	})
	if err != nil || status != "Pass - Verified" {
		t.Fatalf("proxy status = %#v, error=%v", status, err)
	}
	assertProxyVerificationSource(t, ctx, backend, proxy, implementationOne, true)
	if _, err := db.ExecContext(ctx, `
		UPDATE verified_proxy_contracts SET proxy_kind = 'beacon'
		WHERE verification_job_id = $1::uuid`, guid); err == nil {
		t.Fatal("immutable proxy verification publication accepted an update")
	}

	blockTwo := testBundle(2, testHash(95_002), blockOne.Block.Hash(), testHash(95_102), "proxy-verification-two")
	commitCanonical(t, ctx, core, blockTwo)
	blockTwoRef := mustBlockRef(t, blockTwo)
	insertProxyVerificationCode(t, ctx, db, blockTwoRef, proxy, proxyHash, proxyCode)
	insertProxyVerificationCode(t, ctx, db, blockTwoRef, implementationTwo, implementationHashTwo, implementationCodeTwo)
	insertProxyVerificationObservation(
		t, ctx, db, blockTwoRef, proxy, proxyHash, implementationTwo, implementationHashTwo,
	)
	assertProxyVerificationSource(t, ctx, backend, proxy, common.Address{}, false)
	if _, err := submit(implementationTwo); !errors.Is(err, etherscan.ErrProxyImplementationUnverified) {
		t.Fatalf("unverified upgraded implementation error = %v", err)
	}
	insertProxyVerificationSource(t, ctx, db, implementationTwo, implementationHashTwo, "ImplementationV2")
	upgradedGUID, err := submit(implementationTwo)
	if err != nil {
		t.Fatal(err)
	}
	completeProxyVerification(t, ctx, repository, upgradedGUID)
	assertProxyVerificationSource(t, ctx, backend, proxy, implementationTwo, true)

	blockThree := testBundle(3, testHash(95_003), blockTwo.Block.Hash(), testHash(95_103), "proxy-verification-three")
	commitCanonical(t, ctx, core, blockThree)
	blockThreeRef := mustBlockRef(t, blockThree)
	insertProxyVerificationCode(t, ctx, db, blockThreeRef, proxy, proxyHash, proxyCode)
	insertProxyVerificationCode(t, ctx, db, blockThreeRef, implementationThree, implementationHashThree, implementationCodeThree)
	insertProxyVerificationObservation(
		t, ctx, db, blockThreeRef, proxy, proxyHash, implementationThree, implementationHashThree,
	)
	insertProxyVerificationSource(t, ctx, db, implementationThree, implementationHashThree, "ImplementationV3")
	reorgGUID, err := submit(implementationThree)
	if err != nil {
		t.Fatal(err)
	}
	lease, found, err := repository.Claim(ctx, "proxy-verification-reorg", time.Minute)
	if err != nil || !found || lease.Job.ID != reorgGUID {
		t.Fatalf("claim reorg proxy job: lease=%+v found=%t error=%v", lease, found, err)
	}
	execFixture(t, ctx, db, `
		UPDATE proxy_observations SET canonical = FALSE
		WHERE chain_id = 1 AND proxy_address = $1 AND block_hash = $2`,
		proxy.Bytes(), blockThreeRef.Hash.Bytes())
	if err := repository.CompleteProxyV2(ctx, lease); !errors.Is(err, verify.ErrTargetNotCanonical) {
		t.Fatalf("completion after reorg error = %v", err)
	}
	if err := repository.Fail(ctx, lease, verify.ErrorTargetNotCanonical); err != nil {
		t.Fatalf("fail stale proxy job: %v", err)
	}
	if _, err := backend.Execute(ctx, etherscan.Request{
		Module: "contract", Action: "checkproxyverification",
		Values: url.Values{"guid": {reorgGUID}},
	}); !errors.Is(err, etherscan.ErrProxyVerificationFailed) {
		t.Fatalf("reorged proxy status error = %v", err)
	}
	assertProxyVerificationSource(t, ctx, backend, proxy, implementationTwo, true)

	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM verification_results
		WHERE outcome_kind = 'proxy_verification_success'`, 2)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM verified_proxy_contracts`, 2)
}

func insertProxyVerificationCode(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	address common.Address,
	codeHash common.Hash,
	code []byte,
) {
	t.Helper()
	execFixture(t, ctx, db, `
		INSERT INTO contract_code_observations (
			chain_id, address, block_number, block_hash, code_hash, code, canonical
		) VALUES (1, $1, $2::numeric, $3, $4, $5, TRUE)`,
		address.Bytes(), block.Number, block.Hash.Bytes(), codeHash.Bytes(), code)
}

func insertProxyVerificationObservation(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block store.BlockRef,
	proxy common.Address,
	proxyHash common.Hash,
	implementation common.Address,
	implementationHash common.Hash,
) {
	t.Helper()
	execFixture(t, ctx, db, `
		INSERT INTO proxy_observations (
			chain_id, proxy_address, block_number, block_hash, proxy_code_hash,
			proxy_kind, implementation_address, implementation_code_hash,
			confidence, canonical
		) VALUES (1, $1, $2::numeric, $3, $4, 'eip1967', $5, $6, 'high', TRUE)`,
		proxy.Bytes(), block.Number, block.Hash.Bytes(), proxyHash.Bytes(),
		implementation.Bytes(), implementationHash.Bytes())
}

func insertProxyVerificationSource(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	address common.Address,
	codeHash common.Hash,
	name string,
) {
	t.Helper()
	insertVerifiedContractFixture(
		t, ctx, db, address.Bytes(), codeHash.Bytes(), 0, nil,
		"0.8.30+commit.73712a01", name, `[]`,
		`{"Fixture.sol":{"content":"contract Fixture {}"}}`,
		`{"optimizer":{"enabled":false,"runs":200}}`,
	)
}

func completeProxyVerification(
	t *testing.T,
	ctx context.Context,
	repository *verify.PostgresRepository,
	wantGUID string,
) {
	t.Helper()
	lease, found, err := repository.Claim(ctx, "proxy-verification-test", time.Minute)
	if err != nil || !found || lease.Job.ID != wantGUID {
		t.Fatalf("claim proxy verification: lease=%+v found=%t error=%v", lease, found, err)
	}
	if err := repository.CompleteProxyV2(ctx, lease); err != nil {
		t.Fatalf("complete proxy verification: %v", err)
	}
}

func proxyVerificationJobCount(t *testing.T, ctx context.Context, db *sql.DB) int64 {
	t.Helper()
	var count int64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM verification_jobs WHERE kind = 'proxy'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertProxyVerificationSource(
	t *testing.T,
	ctx context.Context,
	backend etherscan.Backend,
	proxy, implementation common.Address,
	wantBound bool,
) {
	t.Helper()
	result, err := backend.Execute(ctx, etherscan.Request{
		Module: "contract", Action: "getsourcecode",
		Values: url.Values{"address": {proxy.Hex()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Proxy          string `json:"Proxy"`
		Implementation string `json:"Implementation"`
	}
	if err := json.Unmarshal(encoded, &rows); err != nil || len(rows) != 1 {
		t.Fatalf("decode proxy source: rows=%#v error=%v", rows, err)
	}
	if wantBound {
		if rows[0].Proxy != "1" || !strings.EqualFold(rows[0].Implementation, implementation.Hex()) {
			t.Fatalf("proxy source = %#v, want %s", rows[0], implementation.Hex())
		}
	} else if rows[0].Proxy != "0" || rows[0].Implementation != "" {
		t.Fatalf("stale proxy source remained public: %#v", rows[0])
	}
}
