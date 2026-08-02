package enrich

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/google/uuid"
)

type uupsProbeState string

type uupsProbeRejection string

const (
	uupsProbeCompatible uupsProbeState = "compatible"
	uupsProbeRejected   uupsProbeState = "rejected"

	uupsRejectUUIDUnavailable    uupsProbeRejection = "proxiable_uuid_unavailable"
	uupsRejectUUIDInvalid        uupsProbeRejection = "proxiable_uuid_invalid"
	uupsRejectVersionUnavailable uupsProbeRejection = "upgrade_interface_version_unavailable"
	uupsRejectVersionInvalid     uupsProbeRejection = "upgrade_interface_version_invalid"
)

type uupsImplementationProbeTarget struct {
	address           common.Address
	codeHash          common.Hash
	verificationJobID string
}

func (target uupsImplementationProbeTarget) validate() error {
	if target.address == (common.Address{}) {
		return errors.New("UUPS probe implementation address is zero")
	}
	if target.codeHash == (common.Hash{}) {
		return errors.New("UUPS probe implementation code hash is zero")
	}
	identifier, err := uuid.Parse(target.verificationJobID)
	if err != nil || identifier.String() != target.verificationJobID {
		return errors.New("UUPS probe verification job ID is not a canonical UUID")
	}
	return nil
}

type uupsImplementationProbeResult struct {
	chainID          string
	blockNumber      uint64
	blockHash        common.Hash
	target           uupsImplementationProbeTarget
	code             []byte
	state            uupsProbeState
	rejection        uupsProbeRejection
	proxiableUUID    common.Hash
	upgradeInterface string
}

func (result uupsImplementationProbeResult) compatible() bool {
	return result.state == uupsProbeCompatible
}

func (result uupsImplementationProbeResult) validate() error {
	if result.chainID == "" || result.blockHash == (common.Hash{}) {
		return errors.New("UUPS probe result block identity is invalid")
	}
	if err := result.target.validate(); err != nil {
		return err
	}
	if len(result.code) == 0 || codeHash(result.code) != result.target.codeHash {
		return errors.New("UUPS probe result code identity is invalid")
	}
	switch result.state {
	case uupsProbeCompatible:
		if result.rejection != "" || result.proxiableUUID != EIP1967ImplementationSlot ||
			result.upgradeInterface != "5.0.0" {
			return errors.New("compatible UUPS probe result lacks exact 5.0.0 evidence")
		}
	case uupsProbeRejected:
		switch result.rejection {
		case uupsRejectUUIDUnavailable, uupsRejectUUIDInvalid,
			uupsRejectVersionUnavailable, uupsRejectVersionInvalid:
		default:
			return errors.New("rejected UUPS probe result has an invalid reason")
		}
		if result.proxiableUUID != (common.Hash{}) || result.upgradeInterface != "" {
			return errors.New("rejected UUPS probe result contains promotable evidence")
		}
	default:
		return errors.New("UUPS probe result state is invalid")
	}
	return nil
}

// probeUUPSImplementationAtBlock calls the verified implementation directly.
// Every request is pinned to the job's canonical block hash; it never probes
// through a proxy, where notDelegated would deliberately reject proxiableUUID.
func probeUUPSImplementationAtBlock(
	ctx context.Context,
	caller rpcCaller,
	job Job,
	target uupsImplementationProbeTarget,
	maxCodeBytes int,
) (uupsImplementationProbeResult, error) {
	if caller == nil {
		return uupsImplementationProbeResult{}, errors.New("UUPS probe RPC caller is nil")
	}
	if err := job.Validate(); err != nil {
		return uupsImplementationProbeResult{}, Permanent(err)
	}
	if job.Stage != ProxyStage {
		return uupsImplementationProbeResult{}, Permanent(errors.New("UUPS probe requires proxy@2"))
	}
	if err := target.validate(); err != nil {
		return uupsImplementationProbeResult{}, Permanent(err)
	}
	if maxCodeBytes <= 0 || maxCodeBytes > 32<<20 {
		return uupsImplementationProbeResult{}, Permanent(errors.New("UUPS probe code limit is invalid"))
	}

	blockReference := rpc.BlockNumberOrHashWithHash(job.BlockHash, true)
	var encodedCode hexutil.Bytes
	if err := caller.CallContext(
		ctx, &encodedCode, "eth_getCode", target.address, blockReference,
	); err != nil {
		return uupsImplementationProbeResult{}, exactStateRPCError(ctx, "eth_getCode", err)
	}
	code := common.CopyBytes(encodedCode)
	if len(code) == 0 {
		return uupsImplementationProbeResult{}, Permanent(errors.New("verified UUPS implementation has no code"))
	}
	if len(code) > maxCodeBytes {
		return uupsImplementationProbeResult{}, Permanent(errors.New("UUPS implementation bytecode exceeds proxy detection limit"))
	}
	if codeHash(code) != target.codeHash {
		return uupsImplementationProbeResult{}, Permanent(errors.New("UUPS implementation code disagrees with verified identity"))
	}

	result := uupsImplementationProbeResult{
		chainID: job.ChainID, blockNumber: job.BlockNumber, blockHash: job.BlockHash,
		target: target, code: code, state: uupsProbeRejected,
	}
	uuidOutput, available, err := callDirectUUPSProbe(
		ctx, caller, target.address, "proxiableUUID", blockReference,
	)
	if err != nil {
		return uupsImplementationProbeResult{}, err
	}
	if !available {
		result.rejection = uupsRejectUUIDUnavailable
		return result, nil
	}
	if ParseUUPSProxiableUUID(uuidOutput) != nil {
		result.rejection = uupsRejectUUIDInvalid
		return result, nil
	}

	versionOutput, available, err := callDirectUUPSProbe(
		ctx, caller, target.address, "UPGRADE_INTERFACE_VERSION", blockReference,
	)
	if err != nil {
		return uupsImplementationProbeResult{}, err
	}
	if !available {
		result.rejection = uupsRejectVersionUnavailable
		return result, nil
	}
	if ParseUUPSInterfaceVersion(versionOutput) != nil {
		result.rejection = uupsRejectVersionInvalid
		return result, nil
	}

	result.state = uupsProbeCompatible
	result.rejection = ""
	result.proxiableUUID = EIP1967ImplementationSlot
	result.upgradeInterface = "5.0.0"
	return result, nil
}

