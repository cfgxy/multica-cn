package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/integrations/lark"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// The capabilities wire contract (RUYI-545) distinguishes three states the
// pre-fix wire collapsed into one: [] = never probed (the bot predates the
// probe — the UI must show "not checked yet" and keep the recheck entry
// reachable), null = the stored verdicts exist but the read failed right
// now (the UI must degrade to a visible error/retry hint, never a silent
// blank), and a populated array = stored verdicts. The field itself being
// absent is reserved for servers older than the feature, which the UI
// hides — before the fix, `omitempty` serialized the never-probed case as
// absent, so pre-feature bots never rendered the permission panel at all.

// seedCapabilityWireInstallation inserts one active feishu installation
// (fresh agent, unique app_id) and removes it and its probe rows when the
// test ends. Returns the installation id.
func seedCapabilityWireInstallation(t *testing.T, appID string) string {
	t.Helper()
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "LarkCapabilityWire-"+appID, []byte("[]"))
	var instID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO channel_installation (workspace_id, agent_id, channel_type, config, installer_user_id, status)
VALUES ($1, $2, 'feishu', jsonb_build_object('app_id', $3::text), $4, 'active')
RETURNING id
`, testWorkspaceID, agentID, appID, testUserID).Scan(&instID); err != nil {
		t.Fatalf("seed feishu installation %s: %v", appID, err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM channel_capability_state WHERE installation_id = $1`, instID)
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM channel_installation WHERE id = $1`, instID)
	})
	return instID
}

// listInstallationsForWireTest calls ListLarkInstallations and returns the
// response object of the installation with the given id, or fails.
func listInstallationsForWireTest(t *testing.T, instID string) map[string]json.RawMessage {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet,
		"/api/workspaces/"+testWorkspaceID+"/lark/installations", nil)
	req = withURLParams(req, "id", testWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.ListLarkInstallations(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Installations []map[string]json.RawMessage `json:"installations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	for _, inst := range resp.Installations {
		var id string
		if err := json.Unmarshal(inst["id"], &id); err != nil {
			t.Fatalf("decode installation id: %v", err)
		}
		if id == instID {
			return inst
		}
	}
	t.Fatalf("installation %s not listed in %d rows", instID, len(resp.Installations))
	return nil
}

func TestListLarkInstallations_EmptyProbeTableServesEmptyArray(t *testing.T) {
	wireLarkInstallServices(t)
	instID := seedCapabilityWireInstallation(t, "cli_r545_empty_probe")

	inst := listInstallationsForWireTest(t, instID)
	raw, ok := inst["capabilities"]
	if !ok {
		t.Fatalf("capabilities key absent from response — a never-probed bot must serialize [] so the UI can tell it apart from an old server (RUYI-545): %v", inst)
	}
	if got := strings.TrimSpace(string(raw)); got != "[]" {
		t.Fatalf("expected capabilities to serialize as [], got %s", got)
	}
}

func TestListLarkInstallations_ServesStoredVerdicts(t *testing.T) {
	wireLarkInstallServices(t)
	instID := seedCapabilityWireInstallation(t, "cli_r545_verdicts")
	if _, err := testPool.Exec(context.Background(), `
INSERT INTO channel_capability_state (id, installation_id, channel_type, capability, status, detail, required_scopes)
VALUES ($1, $2, 'feishu', 'receive_messages', 'granted', 'ok', '["im:message"]'::jsonb)
`, dbid.NewV7(), instID); err != nil {
		t.Fatalf("seed capability state: %v", err)
	}

	inst := listInstallationsForWireTest(t, instID)
	raw, ok := inst["capabilities"]
	if !ok {
		t.Fatalf("capabilities key absent: %v", inst)
	}
	var caps []lark.CapabilityStateView
	if err := json.Unmarshal(raw, &caps); err != nil {
		t.Fatalf("decode capabilities %s: %v", raw, err)
	}
	if len(caps) != 1 {
		t.Fatalf("expected 1 stored verdict, got %d: %s", len(caps), raw)
	}
	if caps[0].Capability != "receive_messages" || caps[0].Status != "granted" {
		t.Fatalf("unexpected verdict: %+v", caps[0])
	}
	if len(caps[0].RequiredScopes) != 1 || caps[0].RequiredScopes[0] != "im:message" {
		t.Fatalf("required_scopes roundtrip failed: %+v", caps[0].RequiredScopes)
	}
}

func TestListLarkInstallations_CapabilityReadFailureServesNullAndWarns(t *testing.T) {
	wireLarkInstallServices(t)
	instID := seedCapabilityWireInstallation(t, "cli_r545_read_fail")

	// Point the handler's capability reads at a closed pool while the
	// installation service keeps the live reference it captured at
	// construction — ListByWorkspace must keep succeeding so the test
	// isolates exactly the degraded-verdict-read path.
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	closed, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open throwaway pool: %v", err)
	}
	closed.Close()
	prevQueries := testHandler.Queries
	testHandler.Queries = db.New(closed)
	t.Cleanup(func() { testHandler.Queries = prevQueries })

	var logs bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	inst := listInstallationsForWireTest(t, instID)
	raw, ok := inst["capabilities"]
	if !ok {
		t.Fatalf("capabilities key absent on read failure — null must stay distinguishable from an old server so the UI can render a retry hint (RUYI-545): %v", inst)
	}
	if got := strings.TrimSpace(string(raw)); got != "null" {
		t.Fatalf("expected capabilities to serialize as null on read failure, got %s", got)
	}
	logged := logs.String()
	if !strings.Contains(logged, "level=WARN") {
		t.Fatalf("expected a WARN log for the capability read failure, got: %s", logged)
	}
	if !strings.Contains(logged, instID) {
		t.Fatalf("expected the WARN log to carry installation_id %s, got: %s", instID, logged)
	}
}
