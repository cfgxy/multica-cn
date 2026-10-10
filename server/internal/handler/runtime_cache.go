package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/computer"
)

type runtimeCacheTransport struct {
	cache *computer.RuntimeDownloadCache
}

func (t runtimeCacheTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	file, entry, err := t.cache.Open(req.Context(), req.URL.String())
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{entry.ContentType}}, Body: file, ContentLength: entry.Size, Request: req}, nil
}
func (h *Handler) runtimeCache() (*computer.RuntimeDownloadCache, *computer.RuntimeVersionCache) {
	h.runtimeCacheOnce.Do(func() {
		if h.runtimeDownloads == nil {
			dir := os.Getenv("MULTICA_RUNTIME_CACHE_DIR")
			if dir == "" {
				state := os.Getenv("MULTICA_COMPUTER_STATE_DIR")
				if state == "" {
					state, _ = os.UserCacheDir()
					state = filepath.Join(state, "multica")
				}
				dir = filepath.Join(state, "runtime-cache")
			}
			h.runtimeDownloads = computer.NewRuntimeDownloadCache(dir, computer.NewRuntimeDownloadClient(os.Getenv("MULTICA_RUNTIME_DOWNLOAD_PROXY")))
		}
		h.runtimeVersions = computer.NewRuntimeVersionCache(&http.Client{Transport: runtimeCacheTransport{h.runtimeDownloads}, Timeout: 10 * time.Second})
	})
	return h.runtimeDownloads, h.runtimeVersions
}

