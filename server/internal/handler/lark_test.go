package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Lark-handler unit tests focus on the no-config short-circuits —
// verifying that a self-host deployment without MULTICA_LARK_SECRET_KEY
// does NOT serve create / revoke / redeem, and that list degrades
// gracefully to an empty response so the Integrations tab still
// renders. Happy-path flows (create + list + revoke; token mint +
// redeem) need a real DB and land alongside the WS hub integration
// tests in a follow-up commit.

func TestCreateLarkInstallation_NotConfigured(t *testing.T) {
	h := &Handler{} // LarkInstallations intentionally nil
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/x/lark/installations", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	h.CreateLarkInstallation(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when lark not configured, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRevokeLarkInstallation_NotConfigured(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/x/lark/installations/y", nil)
	w := httptest.NewRecorder()
	h.RevokeLarkInstallation(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestRedeemLarkBindingToken_NotConfigured(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/api/lark/binding/redeem", strings.NewReader(`{"token":"x"}`))
	w := httptest.NewRecorder()
	h.RedeemLarkBindingToken(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestStartLarkInstall_NotConfiguredReturnsServiceUnavailable(t *testing.T) {
	// When the at-rest key is unset the LarkInstallations service is
	// nil and the install-start handler must short-circuit to 503 with
	// a clear message — degrading to "configured: false" silently would
	// hide a real misconfiguration from the operator.
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/x/lark/install/start?agent_id=y", nil)
	w := httptest.NewRecorder()
	h.StartLarkInstall(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when LarkInstallations is nil, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestLarkInstallCallback_NotConfiguredRedirects(t *testing.T) {
	// The callback always finishes with a redirect to the frontend
	// settings page (success or error) so we never have to render an
	// HTML error page server-side. With LarkInstallations / LarkOAuth
	// nil the redirect's query string carries lark_error=not_configured
	// so the frontend can show the right copy without polling.
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/api/lark/install/callback?code=abc&state=xyz", nil)
	w := httptest.NewRecorder()
	h.LarkInstallCallback(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect, got %d body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if loc == "" || !strings.Contains(loc, "lark_error=not_configured") {
		t.Fatalf("redirect missing lark_error=not_configured marker; loc=%q", loc)
	}
	if !strings.Contains(loc, "/settings?tab=lark") {
		t.Fatalf("redirect must land on lark settings tab; loc=%q", loc)
	}
}

func TestListLarkInstallations_NotConfiguredReturnsEmpty(t *testing.T) {
	// Listing is intentionally a "soft" endpoint: when lark is not
	// configured we return an empty list + configured:false rather
	// than a 503, so the Integrations tab renders normally with a
	// "not connected" empty state instead of an error banner.
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/x/lark/installations", nil)
	w := httptest.NewRecorder()
	h.ListLarkInstallations(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Installations []any `json:"installations"`
		Configured    bool  `json:"configured"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Configured {
		t.Fatalf("configured should be false when LarkInstallations is nil")
	}
	if len(resp.Installations) != 0 {
		t.Fatalf("expected empty installations list, got %d", len(resp.Installations))
	}
}
