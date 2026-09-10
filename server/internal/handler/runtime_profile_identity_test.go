package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// TestCreateRuntimeProfile_AcceptsIndependentRuntimeFamilies pins the API half
// of the identity change: deerflow and zcode are their own protocol families,
// so a profile no longer has to be declared as `kimi` to reach either bridge.
// The reject case is the same request one family name away — an unknown family
// must not be storable, whatever else about the request is valid.
func TestCreateRuntimeProfile_AcceptsIndependentRuntimeFamilies(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	cases := []struct {
		family     string
		command    string
		wantStatus int
	}{
		{family: "deerflow", command: "deerflow-acp", wantStatus: http.StatusCreated},
		{family: "zcode", command: "zcode-acp", wantStatus: http.StatusCreated},
		// Still accepted: the family zcode used to be shelled onto. Existing
		// profiles must keep working.
		{family: "kimi", command: "kimi", wantStatus: http.StatusCreated},
		{family: "deerflow-acp", command: "deerflow-acp", wantStatus: http.StatusBadRequest},
		{family: "zcode-acp", command: "zcode-acp", wantStatus: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.family, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := newRequest("POST", "/api/workspaces/"+testWorkspaceID+"/runtime-profiles", map[string]any{
				"display_name":    "Identity " + tc.family,
				"protocol_family": tc.family,
				"command_name":    tc.command,
			})
			req = withURLParam(req, "id", testWorkspaceID)
			testHandler.CreateRuntimeProfile(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("protocol_family %q: status = %d, want %d: %s", tc.family, w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantStatus == http.StatusBadRequest {
				if !strings.Contains(w.Body.String(), "unsupported protocol_family") {
					t.Fatalf("expected an unsupported-family error naming the whitelist, got %s", w.Body.String())
				}
				return
			}

			var resp RuntimeProfileResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			t.Cleanup(func() {
				testPool.Exec(context.Background(), `DELETE FROM runtime_profile WHERE id = $1`, resp.ID)
			})
			if resp.ProtocolFamily != tc.family {
				t.Fatalf("response protocol_family = %q, want %q", resp.ProtocolFamily, tc.family)
			}
			// The stored row is what the daemon reads back, so the family has
			// to survive the round trip verbatim rather than being normalised
			// to whatever the bridge used to be filed under.
			var stored string
			if err := testPool.QueryRow(ctx, `SELECT protocol_family FROM runtime_profile WHERE id = $1`, resp.ID).Scan(&stored); err != nil {
				t.Fatalf("read stored protocol_family: %v", err)
			}
			if stored != tc.family {
				t.Fatalf("stored protocol_family = %q, want %q", stored, tc.family)
			}
		})
	}
}

// TestRuntimeProfileCheckConstraintMatchesWhitelist is the database layer of
// the same guard. The API check can be bypassed — a direct INSERT, a future
// code path, a migration backfill — so protocol_family carries its own CHECK,
// and the two must agree. A drift in either direction is a defect: a family
// the API accepts but the CHECK rejects 500s on create, and one the CHECK
// accepts but no backend implements registers a runtime that cannot launch.
func TestRuntimeProfileCheckConstraintMatchesWhitelist(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// Probe the constraint by attempting a row per family inside a savepoint
	// rather than parsing pg_get_constraintdef: PostgreSQL normalises an
	// `IN (...)` CHECK to `= ANY (ARRAY[...])`, so a text match on the
	// migration's own wording would report a drift that does not exist. What
	// matters here is what the constraint accepts, not how it prints.
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin probe transaction: %v", err)
	}
	// Everything below is rolled back; the probe never leaves a row behind.
	defer tx.Rollback(context.Background())

	probe := func(family string) error {
		if _, err := tx.Exec(ctx, `SAVEPOINT family_probe`); err != nil {
			t.Fatalf("savepoint: %v", err)
		}
		_, insertErr := tx.Exec(ctx, `
			INSERT INTO runtime_profile (workspace_id, display_name, protocol_family, command_name, created_by)
			VALUES ($1, 'Protocol Family Probe', $2, 'probe', $3)
		`, testWorkspaceID, family, testUserID)
		if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT family_probe`); err != nil {
			t.Fatalf("rollback to savepoint: %v", err)
		}
		return insertErr
	}

	for _, family := range agent.SupportedTypes {
		if err := probe(family); err != nil {
			t.Errorf("protocol_family %q is in agent.SupportedTypes but the runtime_profile CHECK rejects it; migration 907 and the whitelist have drifted: %v", family, err)
		}
	}

	// The CHECK must also actually bite. Insert bypasses the handler entirely,
	// which is the point.
	err = probe("deerflow-acp")
	if err == nil {
		t.Fatal("a protocol_family outside the whitelist was accepted; the CHECK constraint is not enforcing")
	}
	if !strings.Contains(err.Error(), "runtime_profile_protocol_family_check") {
		t.Fatalf("expected the protocol_family CHECK to reject the insert, got: %v", err)
	}
}

// TestUpdateRuntimeProfile_ProtocolFamilyIsImmutable covers the other write
// path. Update deliberately exposes no protocol_family field, so an existing
// profile cannot be edited into a family with no backend — and, just as
// importantly, a compatibility profile filed under `kimi` is never silently
// rewritten to `zcode` by an unrelated edit. Moving a profile to its real
// runtime identity stays an explicit create.
func TestUpdateRuntimeProfile_ProtocolFamilyIsImmutable(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	profileID := insertRuntimeProfileFixture(t, ctx, "Identity Update Profile", "kimi", "zcode-acp")

	w := httptest.NewRecorder()
	req := newRequest("PATCH", "/api/workspaces/"+testWorkspaceID+"/runtime-profiles/"+profileID, map[string]any{
		"display_name":    "Identity Update Profile Renamed",
		"protocol_family": "zcode",
	})
	req = withURLParams(req, "id", testWorkspaceID, "profileId", profileID)
	testHandler.UpdateRuntimeProfile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var stored, storedName string
	if err := testPool.QueryRow(ctx,
		`SELECT protocol_family, display_name FROM runtime_profile WHERE id = $1`,
		profileID).Scan(&stored, &storedName); err != nil {
		t.Fatalf("read stored profile: %v", err)
	}
	if stored != "kimi" {
		t.Fatalf("stored protocol_family = %q, want the shelled-on kimi family left untouched by an edit", stored)
	}
	if storedName != "Identity Update Profile Renamed" {
		t.Fatalf("stored display_name = %q, want the requested rename to have applied", storedName)
	}
}
