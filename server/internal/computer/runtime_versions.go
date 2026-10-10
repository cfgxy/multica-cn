package computer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"
)

// RuntimeVersion is public release metadata, shared across accounts on this server.
type RuntimeVersion struct {
	LatestVersion          string     `json:"latest_version"`
	LatestVersionState     string     `json:"latest_version_state"`
	LatestVersionCheckedAt *time.Time `json:"latest_version_checked_at"`
}

type runtimeVersionEntry struct {
	value      RuntimeVersion
	expires    time.Time
	loading    bool
	generation uint64
}

// RuntimeVersionCache returns immediately and deduplicates background requests.
// Successful lookups live for an hour; failed lookups retry after a minute.
type RuntimeVersionCache struct {
	mu      sync.Mutex
	entries map[string]runtimeVersionEntry
	client  *http.Client
	now     func() time.Time
}

func NewRuntimeVersionCache(client *http.Client) *RuntimeVersionCache {
	return &RuntimeVersionCache{entries: make(map[string]runtimeVersionEntry), client: client, now: time.Now}
}

func (c *RuntimeVersionCache) Get(id, installer string) RuntimeVersion {
	endpoint, format := runtimeVersionSource(id, installer)
	if endpoint == "" {
		return RuntimeVersion{LatestVersionState: "unsupported"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[endpoint]
	if !entry.loading && !c.now().Before(entry.expires) {
		entry.loading = true
		entry.value.LatestVersionState = "checking"
		c.entries[endpoint] = entry
		go c.refresh(endpoint, format, entry.generation)
	}
	return entry.value
}

func (c *RuntimeVersionCache) refresh(endpoint, format string, generation uint64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	version, err := fetchRuntimeVersion(ctx, c.client, endpoint, format)
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	entry := c.entries[endpoint]
	if entry.generation != generation {
		return
	}
	entry.loading = false
	entry.value.LatestVersionCheckedAt = &now
	entry.value.LatestVersionState = "unavailable"
	entry.expires = now.Add(time.Minute)
	if err == nil {
		entry.value.LatestVersion = version
		entry.value.LatestVersionState = "ready"
		entry.expires = now.Add(time.Hour)
	}
	c.entries[endpoint] = entry
}

func runtimeVersionSource(id, installer string) (string, string) {
	if pkg, ok := strings.CutPrefix(installer, "npm:"); ok && pkg != "" {
		return "https://registry.npmjs.org/" + url.PathEscape(pkg) + "/latest", "npm"
	}
	switch id {
	case "omp":
		return "https://api.github.com/repos/can1357/oh-my-pi/releases/latest", "github"
	case "kimi":
		return "https://code.kimi.com/kimi-code/latest", "text"
	case "grok":
		return "https://x.ai/cli/stable", "text"
	default:
		return "", ""
	}
}

func fetchRuntimeVersion(ctx context.Context, client *http.Client, endpoint, format string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Multica-runtime-version-check")
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", errors.New("release lookup failed")
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", err
	}
	version := strings.TrimSpace(string(data))
	if format != "text" {
		var release struct {
			Version string `json:"version"`
			TagName string `json:"tag_name"`
		}
		if err := json.Unmarshal(data, &release); err != nil {
			return "", err
		}
		version = release.Version
		if format == "github" {
			version = release.TagName
		}
	}
	version = strings.TrimPrefix(version, "v")
	if !semver.IsValid("v" + version) {
		return "", errors.New("invalid release version")
	}
	return version, nil
}

var runtimeSemver = regexp.MustCompile(`(?:^|[\s(])v?([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)(?:$|[\s)])`)

// RuntimeUpdateAvailable returns nil if either version cannot be compared.
func RuntimeUpdateAvailable(installed, latest string) *bool {
	match := runtimeSemver.FindStringSubmatch(installed)
	if match == nil || !semver.IsValid("v"+match[1]) || !semver.IsValid("v"+latest) {
		return nil
	}
	newer := semver.Compare("v"+latest, "v"+match[1]) > 0
	return &newer
}

// Invalidate makes the next catalog read recheck public version metadata.
func (c *RuntimeVersionCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.entries {
		entry.expires = time.Time{}
		entry.generation++
		entry.loading = false
		c.entries[key] = entry
	}
}
