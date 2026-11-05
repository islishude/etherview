package httpapi

import (
	"net/http"
	"strings"

	"github.com/islishude/etherview/internal/abicontract"
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
		allowed := key == "cursor" || key == "limit" || key == "from_block" || key == "to_block" || key == "event_id" || (directory && key == "name") || (!directory && (key == "event_signature" || key == "topic0" || key == "topic1" || key == "topic2" || key == "topic3"))
		if !allowed || len(values) != 1 {
			h.handleCatalogError(w, r, catalog.ErrInvalidInput)
			return catalog.AddressLogRequest{}, false
		}
	}
	if len(query.Get("event_signature")) > 4096 || len(query.Get("name")) > 128 {
		h.handleCatalogError(w, r, catalog.ErrInvalidInput)
		return catalog.AddressLogRequest{}, false
	}
	request := catalog.AddressLogRequest{ChainID: h.chainID(), Address: address, Limit: limit, Cursor: cursor, EventSignature: query.Get("event_signature"), EventID: query.Get("event_id"), Name: query.Get("name"), FromBlock: query.Get("from_block"), ToBlock: query.Get("to_block")}
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
	for i, key := range []string{"topic0", "topic1", "topic2", "topic3"} {
		if query.Has(key) && !hashPattern.MatchString(query.Get(key)) {
			h.handleCatalogError(w, r, catalog.ErrInvalidInput)
			return request, false
		}
		request.Topics[i] = query.Get(key)
	}
	if (query.Has("event_id") && !hashPattern.MatchString(request.EventID)) || (query.Has("event_signature") && (request.EventSignature == "" || query.Has("event_id"))) {
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
		fields := make([]gen.EventIndexedField, len(item.IndexedFields))
		for j, field := range item.IndexedFields {
			fields[j] = gen.EventIndexedField{Name: field.Name, Type: field.Type, ParameterIndex: field.ParameterIndex, TopicIndex: field.TopicIndex, Components: eventParameterModels(field.Components)}
		}
		items[i] = gen.AddressLogEvent{Name: item.Name, Signature: item.Signature, EventId: item.EventID, Anonymous: item.Anonymous, IndexedFields: fields}
	}
	writeJSON(w, http.StatusOK, gen.AddressLogEventListResponse{Data: items, Meta: h.catalogPageMeta(r, page.NextCursor, page.Snapshot)})
}

func eventParameterModels(parameters []abicontract.EventParameter) *[]gen.EventParameter {
	if len(parameters) == 0 {
		return nil
	}
	result := make([]gen.EventParameter, len(parameters))
	for i, parameter := range parameters {
		result[i] = gen.EventParameter{Name: parameter.Name, Type: parameter.Type, Components: eventParameterModels(parameter.Components)}
	}
	return &result
}
