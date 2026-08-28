//go:build runtimee2e && hardhat3e2e

package runtimee2e

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
)

func waitHardhatSafe(
	t *testing.T,
	ctx context.Context,
	h *harness,
	deployment hardhatDeployment,
) {
	t.Helper()
	proxy := common.HexToAddress(deployment.Safe.Proxy)
	singleton := common.HexToAddress(deployment.Safe.Singleton)
	creation := deployment.Transactions["safeCreate"]
	if creation.BlockNumber == "" || len(creation.BlockHash) != 66 || len(creation.Hash) != 66 {
		t.Fatalf("Safe creation transaction = %#v", creation)
	}
	waitFor(t, ctx, "published SafeProxy "+deployment.Safe.Proxy, func() (bool, string, error) {
		var state, detector, family, variant, confidence, role, implementation string
		var blockNumber, blockHash string
		var canonicalShell, official bool
		var pathLength, legacyRows, bindings, traceCreates, jobID, generation int64
		err := h.db.QueryRow(ctx, `
			WITH current AS (
			    SELECT evidence.detection_state, evidence.details,
			           evidence.block_number, evidence.block_hash,
			           evidence.durable_job_id, evidence.job_generation
			    FROM proxy_detection_evidence AS evidence
			    JOIN canonical_blocks AS canonical
			      ON canonical.chain_id = evidence.chain_id
			     AND canonical.number = evidence.block_number
			     AND canonical.block_hash = evidence.block_hash
			    JOIN published_block_stage_results AS published
			      ON published.chain_id = evidence.chain_id
			     AND published.block_hash = evidence.block_hash
			     AND published.stage = 'proxy'
			     AND published.stage_version = evidence.stage_version
			     AND published.durable_job_id = evidence.durable_job_id
			     AND published.job_generation = evidence.job_generation
			     AND published.state = 'complete'
			    WHERE evidence.chain_id = 1
			      AND evidence.address = $1
			      AND evidence.candidate_kind = 'proxy_v2'
			      AND evidence.detection_state = 'confirmed'
			      AND evidence.reason = 'resolver'
			      AND evidence.canonical
			    ORDER BY evidence.block_number DESC, evidence.job_generation DESC
			    LIMIT 1
			)
			SELECT
			  COALESCE((SELECT detection_state FROM current), ''),
			  COALESCE((SELECT details #>> '{primary,detector}' FROM current), ''),
			  COALESCE((SELECT details #>> '{primary,family}' FROM current), ''),
			  COALESCE((SELECT details #>> '{primary,variant}' FROM current), ''),
			  COALESCE((SELECT details #>> '{primary,confidence}' FROM current), ''),
			  COALESCE((SELECT details #>> '{primary,implementation_role}' FROM current), ''),
			  COALESCE((SELECT details #>> '{primary,implementation}' FROM current), ''),
			  COALESCE((SELECT (details #>> '{primary,canonical_proxy_shell}')::boolean FROM current), false),
			  COALESCE((SELECT (details #>> '{primary,official_singleton}')::boolean FROM current), false),
			  COALESCE((SELECT jsonb_array_length(details #> '{primary,implementation_path}') FROM current), 0),
			  COALESCE((SELECT block_number::text FROM current), ''),
			  COALESCE((SELECT '0x' || encode(block_hash, 'hex') FROM current), ''),
			  COALESCE((SELECT durable_job_id FROM current), 0),
			  COALESCE((SELECT job_generation FROM current), 0),
			  (SELECT count(*) FROM proxy_observations
			   WHERE chain_id = 1 AND proxy_address = $1),
			  (SELECT count(*) FROM verified_proxy_bindings
			   WHERE chain_id = 1 AND proxy_address = $1),
			  (SELECT count(*) FROM normalized_traces AS trace
			   JOIN canonical_blocks AS canonical
			     ON canonical.chain_id = trace.chain_id
			    AND canonical.number = trace.block_number
			    AND canonical.block_hash = trace.block_hash
			   WHERE trace.chain_id = 1
			     AND trace.transaction_hash = $2
			     AND trace.created_address = $1
			     AND trace.call_type = 'CREATE2'
			     AND NOT trace.reverted
			     AND trace.canonical
			     AND EXISTS (
			         SELECT 1 FROM published_block_stage_results AS published
			         WHERE published.chain_id = trace.chain_id
			           AND published.block_hash = trace.block_hash
			           AND published.stage = 'trace'
			           AND published.state = 'complete'
			     ))`, proxy.Bytes(), common.HexToHash(creation.Hash).Bytes()).Scan(
			&state, &detector, &family, &variant, &confidence, &role, &implementation,
			&canonicalShell, &official, &pathLength, &blockNumber, &blockHash,
			&jobID, &generation, &legacyRows, &bindings, &traceCreates,
		)
		diagnostic := fmt.Sprintf(
			"%s/%s/%s/%s/%s/%s implementation=%s shell=%t official=%t path=%d block=%s/%s job=%d/%d legacy=%d bindings=%d create2=%d",
			state, detector, family, variant, confidence, role, implementation,
			canonicalShell, official, pathLength, blockNumber, blockHash,
			jobID, generation, legacyRows, bindings, traceCreates,
		)
		return err == nil && state == "confirmed" && detector == "safe" && family == "safe" &&
			variant == "safe-proxy" && confidence == "high" && role == "singleton" &&
			strings.EqualFold(implementation, singleton.Hex()) && canonicalShell && !official &&
			pathLength == 2 && blockNumber == strconv.FormatUint(
			mustDecodeUint64(t, creation.BlockNumber), 10,
		) && strings.EqualFold(blockHash, creation.BlockHash) && jobID > 0 && generation > 0 &&
			legacyRows == 0 && bindings == 0 && traceCreates == 1, diagnostic, err
	})

	var detail gen.ProxyDetailsResponse
	path := hardhatContractAPIPath(deployment.Safe.Proxy, "/proxy", nil)
	if err := h.getJSON(ctx, path, &detail); err != nil {
		t.Fatalf("read Safe proxy detail: %v", err)
	}
	detection := detail.Data.ProxyDetectionV2
	if detail.Data.Status != gen.ProxyDetailStatusDetectedUnverified || detail.Data.Proxy != nil ||
		detail.Data.Implementation != nil || detail.Data.ImplementationInteraction != nil ||
		detail.Data.Management != nil || detail.Data.BindingId != nil || detection == nil ||
		detection.Status != gen.ProxyDetectionV2StatusConfirmed || detection.Primary == nil ||
		len(detection.Conflicts) != 0 || !detection.ShadowDiff.Different ||
		!slices.Equal(detection.ShadowDiff.Reasons, []string{"v2_positive_legacy_not_detected"}) {
		t.Fatalf("public Safe proxy detail = %#v", detail.Data)
	}
	primary := detection.Primary
	if primary.Detector != "safe" || primary.Family == nil ||
		*primary.Family != gen.ProxyDetectionV2FamilySafe || primary.Variant == nil ||
		*primary.Variant != "safe-proxy" || primary.Status != gen.ProxyDetectionV2StatusConfirmed ||
		primary.Confidence != gen.ProxyDetectionV2ConfidenceHigh ||
		primary.ImplementationRole == nil ||
		*primary.ImplementationRole != gen.ProxyDetectionV2ImplementationRoleSingleton ||
		primary.Implementation == nil ||
		common.HexToAddress(*primary.Implementation) != singleton ||
		common.HexToAddress(primary.Proxy) != proxy || !primary.CanonicalProxyShell ||
		!primary.ImplementationHasCode || primary.OfficialSingleton ||
		primary.SingletonVersion != nil || primary.InitialSingleton != nil || primary.SingletonChanged ||
		len(primary.ImplementationPath) != 2 ||
		common.HexToAddress(primary.ImplementationPath[0]) != proxy ||
		common.HexToAddress(primary.ImplementationPath[1]) != singleton ||
		primary.BlockNumber != strconv.FormatUint(mustDecodeUint64(t, creation.BlockNumber), 10) ||
		!strings.EqualFold(primary.BlockHash, creation.BlockHash) {
		t.Fatalf("public Safe V2 primary = %#v", primary)
	}
}
