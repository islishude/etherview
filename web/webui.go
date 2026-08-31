// Package webui serves the embedded Etherview single-page application.
package webui

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
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
	cspNonceBytes         = 32
	cspNonceMetaName      = "etherview-csp-nonce"
	assetManifestFile     = "asset-manifest.json"
	assetManifestSchema   = "etherview-web-assets-v1"
)

//go:embed dist
var embedded embed.FS

var distribution = mustSub(embedded, "dist")

// RouteHandler serves and classifies the embedded SPA without exposing raw
// navigation or asset paths as observability labels.
type RouteHandler interface {
	http.Handler
	RoutePattern(*http.Request) string
}

// NewHandler returns a handler for the embedded SPA. API and operational paths
// intentionally never receive the index fallback, so a missing backend route
// cannot be disguised as a successful HTML response.
func NewHandler() RouteHandler {
	metadata, err := loadAssetManifest(distribution)
	if err != nil {
		panic(fmt.Sprintf("load embedded Web asset manifest: %v", err))
	}
	return &handler{
		assets: distribution, metadata: metadata,
		nonceGenerator: generateCSPNonce,
	}
}

// Assets exposes the read-only embedded distribution for diagnostics and tests.
func Assets() fs.FS {
	return distribution
}

type handler struct {
	assets         fs.FS
	metadata       map[string]assetMetadata
	nonceGenerator func() (string, error)
}

type assetRepresentation struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type assetMetadata struct {
	Identity assetRepresentation  `json:"identity"`
	Gzip     *assetRepresentation `json:"gzip,omitempty"`
	Brotli   *assetRepresentation `json:"br,omitempty"`
}

