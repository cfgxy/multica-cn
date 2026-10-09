package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// RUYI-619: the probe must distinguish "key rejected" (invalid) from
// "could not verify" (unreachable). Pure unit tests — no database and no
// real provider: every probe target is an httptest stub and only fake key
// values are used. The key never appears in any assertion output; failures
// assert on the coarse status only.

// TestProbeVoiceCredential_Classification pins the RUYI-619 classification:
// 2xx ok; 401/403 invalid (the provider explicitly rejected the key);
// undecidable responses (4xx other than 401/403, 429, 5xx) record
// unreachable — they say nothing about the key.
func TestProbeVoiceCredential_Classification(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		wantStatus string
	}{
		{"2xx ok", http.StatusOK, "ok"},
		{"401 rejected", http.StatusUnauthorized, "invalid"},
		{"403 rejected", http.StatusForbidden, "invalid"},
		{"400 undecidable", http.StatusBadRequest, "unreachable"},
		{"404 undecidable", http.StatusNotFound, "unreachable"},
		{"429 undecidable", http.StatusTooManyRequests, "unreachable"},
		{"500 undecidable", http.StatusInternalServerError, "unreachable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			h := &Handler{VoiceProbeBaseURL: srv.URL}
			got := h.probeVoiceCredential(context.Background(), "fake-key-never-real")
			if got.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", got.Status, tc.wantStatus)
			}
			if got.HTTPStatus != tc.status {
				t.Errorf("http_status = %d, want %d", got.HTTPStatus, tc.status)
			}
			if got.CheckedAt == "" {
				t.Error("checked_at must always be set")
			}
		})
	}
}

// Transport-level failures (connection refused, timeout) are "could not
// verify" — the RUYI-603 mislabel turned a reachable-world outage into a
// "key invalid" badge, so the unreachable verdict must be its own state.
func TestProbeVoiceCredential_TransportFailureIsUnreachable(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	dead.Close() // nothing listens anymore: the transport will fail

	h := &Handler{VoiceProbeBaseURL: dead.URL}
	got := h.probeVoiceCredential(context.Background(), "fake-key-never-real")
	if got.Status != "unreachable" {
		t.Errorf("transport failure status = %q, want unreachable", got.Status)
	}
	if got.HTTPStatus != 0 {
		t.Errorf("transport failure must not carry an HTTP status, got %d", got.HTTPStatus)
	}
}

// A request that cannot even be built is a probe-side fault, not a verdict
// on the key — same unreachable semantics.
func TestProbeVoiceCredential_UnbuildableRequestIsUnreachable(t *testing.T) {
	h := &Handler{VoiceProbeBaseURL: "://bad base url"} // the space makes NewRequest fail
	got := h.probeVoiceCredential(context.Background(), "fake-key-never-real")
	if got.Status != "unreachable" {
		t.Errorf("unbuildable request status = %q, want unreachable", got.Status)
	}
}

// No probe target configured: skip, unchanged by RUYI-619.
func TestProbeVoiceCredential_SkipsWithoutBaseURL(t *testing.T) {
	h := &Handler{}
	got := h.probeVoiceCredential(context.Background(), "fake-key-never-real")
	if got.Status != "skipped" {
		t.Errorf("status = %q, want skipped", got.Status)
	}
}

// TestRuntimeCredentialStatus_Derivation pins the badge derivation table,
// including the pre-RUYI-619 metadata values (ok | invalid | skipped) that
// must keep rendering exactly as before, and the new unreachable state.
func TestRuntimeCredentialStatus_Derivation(t *testing.T) {
	ref := pgtype.Text{Valid: true, String: "cred-ref"}
	probeBag := func(status string) []byte {
		b, err := json.Marshal(map[string]any{
			"credential_probe": map[string]any{"status": status, "checked_at": "2026-10-10T00:00:00Z"},
		})
		if err != nil {
			t.Fatalf("marshal probe bag: %v", err)
		}
		return b
	}
	cases := []struct {
		name     string
		ref      pgtype.Text
		metadata []byte
		want     string
	}{
		{"no ref", pgtype.Text{}, nil, "not_configured"},
		{"empty ref", pgtype.Text{Valid: true}, nil, "not_configured"},
		{"ref without probe", ref, nil, "configured"},
		{"legacy ok", ref, probeBag("ok"), "configured"},
		{"legacy skipped", ref, probeBag("skipped"), "configured"},
		{"legacy invalid", ref, probeBag("invalid"), "invalid"},
		{"unreachable", ref, probeBag("unreachable"), "unreachable"},
		{"unknown probe value falls back to configured", ref, probeBag("something_new"), "configured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runtimeCredentialStatus(tc.ref, tc.metadata); got != tc.want {
				t.Errorf("runtimeCredentialStatus = %q, want %q", got, tc.want)
			}
		})
	}
	// Corrupt metadata never blocks the configured default.
	if got := runtimeCredentialStatus(ref, []byte("{not-json")); got != "configured" {
		t.Errorf("corrupt metadata → %q, want configured", got)
	}
}
