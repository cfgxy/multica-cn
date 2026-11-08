package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	ttTestToken   = "mat_test_workspace_token"
	ttWorkspaceA  = "0a0a0a0a-0000-4000-8000-00000000000a"
	ttWorkspaceB  = "0b0b0b0b-0000-4000-8000-00000000000b"
	ttUserID      = "11111111-0000-4000-8000-000000000001"
	ttAgentID     = "22222222-0000-4000-8000-000000000002"
	ttTaskID      = "33333333-0000-4000-8000-000000000003"
	ttSlugA       = "ws-a"
	ttSlugB       = "ws-b"
	ttSlugDBError = "ws-db-error"
)

type fakeTaskTokenDB struct {
	t *testing.T
}

func (f *fakeTaskTokenDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	f.t.Errorf("unexpected Exec on task-token auth path")
	return pgconn.CommandTag{}, errors.New("unexpected Exec")
}

func (f *fakeTaskTokenDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	f.t.Errorf("unexpected Query on task-token auth path")
	return nil, errors.New("unexpected Query")
}

func (f *fakeTaskTokenDB) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	f.t.Errorf("unexpected CopyFrom on task-token auth path")
	return 0, errors.New("unexpected CopyFrom")
}

func (f *fakeTaskTokenDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "name: GetTaskTokenByHash"):
		if len(args) != 1 || args[0] != auth.HashToken(ttTestToken) {
			return fakeTaskTokenRow{err: pgx.ErrNoRows}
		}
		return fakeTaskTokenRow{scan: func(dest ...any) {
			*dest[0].(*pgtype.UUID) = util.MustParseUUID("44444444-0000-4000-8000-000000000004")
			*dest[2].(*pgtype.UUID) = util.MustParseUUID(ttTaskID)
			*dest[3].(*pgtype.UUID) = util.MustParseUUID(ttAgentID)
			*dest[4].(*pgtype.UUID) = util.MustParseUUID(ttWorkspaceA)
			*dest[5].(*pgtype.UUID) = util.MustParseUUID(ttUserID)
		}}
	case strings.Contains(sql, "name: GetWorkspaceBySlug"):
		var id string
		switch args[0] {
		case ttSlugA:
			id = ttWorkspaceA
		case ttSlugB:
			id = ttWorkspaceB
		case ttSlugDBError:
			return fakeTaskTokenRow{err: errors.New("connection reset")}
		default:
			return fakeTaskTokenRow{err: pgx.ErrNoRows}
		}
		return fakeTaskTokenRow{scan: func(dest ...any) {
			*dest[0].(*pgtype.UUID) = util.MustParseUUID(id)
		}}
	default:
		f.t.Errorf("unexpected QueryRow on task-token auth path: %s", sql)
		return fakeTaskTokenRow{err: errors.New("unexpected QueryRow")}
	}
}

type fakeTaskTokenRow struct {
	err  error
	scan func(dest ...any)
}

func (r fakeTaskTokenRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	r.scan(dest...)
	return nil
}

