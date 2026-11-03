package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/catalog"
)

func (h *Handler) addressLogRequest(w http.ResponseWriter, r *http.Request) (catalog.AddressLogRequest, bool) {
	address, limit, cursor, ok := h.addressActivityPage(w, r)
	if !ok {
		return catalog.AddressLogRequest{}, false
	}
	query := r.URL.Query()
	directory := strings.HasSuffix(r.URL.Path, "/log-events")
	for key, values := range query {
		allowed := key == "cursor" || key == "limit" || key == "from_block" || key == "to_block" || (directory && key == "name") || (!directory && (key == "event_signature" || key == "topic_index" || key == "topic"))
		if !allowed || len(values) != 1 {
			h.handleCatalogError(w, r, catalog.ErrInvalidInput)
			return catalog.AddressLogRequest{}, false
		}
	}
	if len(query.Get("event_signature")) > 4096 || len(query.Get("name")) > 128 {
		h.handleCatalogError(w, r, catalog.ErrInvalidInput)
		return catalog.AddressLogRequest{}, false
	}
	request := catalog.AddressLogRequest{ChainID: h.chainID(), Address: address, Limit: limit, Cursor: cursor, EventSignature: query.Get("event_signature"), Topic: query.Get("topic"), Name: query.Get("name"), FromBlock: query.Get("from_block"), ToBlock: query.Get("to_block")}
	for _, bound := range []string{"from_block", "to_block"} {
		if query.Has(bound) && !canonicalQuantity(query.Get(bound)) {
			h.handleCatalogError(w, r, catalog.ErrInvalidInput)
			return request, false
		}
	}
	if request.FromBlock != "" && request.ToBlock != "" && (len(request.FromBlock) > len(request.ToBlock) || (len(request.FromBlock) == len(request.ToBlock) && request.FromBlock > request.ToBlock)) {
		h.handleCatalogError(w, r, catalog.ErrInvalidInput)
		return request, false
	}
	if query.Has("topic_index") {
		index, err := strconv.Atoi(query.Get("topic_index"))
		if err != nil || index < 0 || index > 3 {
			h.handleCatalogError(w, r, catalog.ErrInvalidInput)
			return request, false
		}
		request.TopicIndex = &index
	}
	if (query.Has("topic") != query.Has("topic_index")) || (query.Has("event_signature") && (request.EventSignature == "" || query.Has("topic"))) || (query.Has("topic") && !hashPattern.MatchString(request.Topic)) {
		h.handleCatalogError(w, r, catalog.ErrInvalidInput)
		return request, false
	}
	return request, true
}
func (h *Handler) addressLogs(w http.ResponseWriter, r *http.Request) {
	request, ok := h.addressLogRequest(w, r)
	if !ok {
		return
	}
	page, err := h.catalog.AddressLogs(r.Context(), request)
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.AddressLog, len(page.Items))
	for i, item := range page.Items {
		items[i] = gen.AddressLog{Log: gen.TransactionLog{Address: item.Log.Address, LogIndex: item.Log.LogIndex, Topics: item.Log.Topics, Data: item.Log.Data, Decoding: transactionLogDecodingModel(item.Log.Decoding)}, BlockNumber: item.BlockNumber, BlockHash: item.BlockHash, BlockTimestamp: item.BlockTimestamp, TransactionHash: item.TransactionHash, TransactionIndex: item.TransactionIndex}
	}
	writeJSON(w, http.StatusOK, gen.AddressLogListResponse{Data: items, Meta: h.catalogPageMeta(r, page.NextCursor, page.Snapshot)})
}
func (h *Handler) addressLogEvents(w http.ResponseWriter, r *http.Request) {
	request, ok := h.addressLogRequest(w, r)
	if !ok {
		return
	}
	page, err := h.catalog.AddressLogEvents(r.Context(), request)
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.AddressLogEvent, len(page.Items))
	for i, item := range page.Items {
		items[i] = gen.AddressLogEvent{Name: item.Name, Signature: item.Signature}
	}
	writeJSON(w, http.StatusOK, gen.AddressLogEventListResponse{Data: items, Meta: h.catalogPageMeta(r, page.NextCursor, page.Snapshot)})
}
