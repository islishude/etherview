// Package webui serves the immutable, embedded Etherview single-page application.
package webui

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	contentSecurityPolicy = "default-src 'none'; base-uri 'none'; connect-src 'self'; font-src 'self'; form-action 'self'; frame-ancestors 'none'; frame-src 'none'; img-src 'self' data:; manifest-src 'self'; media-src 'self'; object-src 'none'; script-src 'self'; style-src 'self'; worker-src 'none'"
	immutableCache        = "public, max-age=31536000, immutable"
	noStoreCache          = "no-store"
)

//go:embed all:dist
var embedded embed.FS

var distribution = mustSub(embedded, "dist")

// NewHandler returns a handler for the embedded SPA. API and operational paths
// intentionally never receive the index fallback, so a missing backend route
// cannot be disguised as a successful HTML response.
func NewHandler() http.Handler {
	return &handler{assets: distribution}
}

// Assets exposes the read-only embedded distribution for diagnostics and tests.
func Assets() fs.FS {
	return distribution
}

type handler struct {
	assets fs.FS
}

// RoutePattern classifies the catch-all web handler without returning a raw
// navigation or asset path. It follows the same reserved-path, method, asset,
// and HTML-fallback boundaries as ServeHTTP.
func (h *handler) RoutePattern(request *http.Request) string {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		return "method_not_allowed"
	}
	name, valid := requestAssetName(request.URL.Path)
	if !valid || isReservedPath(name) {
		return "unmatched"
	}
	if name == "" {
		return "/"
	}
	if info, err := fs.Stat(h.assets, name); err == nil && !info.IsDir() {
		return "/assets/*"
	}
	if looksLikeAsset(name) {
		return "/assets/*"
	}
	if request.Method != http.MethodGet || !acceptsHTML(request.Header.Get("Accept")) {
		return "unmatched"
	}
	return "/{spa...}"
}

func (h *handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	setSecurityHeaders(response.Header())
	response.Header().Add("Vary", "Accept")

	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		response.Header().Set("Allow", "GET, HEAD")
		response.Header().Set("Cache-Control", noStoreCache)
		http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	name, valid := requestAssetName(request.URL.Path)
	if !valid || isReservedPath(name) {
		notFound(response, request)
		return
	}

	if name == "" {
		h.serveFile(response, request, "index.html", false)
		return
	}

	if info, err := fs.Stat(h.assets, name); err == nil && !info.IsDir() {
		h.serveFile(response, request, name, isHashedAsset(name))
		return
	}

	// HEAD remains available for real embedded files, but only a GET navigation
	// may receive the SPA shell for a route that does not exist in the embedded
	// filesystem. This keeps every non-GET and API-shaped miss distinguishable
	// from a successful application document.
	if request.Method != http.MethodGet || looksLikeAsset(name) || !acceptsHTML(request.Header.Get("Accept")) {
		notFound(response, request)
		return
	}

	h.serveFile(response, request, "index.html", false)
}

func (h *handler) serveFile(
	response http.ResponseWriter,
	request *http.Request,
	name string,
	immutable bool,
) {
	contents, err := fs.ReadFile(h.assets, name)
	if err != nil {
		notFound(response, request)
		return
	}

	if name == "index.html" {
		response.Header().Set("Cache-Control", noStoreCache)
	} else if immutable {
		response.Header().Set("Cache-Control", immutableCache)
	} else {
		response.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	}

	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = http.DetectContentType(contents)
	}
	response.Header().Set("Content-Type", contentType)

	digest := sha256.Sum256(contents)
	etag := `"` + hex.EncodeToString(digest[:]) + `"`
	response.Header().Set("ETag", etag)
	if etagMatches(request.Header.Get("If-None-Match"), etag) {
		response.WriteHeader(http.StatusNotModified)
		return
	}

	http.ServeContent(response, request, name, time.Time{}, bytes.NewReader(contents))
}

func setSecurityHeaders(header http.Header) {
	header.Set("Content-Security-Policy", contentSecurityPolicy)
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
	header.Set("Origin-Agent-Cluster", "?1")
	header.Set("Permissions-Policy", "camera=(), geolocation=(), microphone=(), payment=(), usb=()")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("Strict-Transport-Security", "max-age=31536000")
	header.Set("X-DNS-Prefetch-Control", "off")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}

