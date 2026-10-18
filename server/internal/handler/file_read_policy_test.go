package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/fileaccess"
)

func TestAdminFileReadPolicyVersionedUpdateAndRestore(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	var originalRules []byte
	var originalVersion int64
	if err := testPool.QueryRow(t.Context(), `SELECT rules, version FROM mcp_file_read_policy WHERE singleton = TRUE`).Scan(&originalRules, &originalVersion); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(t.Context(), `UPDATE mcp_file_read_policy SET rules = $1, version = $2 WHERE singleton = TRUE`, originalRules, originalVersion)
	})

	input := fileReadPolicyRequest{Version: originalVersion, Rules: []fileaccess.DenyRule{{
		ID: "custom-private", Pattern: "**/private-config.json", Description: "Private configuration", Enabled: true,
	}}}
	request := newRequestAs(testUserID, http.MethodPut, "/api/admin/file-read-policy", input)
	var updated struct {
		Version int64                 `json:"version"`
		Rules   []fileaccess.DenyRule `json:"rules"`
	}
	testutil.Call(t, testHandler.AdminUpdateFileReadPolicy, request).Want(http.StatusOK).JSON(&updated)
	if updated.Version != originalVersion+1 || len(updated.Rules) != 1 || updated.Rules[0].ID != "custom-private" {
		t.Fatalf("updated policy = %+v", updated)
	}

	stale := newRequestAs(testUserID, http.MethodPut, "/api/admin/file-read-policy", input)
	testutil.Call(t, testHandler.AdminUpdateFileReadPolicy, stale).Want(http.StatusConflict)

	invalid := fileReadPolicyRequest{Version: updated.Version, Rules: []fileaccess.DenyRule{{ID: "bad", Pattern: "**/[", Enabled: true}}}
	testutil.Call(t, testHandler.AdminUpdateFileReadPolicy, newRequestAs(testUserID, http.MethodPut, "/api/admin/file-read-policy", invalid)).Want(http.StatusBadRequest)

	restore := newRequestAs(testUserID, http.MethodPost, "/api/admin/file-read-policy/restore-defaults", map[string]any{"version": updated.Version})
	var restored struct {
		Version int64                 `json:"version"`
		Rules   []fileaccess.DenyRule `json:"rules"`
	}
	testutil.Call(t, testHandler.AdminRestoreFileReadPolicy, restore).Want(http.StatusOK).JSON(&restored)
	if restored.Version != updated.Version+1 || len(restored.Rules) != len(fileaccess.DefaultDenyPolicy().Rules) {
		t.Fatalf("restored policy = version %d with %d rules", restored.Version, len(restored.Rules))
	}
	var auditCount int
	if err := testPool.QueryRow(t.Context(), `SELECT count(*) FROM admin_audit_log WHERE actor_id = $1 AND action = 'file_read_policy_updated'`, testUserID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount < 2 {
		t.Fatalf("audit rows = %d, want at least two configuration changes", auditCount)
	}
	var persistedRaw []byte
	if err := testPool.QueryRow(t.Context(), `SELECT rules FROM mcp_file_read_policy WHERE singleton = TRUE`).Scan(&persistedRaw); err != nil {
		t.Fatal(err)
	}
	var persisted []fileaccess.DenyRule
	if err := json.Unmarshal(persistedRaw, &persisted); err != nil || len(persisted) != len(restored.Rules) {
		t.Fatalf("persisted rules = %d, error=%v", len(persisted), err)
	}
}
