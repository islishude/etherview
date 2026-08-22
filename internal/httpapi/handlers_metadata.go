package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/auth"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/metadata"
)

func (h *Handler) nftMetadata(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.Features.NFTMetadata || h.nftMetadataReader == nil {
		writeError(w, r, http.StatusServiceUnavailable, "nft_metadata_disabled", "NFT metadata is disabled", nil)
		return
	}
	address, ok := parseAddressPath(w, r)
	if !ok {
		return
	}
	parsedAddress, err := ethrpc.ParseAddress(address)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_address", "address must be 20 bytes", nil)
		return
	}
	tokenID := r.PathValue("token_id")
	if !canonicalQuantity(tokenID) {
		writeError(w, r, http.StatusBadRequest, "invalid_token_id", "token_id must be a canonical decimal uint256", nil)
		return
	}
	item, err := h.nftMetadataReader.NFTMetadata(r.Context(), common.Address(parsedAddress), tokenID)
	if err != nil {
		switch {
		case errors.Is(err, metadata.ErrNFTMetadataNotFound):
			writeError(w, r, http.StatusNotFound, "nft_metadata_not_found", "canonical NFT metadata was not found", nil)
		case errors.Is(err, metadata.ErrNFTMetadataNoncanonical):
			writeError(w, r, http.StatusConflict, "nft_metadata_noncanonical", "NFT metadata exists only for a noncanonical block", nil)
		default:
			h.logger.ErrorContext(r.Context(), "NFT metadata display query failed",
				"request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
			writeError(w, r, http.StatusInternalServerError, "nft_metadata_query_failed", "NFT metadata lookup failed", nil)
		}
		return
	}
	writeJSON(w, http.StatusOK, gen.NFTMetadataResponse{
		Data: nftMetadataModel(h.chainID(), common.Address(parsedAddress).Hex(), tokenID, item),
		Meta: h.meta(r),
	})
}

func (h *Handler) nftMedia(w http.ResponseWriter, r *http.Request) {
	setNFTMediaHeaders(w)
	w.Header().Set("X-Etherview-Media-State", "unauthorized")
	if !h.requireAPIKey(w, r, auth.ScopeRead) {
		return
	}
	if h.nftMediaSource == nil || h.nftMediaProxy == nil {
		writeNFTMediaError(w, r, http.StatusServiceUnavailable, "disabled", "nft_media_disabled", "NFT media proxy is unavailable")
		return
	}

	address, ok := parseAddressPath(w, r)
	if !ok {
		w.Header().Set("X-Etherview-Media-State", "invalid")
		return
	}
	parsedAddress, err := ethrpc.ParseAddress(address)
	if err != nil {
		writeNFTMediaError(w, r, http.StatusBadRequest, "invalid", "invalid_address", "address must be 20 bytes")
		return
	}
	tokenID := r.PathValue("token_id")
	if !canonicalQuantity(tokenID) {
		writeNFTMediaError(w, r, http.StatusBadRequest, "invalid", "invalid_token_id", "token_id must be a canonical decimal uint256")
		return
	}

	selection, err := h.nftMediaSource.SelectNFTImage(r.Context(), parsedAddress, tokenID)
	if err != nil {
		if h.handleNFTMediaSourceError(w, r, err) {
			return
		}
		h.logger.ErrorContext(r.Context(), "NFT media source query failed",
			"request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
		writeNFTMediaError(w, r, http.StatusInternalServerError, "error", "nft_media_query_failed", "NFT media lookup failed")
		return
	}

	proxied, err := h.nftMediaProxy.Fetch(r.Context(), selection.URI)
	if err != nil {
		h.handleNFTMediaFetchError(w, r, err)
		return
	}
	extension, ok := nftMediaExtension(proxied.ContentType)
	if !ok || len(proxied.Body) == 0 || !proxied.NoStore {
		h.logger.ErrorContext(r.Context(), "NFT media proxy returned invalid output",
			"request_id", requestIDFrom(r.Context()))
		writeNFTMediaError(w, r, http.StatusBadGateway, "error", "nft_media_fetch_failed", "NFT media could not be fetched safely")
		return
	}
	current, err := h.nftMediaSource.NFTImageCurrent(r.Context(), parsedAddress, tokenID, selection)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "NFT media canonicality recheck failed",
			"request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
		writeNFTMediaError(w, r, http.StatusInternalServerError, "error", "nft_media_query_failed", "NFT media lookup failed")
		return
	}
	if !current {
		writeNFTMediaError(w, r, http.StatusConflict, "noncanonical", "nft_media_noncanonical", "NFT metadata changed while media was fetched")
		return
	}

	w.Header().Set("Content-Type", proxied.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(proxied.Body)))
	w.Header().Set("Content-Disposition", `inline; filename="nft-media.`+extension+`"`)
	w.Header().Set("X-Etherview-Media-State", "available")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(proxied.Body)
}

