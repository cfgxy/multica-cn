package handler

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/jevconfig"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

func withTestJevConfig(t *testing.T) {
	t.Helper()
	key := make([]byte, secretbox.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	box, err := secretbox.New(key)
	if err != nil {
		t.Fatalf("secretbox.New: %v", err)
	}
	previous := testHandler.JevConfig
	testHandler.JevConfig = jevconfig.NewResolver(testHandler.Queries, box)
	t.Cleanup(func() { testHandler.JevConfig = previous })
}

func memberRoute(h http.HandlerFunc) http.Handler {
	return middleware.RequireWorkspaceMember(testHandler.Queries)(h)
}

func TestSelfEvolutionJevConfigPermissionsAndSafeReadback(t *testing.T) {
	withTestJevConfig(t)
	wsID, ownerID, memberID := legislationFixture(t)

	get := memberRoute(http.HandlerFunc(testHandler.GetSelfEvolutionJevConfig))
	getReq := legislationReq(memberID, wsID, http.MethodGet, "/api/self-evolution/jev-config", nil)
	getRec := httptest.NewRecorder()
	get.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("member GET status = %d: %s", getRec.Code, getRec.Body.String())
	}
	if !strings.Contains(getRec.Body.String(), `"status":"unconfigured"`) {
		t.Fatalf("member GET = %s", getRec.Body.String())
	}

	put := ownerRoute(http.HandlerFunc(testHandler.PutSelfEvolutionJevConfig))
	memberReq := legislationReq(memberID, wsID, http.MethodPut, "/api/self-evolution/jev-config", map[string]any{
		"endpoint": "http://127.0.0.1:1", "api_key": "member-key", "model": "jev-local", "timeout_ms": 100,
	})
	memberRec := httptest.NewRecorder()
	put.ServeHTTP(memberRec, memberReq)
	if memberRec.Code != http.StatusForbidden {
		t.Fatalf("member PUT status = %d, want 403: %s", memberRec.Code, memberRec.Body.String())
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"engine": "handler-test", "p_failure": 0.1, "threshold": 0.5, "warn": false, "decision": "pass",
		})
	}))
	t.Cleanup(server.Close)
	ownerReq := legislationReq(ownerID, wsID, http.MethodPut, "/api/self-evolution/jev-config", map[string]any{
		"endpoint": server.URL, "api_key": "owner-key", "model": "jev-local", "timeout_ms": 1500,
	})
	ownerRec := httptest.NewRecorder()
	put.ServeHTTP(ownerRec, ownerReq)
	if ownerRec.Code != http.StatusOK {
		t.Fatalf("owner PUT status = %d: %s", ownerRec.Code, ownerRec.Body.String())
	}
	if strings.Contains(ownerRec.Body.String(), "owner-key") || strings.Contains(ownerRec.Body.String(), `"api_key":`) {
		t.Fatalf("owner PUT leaked credential material: %s", ownerRec.Body.String())
	}
	if !strings.Contains(ownerRec.Body.String(), `"has_api_key":true`) || !strings.Contains(ownerRec.Body.String(), `"status":"available"`) {
		t.Fatalf("owner PUT response = %s", ownerRec.Body.String())
	}
}

func TestSelfEvolutionJevConfigWriteFailsWithoutEncryption(t *testing.T) {
	previous := testHandler.JevConfig
	testHandler.JevConfig = nil
	t.Cleanup(func() { testHandler.JevConfig = previous })
	wsID, ownerID, _ := legislationFixture(t)
	req := legislationReq(ownerID, wsID, http.MethodPut, "/api/self-evolution/jev-config", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	ownerRoute(http.HandlerFunc(testHandler.PutSelfEvolutionJevConfig)).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("PUT without encryption status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
}
