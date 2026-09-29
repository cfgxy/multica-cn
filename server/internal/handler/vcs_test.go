package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func vcsHandlerRequest(method, path string, body any, connectionID string) *http.Request {
	req := newRequest(method, path, body)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", testWorkspaceID)
	if connectionID != "" {
		rctx.URLParams.Add("connectionId", connectionID)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestListVCSConnectionsHonorsDeploymentSwitch(t *testing.T) {
	ctx := context.Background()
	box := withVCSBox(t)
	connID := seedVCSConnection(t, ctx, box, "forgejo", "https://forgejo-list.test")
	t.Cleanup(func() { cleanupVCS(context.Background(), "") })

	fetch := func() struct {
		Connections []VCSConnectionResponse `json:"connections"`
		Available   bool                    `json:"available"`
		Configured  bool                    `json:"configured"`
	} {
		t.Helper()
		req := vcsHandlerRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/vcs/connections", nil, "")
		w := httptest.NewRecorder()
		testHandler.ListVCSConnections(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("ListVCSConnections: expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp struct {
			Connections []VCSConnectionResponse `json:"connections"`
			Available   bool                    `json:"available"`
			Configured  bool                    `json:"configured"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode ListVCSConnections: %v", err)
		}
		return resp
	}

	testHandler.cfg.VCSIntegrationEnabled = false
	disabled := fetch()
	if disabled.Available || disabled.Configured || len(disabled.Connections) != 0 {
		t.Fatalf("disabled response must hide stored connections and availability, got %+v", disabled)
	}

	testHandler.cfg.VCSIntegrationEnabled = true
	enabled := fetch()
	if !enabled.Available || !enabled.Configured {
		t.Fatalf("enabled response must expose availability and configuration, got %+v", enabled)
	}
	if len(enabled.Connections) != 1 || enabled.Connections[0].ID != connID {
		t.Fatalf("enabled response must include seeded connection %s, got %+v", connID, enabled.Connections)
	}
}

func TestConnectVCSHonorsDeploymentSwitch(t *testing.T) {
	var validationCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		validationCalls.Add(1)
		if r.URL.Path != "/api/v1/user" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"login":"vcs-test-user"}`))
	}))
	defer provider.Close()

	withVCSBox(t)
	t.Cleanup(func() { cleanupVCS(context.Background(), "") })
	body := map[string]any{
		"provider":     "forgejo",
		"instance_url": provider.URL,
		"access_token": "test-token",
	}
	connect := func() *httptest.ResponseRecorder {
		t.Helper()
		req := vcsHandlerRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/vcs/connections", body, "")
		w := httptest.NewRecorder()
		testHandler.ConnectVCS(w, req)
		return w
	}
	countConnections := func() int {
		t.Helper()
		var count int
		if err := testPool.QueryRow(context.Background(),
			`SELECT count(*) FROM vcs_connection WHERE workspace_id = $1 AND instance_url = $2`,
			testWorkspaceID, provider.URL,
		).Scan(&count); err != nil {
			t.Fatalf("count VCS connections: %v", err)
		}
		return count
	}

	testHandler.cfg.VCSIntegrationEnabled = false
	if w := connect(); w.Code != http.StatusNotFound {
		t.Fatalf("disabled ConnectVCS: expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if got := validationCalls.Load(); got != 0 {
		t.Fatalf("disabled ConnectVCS must not call provider, got %d requests", got)
	}
	if got := countConnections(); got != 0 {
		t.Fatalf("disabled ConnectVCS must not write a connection, got %d rows", got)
	}

	testHandler.cfg.VCSIntegrationEnabled = true
	if w := connect(); w.Code != http.StatusOK {
		t.Fatalf("enabled ConnectVCS: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := validationCalls.Load(); got != 1 {
		t.Fatalf("enabled ConnectVCS: expected one provider validation, got %d", got)
	}
	if got := countConnections(); got != 1 {
		t.Fatalf("enabled ConnectVCS: expected one stored connection, got %d", got)
	}
}

func TestRotateVCSConnectionWebhookHonorsDeploymentSwitch(t *testing.T) {
	ctx := context.Background()
	box := withVCSBox(t)
	connID := seedVCSConnection(t, ctx, box, "forgejo", "https://forgejo-rotate.test")
	t.Cleanup(func() { cleanupVCS(context.Background(), "") })
	connUUID := parseUUID(connID)

	loadSecret := func() string {
		t.Helper()
		conn, err := testHandler.Queries.GetVCSConnectionByID(context.Background(), connUUID)
		if err != nil {
			t.Fatalf("GetVCSConnectionByID: %v", err)
		}
		return conn.WebhookSecretEncrypted
	}
	rotate := func() *httptest.ResponseRecorder {
		t.Helper()
		req := vcsHandlerRequest(
			http.MethodPost,
			"/api/workspaces/"+testWorkspaceID+"/vcs/connections/"+connID+"/rotate-webhook",
			nil,
			connID,
		)
		w := httptest.NewRecorder()
		testHandler.RotateVCSConnectionWebhook(w, req)
		return w
	}

	originalSecret := loadSecret()
	testHandler.cfg.VCSIntegrationEnabled = false
	if w := rotate(); w.Code != http.StatusNotFound {
		t.Fatalf("disabled RotateVCSConnectionWebhook: expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if got := loadSecret(); got != originalSecret {
		t.Fatal("disabled RotateVCSConnectionWebhook must not modify the stored secret")
	}

	testHandler.cfg.VCSIntegrationEnabled = true
	if w := rotate(); w.Code != http.StatusOK {
		t.Fatalf("enabled RotateVCSConnectionWebhook: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := loadSecret(); got == originalSecret {
		t.Fatal("enabled RotateVCSConnectionWebhook must replace the stored secret")
	}
}

func TestListVCSConnectionRepositories(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("PRIVATE-TOKEN") != "tok" {
			t.Error("missing stored access token")
		}
		switch r.URL.Query().Get("page") {
		case "2":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("private-token-in-upstream-response"))
			return
		case "3":
			w.WriteHeader(http.StatusTooManyRequests)
			return
		case "4":
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`[{"id":1,"path_with_namespace":"team/sub/repo","ssh_url_to_repo":"git@git.test:team/sub/repo.git","visibility":"private","archived":false}]`))
	}))
	defer provider.Close()
	box := withVCSBox(t)
	id := seedVCSConnection(t, context.Background(), box, "gitlab", provider.URL)
	forgejoID := seedVCSConnection(t, context.Background(), box, "forgejo", "https://forgejo.test")
	t.Cleanup(func() { cleanupVCS(context.Background(), "") })

	request := func(path, connID, wsID string) *http.Request {
		req := vcsHandlerRequest(http.MethodGet, path, nil, connID)
		rctx := chi.RouteContext(req.Context())
		rctx.URLParams.Add("id", wsID)
		return req
	}
	path := "/api/workspaces/" + testWorkspaceID + "/vcs/connections/" + id + "/repositories"
	var response struct {
		Repositories []struct {
			FullName string `json:"full_name"`
			CloneURL string `json:"clone_url"`
			Private  bool   `json:"private"`
		} `json:"repositories"`
		NextPage *int `json:"next_page"`
	}
	testutil.Call(t, testHandler.ListVCSConnectionRepositories, request(path, id, testWorkspaceID)).Want(http.StatusOK).JSON(&response)
	if len(response.Repositories) != 1 || response.Repositories[0].FullName != "team/sub/repo" || !response.Repositories[0].Private || response.NextPage != nil {
		t.Fatalf("unexpected GitLab projection: %+v", response)
	}
	for _, tc := range []struct {
		connection, workspace, path string
		want                        int
	}{
		{forgejoID, testWorkspaceID, path, http.StatusBadRequest},
		{id, "11111111-1111-1111-1111-111111111111", path, http.StatusNotFound},
		{"22222222-2222-2222-2222-222222222222", testWorkspaceID, path, http.StatusNotFound},
		{id, testWorkspaceID, path + "?page=0", http.StatusBadRequest},
		{id, testWorkspaceID, path + "?per_page=101", http.StatusBadRequest},
	} {
		testutil.Call(t, testHandler.ListVCSConnectionRepositories, request(tc.path, tc.connection, tc.workspace)).Want(tc.want)
	}
	for _, tc := range []struct {
		page   string
		status int
	}{
		{"2", http.StatusFailedDependency},
		{"3", http.StatusTooManyRequests},
		{"4", http.StatusBadGateway},
	} {
		resp := testutil.Call(t, testHandler.ListVCSConnectionRepositories, request(path+"?page="+tc.page, id, testWorkspaceID)).Want(tc.status)
		if strings.Contains(resp.Body.String(), "private-token-in-upstream-response") {
			t.Fatal("GitLab upstream body reached the client")
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("unauthorized or invalid request reached GitLab: %d", calls.Load())
	}
}

