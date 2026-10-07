package lark

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestHTTPClient_GetUserName exercises the single-user contact lookup
// (CC Connect parity): GET /contact/v3/users/{open_id} with
// user_id_type=open_id, resolving data.user.name.
func TestHTTPClient_GetUserName(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tok", 7200)
	fake.mux.HandleFunc("/open-apis/contact/v3/users/ou_abc", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("want GET, got %s", r.Method)
		}
		if q := r.URL.Query(); q.Get("user_id_type") != "open_id" {
			t.Errorf("user_id_type = %q", q.Get("user_id_type"))
		}
		writeJSON(w, map[string]any{
			"code": 0, "msg": "ok",
			"data": map[string]any{
				"user": map[string]any{"open_id": "ou_abc", "name": "Alice"},
			},
		})
	})

	c := newTestClient(fake, time.Now)
	name, err := c.GetUserName(context.Background(), testCreds(), "ou_abc")
	if err != nil {
		t.Fatalf("GetUserName: %v", err)
	}
	if name != "Alice" {
		t.Errorf("name = %q, want Alice", name)
	}
}

// TestHTTPClient_GetUserNameMissingName: code=0 without a name (the
// response the API returns when the app lacks every name-field scope) is
// an error, mirroring CC Connect's "no data" failure handling — callers
// degrade instead of caching an empty name.
func TestHTTPClient_GetUserNameMissingName(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tok", 7200)
	fake.mux.HandleFunc("/open-apis/contact/v3/users/ou_abc", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"code": 0, "msg": "ok",
			"data": map[string]any{"user": map[string]any{"open_id": "ou_abc"}},
		})
	})

	c := newTestClient(fake, time.Now)
	if _, err := c.GetUserName(context.Background(), testCreds(), "ou_abc"); err == nil {
		t.Fatal("GetUserName(missing name) = nil error, want error")
	}
}

// TestHTTPClient_GetUserNamePermissionCode: a Lark permission business
// code surfaces as an error carrying that code (the enricher's hint
// machinery classifies it).
func TestHTTPClient_GetUserNamePermissionCode(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tok", 7200)
	fake.mux.HandleFunc("/open-apis/contact/v3/users/ou_abc", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"code": 99991672, "msg": "permission denied"})
	})

	c := newTestClient(fake, time.Now)
	_, err := c.GetUserName(context.Background(), testCreds(), "ou_abc")
	if err == nil {
		t.Fatal("GetUserName(99991672) = nil error, want error")
	}
	if code, _, ok := larkErrorCodeMsg(err); !ok || code != 99991672 {
		t.Errorf("err = %v, want business code 99991672", err)
	}
}

// TestHTTPClient_GetUserNamePathEscaping: the open_id is path-escaped so
// a malformed id cannot alter the request path.
func TestHTTPClient_GetUserNamePathEscaping(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tok", 7200)
	var gotEscaped string
	fake.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		gotEscaped = r.URL.EscapedPath()
		writeJSON(w, map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"user": map[string]any{"name": "X"}}})
	})

	c := newTestClient(fake, time.Now)
	_, _ = c.GetUserName(context.Background(), testCreds(), "ou/../../evil")
	// Slashes must be percent-escaped so the id stays ONE path segment
	// (fetchBotUnionID pins the same contract for the same endpoint).
	if !strings.Contains(gotEscaped, "users/ou%2F") {
		t.Errorf("request path = %q, want escaped single-segment lookup", gotEscaped)
	}
}

// TestHTTPClient_GetUserNameEmptyID returns an error and makes no HTTP
// call for an empty id.
func TestHTTPClient_GetUserNameEmptyID(t *testing.T) {
	fake := newLarkFake(t)
	// No token stub and no handler: any HTTP call would panic the fake.
	c := newTestClient(fake, time.Now)
	if _, err := c.GetUserName(context.Background(), testCreds(), ""); err == nil {
		t.Fatal("GetUserName(empty) = nil error, want error")
	}
}