func requestAssetName(urlPath string) (string, bool) {
	name := strings.TrimPrefix(urlPath, "/")
	if strings.ContainsRune(name, '\x00') || strings.Contains(name, `\`) {
		return "", false
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "." || segment == ".." {
			return "", false
		}
	}
	if name != "" && (!fs.ValidPath(name) || path.Clean(name) != name) {
		return "", false
	}
	return name, true
}

func isReservedPath(name string) bool {
	name = strings.ToLower(name)
	for _, reserved := range []string{"api", "v2/api", "health", "metrics"} {
		if name == reserved || strings.HasPrefix(name, reserved+"/") {
			return true
		}
	}
	return false
}

func acceptsHTML(accept string) bool {
	if strings.TrimSpace(accept) == "" {
		return true
	}
	bestSpecificity := -1
	bestQuality := 0.0
	for _, mediaRange := range strings.Split(accept, ",") {
		mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(mediaRange))
		if err != nil {
			continue
		}
		specificity := -1
		switch strings.ToLower(mediaType) {
		case "text/html":
			specificity = 2
		case "text/*":
			specificity = 1
		case "*/*":
			specificity = 0
		}
		if specificity < 0 {
			continue
		}

		quality := 1.0
		if rawQuality, ok := parameters["q"]; ok {
			parsed, valid := parseAcceptQuality(rawQuality)
			if !valid {
				continue
			}
			quality = parsed
		}
		if specificity > bestSpecificity {
			bestSpecificity = specificity
			bestQuality = quality
		} else if specificity == bestSpecificity && quality > bestQuality {
			bestQuality = quality
		}
	}
	return bestSpecificity >= 0 && bestQuality > 0
}

func parseAcceptQuality(raw string) (float64, bool) {
	if raw == "0" {
		return 0, true
	}
	if raw == "1" {
		return 1, true
	}
	if len(raw) < 2 || len(raw) > 5 || raw[1] != '.' || (raw[0] != '0' && raw[0] != '1') {
		return 0, false
	}
	for _, digit := range raw[2:] {
		if digit < '0' || digit > '9' || (raw[0] == '1' && digit != '0') {
			return 0, false
		}
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	return parsed, err == nil
}

func looksLikeAsset(name string) bool {
	if strings.HasPrefix(name, "assets/") {
		return true
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".avif", ".bmp", ".br", ".cjs", ".css", ".csv", ".eot", ".gif", ".gz", ".htm", ".html", ".ico", ".jpeg", ".jpg", ".js", ".json", ".jsx", ".map", ".mjs", ".mp3", ".mp4", ".ogg", ".otf", ".pdf", ".png", ".rss", ".svg", ".tar", ".ts", ".tsx", ".ttf", ".txt", ".wasm", ".webm", ".webmanifest", ".webp", ".woff", ".woff2", ".xml", ".zip":
		return true
	default:
		return false
	}
}

func isHashedAsset(name string) bool {
	if !strings.HasPrefix(name, "assets/") {
		return false
	}
	base := strings.TrimPrefix(name, "assets/")
	if base == "" || strings.ContainsRune(base, '/') {
		return false
	}
	dot := strings.LastIndexByte(base, '.')
	separator := dot - 9
	if separator <= 0 || dot == len(base)-1 || base[separator] != '-' {
		return false
	}
	for _, character := range base[dot-8 : dot] {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_') {
			return false
		}
	}
	return true
}

func etagMatches(condition, current string) bool {
	for _, candidate := range strings.Split(condition, ",") {
		candidate = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(candidate), "W/"))
		if candidate == "*" || candidate == current {
			return true
		}
	}
	return false
}

func notFound(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", noStoreCache)
	http.NotFound(response, request)
}

func mustSub(source fs.FS, directory string) fs.FS {
	result, err := fs.Sub(source, directory)
	if err != nil {
		panic(fmt.Sprintf("webui: embedded distribution is invalid: %v", err))
	}
	return result
}
