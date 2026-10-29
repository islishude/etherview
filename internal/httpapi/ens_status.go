package httpapi

import (
	"context"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/enscontract"
)

func (h *Handler) nameSourceStatus(ctx context.Context) []gen.NameSourceStatus {
	reader, ok := h.addressNames.(enscontract.StatusReader)
	if !ok || !h.cfg.Features.ENS {
		return nil
	}
	rows, e := reader.NameStatus(ctx)
	if e != nil {
		return nil
	} // Optional name health never changes core readiness.
	result := make([]gen.NameSourceStatus, 0, len(rows))
	for _, row := range rows {
		item := gen.NameSourceStatus{Source: gen.NameSourceStatusSource(row.Source), State: gen.NameSourceStatusState(row.State), Pending: gen.Quantity(row.Pending), Target: (*gen.Quantity)(row.Target), Scanned: (*gen.Quantity)(row.Scanned), Published: (*gen.Quantity)(row.Published), Protocols: []gen.NameSourceStatusProtocols{}}
		for _, protocol := range row.Protocols {
			item.Protocols = append(item.Protocols, gen.NameSourceStatusProtocols(protocol))
		}
		if !row.ValidUntil.IsZero() {
			item.ValidUntil = &row.ValidUntil
		}
		if !row.LastSuccess.IsZero() {
			item.LastSuccess = &row.LastSuccess
		}
		if row.Code != "" {
			item.Code = &row.Code
		}
		result = append(result, item)
	}
	return result
}
