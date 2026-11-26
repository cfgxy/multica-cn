package computer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const RuntimeCacheLimit = int64(10 << 30)

// RuntimeDownloadTimeout leaves time for installation inside the 270-second user command.
const RuntimeDownloadTimeout = 4 * time.Minute
const runtimeArtifactLimit = int64(512 << 20)

var ErrRuntimeCacheBusy = errors.New("runtime downloads are in progress")

type RuntimeCacheEntry struct {
	ID          string    `json:"id"`
	URL         string    `json:"url"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	ContentType string    `json:"content_type"`
	CachedAt    time.Time `json:"cached_at"`
	LastUsedAt  time.Time `json:"last_used_at"`
	Metadata    bool      `json:"metadata"`
}

type RuntimeCacheSnapshot struct {
	Entries    []RuntimeCacheEntry `json:"entries"`
	Bytes      int64               `json:"bytes"`
	LimitBytes int64               `json:"limit_bytes"`
	Downloads  int                 `json:"downloads"`
}

// RuntimeDownloadCache stores only approved public upstream assets. File names
// are hashes, writes are atomic, and concurrent misses share one download.
type RuntimeDownloadCache struct {
	mu               sync.Mutex
	dir              string
	client           *http.Client
	entries          map[string]RuntimeCacheEntry
	loaded           bool
	flights          singleflight.Group
	slots            chan struct{}
	active           int
	reserved         int64
	limit, itemLimit int64
}

func NewRuntimeDownloadCache(dir string, client *http.Client) *RuntimeDownloadCache {
	return &RuntimeDownloadCache{dir: dir, client: client, entries: make(map[string]RuntimeCacheEntry), slots: make(chan struct{}, 3), limit: RuntimeCacheLimit, itemLimit: runtimeArtifactLimit}
}

func (c *RuntimeDownloadCache) loadLocked() error {
	if c.loaded {
		return nil
	}
	if err := os.MkdirAll(c.dir, 0700); err != nil {
		return err
	}
	files, err := os.ReadDir(c.dir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(c.dir, file.Name()))
		if err != nil {
			continue
		}
		var entry RuntimeCacheEntry
		if json.Unmarshal(data, &entry) != nil || entry.ID+".json" != file.Name() || !validCacheID(entry.ID) {
			continue
		}
		stat, err := os.Stat(c.path(entry.ID))
		if err != nil || !stat.Mode().IsRegular() || stat.Size() != entry.Size {
			continue
		}
		c.entries[entry.ID] = entry
	}
	for _, file := range files {
		name := file.Name()
		if strings.HasPrefix(name, "download-") || strings.HasPrefix(name, "metadata-") {
			_ = os.Remove(filepath.Join(c.dir, name))
			continue
		}
		if strings.HasSuffix(name, ".blob") {
			id := strings.TrimSuffix(name, ".blob")
			if validCacheID(id) {
				if _, ok := c.entries[id]; !ok {
					_ = os.Remove(filepath.Join(c.dir, name))
				}
			}
		}
	}
	c.loaded = true
	return nil
}
func validCacheID(id string) bool {
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == sha256.Size
}
func (c *RuntimeDownloadCache) path(id string) string { return filepath.Join(c.dir, id+".blob") }
func cacheID(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}
func runtimeMetadata(source string) bool {
	u, _ := url.Parse(source)
	if (u.Host == "x.ai" && strings.HasPrefix(u.Path, "/cli/grok-")) ||
		(u.Host == "storage.googleapis.com" && strings.HasPrefix(u.Path, "/grok-build-public-artifacts/cli/grok-")) {
		return false
	}
	return !(strings.HasSuffix(u.Path, ".tgz") || strings.HasSuffix(u.Path, ".tar.gz") || strings.Contains(u.Path, "/releases/download/") || strings.Contains(u.Path, "/binaries/"))
}
func (c *RuntimeDownloadCache) cachedLocked(id string) (RuntimeCacheEntry, bool) {
	entry, ok := c.entries[id]
	ttl := 30 * 24 * time.Hour
	if entry.Metadata {
		ttl = time.Hour
	}
	return entry, ok && time.Since(entry.CachedAt) < ttl
}

func (c *RuntimeDownloadCache) Open(ctx context.Context, source string) (*os.File, RuntimeCacheEntry, error) {
	if !AllowedRuntimeSource(source) {
		return nil, RuntimeCacheEntry{}, errors.New("unsupported runtime download source")
	}
	id := cacheID(source)
	c.mu.Lock()
	if err := c.loadLocked(); err != nil {
		c.mu.Unlock()
		return nil, RuntimeCacheEntry{}, err
	}
	entry, hit := c.cachedLocked(id)
	if hit {
		file, err := os.Open(c.path(id))
		if err == nil {
			entry.LastUsedAt = time.Now()
			c.entries[id] = entry
			c.mu.Unlock()
			return file, entry, nil
		}
	}
	c.mu.Unlock()
	// Shared downloads have their own deadline; a disconnected reader must not
	// cancel the artifact another installation is waiting for.
	result := c.flights.DoChan(id, func() (any, error) { return c.download(source, id) })
	select {
	case <-ctx.Done():
		return nil, RuntimeCacheEntry{}, ctx.Err()
	case result := <-result:
		if result.Err != nil {
			return nil, RuntimeCacheEntry{}, result.Err
		}
		entry = result.Val.(RuntimeCacheEntry)
		c.mu.Lock()
		defer c.mu.Unlock()
		file, err := os.Open(c.path(id))
		return file, entry, err
	}
}

func (c *RuntimeDownloadCache) download(source, id string) (RuntimeCacheEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), RuntimeDownloadTimeout)
	defer cancel()
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	case <-ctx.Done():
		return RuntimeCacheEntry{}, ctx.Err()
	}
	c.mu.Lock()
	if entry, ok := c.cachedLocked(id); ok {
		c.mu.Unlock()
		return entry, nil
	}
	limit := c.itemLimit
	if runtimeMetadata(source) && limit > 16<<20 {
		limit = 16 << 20
	}
	if err := c.makeRoomLocked(limit); err != nil {
		c.mu.Unlock()
		return RuntimeCacheEntry{}, err
	}
	c.active++
	c.reserved += limit
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.active--; c.reserved -= limit; c.mu.Unlock() }()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return RuntimeCacheEntry{}, err
	}
	req.Header.Set("User-Agent", "Multica-runtime-cache")
	req.Header.Set("Accept", "application/json")
	if req.URL.Host == "registry.npmjs.org" && runtimeMetadata(source) {
		// Full packuments include readmes and release history unrelated to installation.
		req.Header.Set("Accept", "application/vnd.npm.install-v1+json")
	}
	client := *c.client
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 || !allowedRuntimeRedirect(req.URL) {
			return errors.New("unsupported runtime download redirect")
		}
		return nil
	}
	response, err := client.Do(req)
	if err != nil {
		return RuntimeCacheEntry{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return RuntimeCacheEntry{}, fmt.Errorf("upstream returned %d", response.StatusCode)
	}
	if response.ContentLength > limit {
		return RuntimeCacheEntry{}, errors.New("runtime artifact exceeds cache limit")
	}
	temp, err := os.CreateTemp(c.dir, "download-*")
	if err != nil {
		return RuntimeCacheEntry{}, err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	digest := sha256.New()
	size, err := io.Copy(io.MultiWriter(temp, digest), io.LimitReader(response.Body, limit+1))
	if err != nil {
		return RuntimeCacheEntry{}, err
	}
	if size > limit {
		return RuntimeCacheEntry{}, errors.New("runtime artifact exceeds cache limit")
	}
	if err = temp.Sync(); err != nil {
		return RuntimeCacheEntry{}, err
	}
	if err = temp.Close(); err != nil {
		return RuntimeCacheEntry{}, err
	}
	now := time.Now()
	entry := RuntimeCacheEntry{ID: id, URL: source, Size: size, SHA256: hex.EncodeToString(digest.Sum(nil)), ContentType: response.Header.Get("Content-Type"), CachedAt: now, LastUsedAt: now, Metadata: runtimeMetadata(source)}
	data, _ := json.Marshal(entry)
	meta, err := os.CreateTemp(c.dir, "metadata-*")
	if err != nil {
		return RuntimeCacheEntry{}, err
	}
	defer os.Remove(meta.Name())
	if _, err = meta.Write(data); err != nil {
		meta.Close()
		return RuntimeCacheEntry{}, err
	}
	if err = meta.Close(); err != nil {
		return RuntimeCacheEntry{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err = os.Rename(temp.Name(), c.path(id)); err != nil {
		return RuntimeCacheEntry{}, err
	}
	if err = os.Rename(meta.Name(), filepath.Join(c.dir, id+".json")); err != nil {
		os.Remove(c.path(id))
		return RuntimeCacheEntry{}, err
	}
	c.entries[id] = entry
	return entry, nil
}

func (c *RuntimeDownloadCache) makeRoomLocked(size int64) error {
	entries := make([]RuntimeCacheEntry, 0, len(c.entries))
	total := c.reserved + size
	for _, entry := range c.entries {
		total += entry.Size
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].LastUsedAt.Before(entries[j].LastUsedAt) })
	for _, entry := range entries {
		if total <= c.limit {
			break
		}
		if err := c.removeLocked(entry.ID); err != nil {
			return err
		}
		total -= entry.Size
	}
	if total > c.limit {
		return ErrRuntimeCacheBusy
	}
	return nil
}
func (c *RuntimeDownloadCache) removeLocked(id string) error {
	for _, file := range []string{c.path(id), filepath.Join(c.dir, id+".json")} {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	delete(c.entries, id)
	return nil
}
func (c *RuntimeDownloadCache) Snapshot() (RuntimeCacheSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.loadLocked(); err != nil {
		return RuntimeCacheSnapshot{}, err
	}
	snapshot := RuntimeCacheSnapshot{Entries: []RuntimeCacheEntry{}, LimitBytes: c.limit, Downloads: c.active}
	for _, entry := range c.entries {
		snapshot.Entries = append(snapshot.Entries, entry)
		snapshot.Bytes += entry.Size
	}
	sort.Slice(snapshot.Entries, func(i, j int) bool { return snapshot.Entries[i].LastUsedAt.After(snapshot.Entries[j].LastUsedAt) })
	return snapshot, nil
}
func (c *RuntimeDownloadCache) Clear(metadataOnly bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.loadLocked(); err != nil {
		return err
	}
	if c.active > 0 {
		return ErrRuntimeCacheBusy
	}
	for id, entry := range c.entries {
		if !metadataOnly || entry.Metadata {
			if err := c.removeLocked(id); err != nil {
				return err
			}
		}
	}
	return nil
}

// Only catalog origins and their documented release paths can populate disk.
func AllowedRuntimeSource(source string) bool {
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() != "" {
		return false
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == ".." || part == "." {
			return false
		}
	}
	switch u.Host {
	case "registry.npmjs.org":
		return strings.HasPrefix(u.Path, "/") && u.Path != "/" && !strings.HasPrefix(u.Path, "/-/")
	case "raw.githubusercontent.com":
		return u.Path == "/can1357/oh-my-pi/main/scripts/install.sh"
	case "api.github.com":
		return strings.HasPrefix(u.Path, "/repos/can1357/oh-my-pi/releases/")
	case "github.com":
		return strings.HasPrefix(u.Path, "/can1357/oh-my-pi/releases/download/")
	case "code.kimi.com", "cdn.kimi.com":
		return strings.HasPrefix(u.Path, "/kimi-code/")
	case "x.ai":
		return strings.HasPrefix(u.Path, "/cli/")
	case "storage.googleapis.com":
		return strings.HasPrefix(u.Path, "/grok-build-public-artifacts/cli/")
	default:
		return false
	}
}
func allowedRuntimeRedirect(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	if u.Host == "release-assets.githubusercontent.com" || u.Host == "objects.githubusercontent.com" {
		return true
	}
	copy := *u
	copy.RawQuery = ""
	return AllowedRuntimeSource(copy.String())
}

// MirrorRuntimeURLs preserves upstream bytes on disk; installation capabilities
// are inserted only into responses and never persisted in shared metadata.
func MirrorRuntimeURLs(data []byte, base string) []byte {
	replacements := []string{}
	for _, host := range []string{"registry.npmjs.org", "raw.githubusercontent.com", "api.github.com", "github.com", "code.kimi.com", "cdn.kimi.com", "x.ai", "storage.googleapis.com"} {
		replacements = append(replacements, "https://"+host+"/", base+"/"+host+"/")
	}
	return []byte(strings.NewReplacer(replacements...).Replace(string(data)))
}

// PrepareRuntimeDownload adapts metadata at response time; cached upstream bytes
// stay unchanged. OMP's low-speed deadline must include a complete cold-cache fill.
func PrepareRuntimeDownload(data []byte, source, base string) []byte {
	data = MirrorRuntimeURLs(data, base)
	if source == "https://raw.githubusercontent.com/can1357/oh-my-pi/main/scripts/install.sh" {
		data = []byte(strings.ReplaceAll(string(data), "--speed-time 30", "--speed-time 250"))
	}
	return data
}