func callDirectUUPSProbe(
	ctx context.Context,
	caller rpcCaller,
	address common.Address,
	method string,
	blockReference rpc.BlockNumberOrHash,
) ([]byte, bool, error) {
	input, err := packStateProbe(method)
	if err != nil {
		return nil, false, Permanent(err)
	}
	request := map[string]any{"to": address, "data": hexutil.Bytes(input)}
	var encoded hexutil.Bytes
	if err := caller.CallContext(ctx, &encoded, "eth_call", request, blockReference); err != nil {
		if executionReverted(err) {
			return nil, false, nil
		}
		return nil, false, exactStateRPCError(ctx, "eth_call", err)
	}
	return common.CopyBytes(encoded), true, nil
}

// persistUUPSImplementationProbe writes one immutable probe fact and its
// generation witness in the caller's proxy@2 publication transaction. The
// migration guard independently checks the verified artifact, exact code fact,
// canonical block, and active lease before either row can become consumable.
func persistUUPSImplementationProbe(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	result uupsImplementationProbeResult,
) error {
	if tx == nil {
		return errors.New("persist UUPS implementation probe using nil transaction")
	}
	if err := job.Validate(); err != nil {
		return Permanent(err)
	}
	if job.Stage != ProxyStage || result.chainID != job.ChainID ||
		result.blockNumber != job.BlockNumber || result.blockHash != job.BlockHash {
		return Permanent(errors.New("UUPS probe result disagrees with proxy@2 job identity"))
	}
	if err := result.validate(); err != nil {
		return Permanent(err)
	}
	jobID, generation, err := durableJobGeneration(job)
	if err != nil {
		return Permanent(err)
	}

	var rejection, proxiableUUID, interfaceVersion any
	if result.compatible() {
		proxiableUUID = result.proxiableUUID[:]
		interfaceVersion = result.upgradeInterface
	} else {
		rejection = string(result.rejection)
	}
	observation, err := tx.ExecContext(ctx, upsertUUPSImplementationObservationSQL,
		job.ChainID, result.target.address[:], strconv.FormatUint(job.BlockNumber, 10),
		job.BlockHash[:], result.target.codeHash[:], result.target.verificationJobID,
		job.Stage.Version, OpenZeppelin561Standard, result.state, rejection,
		proxiableUUID, interfaceVersion,
	)
	if err != nil {
		return fmt.Errorf("persist direct UUPS implementation observation: %w", err)
	}
	affected, err := observation.RowsAffected()
	if err != nil {
		return fmt.Errorf("read UUPS implementation observation result: %w", err)
	}
	if affected != 1 {
		return Permanent(errors.New("existing UUPS implementation observation conflicts with exact probe"))
	}

	witness, err := tx.ExecContext(ctx, insertUUPSImplementationObservationGenerationSQL,
		job.ChainID, result.target.address[:], job.BlockHash[:], job.Stage.Version,
		result.target.verificationJobID, jobID, generation,
	)
	if err != nil {
		return fmt.Errorf("persist UUPS implementation observation generation: %w", err)
	}
	affected, err = witness.RowsAffected()
	if err != nil {
		return fmt.Errorf("read UUPS implementation observation generation result: %w", err)
	}
	if affected > 1 {
		return Permanent(errors.New("UUPS implementation observation generation affected multiple rows"))
	}
	return nil
}

const upsertUUPSImplementationObservationSQL = `
INSERT INTO uups_implementation_observations AS current (
    chain_id, implementation_address, block_number, block_hash,
    implementation_code_hash, verification_job_id, stage_version,
    standard_version, probe_state, rejection_reason, proxiable_uuid,
    upgrade_interface_version, canonical
) VALUES (
    $1::numeric, $2, $3::numeric, $4,
    $5, $6::uuid, $7, $8, $9, $10, $11, $12, TRUE
)
ON CONFLICT (
    chain_id, implementation_address, block_hash,
    stage_version, verification_job_id
) DO UPDATE SET canonical = EXCLUDED.canonical
WHERE current.block_number = EXCLUDED.block_number
  AND current.implementation_code_hash = EXCLUDED.implementation_code_hash
  AND current.standard_version = EXCLUDED.standard_version
  AND current.probe_state = EXCLUDED.probe_state
  AND current.rejection_reason IS NOT DISTINCT FROM EXCLUDED.rejection_reason
  AND current.proxiable_uuid IS NOT DISTINCT FROM EXCLUDED.proxiable_uuid
  AND current.upgrade_interface_version IS NOT DISTINCT FROM EXCLUDED.upgrade_interface_version`

const insertUUPSImplementationObservationGenerationSQL = `
INSERT INTO uups_implementation_observation_generations (
    chain_id, implementation_address, observation_block_hash,
    observation_stage_version, verification_job_id,
    durable_job_id, job_generation
) VALUES (
    $1::numeric, $2, $3, $4, $5::uuid, $6::bigint, $7::bigint
)
ON CONFLICT DO NOTHING`
