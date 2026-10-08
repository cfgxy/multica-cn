package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

// credentialTestBox installs a throwaway AES-256 key on the shared test
// handler and restores the previous box at cleanup.
func credentialTestBox(t *testing.T) *secretbox.Box {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	box, err := secretbox.New(key)
	if err != nil {
		t.Fatalf("build test box: %v", err)
	}
	previous := testHandler.RuntimeCredentialBox
	testHandler.RuntimeCredentialBox = box
	t.Cleanup(func() {
		testHandler.RuntimeCredentialBox = previous
	})
	return box
}

func putRuntimeCredential(t *testing.T, runtimeID, key, value string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPut, "/api/runtimes/"+runtimeID+"/credentials/"+key, map[string]any{"value": value})
	req = withURLParams(req, "runtimeId", runtimeID, "credentialKey", key)
	testHandler.PutRuntimeCredential(w, req)
	return w
}

func deleteRuntimeCredential(t *testing.T, runtimeID, key string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest(http.MethodDelete, "/api/runtimes/"+runtimeID+"/credentials/"+key, nil)
	req = withURLParams(req, "runtimeId", runtimeID, "credentialKey", key)
	testHandler.DeleteRuntimeCredential(w, req)
	return w
}

// TestRuntimeCredentialRoundTrip is the §4.5 end-to-end guard: a PUT stores
// ONLY ciphertext in the secret store and a pointer on the instance row, the
// instance list exposes the badge but never the value, rotation replaces the
// ciphertext in place, and DELETE removes both and returns the badge to
// not_configured — twice, because the endpoint is idempotent.
func TestRuntimeCredentialRoundTrip(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	box := credentialTestBox(t)

	_, instanceID := insertVoiceFamilyFixture(t, ctx, "Credential Round Trip")
	const secretValue = "fake-api-key-for-round-trip-test-NOT-A-REAL-KEY"

	// First PUT configures.
	if w := putRuntimeCredential(t, instanceID, "gemini_api_key", secretValue); w.Code != http.StatusOK {
		t.Fatalf("first PUT: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// The store row holds sealed bytes, not the plaintext, and the box can
	// read them back.
	var sealed []byte
	var credRef *string
	if err := testPool.QueryRow(ctx, `
		SELECT rc.secret_encrypted, ar.credential_ref
		FROM runtime_credential rc
		JOIN agent_runtime ar ON ar.id = rc.runtime_instance_id
		WHERE rc.runtime_instance_id = $1 AND rc.credential_key = 'gemini_api_key'
	`, instanceID).Scan(&sealed, &credRef); err != nil {
		t.Fatalf("read stored credential: %v", err)
	}
	if bytes.Contains(sealed, []byte(secretValue)) {
		t.Fatal("plaintext value found in runtime_credential.secret_encrypted")
	}
	opened, err := box.Open(sealed)
	if err != nil {
		t.Fatalf("open stored credential: %v", err)
	}
	if string(opened) != secretValue {
		t.Errorf("decrypted value = %q, want %q", opened, secretValue)
	}
	wantRef := instanceID + ":gemini_api_key"
	if credRef == nil || *credRef != wantRef {
		t.Errorf("credential_ref = %v, want %q", credRef, wantRef)
	}

	// The instance list carries the badge and never the value.
	listW := testutil.Call(t, testHandler.ListAgentRuntimes,
		newRequest(http.MethodGet, "/api/runtimes", nil),
	).Want(http.StatusOK)
	if strings.Contains(listW.Body.String(), secretValue) {
		t.Fatal("plaintext value leaked into the runtime list response")
	}
	var runtimes []AgentRuntimeResponse
	if err := json.NewDecoder(listW.Body).Decode(&runtimes); err != nil {
		t.Fatalf("decode runtime list: %v", err)
	}
	badgeFound := false
	for _, rt := range runtimes {
		if rt.ID != instanceID {
			continue
		}
		badgeFound = true
		if rt.CredentialStatus != "configured" {
			t.Errorf("credential_status = %q, want configured", rt.CredentialStatus)
		}
		if rt.RegistrationSource != "daemon_discovered" {
			t.Errorf("registration_source = %q, want daemon_discovered (fixture instance was daemon-registered shape)", rt.RegistrationSource)
		}
	}
	if !badgeFound {
		t.Fatal("instance missing from the runtime list")
	}

	// Rotation replaces the ciphertext; the ref is stable (§4.5).
	const rotatedValue = "fake-api-key-rotated-STILL-NOT-REAL"
	if w := putRuntimeCredential(t, instanceID, "gemini_api_key", rotatedValue); w.Code != http.StatusOK {
		t.Fatalf("rotation PUT: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var sealedAfter []byte
	if err := testPool.QueryRow(ctx, `
		SELECT secret_encrypted FROM runtime_credential
		WHERE runtime_instance_id = $1 AND credential_key = 'gemini_api_key'
	`, instanceID).Scan(&sealedAfter); err != nil {
		t.Fatalf("read rotated credential: %v", err)
	}
	if bytes.Equal(sealed, sealedAfter) {
		t.Error("rotation stored identical ciphertext — AES-GCM is randomized, the seal must differ")
	}
	openedAfter, err := box.Open(sealedAfter)
	if err != nil {
		t.Fatalf("open rotated credential: %v", err)
	}
	if string(openedAfter) != rotatedValue {
		t.Errorf("decrypted rotated value = %q, want %q", openedAfter, rotatedValue)
	}

	// First delete removes both the row and the pointer.
	if w := deleteRuntimeCredential(t, instanceID, "gemini_api_key"); w.Code != http.StatusNoContent {
		t.Fatalf("first DELETE: expected 204, got %d: %s", w.Code, w.Body.String())
	}
	var rows int
	var refAfter *string
	if err := testPool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM runtime_credential WHERE runtime_instance_id = $1 AND credential_key = 'gemini_api_key'),
			(SELECT credential_ref::text FROM agent_runtime WHERE id = $1)
	`, instanceID).Scan(&rows, &refAfter); err != nil {
		t.Fatalf("read state after delete: %v", err)
	}
	if rows != 0 || refAfter != nil {
		t.Fatalf("after DELETE: credential rows = %d, ref = %v, want both gone", rows, refAfter)
	}

	// Second delete is idempotent.
	if w := deleteRuntimeCredential(t, instanceID, "gemini_api_key"); w.Code != http.StatusNoContent {
		t.Fatalf("idempotent DELETE: expected 204, got %d: %s", w.Code, w.Body.String())
	}

	// The badge is back to not_configured.
	listW = testutil.Call(t, testHandler.ListAgentRuntimes,
		newRequest(http.MethodGet, "/api/runtimes", nil),
	).Want(http.StatusOK)
	if err := json.NewDecoder(listW.Body).Decode(&runtimes); err != nil {
		t.Fatalf("decode runtime list after delete: %v", err)
	}
	for _, rt := range runtimes {
		if rt.ID == instanceID && rt.CredentialStatus != "not_configured" {
			t.Errorf("credential_status after delete = %q, want not_configured", rt.CredentialStatus)
		}
	}
}

// TestPutRuntimeCredential_InputGuards: malformed keys and empty values are
// refused before anything touches the store.
func TestPutRuntimeCredential_InputGuards(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	credentialTestBox(t)
	ctx := context.Background()
	_, instanceID := insertVoiceFamilyFixture(t, ctx, "Credential Guards")

	cases := []struct {
		name    string
		key     string
		value   string
		wantMsg string
	}{
		{"uppercase key refused", "GEMINI_API_KEY", "fake-value", "credential key must match"},
		{"path-ish key refused", "../secrets", "fake-value", "credential key must match"},
		{"empty value refused", "gemini_api_key", "   ", "value is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := putRuntimeCredential(t, instanceID, tc.key, tc.value)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.wantMsg) {
				t.Errorf("error should say %q: %s", tc.wantMsg, w.Body.String())
			}
		})
	}

	var stored int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM runtime_credential WHERE runtime_instance_id = $1`, instanceID,
	).Scan(&stored); err != nil {
		t.Fatalf("count stored credentials: %v", err)
	}
	if stored != 0 {
		t.Errorf("refused requests stored %d credential rows", stored)
	}
}

// TestPutRuntimeCredential_FailsClosedWithoutBox: a server without
// MULTICA_RUNTIME_CREDENTIAL_SECRET_KEY must refuse to store credentials
// rather than fall back to plaintext.
func TestPutRuntimeCredential_FailsClosedWithoutBox(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	previous := testHandler.RuntimeCredentialBox
	testHandler.RuntimeCredentialBox = nil
	t.Cleanup(func() {
		testHandler.RuntimeCredentialBox = previous
	})
	ctx := context.Background()
	_, instanceID := insertVoiceFamilyFixture(t, ctx, "Credential No Box")

	w := putRuntimeCredential(t, instanceID, "gemini_api_key", "fake-value")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
	// RUYI-540: the fail-closed body must name the missing variable so the
	// client alert (and the operator reading it) can act on it directly.
	if body := w.Body.String(); !strings.Contains(body, "MULTICA_RUNTIME_CREDENTIAL_SECRET_KEY") {
		t.Errorf("503 body must name MULTICA_RUNTIME_CREDENTIAL_SECRET_KEY, got: %s", body)
	}

	var stored int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM runtime_credential WHERE runtime_instance_id = $1`, instanceID,
	).Scan(&stored); err != nil {
		t.Fatalf("count stored credentials: %v", err)
	}
	if stored != 0 {
		t.Errorf("a boxed-off server stored %d credential rows", stored)
	}
}
