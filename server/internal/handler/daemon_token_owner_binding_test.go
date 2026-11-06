package handler

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestDaemonTokenRuntimeBinding(t *testing.T) {
	runtime := db.AgentRuntime{
		DaemonID: strToText("owner-bound-daemon"),
		OwnerID:  parseUUID(testUserID),
	}

	tests := []struct {
		name    string
		request *http.Request
		runtime db.AgentRuntime
		want    bool
	}{
		{
			name:    "matching owner and daemon",
			request: newDaemonTokenRequest(http.MethodPost, "/api/daemon/heartbeat", nil, testWorkspaceID, runtime.DaemonID.String),
			runtime: runtime,
			want:    true,
		},
		{
			name:    "same owner different daemon",
			request: newDaemonTokenRequest(http.MethodPost, "/api/daemon/heartbeat", nil, testWorkspaceID, "other-daemon"),
			runtime: runtime,
			want:    false,
		},
		{
			name:    "matching owner unbound runtime daemon",
			request: newDaemonTokenRequest(http.MethodPost, "/api/daemon/heartbeat", nil, testWorkspaceID, "request-daemon"),
			runtime: db.AgentRuntime{OwnerID: runtime.OwnerID},
			want:    true,
		},
		{
			name:    "matching owner empty runtime daemon",
			request: newDaemonTokenRequest(http.MethodPost, "/api/daemon/heartbeat", nil, testWorkspaceID, "request-daemon"),
			runtime: db.AgentRuntime{DaemonID: pgtype.Text{String: "", Valid: true}, OwnerID: runtime.OwnerID},
			want:    true,
		},
		{
			name: "daemon token without daemon id",
			request: func() *http.Request {
				req := testutil.JSONRequest(http.MethodPost, "/api/daemon/heartbeat", nil)
				ctx := middleware.WithDaemonContext(req.Context(), testWorkspaceID, "")
				ctx = middleware.WithDaemonTokenOwnerID(ctx, testUserID)
				return req.WithContext(ctx)
			}(),
			runtime: db.AgentRuntime{OwnerID: runtime.OwnerID},
			want:    false,
		},
		{
			name:    "user credential ignores daemon binding",
			request: newOwnerDaemonRequest(http.MethodPost, "/api/daemon/heartbeat", nil),
			runtime: runtime,
			want:    true,
		},
		{
			name: "daemon token without owner",
			request: func() *http.Request {
				req := testutil.JSONRequest(http.MethodPost, "/api/daemon/heartbeat", nil)
				return req.WithContext(middleware.WithDaemonContext(req.Context(), testWorkspaceID, runtime.DaemonID.String))
			}(),
			runtime: runtime,
			want:    false,
		},
		{
			name: "daemon token with different owner",
			request: func() *http.Request {
				req := testutil.JSONRequest(http.MethodPost, "/api/daemon/heartbeat", nil)
				ctx := middleware.WithDaemonContext(req.Context(), testWorkspaceID, runtime.DaemonID.String)
				ctx = middleware.WithDaemonTokenOwnerID(ctx, "22222222-2222-4222-8222-222222222222")
				return req.WithContext(ctx)
			}(),
			runtime: runtime,
			want:    false,
		},
		{
			name:    "runtime without owner",
			request: newDaemonTokenRequest(http.MethodPost, "/api/daemon/heartbeat", nil, testWorkspaceID, runtime.DaemonID.String),
			runtime: db.AgentRuntime{DaemonID: runtime.DaemonID},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := daemonTokenRuntimeBindingAllowed(tt.request, tt.runtime); got != tt.want {
				t.Fatalf("daemonTokenRuntimeBindingAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDaemonRegisterRejectsUnboundToken(t *testing.T) {
	daemonID := "unbound-register-" + uuid.NewString()
	req := testutil.JSONRequest(http.MethodPost, "/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
		"runtimes": []map[string]any{{
			"name": "Unbound runtime",
			"type": "claude",
		}},
	})
	req = req.WithContext(middleware.WithDaemonContext(req.Context(), testWorkspaceID, daemonID))
	h := &Handler{}
	testutil.Call(t, h.DaemonRegister, req).Want(http.StatusForbidden)
}

func TestDaemonRegisterRejectsRuntimeOwnedByAnotherUser(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID := "owned-register-" + uuid.NewString()
	otherOwner := dbfx.User(t, "daemon registration owner", "daemon-register-"+uuid.NewString()+"@example.com")
	dbfx.Member(t, testWorkspaceID, otherOwner, "member")
	dbfx.Runtime(t, "Other owner's runtime", testutil.Cols{
		"daemon_id": daemonID,
		"provider":  "claude",
		"owner_id":  otherOwner,
	})

	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
		"runtimes": []map[string]any{{
			"name": "Attempted takeover",
			"type": "codex",
		}},
	}, testWorkspaceID, daemonID)
	testutil.Call(t, testHandler.DaemonRegister, req).Want(http.StatusForbidden)
}

type legacyDaemonTokenFixture struct {
	daemonID  string
	runtimeID string
	agentID   string
	taskID    string
}

func newLegacyDaemonTokenFixture(t *testing.T, label string) legacyDaemonTokenFixture {
	t.Helper()
	suffix := uuid.NewString()[:8]
	fixture := legacyDaemonTokenFixture{daemonID: "legacy-" + label + "-" + suffix}
	fixture.runtimeID = dbfx.Runtime(t, label+" legacy runtime", testutil.Cols{
		"daemon_id": fixture.daemonID,
		"provider":  "claude",
	})
	var issueID string
	fixture.agentID, issueID = createClaimReclaimAgentAndIssue(t, context.Background(), fixture.runtimeID, label+" legacy agent")
	fixture.taskID = dbfx.Task(t, fixture.agentID, testutil.Cols{
		"runtime_id": fixture.runtimeID,
		"issue_id":   issueID,
		"status":     "running",
		"started_at": testutil.Raw("now()"),
	})
	return fixture
}

type legacyTaskTokenOwner struct {
	taskID  string
	agentID string
	userID  string
}

func mintLegacyDaemonToken(t *testing.T, daemonID string, taskTokens ...legacyTaskTokenOwner) string {
	t.Helper()
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	expiresAt := createdAt.Add(time.Hour)
	for _, token := range taskTokens {
		dbfx.Insert(t, "task_token", testutil.Cols{
			"token_hash":   "legacy-task-" + uuid.NewString(),
			"task_id":      token.taskID,
			"agent_id":     token.agentID,
			"workspace_id": testWorkspaceID,
			"user_id":      token.userID,
			"expires_at":   expiresAt,
			"created_at":   createdAt,
		})
	}

	raw, err := auth.GenerateDaemonToken()
	if err != nil {
		t.Fatalf("generate legacy daemon token: %v", err)
	}
	dbfx.Insert(t, "daemon_token", testutil.Cols{
		"token_hash":   auth.HashToken(raw),
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
		"expires_at":   expiresAt,
		"created_at":   createdAt,
	})
	return raw
}

func legacyDaemonTaskStatus(t *testing.T, raw, taskID string) int {
	t.Helper()
	req := testutil.JSONRequest(http.MethodGet, "/api/daemon/tasks/"+taskID+"/status", nil)
	req = testutil.WithURLParams(req, "taskId", taskID)
	req = testutil.WithHeaders(req, "Authorization", "Bearer "+raw)
	handler := middleware.DaemonAuth(testHandler.Queries, nil, nil, nil, nil)(http.HandlerFunc(testHandler.GetTaskStatus))
	return testutil.Call(t, http.HandlerFunc(handler.ServeHTTP), req).Code
}

func TestLegacyDaemonTokenOwnerRecovery(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	t.Run("single live owner remains compatible", func(t *testing.T) {
		fixture := newLegacyDaemonTokenFixture(t, "single")
		raw := mintLegacyDaemonToken(t, fixture.daemonID, legacyTaskTokenOwner{
			taskID: fixture.taskID, agentID: fixture.agentID, userID: testUserID,
		})
		if code := legacyDaemonTaskStatus(t, raw, fixture.taskID); code != http.StatusOK {
			t.Fatalf("legacy token status = %d, want 200", code)
		}
	})

	t.Run("no matching task token", func(t *testing.T) {
		fixture := newLegacyDaemonTokenFixture(t, "none")
		raw := mintLegacyDaemonToken(t, fixture.daemonID)
		if code := legacyDaemonTaskStatus(t, raw, fixture.taskID); code != http.StatusNotFound {
			t.Fatalf("legacy token status = %d, want 404", code)
		}
	})

	t.Run("expired proof", func(t *testing.T) {
		fixture := newLegacyDaemonTokenFixture(t, "expired")
		raw := mintLegacyDaemonToken(t, fixture.daemonID, legacyTaskTokenOwner{
			taskID: fixture.taskID, agentID: fixture.agentID, userID: testUserID,
		})
		dbfx.Exec(t, `UPDATE task_token SET expires_at = now() - interval '1 second' WHERE task_id = $1`, fixture.taskID)
		if code := legacyDaemonTaskStatus(t, raw, fixture.taskID); code != http.StatusNotFound {
			t.Fatalf("legacy token status = %d, want 404", code)
		}
	})

	t.Run("runtime owner changed", func(t *testing.T) {
		fixture := newLegacyDaemonTokenFixture(t, "moved")
		raw := mintLegacyDaemonToken(t, fixture.daemonID, legacyTaskTokenOwner{
			taskID: fixture.taskID, agentID: fixture.agentID, userID: testUserID,
		})
		otherOwner := dbfx.User(t, "legacy replacement owner", "legacy-owner-"+uuid.NewString()+"@example.com")
		dbfx.Member(t, testWorkspaceID, otherOwner, "member")
		dbfx.Exec(t, `UPDATE agent_runtime SET owner_id = $1 WHERE id = $2`, otherOwner, fixture.runtimeID)
		if code := legacyDaemonTaskStatus(t, raw, fixture.taskID); code != http.StatusNotFound {
			t.Fatalf("legacy token status = %d, want 404", code)
		}
	})

	t.Run("ambiguous owners", func(t *testing.T) {
		first := newLegacyDaemonTokenFixture(t, "ambiguous-a")
		second := newLegacyDaemonTokenFixture(t, "ambiguous-b")
		otherOwner := dbfx.User(t, "legacy ambiguous owner", "legacy-ambiguous-"+uuid.NewString()+"@example.com")
		dbfx.Member(t, testWorkspaceID, otherOwner, "member")
		dbfx.Exec(t, `UPDATE agent_runtime SET daemon_id = $1, provider = 'codex', owner_id = $2 WHERE id = $3`, first.daemonID, otherOwner, second.runtimeID)
		raw := mintLegacyDaemonToken(t, first.daemonID,
			legacyTaskTokenOwner{taskID: first.taskID, agentID: first.agentID, userID: testUserID},
			legacyTaskTokenOwner{taskID: second.taskID, agentID: second.agentID, userID: otherOwner},
		)
		if code := legacyDaemonTaskStatus(t, raw, first.taskID); code != http.StatusNotFound {
			t.Fatalf("legacy token status = %d, want 404", code)
		}
	})
}
