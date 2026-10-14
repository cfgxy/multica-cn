package main

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/realtime"
)

func TestRouterCORSContract(t *testing.T) {
	const origin = "https://cors-client.example"
	t.Setenv("CORS_ALLOWED_ORIGINS", origin)
	router := NewRouter(nil, realtime.NewHub(), events.New(), analytics.NoopClient{}, nil)

	t.Run("preflight accepts browser request headers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/config", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", http.MethodPost)
		req.Header.Set("Access-Control-Request-Headers", "X-Client-Capabilities, Idempotency-Key")
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("preflight status = %d, want %d", rec.Code, http.StatusOK)
		}
		for _, want := range []string{"X-Client-Capabilities", "Idempotency-Key"} {
			if !headerListContains(rec.Header().Get("Access-Control-Allow-Headers"), want) {
				t.Errorf("Access-Control-Allow-Headers = %q, missing %q", rec.Header().Get("Access-Control-Allow-Headers"), want)
			}
		}
	})

	t.Run("browser can read truncation signals", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		for _, want := range []string{
			handler.HeaderCommentsTruncated,
			handler.HeaderTimelineTruncated,
			handler.HeaderActiveRunsTruncated,
		} {
			if !headerListContains(rec.Header().Get("Access-Control-Expose-Headers"), want) {
				t.Errorf("Access-Control-Expose-Headers = %q, missing %q", rec.Header().Get("Access-Control-Expose-Headers"), want)
			}
		}
	})
}

func headerListContains(header, want string) bool {
	for _, value := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(value), want) {
			return true
		}
	}
	return false
}

// Computer provisioning runs long remote operations (credential writes, runtime
// installs, model-config saves) that clients request with the standard
// Prefer: respond-async header. Browsers preflight that header on every such
// call, so a missing AllowedHeaders entry turns each async operation into a
// failed CORS preflight (RUYI-670).
func TestCORSAllowedHeaders_IncludePrefer(t *testing.T) {
	if !slices.Contains(corsAllowedHeaders, "Prefer") {
		t.Fatalf("Prefer missing from CORS allowed headers: %v", corsAllowedHeaders)
	}
}

// Workspace subscription checkout and portal requests use Idempotency-Key to
// make retries safe. Browsers preflight that custom request header, so omitting
// it from AllowedHeaders prevents the billing request from reaching the server.
func TestCORSAllowedHeaders_IncludeIdempotencyKey(t *testing.T) {
	if !slices.Contains(corsAllowedHeaders, "Idempotency-Key") {
		t.Fatalf("Idempotency-Key missing from CORS allowed headers: %v", corsAllowedHeaders)
	}
}

// Timeline, comment-list and active-run reads report defensive hard-cap clamps
// with custom response headers.
// Custom response headers are not readable from browser JS unless the server
// exposes them, and only the CORS-safelisted headers are exposed by default — so
// an entry missing here is not a degraded signal, it is no signal at all: the
// header arrives on the wire and the client cannot see it (MUL-5492).
func TestCORSExposedHeaders_IncludeTruncationSignals(t *testing.T) {
	for _, want := range []string{
		handler.HeaderCommentsTruncated,
		handler.HeaderTimelineTruncated,
		handler.HeaderActiveRunsTruncated,
	} {
		if !slices.Contains(corsExposedHeaders, want) {
			t.Errorf("%s missing from CORS exposed headers: %v", want, corsExposedHeaders)
		}
	}
}