func (h *Handler) handleNFTMediaSourceError(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case errors.Is(err, metadata.ErrMediaSourceNotFound):
		writeNFTMediaError(w, r, http.StatusNotFound, "not_found", "nft_metadata_not_found", "canonical NFT metadata was not found")
	case errors.Is(err, metadata.ErrMediaImageNotFound):
		writeNFTMediaError(w, r, http.StatusNotFound, "not_found", "nft_media_not_found", "canonical NFT metadata has no image")
	case errors.Is(err, metadata.ErrMediaSourcePending):
		w.Header().Set("Retry-After", "30")
		writeNFTMediaError(w, r, http.StatusServiceUnavailable, "pending", "nft_metadata_pending", "NFT metadata is still pending")
	case errors.Is(err, metadata.ErrMediaSourceUnavailable):
		writeNFTMediaError(w, r, http.StatusServiceUnavailable, "unavailable", "nft_media_unavailable", "NFT media is unavailable")
	case errors.Is(err, metadata.ErrMediaSourceError):
		writeNFTMediaError(w, r, http.StatusServiceUnavailable, "error", "nft_metadata_error", "NFT metadata processing failed")
	case errors.Is(err, metadata.ErrMediaSourceNoncanonical):
		writeNFTMediaError(w, r, http.StatusConflict, "noncanonical", "nft_media_noncanonical", "NFT metadata exists only for a noncanonical block")
	case errors.Is(err, metadata.ErrMediaSourceUnsafe):
		writeNFTMediaError(w, r, http.StatusUnprocessableEntity, "unsafe", "nft_media_unsafe", "NFT media source is unsafe")
	default:
		return false
	}
	return true
}

func (h *Handler) handleNFTMediaFetchError(w http.ResponseWriter, r *http.Request, err error) {
	var fetchError *metadata.FetchError
	if !errors.As(err, &fetchError) {
		h.logger.ErrorContext(r.Context(), "NFT media fetch failed",
			"request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
		writeNFTMediaError(w, r, http.StatusBadGateway, "error", "nft_media_fetch_failed", "NFT media could not be fetched safely")
		return
	}
	switch fetchError.Kind {
	case metadata.FailureUnsafeURL, metadata.FailureUnsafeContent:
		writeNFTMediaError(w, r, http.StatusUnprocessableEntity, "unsafe", "nft_media_unsafe", "NFT media source or content is unsafe")
	case metadata.FailureUnavailable:
		writeNFTMediaError(w, r, http.StatusBadGateway, "unavailable", "nft_media_unavailable", "NFT media is unavailable")
	case metadata.FailureTemporary:
		w.Header().Set("Retry-After", "30")
		writeNFTMediaError(w, r, http.StatusServiceUnavailable, "temporary", "nft_media_temporary_unavailable", "NFT media is temporarily unavailable")
	case metadata.FailureTooLarge:
		writeNFTMediaError(w, r, http.StatusRequestEntityTooLarge, "too_large", "nft_media_too_large", "NFT media exceeds the configured size limit")
	case metadata.FailureInvalid:
		writeNFTMediaError(w, r, http.StatusUnprocessableEntity, "unsafe", "nft_media_invalid", "NFT media response is invalid")
	default:
		writeNFTMediaError(w, r, http.StatusBadGateway, "error", "nft_media_fetch_failed", "NFT media could not be fetched safely")
	}
}

func setNFTMediaHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox; frame-ancestors 'none'")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// NFTMediaSecurityMiddleware applies the media boundary headers before
// authentication and rate limiting can reject a request. This keeps every
// response for the fixed media route no-store and hostile-content-safe.
func NFTMediaSecurityMiddleware(next http.Handler) http.Handler {
	if next == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isNFTMediaPath(r.URL.Path) {
			setNFTMediaHeaders(w)
			w.Header().Set("X-Etherview-Media-State", "unauthorized")
		}
		next.ServeHTTP(w, r)
	})
}

func isNFTMediaPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return len(parts) == 6 && parts[0] == "api" && parts[1] == "v1" &&
		parts[2] == "nfts" && parts[3] != "" && parts[4] != "" && parts[5] == "media"
}

func writeNFTMediaError(w http.ResponseWriter, r *http.Request, status int, state, code, message string) {
	w.Header().Set("X-Etherview-Media-State", state)
	writeError(w, r, status, code, message, nil)
}

func nftMediaExtension(contentType string) (string, bool) {
	switch contentType {
	case "image/png":
		return "png", true
	case "image/jpeg":
		return "jpg", true
	case "image/gif":
		return "gif", true
	case "image/webp":
		return "webp", true
	case "image/avif":
		return "avif", true
	default:
		return "", false
	}
}