func TestAuth_TaskTokenWorkspaceSelectors(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		target     string
		headers    map[string][]string
		wantStatus int
	}{
		{name: "no selector", method: http.MethodGet, target: "/api/projects", wantStatus: http.StatusOK},
		{name: "same header id", method: http.MethodGet, target: "/api/projects", headers: map[string][]string{"X-Workspace-ID": {ttWorkspaceA}}, wantStatus: http.StatusOK},
		{name: "same uppercase header id", method: http.MethodGet, target: "/api/projects", headers: map[string][]string{"X-Workspace-ID": {strings.ToUpper(ttWorkspaceA)}}, wantStatus: http.StatusOK},
		{name: "same query id", method: http.MethodGet, target: "/api/projects?workspace_id=" + ttWorkspaceA, wantStatus: http.StatusOK},
		{name: "same header slug", method: http.MethodGet, target: "/api/projects", headers: map[string][]string{"X-Workspace-Slug": {ttSlugA}}, wantStatus: http.StatusOK},
		{name: "same query slug", method: http.MethodGet, target: "/api/projects?workspace_slug=" + ttSlugA, wantStatus: http.StatusOK},
		{name: "all selectors same", method: http.MethodPost, target: "/api/issues?workspace_id=" + ttWorkspaceA + "&workspace_slug=" + ttSlugA, headers: map[string][]string{"X-Workspace-ID": {ttWorkspaceA}, "X-Workspace-Slug": {ttSlugA}}, wantStatus: http.StatusOK},
		{name: "empty query id", method: http.MethodGet, target: "/api/projects?workspace_id=", wantStatus: http.StatusOK},
		{name: "header id conflict", method: http.MethodGet, target: "/api/projects", headers: map[string][]string{"X-Workspace-ID": {ttWorkspaceB}}, wantStatus: http.StatusForbidden},
		{name: "query id conflict", method: http.MethodGet, target: "/api/projects?workspace_id=" + ttWorkspaceB, wantStatus: http.StatusForbidden},
		{name: "header slug conflict", method: http.MethodGet, target: "/api/projects", headers: map[string][]string{"X-Workspace-Slug": {ttSlugB}}, wantStatus: http.StatusForbidden},
		{name: "query slug conflict", method: http.MethodGet, target: "/api/projects?workspace_slug=" + ttSlugB, wantStatus: http.StatusForbidden},
		{name: "header A query B", method: http.MethodGet, target: "/api/projects?workspace_id=" + ttWorkspaceB, headers: map[string][]string{"X-Workspace-ID": {ttWorkspaceA}}, wantStatus: http.StatusForbidden},
		{name: "id A slug B", method: http.MethodGet, target: "/api/projects", headers: map[string][]string{"X-Workspace-ID": {ttWorkspaceA}, "X-Workspace-Slug": {ttSlugB}}, wantStatus: http.StatusForbidden},
		{name: "duplicate query id", method: http.MethodGet, target: "/api/projects?workspace_id=" + ttWorkspaceA + "&workspace_id=" + ttWorkspaceB, wantStatus: http.StatusForbidden},
		{name: "duplicate header id", method: http.MethodGet, target: "/api/projects", headers: map[string][]string{"X-Workspace-ID": {ttWorkspaceA, ttWorkspaceB}}, wantStatus: http.StatusForbidden},
		{name: "duplicate query slug", method: http.MethodGet, target: "/api/projects?workspace_slug=" + ttSlugA + "&workspace_slug=" + ttSlugB, wantStatus: http.StatusForbidden},
		{name: "malformed header id", method: http.MethodGet, target: "/api/projects", headers: map[string][]string{"X-Workspace-ID": {"not-a-uuid"}}, wantStatus: http.StatusBadRequest},
		{name: "malformed query id", method: http.MethodGet, target: "/api/projects?workspace_id=nope", wantStatus: http.StatusBadRequest},
		{name: "unknown slug", method: http.MethodGet, target: "/api/projects", headers: map[string][]string{"X-Workspace-Slug": {"does-not-exist"}}, wantStatus: http.StatusNotFound},
		{name: "slug lookup error", method: http.MethodGet, target: "/api/projects?workspace_slug=" + ttSlugDBError, wantStatus: http.StatusInternalServerError},
		{name: "bad id escape", method: http.MethodPost, target: "/api/projects?workspace_id=" + ttWorkspaceB + "%zz", wantStatus: http.StatusBadRequest},
		{name: "bad slug escape", method: http.MethodPost, target: "/api/projects?workspace_slug=ws-b%zz", wantStatus: http.StatusBadRequest},
		{name: "unencoded semicolon", method: http.MethodPost, target: "/api/projects?workspace_id=" + ttWorkspaceB + ";ignored=1", wantStatus: http.StatusBadRequest},
		{name: "valid then malformed", method: http.MethodPost, target: "/api/projects?workspace_id=" + ttWorkspaceA + "&workspace_id=" + ttWorkspaceB + "%zz", wantStatus: http.StatusBadRequest},
		{name: "malformed non-selector", method: http.MethodPost, target: "/api/projects?workspace_id=" + ttWorkspaceA + "&q=%zz", wantStatus: http.StatusBadRequest},
		{name: "encoded values", method: http.MethodGet, target: "/api/projects?workspace_slug=ws%2Da&q=a%20b", wantStatus: http.StatusOK},
		{name: "upload conflict", method: http.MethodPost, target: "/api/upload-file", headers: map[string][]string{"X-Workspace-ID": {ttWorkspaceB}}, wantStatus: http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			queries := db.New(&fakeTaskTokenDB{t: t})
			var called bool
			var gotWorkspace, gotSource, gotAgent, gotTask string
			handler := Auth(queries, nil, nil, nil, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				gotWorkspace = r.Header.Get("X-Workspace-ID")
				gotSource = r.Header.Get("X-Actor-Source")
				gotAgent = r.Header.Get("X-Agent-ID")
				gotTask = r.Header.Get("X-Task-ID")
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(tc.method, tc.target, nil)
			req.Header.Set("Authorization", "Bearer "+ttTestToken)
			for key, values := range tc.headers {
				for _, value := range values {
					req.Header.Add(key, value)
				}
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				if called {
					t.Fatal("handler must not run when selector is rejected")
				}
				if tc.wantStatus == http.StatusForbidden && !strings.Contains(w.Body.String(), errTaskTokenWorkspaceMismatch) {
					t.Fatalf("403 body = %s, want %q", w.Body.String(), errTaskTokenWorkspaceMismatch)
				}
				return
			}
			if !called {
				t.Fatal("handler was not called")
			}
			if gotWorkspace != ttWorkspaceA || gotSource != "task_token" || gotAgent != ttAgentID || gotTask != ttTaskID {
				t.Fatalf("authoritative headers = workspace:%q source:%q agent:%q task:%q", gotWorkspace, gotSource, gotAgent, gotTask)
			}
		})
	}
}

func TestAuth_JWTWorkspaceSelectorUntouched(t *testing.T) {
	var gotWorkspace string
	handler := Auth(nil, nil, nil, nil, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotWorkspace = r.Header.Get("X-Workspace-ID")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/projects?workspace_slug=anything", nil)
	req.Header.Set("Authorization", "Bearer "+generateToken(validClaims(), auth.JWTSecret()))
	req.Header.Set("X-Workspace-ID", ttWorkspaceB)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	if gotWorkspace != ttWorkspaceB {
		t.Fatalf("X-Workspace-ID = %q, want client value %q", gotWorkspace, ttWorkspaceB)
	}
}
