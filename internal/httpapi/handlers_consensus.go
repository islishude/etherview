package httpapi

import (
	"net/http"
	"strconv"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/publicquery"
)

func (h *Handler) blockConsensus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validBlockID(id) {
		h.handleReaderError(w, r, ErrInvalidInput)
		return
	}
	reader := h.consensusReader
	if reader == nil {
		reader, _ = h.reader.(publicquery.ConsensusReader)
	}
	ok := reader != nil
	if !ok {
		h.handleReaderError(w, r, ErrUnavailable)
		return
	}
	data, err := reader.BlockConsensus(r.Context(), id)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.ConsensusResponse{Data: data, Meta: h.meta(r)})
}
func (h *Handler) blockBlobs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validBlockID(id) {
		h.handleReaderError(w, r, ErrInvalidInput)
		return
	}
	reader := h.consensusReader
	if reader == nil {
		reader, _ = h.reader.(publicquery.ConsensusReader)
	}
	ok := reader != nil
	if !ok {
		h.handleReaderError(w, r, ErrUnavailable)
		return
	}
	data, err := reader.BlockBlobs(r.Context(), id)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.BlockBlobsResponse{Data: data, Meta: h.meta(r)})
}
func (h *Handler) blobContent(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	raw := r.PathValue("index")
	index, err := strconv.ParseUint(raw, 10, 64)
	if !hashPattern.MatchString(hash) || err != nil || strconv.FormatUint(index, 10) != raw {
		h.handleReaderError(w, r, ErrInvalidInput)
		return
	}
	reader := h.consensusReader
	if reader == nil {
		reader, _ = h.reader.(publicquery.ConsensusReader)
	}
	ok := reader != nil
	if !ok {
		h.handleReaderError(w, r, ErrUnavailable)
		return
	}
	block, err := h.reader.Block(r.Context(), hash)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	data, err := reader.BlobContent(r.Context(), hash, index)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=blob-"+block.Number+"-"+raw+".bin")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

func (h *Handler) browserBlobContent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.userAuthAvailable(w, r) {
		return
	}
	authentication, ok := h.requireUserSession(w, r)
	if !ok || !h.requireBlobReadOrigin(w, r) || !h.requireCSRF(w, r, authentication) {
		return
	}
	if !h.cfg.Consensus.Blobs.Enabled {
		h.handleReaderError(w, r, ErrUnavailable)
		return
	}
	h.blobContent(w, r)
}

// Same-origin browser GETs may omit Origin. The session-bound CSRF header
// remains mandatory; reject contradictory browser provenance when supplied.
func (h *Handler) requireBlobReadOrigin(w http.ResponseWriter, r *http.Request) bool {
	if len(r.Header.Values("Origin")) > 0 && !h.requireAuthOrigin(w, r) {
		return false
	}
	if values := r.Header.Values("Sec-Fetch-Site"); len(values) > 0 &&
		(len(values) != 1 || values[0] != "same-origin") {
		writeError(w, r, http.StatusForbidden, "origin_invalid", "request origin is invalid", nil)
		return false
	}
	return true
}
