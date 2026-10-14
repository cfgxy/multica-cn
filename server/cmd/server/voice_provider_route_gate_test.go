package main

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// TestVoiceProviderRouteGate locks in the route-level gates on the voice
// provider pool (RUYI-693). The listing is member-visible — the settings
// page renders the pool for every workspace member — so the gate lives on
// the route registration and this test exercises the real router: a valid
// JWT from a non-member must be rejected before the handler (404, exactly
// like any workspace the user cannot see), members and the owner keep
// read access, and the write edges of the permission matrix (member write
// 403 behind the owner/admin gate, unauthenticated 401) must not regress.
func TestVoiceProviderRouteGate(t *testing.T) {
	if testPool == nil {
		t.Skip("no database connection")
	}

	// A plain member of the fixture workspace.
	var memberUserID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO "user" (name, email) VALUES ('Voice gate member', $1) RETURNING id
`, "voice-gate-member-"+time.Now().Format("150405.000000000")+"@example.test").Scan(&memberUserID); err != nil {
		t.Fatalf("create member user: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')
`, testWorkspaceID, memberUserID); err != nil {
		t.Fatalf("create workspace member: %v", err)
	}
	memberToken, err := generateTestJWT(memberUserID, "voice-gate-member@example.test", "Voice gate member")
	if err != nil {
		t.Fatalf("generate member token: %v", err)
	}

	// An authenticated user holding no membership in any workspace.
	var outsiderUserID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO "user" (name, email) VALUES ('Voice gate outsider', $1) RETURNING id
`, "voice-gate-outsider-"+time.Now().Format("150405.000000000")+"@example.test").Scan(&outsiderUserID); err != nil {
		t.Fatalf("create outsider user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`, testWorkspaceID, memberUserID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, memberUserID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, outsiderUserID)
	})
	outsiderToken, err := generateTestJWT(outsiderUserID, "voice-gate-outsider@example.test", "Voice gate outsider")
	if err != nil {
		t.Fatalf("generate outsider token: %v", err)
	}

	listPath := "/api/workspaces/" + testWorkspaceID + "/voice-providers"
	tests := []struct {
		name       string
		method     string
		token      string
		wantStatus int
	}{
		{name: "unauthenticated list is rejected", method: http.MethodGet, token: "", wantStatus: http.StatusUnauthorized},
		{name: "non-member list is rejected by the route gate", method: http.MethodGet, token: outsiderToken, wantStatus: http.StatusNotFound},
		{name: "plain member keeps read access", method: http.MethodGet, token: memberToken, wantStatus: http.StatusOK},
		{name: "owner keeps read access", method: http.MethodGet, token: testToken, wantStatus: http.StatusOK},
		{name: "member write stays behind the admin gate", method: http.MethodPost, token: memberToken, wantStatus: http.StatusForbidden},
		{name: "non-member write is rejected by the role gate", method: http.MethodPost, token: outsiderToken, wantStatus: http.StatusNotFound},
		{name: "unauthenticated write is rejected", method: http.MethodPost, token: "", wantStatus: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, testServer.URL+listPath, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
		})
	}
}
