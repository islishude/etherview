//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/verify"
)

func TestVerifiedArtifactResolvesSameCodeWithoutChangingSourceIdentity(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(15_000), testHash(0), testHash(16_000), "artifact-genesis")
	blockOne := testBundle(1, testHash(15_001), testHash(15_000), testHash(16_001), "artifact-one")
	blockTwo := testBundle(2, testHash(15_002), testHash(15_001), testHash(16_002), "artifact-two")
	commitCanonical(t, ctx, core, genesis)
	commitCanonical(t, ctx, core, blockOne)
	commitCanonical(t, ctx, core, blockTwo)

	runtime := []byte{0x60, 0x2a, 0x60, 0x00, 0x52}
	codeHash := keccak256(runtime)
	source := mustBytes(t, testAddress(1_500))
	target := mustBytes(t, testAddress(1_501))
	for _, fixture := range []struct {
		address   []byte
		number    int
		blockHash []byte
	}{
		{source, 1, mustBytes(t, testHash(15_001))},
		{target, 2, mustBytes(t, testHash(15_002))},
	} {
		execFixture(t, ctx, db, `
			INSERT INTO contract_code_observations (
				chain_id, address, block_number, block_hash, code_hash, code, canonical
			) VALUES (1, $1, $2, $3, $4, $5, TRUE)`,
			fixture.address, fixture.number, fixture.blockHash, codeHash, runtime,
		)
	}
	closedAt := uint64(1)
	insertVerifiedContractFixture(
		t, ctx, db, source, codeHash, 1, &closedAt,
		"0.8.30+commit.73712a01", "SharedRuntime",
		`[{"type":"event","name":"Changed","inputs":[]}]`,
		`{"Shared.sol":{"content":"contract SharedRuntime {}"}}`, `{}`,
	)

	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	resolved, found, err := repository.VerifiedContract(ctx, 1, "0x"+hexString(target))
	if err != nil || !found {
		t.Fatalf("resolve same-code artifact: found=%t error=%v", found, err)
	}
	if resolved.Resolution != "code_hash" ||
		resolved.Target.Address != "0x"+hexString(target) ||
		resolved.Source.Address != "0x"+hexString(source) ||
		resolved.Target.CodeHash != resolved.Source.CodeHash ||
		resolved.Source.ValidToBlock == nil || *resolved.Source.ValidToBlock != 1 {
		t.Fatalf("same-code artifact=%+v", resolved)
	}

	insertVerifiedContractFixture(
		t, ctx, db, target, codeHash, 2, nil,
		"0.8.30+commit.73712a01", "ExactRuntime",
		`[{"type":"event","name":"ExactChanged","inputs":[]}]`,
		`{"Exact.sol":{"content":"contract ExactRuntime {}"}}`, `{}`,
	)
	resolved, found, err = repository.VerifiedContract(ctx, 1, "0x"+hexString(target))
	if err != nil || !found || resolved.Resolution != "exact_address" ||
		resolved.Source.Address != resolved.Target.Address || resolved.ContractName != "ExactRuntime" {
		t.Fatalf("resolve exact artifact: found=%t artifact=%+v error=%v", found, resolved, err)
	}
}

func hexString(value []byte) string {
	const digits = "0123456789abcdef"
	encoded := make([]byte, len(value)*2)
	for index, item := range value {
		encoded[index*2] = digits[item>>4]
		encoded[index*2+1] = digits[item&0x0f]
	}
	return string(encoded)
}
