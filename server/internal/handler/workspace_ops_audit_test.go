package handler

// RUYI-355 P2-3 regression: ops.server_started only reaches workspaces that
// existed at boot, so a workspace created mid-flight had no ops-domain audit
// row until the next restart. Creation itself must anchor the ops trail —
// and the row must be retrievable through the same workspace-scoped,
// domain-filtered search REST and the MCP search_audit_events tool share.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestCreateWorkspaceAnchorsOpsAuditTrail(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	slug := handlerTestSlug("handler-tests-ops-audit")
	_, _ = testPool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, slug)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE slug = $1`, slug)
	})

	req := newRequest("POST", "/api/workspaces", map[string]any{
		"name": "Ops Trail Probe",
		"slug": slug,
	})
	res := testutil.Call(t, testHandler.CreateWorkspace, req).Want(http.StatusCreated)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("decode created workspace: %v (body: %s)", err, res.Body.String())
	}

	// DB truth: exactly one ops row, member-attributed, carrying the
	// workspace identity in details.
	var eventType, actorType, detailsName, detailsSlug string
	var actorID *string
	err := testPool.QueryRow(ctx, `
		SELECT event_type, actor_type, actor_id::text, details->>'name', details->>'slug'
		FROM audit_event
		WHERE workspace_id = $1 AND domain = 'ops'`, created.ID).
		Scan(&eventType, &actorType, &actorID, &detailsName, &detailsSlug)
	if err != nil {
		t.Fatalf("workspace created mid-flight has no ops-domain audit row: %v", err)
	}
	if eventType != "ops.workspace_created" {
		t.Fatalf("event_type = %q, want ops.workspace_created", eventType)
	}
	if actorType != "member" || actorID == nil || *actorID != testUserID {
		t.Fatalf("actor = %s/%v, want member/%s", actorType, actorID, testUserID)
	}
	if detailsName != "Ops Trail Probe" || detailsSlug != slug {
		t.Fatalf("details name/slug = %q/%q, want %q/%q", detailsName, detailsSlug, "Ops Trail Probe", slug)
	}

	// Object-dimension readability: the ops trail of the new workspace is
	// retrievable through the shared audit search with the domain filter —
	// the exact surface the MCP search_audit_events tool proxies.
	w := httptest.NewRecorder()
	listReq := newRequest("GET", "/api/workspaces/"+created.ID+"/audit-events?domain=ops", nil)
	listReq = withURLParam(listReq, "id", created.ID)
	testHandler.ListAuditEvents(w, listReq)
	var page auditEventsPageWire
	if w.Code != http.StatusOK {
		t.Fatalf("list ops events: %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode audit page: %v", err)
	}
	if len(page.Events) != 1 || page.Events[0].EventType != "ops.workspace_created" {
		t.Fatalf("ops-filtered trail = %d events (first type %v), want exactly one ops.workspace_created",
			len(page.Events), firstEventType(page))
	}
}

func firstEventType(page auditEventsPageWire) any {
	if len(page.Events) == 0 {
		return nil
	}
	return page.Events[0].EventType
}
