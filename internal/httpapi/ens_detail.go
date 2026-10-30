package httpapi

import (
	"encoding/json"
	"errors"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/enscontract"
	"net/http"
)

func (h *Handler) nameDetail(w http.ResponseWriter, r *http.Request) {
	reader, ok := h.addressNames.(enscontract.DetailReader)
	if !ok || !h.cfg.Features.ENS {
		h.handleReaderError(w, r, NewCapabilityUnavailableError("name", "unavailable", "not_configured"))
		return
	}
	detail, err := reader.NameDetail(r.Context(), r.PathValue("name"))
	if errors.Is(err, enscontract.ErrInvalidName) {
		writeError(w, r, http.StatusBadRequest, "invalid_name", "name is not a valid ENS name", nil)
		return
	}
	if err != nil {
		h.handleENSReaderError(w, r, err)
		return
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	var data gen.NameDetail
	if err = json.Unmarshal(raw, &data); err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.NameDetailResponse{Data: data, Meta: h.meta(r)})
}
