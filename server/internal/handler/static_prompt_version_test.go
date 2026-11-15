package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// Fail the actual version INSERT, leaving the real transaction and all other
// statements intact so deferred rollback exercises the database boundary.
type staticPromptFaultTx struct {
	pgx.Tx
	baseline bool
}

func (tx staticPromptFaultTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "-- name: CreatePromptVersion :one") {
		return tx.Tx.QueryRow(ctx, "SELECT 1 / 0")
	}
	return tx.Tx.QueryRow(ctx, sql, args...)
}

func (tx staticPromptFaultTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if tx.baseline && strings.Contains(sql, "-- name: InsertPromptVersionBaselineIfAbsent :exec") {
		return tx.Tx.Exec(ctx, "SELECT 1 / 0")
	}
	return tx.Tx.Exec(ctx, sql, args...)
}

type staticPromptFaultStarter struct {
	inner    txStarter
	baseline bool
}

func (s staticPromptFaultStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.inner.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return staticPromptFaultTx{Tx: tx, baseline: s.baseline}, nil
}

func staticPromptCarrier(t *testing.T, scope string) string {
	t.Helper()
	var id string
	switch scope {
	case "workspace":
		id = dbfx.Workspace(t, "static prompt workspace", handlerTestSlug(t.Name()), testutil.Cols{"context": "original"})
		dbfx.Member(t, id, testUserID, "owner")
	case "project":
		id = dbfx.Project(t, "static prompt project", testutil.Cols{"instructions": "original"})
	case "agent":
		id = dbfx.Agent(t, "static prompt agent", handlerTestRuntimeID(t), testutil.Cols{"instructions": "original"})
	case "squad":
		leader := dbfx.Agent(t, "static prompt leader", handlerTestRuntimeID(t))
		id = dbfx.Squad(t, "static prompt squad", leader, testutil.Cols{"instructions": "original"})
	default:
		t.Fatal("unsupported scope")
	}
	dbfx.Cleanup(t, `DELETE FROM prompt_version WHERE scope = $1 AND scope_id = $2`, scope, id)
	return id
}

func staticPromptUpdate(h *Handler, scope string) http.HandlerFunc {
	switch scope {
	case "workspace":
		return h.UpdateWorkspace
	case "project":
		return h.UpdateProject
	case "agent":
		return h.UpdateAgent
	case "squad":
		return h.UpdateSquad
	}
	panic("unsupported scope")
}

func staticPromptField(scope string) string {
	if scope == "workspace" {
		return "context"
	}
	return "instructions"
}

func staticPromptContent(t *testing.T, scope, id string) string {
	t.Helper()
	var content string
	dbfx.QueryRow(t, "SELECT COALESCE("+staticPromptField(scope)+", '') FROM "+scope+" WHERE id = $1", id).Scan(&content)
	return content
}

func TestStaticPromptUpdatesAtomic(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	for _, scope := range []string{"workspace", "project", "squad", "agent"} {
		t.Run(scope, func(t *testing.T) {
			id := staticPromptCarrier(t, scope)
			for _, fault := range []string{"version_insert", "commit", "none"} {
				t.Run(fault, func(t *testing.T) {
					h := *testHandler
					status := http.StatusInternalServerError
					switch fault {
					case "version_insert":
						h.TxStarter = staticPromptFaultStarter{inner: h.TxStarter}
					case "commit":
						h.TxStarter = rollbackOnCommitTxStarter{pool: testPool}
					case "none":
						status = http.StatusOK
					}
					req := withURLParams(newRequest(http.MethodPatch, "/api/"+scope+"s/"+id+"?workspace_id="+testWorkspaceID,
						map[string]any{staticPromptField(scope): "updated"}), "id", id, "workspaceId", testWorkspaceID)
					testutil.Call(t, staticPromptUpdate(&h, scope), req).Want(status)
					want, versions := "original", 0
					if fault == "none" {
						want, versions = "updated", 2
					}
					if got := staticPromptContent(t, scope, id); got != want {
						t.Fatalf("content after %s = %q, want %q", fault, got, want)
					}
					if got := dbfx.Count(t, `SELECT count(*) FROM prompt_version WHERE scope = $1 AND scope_id = $2`, scope, id); got != versions {
						t.Fatalf("versions after %s = %d, want %d", fault, got, versions)
					}
				})
			}
			var content, digest string
			dbfx.QueryRow(t, `SELECT content, content_sha256 FROM prompt_version WHERE scope = $1 AND scope_id = $2 AND version = 2`, scope, id).Scan(&content, &digest)
			if content != "updated" || digest != sha256Hex(content) {
				t.Fatalf("version does not describe committed content: %q %q", content, digest)
			}
			if scope == "project" {
				var revision int64
				dbfx.QueryRow(t, `SELECT revision FROM project WHERE id = $1`, id).Scan(&revision)
				if revision != 2 {
					t.Fatalf("project revision = %d, want one increment to 2", revision)
				}
			}
			// No-op saves do not manufacture a version; explicit clearing still
			// follows the ordinary endpoint's existing null/empty contract.
			for _, value := range []any{"updated", ""} {
				if scope == "project" && value == "" {
					value = nil
				}
				testutil.Call(t, staticPromptUpdate(testHandler, scope), withURLParams(newRequest(http.MethodPatch,
					"/api/"+scope+"s/"+id+"?workspace_id="+testWorkspaceID, map[string]any{staticPromptField(scope): value}), "id", id, "workspaceId", testWorkspaceID)).Want(http.StatusOK)
			}
			if got := staticPromptContent(t, scope, id); got != "" {
				t.Fatalf("clear left content %q", got)
			}
			if got := dbfx.Count(t, `SELECT count(*) FROM prompt_version WHERE scope = $1 AND scope_id = $2`, scope, id); got != 3 {
				t.Fatalf("versions after no-op and clear = %d, want 3", got)
			}
		})
	}
}

