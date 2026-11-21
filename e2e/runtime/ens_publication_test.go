//go:build runtimee2e

package runtimee2e

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/jackc/pgx/v5"
)

func (h *harness) assertENS(ctx context.Context) {
	waitFor(h.t, ctx, "autonomous real v2 publication", func() (bool, string, error) {
		var count int
		err := h.db.QueryRow(ctx, `SELECT count(DISTINCT direction) FROM ens_effective_publications WHERE target_chain_id=1 AND name=$1 AND ens_effective_publication_current(id)`, runtimeENSName).Scan(&count)
		return count == 3, fmt.Sprint(count), err
	})
	var config gen.PublicConfigResponse
	h.mustGetJSON(ctx, "/api/v1/config", &config)
	if !config.Data.Features["ens"] {
		h.t.Fatal("runtime public config did not enable ENS")
	}
	var names gen.AddressNamePageResponse
	h.mustGetJSON(ctx, "/api/v1/address-names?addresses="+url.QueryEscape(h.fixture.authority), &names)
	if len(names.Data.Items) != 1 || names.Data.Items[0].State != gen.AddressNameLookupStateResolved ||
		names.Data.Items[0].PrimaryName == nil || names.Data.Items[0].PrimaryName.Name != runtimeENSName ||
		names.Data.Snapshot == "" {
		h.t.Fatalf("runtime ENS address names = %#v", names.Data)
	}
	var search gen.SearchResponse
	h.mustGetJSON(ctx, "/api/v1/search?q="+url.QueryEscape(runtimeENSName), &search)
	if len(search.Data) != 1 || search.Data[0].Key != runtimeENSName || search.Data[0].Kind != gen.SearchResultKindName || search.Data[0].NameDetails == nil || search.Data[0].NameDetails.ResolvedAddress.Value == nil || !strings.EqualFold(*search.Data[0].NameDetails.ResolvedAddress.Value, h.fixture.authority) {
		h.t.Fatalf("runtime ENS search = %#v", search.Data)
	}

	var detail gen.NameDetailResponse
	h.mustGetJSON(ctx, "/api/v1/names/"+runtimeENSName, &detail)
	if detail.Data.ProfileObservation == nil || detail.Data.Registrant.Value == nil || !strings.EqualFold(*detail.Data.Registrant.Value, h.ensOwner) {
		h.t.Fatalf("runtime registration profile: %+v", detail.Data)
	}

	var history gen.NameHistoryResponse
	h.mustGetJSON(ctx, "/api/v1/names/"+runtimeENSName+"/history", &history)
	if len(history.Data.Items) == 0 || history.Data.Snapshot == "" {
		h.t.Fatalf("runtime history: %+v", history.Data)
	}
	first := history.Data.Items[0]
	var events gen.NameHistoryEventsResponse
	h.mustGetJSON(ctx, "/api/v1/names/"+runtimeENSName+"/history/"+first.TransactionHash+"/events?block_hash="+first.BlockHash+"&snapshot="+url.QueryEscape(history.Data.Snapshot), &events)
	if len(events.Data.Items) == 0 {
		h.t.Fatal("runtime history lost transaction events")
	}

	for _, name := range []string{"plain.eth", "wrapped.eth", "locked.eth"} {
		waitFor(h.t, ctx, "migrated forward "+name, func() (bool, string, error) {
			var count int
			err := h.db.QueryRow(ctx, `SELECT count(*) FROM ens_effective_publications WHERE target_chain_id=1 AND direction='forward' AND state='resolved' AND name=$1 AND ens_effective_publication_current(id)`, name).Scan(&count)
			return count > 0, fmt.Sprint(count), err
		})
		h.mustGetJSON(ctx, "/api/v1/search?q="+name, &search)
		if len(search.Data) != 1 || search.Data[0].Key != name || search.Data[0].NameDetails == nil || search.Data[0].NameDetails.ResolutionObservation == nil {
			h.t.Fatalf("migrated name %s: %#v", name, search.Data)
		}
	}
	waitFor(h.t, ctx, "CCIP failure isolated from published names", func() (bool, string, error) {
		var failed bool
		err := h.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ens_sync_jobs j JOIN ens_sync_candidates c ON c.id=j.candidate_id WHERE c.lookup_key='ccip.eth' AND j.failure_code='ccip_unavailable')`).Scan(&failed)
		return failed, fmt.Sprint(failed), err
	})
	var candidateID, observationID int64
	var validUntil time.Time
	waitFor(h.t, ctx, "definitive negative for refresh", func() (bool, string, error) {
		err := h.db.QueryRow(ctx, `SELECT c.id,o.id,o.valid_until FROM ens_sync_candidates c JOIN ens_sync_observations o ON o.candidate_id=c.id WHERE c.direction='primary' AND c.lookup_key=$1 AND o.outcome='not_found' ORDER BY o.id DESC LIMIT 1`, strings.ToLower(h.fixture.accounts[0])).Scan(&candidateID, &observationID, &validUntil)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "pending", nil
		}
		return err == nil, fmt.Sprint(candidateID), err
	})

	// ENS eth_call failures share the current-chain pool but do not expire an
	// already verified observation early or create request-triggered jobs.
	h.rpcProxy.ensOffline.Store(true)
	defer h.rpcProxy.ensOffline.Store(false)
	// Evict the disposable fixture's resolver calls to exercise a cache miss at
	// the same block. Previously collected inputs otherwise satisfy the refresh.
	if _, err := h.db.Exec(ctx, `DELETE FROM stage_state_inputs s USING state_input_groups g
		WHERE s.group_id=g.id AND g.consumer LIKE 'ens-resolution-%'
		AND convert_from(s.request,'UTF8')::jsonb->>'Kind'='call'`); err != nil {
		h.t.Fatal(err)
	}
	if _, e := h.db.Exec(ctx, `UPDATE ens_sync_candidates SET refresh_at=clock_timestamp() WHERE id=$1`, candidateID); e != nil {
		h.t.Fatal(e)
	}
	waitFor(h.t, ctx, "background resolver failure", func() (bool, string, error) {
		var failed bool
		err := h.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ens_sync_jobs WHERE candidate_id=$1 AND failure_code IS NOT NULL AND attempts>0)`, candidateID).Scan(&failed)
		return failed, fmt.Sprint(failed), err
	})
	var latestID int64
	var latestExpiry time.Time
	if e := h.db.QueryRow(ctx, `SELECT id,valid_until FROM ens_sync_observations WHERE candidate_id=$1 ORDER BY id DESC LIMIT 1`, candidateID).Scan(&latestID, &latestExpiry); e != nil || latestID != observationID || !latestExpiry.Equal(validUntil) {
		h.t.Fatalf("failed refresh renewed observation: %d %v %v", latestID, latestExpiry, e)
	}

	response := h.requireHTTPStatus(ctx, "/health/ready", http.StatusOK)
	_ = response.Body.Close()
	response = h.requireHTTPStatus(ctx, "/api/v1/search?q=never-imported.eth", http.StatusOK)
	_ = response.Body.Close()
	invalidName := h.requireHTTPStatus(ctx, "/api/v1/names/%20", http.StatusBadRequest)
	_ = invalidName.Body.Close()
	var candidates int
	if e := h.db.QueryRow(ctx, `SELECT count(*) FROM ens_sync_candidates WHERE lookup_key='never-imported.eth'`).Scan(&candidates); e != nil || candidates != 0 {
		h.t.Fatalf("HTTP enqueued missing name: %d %v", candidates, e)
	}
	h.rpcProxy.ensOffline.Store(false)

}
