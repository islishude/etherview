package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/islishude/etherview/internal/analytics"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/catalog"
)

func (h *Handler) nftBalances(w http.ResponseWriter, r *http.Request) {
	owner, ok := parseAddressPath(w, r)
	if !ok {
		return
	}
	limit, cursor, ok := parseCatalogPage(w, r)
	if !ok {
		return
	}
	page, err := h.catalog.NFTBalances(r.Context(), catalog.NFTBalanceRequest{
		ChainID: h.chainID(), Owner: owner, Cursor: cursor, Limit: limit,
	})
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.NFTBalance, len(page.Items))
	for index := range page.Items {
		items[index] = nftBalanceModel(page.Items[index])
	}
	meta := h.catalogPageMeta(r, page.NextCursor, page.Snapshot)
	writeJSON(w, http.StatusOK, gen.NFTBalanceListResponse{Data: items, Meta: meta})
}

func (h *Handler) erc20Balances(w http.ResponseWriter, r *http.Request) {
	owner, ok := parseAddressPath(w, r)
	if !ok {
		return
	}
	limit, cursor, ok := parseCatalogPage(w, r)
	if !ok {
		return
	}
	page, err := h.catalog.ERC20Balances(r.Context(), catalog.ERC20BalanceRequest{
		ChainID: h.chainID(), Owner: owner, Cursor: cursor, Limit: limit,
	})
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.ERC20Balance, len(page.Items))
	for index, item := range page.Items {
		var decimals *int
		if item.Decimals != nil {
			value := int(*item.Decimals)
			decimals = &value
		}
		items[index] = gen.ERC20Balance{
			ChainId: item.ChainID, Owner: item.Owner,
			TokenAddress: item.TokenAddress, Balance: item.Balance,
			Confidence: gen.StateConfidence(item.Confidence),
			Name:       item.Name, Symbol: item.Symbol, Decimals: decimals,
		}
	}
	writeJSON(w, http.StatusOK, gen.ERC20BalanceListResponse{
		Data: items, Meta: h.catalogPageMeta(r, page.NextCursor, page.Snapshot),
	})
}

func (h *Handler) blockStats(w http.ResponseWriter, r *http.Request) {
	from, to := r.URL.Query().Get("from_block"), r.URL.Query().Get("to_block")
	if !canonicalQuantity(from) || !canonicalQuantity(to) {
		writeError(w, r, http.StatusBadRequest, "invalid_block_range", "from_block and to_block must be canonical decimal uint256 values", nil)
		return
	}
	items, err := h.catalog.BlockStats(r.Context(), catalog.BlockStatsRequest{
		ChainID: h.chainID(), FromBlock: from, ToBlock: to,
	})
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	models := make([]gen.BlockStat, len(items))
	for index := range items {
		models[index] = blockStatModel(items[index])
	}
	meta := h.meta(r)
	meta.CoverageStart, meta.CoverageEnd = &from, &to
	writeJSON(w, http.StatusOK, gen.BlockStatListResponse{Data: models, Meta: meta})
}

func (h *Handler) aggregateStats(w http.ResponseWriter, r *http.Request) {
	from, to := r.URL.Query().Get("from_block"), r.URL.Query().Get("to_block")
	if !canonicalQuantity(from) || !canonicalQuantity(to) {
		writeError(w, r, http.StatusBadRequest, "invalid_block_range", "from_block and to_block must be canonical decimal uint256 values", nil)
		return
	}
	item, err := h.catalog.AggregateStats(r.Context(), catalog.AggregateStatsRequest{
		ChainID: h.chainID(), FromBlock: from, ToBlock: to,
	})
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	meta := h.catalogPageMeta(r, "", item.Snapshot)
	meta.CoverageStart, meta.CoverageEnd = &from, &to
	writeJSON(w, http.StatusOK, gen.AggregateStatsResponse{Data: aggregateStatsModel(item), Meta: meta})
}

func (h *Handler) chartOverview(w http.ResponseWriter, r *http.Request) {
	if h.analytics == nil {
		writeError(w, r, http.StatusServiceUnavailable, "analytics_pending", "historical analytics are still being rebuilt", nil)
		return
	}
	item, err := h.analytics.Overview(r.Context(), h.chainID(), h.now().UTC())
	if err != nil {
		h.handleAnalyticsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.ChartOverviewResponse{
		Data: chartOverviewModel(item),
		Meta: h.meta(r),
	})
}

func (h *Handler) chartMetric(w http.ResponseWriter, r *http.Request) {
	if h.analytics == nil {
		writeError(w, r, http.StatusServiceUnavailable, "analytics_pending", "historical analytics are still being rebuilt", nil)
		return
	}
	metric, ok := analytics.ParseMetric(r.PathValue("metric"))
	if !ok {
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_chart_metric", "metric is not supported", nil)
		return
	}
	from, fromErr := time.Parse(time.RFC3339, r.URL.Query().Get("from_time"))
	to, toErr := time.Parse(time.RFC3339, r.URL.Query().Get("to_time"))
	intervalText := r.URL.Query().Get("interval")
	if intervalText == "" {
		intervalText = string(analytics.IntervalAuto)
	}
	interval, intervalOK := analytics.ParseInterval(intervalText)
	if fromErr != nil || toErr != nil || !intervalOK {
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_chart_range", "chart times or interval are invalid", nil)
		return
	}
	item, err := h.analytics.Detail(r.Context(), analytics.DetailRequest{
		ChainID: h.chainID(), Metric: metric, From: from, To: to,
		Interval: interval, Now: h.now().UTC(),
	})
	if err != nil {
		h.handleAnalyticsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.ChartMetricResponse{
		Data: chartSeriesModel(item),
		Meta: h.meta(r),
	})
}

func (h *Handler) handleAnalyticsError(w http.ResponseWriter, r *http.Request, err error) {
	var pending analytics.PendingError
	switch {
	case errors.As(err, &pending), errors.Is(err, analytics.ErrPending):
		details := map[string]any{"state": "pending"}
		if errors.As(err, &pending) {
			details["coverage"] = chartCoverageModel(pending.Coverage)
		}
		writeError(w, r, http.StatusServiceUnavailable, "analytics_pending", "historical analytics are still being rebuilt", details)
	case errors.Is(err, analytics.ErrInvalidInput):
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_chart_range", "chart range exceeds the supported point limit or is invalid", nil)
	case errors.Is(err, analytics.ErrCorruptData):
		writeError(w, r, http.StatusServiceUnavailable, "analytics_inconsistent", "historical analytics are temporarily unavailable", nil)
	default:
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal server error", nil)
	}
}