func TestGitLabBrowseRouteRequiresAdmin(t *testing.T) {
	withVCSBox(t)
	memberID := dbfx.Insert(t, "user", testutil.Cols{"name": "Browse Member", "email": "gitlab-browse-member@multica.ai"})
	adminID := dbfx.Insert(t, "user", testutil.Cols{"name": "Browse Admin", "email": "gitlab-browse-admin@multica.ai"})
	dbfx.Insert(t, "member", testutil.Cols{"workspace_id": testWorkspaceID, "user_id": memberID, "role": "member"})
	dbfx.Insert(t, "member", testutil.Cols{"workspace_id": testWorkspaceID, "user_id": adminID, "role": "admin"})
	path := "/api/workspaces/" + testWorkspaceID + "/vcs/connections/22222222-2222-2222-2222-222222222222/repositories"
	router := chi.NewRouter()
	router.Route("/api/workspaces/{id}", func(r chi.Router) {
		r.Use(middleware.RequireWorkspaceRoleFromURL(testHandler.Queries, "id", "owner", "admin"))
		r.Get("/vcs/connections/{connectionId}/repositories", testHandler.ListVCSConnectionRepositories)
	})
	for _, tc := range []struct {
		userID string
		want   int
	}{
		{memberID, http.StatusForbidden},
		{adminID, http.StatusNotFound},
		{testUserID, http.StatusNotFound},
		{"11111111-1111-1111-1111-111111111111", http.StatusNotFound},
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-User-ID", tc.userID)
		testutil.Call(t, router.ServeHTTP, req).Want(tc.want)
	}
}