type assetManifest struct {
	Schema string                   `json:"schema"`
	Assets map[string]assetMetadata `json:"assets"`
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
	if name == "" || name == "index.html" {
		return "/"
	}
	if isInternalDistributionFile(name) {
		return "/assets/*"
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

	if name == "" || name == "index.html" {
		h.serveShell(response, request)
		return
	}
	if isInternalDistributionFile(name) {
		notFound(response, request)
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

	h.serveShell(response, request)
}

func (h *handler) serveShell(
	response http.ResponseWriter,
	request *http.Request,
) {
	contents, err := fs.ReadFile(h.assets, "index.html")
	if err != nil {
		notFound(response, request)
		return
	}

	nonceGenerator := h.nonceGenerator
	if nonceGenerator == nil {
		nonceGenerator = generateCSPNonce
	}
	nonce, err := nonceGenerator()
	if err != nil || !validCSPNonce(nonce) {
		shellFailure(response)
		return
	}
	contents, err = injectCSPNonce(contents, nonce)
	if err != nil {
		shellFailure(response)
		return
	}

	response.Header().Set("Cache-Control", noStoreCache)
	response.Header().Del("ETag")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Content-Security-Policy", contentSecurityPolicyWithNonce(nonce))
	http.ServeContent(response, request, "index.html", time.Time{}, bytes.NewReader(contents))
}

func (h *handler) serveFile(
	response http.ResponseWriter,
	request *http.Request,
	name string,
	immutable bool,
) {
	addVaryToken(response.Header(), "Accept-Encoding")
	representation, encoding, acceptable := h.selectRepresentation(request, name)
	if !acceptable {
		response.Header().Set("Cache-Control", noStoreCache)
		http.Error(response, http.StatusText(http.StatusNotAcceptable), http.StatusNotAcceptable)
		return
	}
	contents, err := fs.ReadFile(h.assets, representation.Path)
	if err != nil {
		notFound(response, request)
		return
	}
	if representation.Bytes != int64(len(contents)) || !validSHA256(representation.SHA256) {
		shellFailure(response)
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
	if encoding != "" {
		response.Header().Set("Content-Encoding", encoding)
	} else {
		response.Header().Del("Content-Encoding")
	}

	etag := `"` + representation.SHA256 + `"`
	response.Header().Set("ETag", etag)
	if etagMatches(request.Header.Get("If-None-Match"), etag) {
		response.WriteHeader(http.StatusNotModified)
		return
	}

	http.ServeContent(response, request, name, time.Time{}, bytes.NewReader(contents))
}

func (h *handler) selectRepresentation(
	request *http.Request,
	name string,
) (assetRepresentation, string, bool) {
	metadata, exists := h.metadata[name]
	if !exists {
		contents, err := fs.ReadFile(h.assets, name)
		if err != nil {
			return assetRepresentation{}, "", false
		}
		digest := sha256.Sum256(contents)
		metadata.Identity = assetRepresentation{
			Path: name, Bytes: int64(len(contents)), SHA256: hex.EncodeToString(digest[:]),
		}
	}
	encoding, acceptable := preferredEncoding(
		request.Header.Get("Accept-Encoding"),
		request.Header.Get("Range") != "",
		metadata.Brotli != nil,
		metadata.Gzip != nil,
	)
	if !acceptable {
		return assetRepresentation{}, "", false
	}
	switch encoding {
	case "br":
		return *metadata.Brotli, encoding, true
	case "gzip":
		return *metadata.Gzip, encoding, true
	default:
		return metadata.Identity, "", true
	}
}

func loadAssetManifest(assets fs.FS) (map[string]assetMetadata, error) {
	contents, err := fs.ReadFile(assets, assetManifestFile)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var manifest assetManifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("invalid trailing Web asset manifest data")
	}
	if manifest.Schema != assetManifestSchema ||
		len(manifest.Assets) == 0 || len(manifest.Assets) > 10_000 {
		return nil, fmt.Errorf("invalid Web asset manifest")
	}
	for name, metadata := range manifest.Assets {
		if !validManifestAssetName(name) || metadata.Identity.Path != name ||
			!validAssetRepresentation(assets, metadata.Identity) {
			return nil, fmt.Errorf("invalid Web asset metadata for %q", name)
		}
		for encoding, representation := range map[string]*assetRepresentation{
			"gzip": metadata.Gzip,
			"br":   metadata.Brotli,
		} {
			if representation == nil {
				continue
			}
			suffix := ".gz"
			if encoding == "br" {
				suffix = ".br"
			}
			if representation.Path != name+suffix ||
				!validAssetRepresentation(assets, *representation) {
				return nil, fmt.Errorf("invalid %s metadata for %q", encoding, name)
			}
		}
	}
	return manifest.Assets, nil
}

func validManifestAssetName(name string) bool {
	return name != "" && name != assetManifestFile && !isInternalDistributionFile(name) &&
		fs.ValidPath(name) && path.Clean(name) == name
}

func validAssetRepresentation(assets fs.FS, representation assetRepresentation) bool {
	if representation.Bytes < 0 || !validSHA256(representation.SHA256) ||
		!fs.ValidPath(representation.Path) {
		return false
	}
	info, err := fs.Stat(assets, representation.Path)
	return err == nil && !info.IsDir() && info.Size() == representation.Bytes
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func isInternalDistributionFile(name string) bool {
	return name == assetManifestFile || strings.HasSuffix(name, ".gz") ||
		strings.HasSuffix(name, ".br")
}

func preferredEncoding(
	header string,
	rangeRequest bool,
	hasBrotli bool,
	hasGzip bool,
) (string, bool) {
	quality := map[string]float64{}
	wildcard := -1.0
	for raw := range strings.SplitSeq(header, ",") {
		parts := strings.Split(strings.TrimSpace(raw), ";")
		name := strings.ToLower(strings.TrimSpace(parts[0]))
		if name == "" {
			continue
		}
		value := 1.0
		valid := true
		for _, parameter := range parts[1:] {
			key, rawValue, found := strings.Cut(strings.TrimSpace(parameter), "=")
			if !found || !strings.EqualFold(strings.TrimSpace(key), "q") {
				valid = false
				break
			}
			value, valid = parseAcceptQuality(strings.TrimSpace(rawValue))
			if !valid {
				break
			}
		}
		if !valid {
			continue
		}
		if name == "*" {
			wildcard = max(wildcard, value)
		} else {
			quality[name] = max(quality[name], value)
		}
	}
	encodingQuality := func(name string) float64 {
		if value, exists := quality[name]; exists {
			return value
		}
		if wildcard >= 0 {
			return wildcard
		}
		return 0
	}
	identityQuality := 1.0
	if value, exists := quality["identity"]; exists {
		identityQuality = value
	}
	if rangeRequest {
		return "", identityQuality > 0
	}
	type candidate struct {
		name     string
		quality  float64
		priority int
	}
	candidates := []candidate{{quality: identityQuality, priority: 1}}
	if hasGzip {
		candidates = append(candidates, candidate{name: "gzip", quality: encodingQuality("gzip"), priority: 2})
	}
	if hasBrotli {
		candidates = append(candidates, candidate{name: "br", quality: encodingQuality("br"), priority: 3})
	}
	selected := candidate{quality: -1}
	for _, current := range candidates {
		if current.quality > selected.quality ||
			current.quality == selected.quality && current.priority > selected.priority {
			selected = current
		}
	}
	return selected.name, selected.quality > 0
}

func addVaryToken(header http.Header, value string) {
	for _, existing := range header.Values("Vary") {
		for token := range strings.SplitSeq(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(token), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}

func generateCSPNonce() (string, error) {
	raw := make([]byte, cspNonceBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate CSP nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func validCSPNonce(nonce string) bool {
	if len(nonce) != base64.RawURLEncoding.EncodedLen(cspNonceBytes) {
		return false
	}
	for _, character := range []byte(nonce) {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

func injectCSPNonce(contents []byte, nonce string) ([]byte, error) {
	const closingHead = "</head>"
	if bytes.Count(contents, []byte(closingHead)) != 1 ||
		bytes.Contains(contents, []byte(`name="`+cspNonceMetaName+`"`)) {
		return nil, fmt.Errorf("inject CSP nonce metadata into shell")
	}
	index := bytes.Index(contents, []byte(closingHead))
	metadata := []byte("\n    <meta name=\"" + cspNonceMetaName + "\" content=\"" + html.EscapeString(nonce) + "\">\n")
	result := make([]byte, 0, len(contents)+len(metadata))
	result = append(result, contents[:index]...)
	result = append(result, metadata...)
	result = append(result, contents[index:]...)
	return result, nil
}

func contentSecurityPolicyWithNonce(nonce string) string {
	return strings.Replace(contentSecurityPolicy, "style-src 'self'", "style-src 'self' 'nonce-"+nonce+"'", 1)
}

func shellFailure(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", noStoreCache)
	response.Header().Del("ETag")
	http.Error(response, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
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
	for segment := range strings.SplitSeq(name, "/") {
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
	for mediaRange := range strings.SplitSeq(accept, ",") {
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
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

func etagMatches(condition, current string) bool {
	for candidate := range strings.SplitSeq(condition, ",") {
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
