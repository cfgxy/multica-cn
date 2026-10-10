package computer

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type versionTransport func(*http.Request) (*http.Response, error)

func (f versionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func versionResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestRuntimeUpdateAvailable(t *testing.T) {
	for _, tc := range []struct{ installed, latest, want string }{
		{"codex-cli 0.100.0", "0.100.0", "false"},
		{"2.1.9 (Claude Code)", "2.1.10", "true"},
		{"v2.2.0", "2.1.10", "false"},
		{"1.2.3-beta.1", "1.2.3", "true"},
		{"1.2.3+build.1", "1.2.3", "false"},
		{"development", "1.2.3", "unknown"},
		{"", "1.2.3", "unknown"},
		{"1.2.3", "invalid", "unknown"},
		{"1.2.30broken", "1.2.31", "unknown"},
	} {
		t.Run(tc.installed+"/"+tc.latest, func(t *testing.T) {
			got := RuntimeUpdateAvailable(tc.installed, tc.latest)
			value := "unknown"
			if got != nil {
				value = "false"
				if *got {
					value = "true"
				}
			}
			if value != tc.want {
				t.Fatalf("got %s, want %s", value, tc.want)
			}
		})
	}
}

func TestFetchRuntimeVersion(t *testing.T) {
	for _, tc := range []struct {
		format, body, want string
		status             int
	}{
		{"npm", `{"version":"1.2.3"}`, "1.2.3", 200},
		{"github", `{"tag_name":"v1.2.3"}`, "1.2.3", 200},
		{"text", "1.2.3\n", "1.2.3", 200},
		{"npm", `{"version":null}`, "", 200},
		{"npm", `{"version":123}`, "", 200},
		{"npm", `{"version":"latest"}`, "", 200},
		{"github", `<html>unavailable</html>`, "", 200},
		{"npm", `{"version":"1.2.3"}`, "", 429},
	} {
		t.Run(tc.format+tc.body, func(t *testing.T) {
			client := &http.Client{Transport: versionTransport(func(r *http.Request) (*http.Response, error) {
				if _, ok := r.Context().Deadline(); !ok {
					t.Error("request has no deadline")
				}
				return versionResponse(tc.status, tc.body), nil
			})}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			got, err := fetchRuntimeVersion(ctx, client, "https://releases.invalid/latest", tc.format)
			if got != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func awaitVersion(t *testing.T, cache *RuntimeVersionCache, want string) RuntimeVersion {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got := cache.Get("codex", "npm:@openai/codex")
		if got.LatestVersionState == want {
			return got
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("version lookup did not reach %s", want)
	return RuntimeVersion{}
}

func TestRuntimeVersionCacheDeduplicatesExpiresAndRetries(t *testing.T) {
	var calls atomic.Int32
	var failure atomic.Bool
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	release := make(chan struct{})
	client := &http.Client{Transport: versionTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.URL.EscapedPath() != "/@openai%2Fcodex/latest" {
			t.Errorf("wrong npm URL: %s", r.URL)
		}
		<-release
		if failure.Load() {
			return versionResponse(503, "unavailable"), nil
		}
		return versionResponse(200, `{"version":"1.2.3"}`), nil
	})}
	cache := NewRuntimeVersionCache(client)
	cache.now = func() time.Time { return time.Unix(0, clock.Load()) }
	for range 20 {
		if got := cache.Get("codex", "npm:@openai/codex"); got.LatestVersionState != "checking" {
			t.Fatalf("expected nonblocking check: %+v", got)
		}
	}
	close(release)
	got := awaitVersion(t, cache, "ready")
	if got.LatestVersion != "1.2.3" || got.LatestVersionCheckedAt == nil || calls.Load() != 1 {
		t.Fatalf("bad cached result: %+v (%d calls)", got, calls.Load())
	}
	clock.Add(int64(59 * time.Minute))
	if got := cache.Get("codex", "npm:@openai/codex"); got.LatestVersionState != "ready" || calls.Load() != 1 {
		t.Fatal("cache expired early")
	}
	failure.Store(true)
	clock.Add(int64(time.Minute))
	if got := cache.Get("codex", "npm:@openai/codex"); got.LatestVersionState != "checking" || got.LatestVersion != "1.2.3" {
		t.Fatalf("did not retain version during refresh: %+v", got)
	}
	awaitVersion(t, cache, "unavailable")
	for range 20 {
		cache.Get("codex", "npm:@openai/codex")
	}
	if calls.Load() != 2 {
		t.Fatal("failed lookups were not cached")
	}
	failure.Store(false)
	clock.Add(int64(time.Minute))
	awaitVersion(t, cache, "ready")
	if calls.Load() != 3 {
		t.Fatal("failed lookup did not retry")
	}
	if got := cache.Get("unknown", ""); got.LatestVersionState != "unsupported" {
		t.Fatal("unknown runtime must not claim a version")
	}
}

func TestRuntimeGrokVersionUsesPublicStableChannel(t *testing.T) {
	endpoint, format := runtimeVersionSource("grok", "https://x.ai/cli/install.sh")
	if endpoint != "https://x.ai/cli/stable" || format != "text" {
		t.Fatalf("wrong Grok channel endpoint/format: %s %s", endpoint, format)
	}
	client := &http.Client{Transport: versionTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != endpoint {
			t.Fatal("unexpected version source")
		}
		return versionResponse(200, "1.0.41"), nil
	})}
	version, err := fetchRuntimeVersion(context.Background(), client, endpoint, format)
	if err != nil || version != "1.0.41" {
		t.Fatalf("invalid stable version: %s %v", version, err)
	}
}
