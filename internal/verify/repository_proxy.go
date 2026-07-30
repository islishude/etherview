package verify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
)

// CompleteProxyV2 publishes a proxy binding only while the exact submitted
// observation remains the current canonical mapping and both code identities
// retain verified source publications.
func (repository *PostgresRepository) CompleteProxyV2(
	ctx context.Context,
	lease VerificationLease,
) error {
	if err := validateVerificationLease(lease); err != nil {
		return err
	}
	if lease.Job.RequestV2 == nil || lease.Job.RequestV2.Kind != JobProxy ||
		lease.Job.RequestV2.Target == nil || lease.Job.RequestV2.ProxyTarget == nil {
		return errors.New("proxy verification lease is invalid")
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	job, err := repository.scanV2Job(tx.QueryRowContext(ctx, `
		SELECT `+v2VerificationJobColumns+` FROM verification_jobs
		WHERE id = $1::uuid AND status = 'running' AND lease_token = $2
		  AND lease_expires_at > clock_timestamp() FOR UPDATE
	`, lease.Job.ID, lease.Token))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	request := job.RequestV2
	if request == nil || request.Kind != JobProxy || request.Target == nil ||
		request.ProxyTarget == nil {
		return errors.New("stored proxy verification request is invalid")
	}
	proxyAddress, _ := decodeFixedHex(request.Target.Address, 20)
	proxyCodeHash, _ := decodeFixedHex(request.Target.CodeHash, 32)
	blockHash, _ := decodeFixedHex(request.Target.AtBlockHash, 32)
	implementationAddress, _ := decodeFixedHex(request.ProxyTarget.ImplementationAddress, 20)
	implementationCodeHash, _ := decodeFixedHex(request.ProxyTarget.ImplementationCodeHash, 32)

	var observationBlock string
	err = tx.QueryRowContext(ctx, proxyVerificationCurrentTargetSQL,
		strconv.FormatUint(request.Target.ChainID, 10),
		proxyAddress,
		proxyCodeHash,
		blockHash,
		request.ProxyTarget.Kind,
		implementationAddress,
		implementationCodeHash,
	).Scan(&observationBlock)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrTargetNotCanonical
	}
	if err != nil {
		return err
	}
	outcome, err := json.Marshal(map[string]string{
		"kind":                     "proxy_verification_success",
		"proxy_address":            request.Target.Address,
		"proxy_code_hash":          request.Target.CodeHash,
		"observation_block_hash":   request.Target.AtBlockHash,
		"proxy_kind":               request.ProxyTarget.Kind,
		"implementation_address":   request.ProxyTarget.ImplementationAddress,
		"implementation_code_hash": request.ProxyTarget.ImplementationCodeHash,
	})
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE verification_jobs
		SET status = 'succeeded', outcome_kind = 'proxy_verification_success',
		    outcome = $3::jsonb, error_code = NULL, leased_by = NULL,
		    lease_token = NULL, lease_expires_at = NULL,
		    updated_at = clock_timestamp()
		WHERE id = $1::uuid AND lease_token = $2
	`, job.ID, lease.Token, string(outcome)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verification_results (
			job_id, request_digest, outcome_kind, outcome
		) VALUES ($1::uuid, $2, 'proxy_verification_success', $3::jsonb)
	`, job.ID, job.RequestDigest[:], string(outcome)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verified_proxy_contracts (
			chain_id, proxy_address, proxy_code_hash, observation_block_number,
			observation_block_hash, proxy_kind, implementation_address,
			implementation_code_hash, verification_job_id, request_digest
		) VALUES (
			$1::numeric, $2, $3, $4::numeric, $5, $6, $7, $8, $9::uuid, $10
		)
	`, strconv.FormatUint(request.Target.ChainID, 10), proxyAddress, proxyCodeHash,
		observationBlock, blockHash, request.ProxyTarget.Kind, implementationAddress,
		implementationCodeHash, job.ID, job.RequestDigest[:],
	); err != nil {
		return err
	}
	return tx.Commit()
}

const proxyVerificationCurrentTargetSQL = `
WITH canonical_tip AS (
    SELECT number
    FROM canonical_blocks
    WHERE chain_id = $1::numeric
    ORDER BY number DESC
    LIMIT 1
), current_proxy AS (
    SELECT observation.block_number, observation.block_hash,
           observation.proxy_code_hash, observation.proxy_kind,
           observation.implementation_address,
           observation.implementation_code_hash,
           tip.number AS context_number
    FROM canonical_tip AS tip
    JOIN LATERAL (
        SELECT observation.*
        FROM proxy_observations AS observation
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        WHERE observation.chain_id = $1::numeric
          AND observation.proxy_address = $2
          AND observation.canonical = TRUE
          AND observation.confidence IN ('verified', 'high')
          AND observation.block_number <= tip.number
        ORDER BY observation.block_number DESC, observation.block_hash DESC
        LIMIT 1
    ) AS observation ON TRUE
)
SELECT current_proxy.block_number::text
FROM current_proxy
WHERE current_proxy.proxy_code_hash = $3
  AND current_proxy.block_hash = $4
  AND current_proxy.proxy_kind = $5
  AND current_proxy.implementation_address = $6
  AND current_proxy.implementation_code_hash = $7
  AND EXISTS (
      SELECT 1 FROM verified_contracts AS verified
      WHERE verified.chain_id = $1::numeric
        AND verified.address = $2
        AND verified.code_hash = $3
        AND verified.valid_from_block <= current_proxy.context_number
        AND (verified.valid_to_block IS NULL
             OR verified.valid_to_block >= current_proxy.context_number)
  )
  AND EXISTS (
      SELECT 1 FROM verified_contracts AS verified
      WHERE verified.chain_id = $1::numeric
        AND verified.address = $6
        AND verified.code_hash = $7
        AND verified.valid_from_block <= current_proxy.context_number
        AND (verified.valid_to_block IS NULL
             OR verified.valid_to_block >= current_proxy.context_number)
  )`