func runtimeDownloadSignature(operation, expiry string) string {
	mac := hmac.New(sha256.New, auth.JWTSecret())
	mac.Write([]byte("runtime-download:v1|" + operation + "|" + expiry))
	return hex.EncodeToString(mac.Sum(nil))
}
func runtimeDownloadBase(server, operation string, now time.Time) string {
	expiry := strconv.FormatInt(now.Add(6*time.Minute).Unix(), 10)
	return strings.TrimRight(server, "/") + "/api/runtime-downloads/" + operation + "/" + expiry + "/" + runtimeDownloadSignature(operation, expiry)
}
func validRuntimeDownloadSignature(operation, expiry, signature string, now time.Time) bool {
	expires, err := strconv.ParseInt(expiry, 10, 64)
	if err != nil || now.Unix() >= expires || expires > now.Add(6*time.Minute).Unix() {
		return false
	}
	expected := runtimeDownloadSignature(operation, expiry)
	return hmac.Equal([]byte(expected), []byte(signature))
}
func runtimeCacheServerURL() (string, bool) {
	raw := strings.TrimRight(os.Getenv("MULTICA_COMPUTER_SERVER_URL"), "/")
	if strings.ContainsAny(raw, " \t\r\n\"'`$;&|<>()\\") {
		return "", false
	}
	parsed, err := url.Parse(raw)
	return raw, err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

// Signed downloads grant access only during the associated installation. The
// catalog allowlist prevents the route from becoming an arbitrary URL proxy.
func (h *Handler) RuntimeDownload(w http.ResponseWriter, r *http.Request) {
	operation, expiry, signature := chi.URLParam(r, "operation"), chi.URLParam(r, "expiry"), chi.URLParam(r, "signature")
	if !validRuntimeDownloadSignature(operation, expiry, signature, time.Now()) {
		writeError(w, 401, "Invalid or expired runtime download")
		return
	}
	id, ok := parseUUIDOrBadRequest(w, operation, "operation")
	if !ok {
		return
	}
	var runtimeID string
	err := h.DB.QueryRow(r.Context(), `SELECT runtime_id FROM computer_operation WHERE id=$1 AND kind='runtime_install' AND state='running' AND deadline_at>now()`, id).Scan(&runtimeID)
	if err != nil {
		writeError(w, 403, "Runtime installation is not active")
		return
	}
	host := chi.URLParam(r, "source")
	// Chi matches RawPath when present, so its wildcard may still contain %2f
	// from a scoped npm package. URL.Path is already decoded exactly once.
	rest := strings.TrimPrefix(r.URL.Path, "/api/runtime-downloads/"+operation+"/"+expiry+"/"+signature+"/"+host+"/")
	source := (&url.URL{Scheme: "https", Host: host, Path: "/" + rest}).String()
	allowed := false
	for _, target := range runtimeTargets() {
		if target.id == runtimeID {
			switch runtimeID {
			case "omp":
				allowed = host == "raw.githubusercontent.com" || host == "api.github.com" || host == "github.com"
			case "kimi":
				allowed = host == "code.kimi.com" || host == "cdn.kimi.com"
			case "grok":
				allowed = host == "x.ai" || host == "storage.googleapis.com"
			default:
				allowed = strings.HasPrefix(target.source, "npm:") && host == "registry.npmjs.org"
			}
		}
	}
	if !allowed || !computer.AllowedRuntimeSource(source) {
		writeError(w, 403, "Unsupported runtime source")
		return
	}
	cache, _ := h.runtimeCache()
	file, entry, err := cache.Open(r.Context(), source)
	if err != nil {
		slog.WarnContext(r.Context(), "runtime download failed", "operation_id", operation, "source", source, "error", err)
		writeError(w, 502, "Cannot download runtime from upstream; retry the installation")
		return
	}
	defer file.Close()
	server, ok := runtimeCacheServerURL()
	if !ok {
		writeError(w, 503, "Runtime cache server URL is not configured")
		return
	}
	base := strings.TrimRight(server, "/") + "/api/runtime-downloads/" + operation + "/" + expiry + "/" + signature
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if entry.ContentType != "" {
		w.Header().Set("Content-Type", entry.ContentType)
	}
	// Metadata and native installer scripts point all supported downloads back
	// through this same capability. Binary files remain byte-for-byte upstream.
	if entry.Metadata {
		data, err := io.ReadAll(io.LimitReader(file, 16<<20))
		if err != nil || entry.Size > 16<<20 {
			writeError(w, 502, "Runtime metadata is too large")
			return
		}
		data = computer.PrepareRuntimeDownload(data, source, base)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method != http.MethodHead {
			w.Write(data)
		}
		return
	}
	http.ServeContent(w, r, filepath.Base(rest), entry.CachedAt, file)
}

func (h *Handler) AdminRuntimeCache(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireInstanceAdmin(w, r); !ok {
		return
	}
	cache, versions := h.runtimeCache()
	snapshot, err := cache.Snapshot()
	if err != nil {
		writeError(w, 500, "Cannot read runtime cache")
		return
	}
	type catalogEntry struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		computer.RuntimeVersion
	}
	catalog := []catalogEntry{}
	for _, target := range runtimeTargets() {
		catalog = append(catalog, catalogEntry{target.id, target.name, versions.Get(target.id, target.source)})
	}
	writeJSON(w, 200, struct {
		computer.RuntimeCacheSnapshot
		Catalog []catalogEntry `json:"catalog"`
	}{snapshot, catalog})
}
func (h *Handler) AdminRuntimeCacheRefresh(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireInstanceAdmin(w, r); !ok {
		return
	}
	cache, versions := h.runtimeCache()
	if err := cache.Clear(true); err != nil {
		writeError(w, 409, "Downloads are in progress; retry when they finish")
		return
	}
	versions.Invalidate()
	for _, target := range runtimeTargets() {
		versions.Get(target.id, target.source)
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) AdminRuntimeCacheClear(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireInstanceAdmin(w, r); !ok {
		return
	}
	// Preserve cache files and in-flight installer responses while any managed
	// installation is active, even between its individual download requests.
	var active bool
	if err := h.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM computer_operation WHERE kind='runtime_install' AND state IN ('queued','running') AND deadline_at>now())`).Scan(&active); err != nil {
		writeError(w, 500, "Cannot check active installations")
		return
	}
	if active {
		writeError(w, 409, "Runtime installations are in progress; retry when they finish")
		return
	}
	cache, versions := h.runtimeCache()
	if err := cache.Clear(false); err != nil {
		writeError(w, 409, "Downloads are in progress; retry when they finish")
		return
	}
	versions.Invalidate()
	w.WriteHeader(http.StatusNoContent)
}

func cachedRuntimeInstallCommand(target runtimeTarget, base, user, version string) string {
	installer := string(computer.MirrorRuntimeURLs([]byte(target.install), base))
	if strings.HasPrefix(target.source, "npm:") {
		installer = strings.Replace(installer, "npm install ", "npm install --registry "+base+"/registry.npmjs.org/ ", 1)
	}
	return strings.ReplaceAll(strings.ReplaceAll(installer, "{{user}}", user), "{{version}}", version)
}