// The marketplace has its own business UPDATE to persist the restore pointer.
// A failed version write must preserve both the old text and that pointer.
func TestStaticPromptMarketplaceApplyRestoreAtomic(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	for _, scope := range []string{"agent", "squad"} {
		t.Run(scope, func(t *testing.T) {
			id := staticPromptCarrier(t, scope)
			var published PromptVersionResponse
			if scope == "agent" {
				_, published = publishedPrompt(t, "static-market-"+scope, "marketplace text")
			} else {
				sourceID := promptTestSquad(t, "static-market-source", "marketplace text")
				var draft PromptVersionResponse
				testutil.Call(t, testHandler.CreatePromptVersion, newRequest(http.MethodPost,
					"/api/marketplace/prompt-versions", map[string]any{
						"source_type": "squad", "source_id": sourceID, "name": "Static squad prompt", "license_code": "cc-by-4.0",
					})).Want(http.StatusCreated).JSON(&draft)
				dbfx.Cleanup(t, `DELETE FROM marketplace_prompt_version WHERE series_id = $1`, draft.SeriesID)
				published = publishPromptDraft(t, draft.ID)
			}
			install := installPrompt(t, published.ID)
			code, raw := previewApply(t, install.ID, scope, id)
			if code != http.StatusOK {
				t.Fatalf("preview: %d %s", code, raw)
			}
			preview := decodePromptPreview(t, raw)
			for _, operation := range []string{"apply", "restore"} {
				for _, fault := range []bool{true, false} {
					h := *testHandler
					status := http.StatusOK
					if fault {
						h.TxStarter = staticPromptFaultStarter{inner: h.TxStarter}
						status = http.StatusInternalServerError
					}
					body := map[string]any{"target_type": scope, "target_id": id, "strategy": "replace", "preview_token": preview.PreviewToken, "operation_id": "static-apply"}
					params := []string{"id", install.ID}
					handler := h.ApplyPrompt
					want, versions := "marketplace text", 2
					if operation == "restore" {
						handler = h.RestorePrompt
						body = map[string]any{"operation_id": "static-restore"}
						params = []string{"type", scope, "id", id}
						want, versions = "original", 3
					}
					if fault && operation == "apply" {
						want, versions = "original", 0
					} else if fault {
						want, versions = "marketplace text", 2
					}
					testutil.Call(t, handler, withURLParams(newRequest(http.MethodPost,
						"/api/marketplace/prompt-installations/"+install.ID+"/"+operation, body), params...)).Want(status)
					if got := staticPromptContent(t, scope, id); got != want {
						t.Fatalf("%s fault=%v content=%q, want %q", operation, fault, got, want)
					}
					if got := dbfx.Count(t, `SELECT count(*) FROM prompt_version WHERE scope = $1 AND scope_id = $2`, scope, id); got != versions {
						t.Fatalf("%s fault=%v versions=%d, want %d", operation, fault, got, versions)
					}
				}
			}
		})
	}
}

func TestStaticPromptCreateBaselinesAtomic(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	for _, scope := range []string{"workspace", "project", "squad", "agent"} {
		t.Run(scope, func(t *testing.T) {
			leader := dbfx.Agent(t, "baseline leader", handlerTestRuntimeID(t))
			for _, fault := range []bool{true, false} {
				h := *testHandler
				status := http.StatusCreated
				if fault {
					h.TxStarter = staticPromptFaultStarter{inner: h.TxStarter, baseline: true}
					status = http.StatusInternalServerError
				}
				name := "static baseline " + scope
				body := map[string]any{"name": name, "instructions": "initial", "runtime_id": handlerTestRuntimeID(t)}
				handler, nameColumn := h.CreateAgent, "name"
				switch scope {
				case "workspace":
					handler = h.CreateWorkspace
					body = map[string]any{"name": name, "slug": handlerTestSlug("static-baseline"), "context": "initial"}
				case "project":
					handler, nameColumn = h.CreateProject, "title"
					body = map[string]any{"title": name, "instructions": "initial"}
				case "squad":
					handler = h.CreateSquad
					body = map[string]any{"name": name, "leader_id": leader}
				}
				var created struct {
					ID string `json:"id"`
				}
				response := testutil.Call(t, handler, withURLParams(newRequest(http.MethodPost,
					"/api/"+scope+"s?workspace_id="+testWorkspaceID, body), "workspaceId", testWorkspaceID)).Want(status)
				if fault {
					if got := dbfx.Count(t, "SELECT count(*) FROM "+scope+" WHERE "+nameColumn+" = $1", name); got != 0 {
						t.Fatalf("baseline failure left %d %s rows", got, scope)
					}
					continue
				}
				response.JSON(&created)
				dbfx.Cleanup(t, "DELETE FROM "+scope+" WHERE id = $1", created.ID)
				if scope == "workspace" {
					dbfx.Cleanup(t, `DELETE FROM member WHERE workspace_id = $1`, created.ID)
				} else if scope == "squad" {
					dbfx.Cleanup(t, `DELETE FROM squad_member WHERE squad_id = $1`, created.ID)
				}
				dbfx.Cleanup(t, `DELETE FROM prompt_version WHERE scope = $1 AND scope_id = $2`, scope, created.ID)
				var baseline string
				dbfx.QueryRow(t, `SELECT content FROM prompt_version WHERE scope = $1 AND scope_id = $2 AND version = 1`, scope, created.ID).Scan(&baseline)
				if got := staticPromptContent(t, scope, created.ID); baseline != got {
					t.Fatalf("baseline = %q, business content = %q", baseline, got)
				}
			}
		})
	}
}
