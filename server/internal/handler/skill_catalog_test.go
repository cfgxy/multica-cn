package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// RUYI-288: workspace skill catalog — discovery index sync, catalog union
// read, and the fan-out sync trigger.

func cleanupSkillCatalogFixtures(t *testing.T, workspaceID string, runtimeIDs []string) {
	t.Helper()
	for _, rt := range runtimeIDs {
		testPool.Exec(context.Background(), `DELETE FROM runtime_skill_discovery WHERE runtime_id = $1`, rt)
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, rt)
	}
	testPool.Exec(context.Background(), `DELETE FROM skill WHERE workspace_id = $1 AND name LIKE 'catalog-test-%'`, workspaceID)
}

// seedCatalogDiscovery inserts one discovery sighting row directly.
func seedCatalogDiscovery(t *testing.T, workspaceID, runtimeID, key, name, sourcePath string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO runtime_skill_discovery (
			workspace_id, runtime_id, provider, root, plugin, key, name, description, source_path, file_count
		) VALUES ($1, $2, 'claude', 'provider', '', $3, $4, 'seeded', $5, 2)
	`, workspaceID, runtimeID, key, name, sourcePath); err != nil {
		t.Fatalf("seed discovery row: %v", err)
	}
}

func countCatalogDiscoveries(t *testing.T, workspaceID, runtimeID string) int {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM runtime_skill_discovery WHERE workspace_id = $1 AND runtime_id = $2
	`, workspaceID, runtimeID).Scan(&count); err != nil {
		t.Fatalf("count discovery rows: %v", err)
	}
	return count
}

// A completed discovery report must land in the workspace index, and a
// follow-up report that no longer lists a key must prune it — the catalog may
// only advertise skills the runtime still reports.
func TestReportLocalSkillListResultSyncsDiscoveryIndex(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID := createRuntimeLocalSkillTestRuntime(t, testUserID)
	t.Cleanup(func() {
		cleanupSkillCatalogFixtures(t, testWorkspaceID, []string{runtimeID})
	})

	initiate := func() string {
		w := httptest.NewRecorder()
		req := withURLParams(
			newRequestAsUser(testUserID, http.MethodPost, "/api/runtimes/"+runtimeID+"/local-skills", nil),
			"runtimeId", runtimeID,
		)
		testHandler.InitiateListLocalSkills(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("InitiateListLocalSkills: expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var listReq RuntimeLocalSkillListRequest
		if err := json.NewDecoder(w.Body).Decode(&listReq); err != nil {
			t.Fatalf("decode list request: %v", err)
		}
		return listReq.ID
	}

	// Each discovery cycle carries its own request: a completed request is
	// terminal and a re-report against it is ignored by design.
	report := func(requestID string, skills []map[string]any) {
		w := httptest.NewRecorder()
		req := withURLParams(
			newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/local-skills/"+requestID+"/result",
				map[string]any{"status": "completed", "skills": skills}, testWorkspaceID, "catalog-test-daemon"),
			"runtimeId", runtimeID,
			"requestId", requestID,
		)
		testHandler.ReportLocalSkillListResult(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("ReportLocalSkillListResult: expected 200, got %d: %s", w.Code, w.Body.String())
		}
	}

	skillBody := func(key, name string) map[string]any {
		return map[string]any{"key": key, "name": name, "source_path": "~/.agents/skills/" + key, "provider": "claude", "file_count": 2}
	}

	report(initiate(), []map[string]any{skillBody("alpha", "catalog-test-alpha"), skillBody("beta", "catalog-test-beta")})
	if got := countCatalogDiscoveries(t, testWorkspaceID, runtimeID); got != 2 {
		t.Fatalf("after first report: discovery rows = %d, want 2", got)
	}

	// Second cycle drops "beta": the index must prune it.
	report(initiate(), []map[string]any{skillBody("alpha", "catalog-test-alpha")})
	if got := countCatalogDiscoveries(t, testWorkspaceID, runtimeID); got != 1 {
		t.Fatalf("after pruning report: discovery rows = %d, want 1", got)
	}
	var key string
	if err := testPool.QueryRow(context.Background(),
		`SELECT key FROM runtime_skill_discovery WHERE workspace_id = $1 AND runtime_id = $2`,
		testWorkspaceID, runtimeID).Scan(&key); err != nil || key != "alpha" {
		t.Fatalf("surviving discovery key = %q (err=%v), want alpha", key, err)
	}
}

// The catalog unions cataloged skills with discovery sightings, classifies
// the source, and never leaks another workspace's rows.
//
// 有效断言判据：跨工作区隔离断言以「他区行确实在表里」为前提——隔离谓词
// （WHERE workspace_id = $1）一旦被移除，该断言必然失败。
func TestListSkillCatalogUnionsAndIsolates(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	runtimeID := createRuntimeLocalSkillTestRuntime(t, testUserID)
	t.Cleanup(func() {
		cleanupSkillCatalogFixtures(t, testWorkspaceID, []string{runtimeID})
	})

	seedCatalogDiscovery(t, testWorkspaceID, runtimeID, "local-only", "catalog-test-collide", "~/.agents/skills/local-only")

	var wsSkillID, rtSkillID, pluginSkillID, collideSkillID string
	for _, s := range []struct {
		name   string
		config string
		plugin bool
	}{
		{name: "catalog-test-workspace", config: "{}", plugin: false},
		{name: "catalog-test-imported", config: `{"origin":{"type":"runtime_local"}}`, plugin: false},
		{name: "catalog-test-plugin", config: "{}", plugin: true},
		{name: "catalog-test-collide", config: "{}", plugin: false},
	} {
		var id string
		var err error
		if s.plugin {
			err = testPool.QueryRow(context.Background(), `
				INSERT INTO skill (workspace_id, name, description, content, config, plugin_installation_id)
				VALUES ($1, $2, '', '', $3::jsonb, gen_random_uuid())
				RETURNING id
			`, testWorkspaceID, s.name, s.config).Scan(&id)
		} else {
			err = testPool.QueryRow(context.Background(), `
				INSERT INTO skill (workspace_id, name, description, content, config)
				VALUES ($1, $2, '', '', $3::jsonb)
				RETURNING id
			`, testWorkspaceID, s.name, s.config).Scan(&id)
		}
		if err != nil {
			t.Fatalf("seed skill %s: %v", s.name, err)
		}
		switch s.name {
		case "catalog-test-workspace":
			wsSkillID = id
		case "catalog-test-imported":
			rtSkillID = id
		case "catalog-test-plugin":
			pluginSkillID = id
		case "catalog-test-collide":
			collideSkillID = id
		}
	}

	// Another workspace with its own discovery sighting must never appear.
	var otherWorkspaceID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ('catalog-test-other', 'catalog-test-other', '', 'CTO')
		RETURNING id
	`).Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("create other workspace: %v", err)
	}
	t.Cleanup(func() {
		cleanupSkillCatalogFixtures(t, testWorkspaceID, []string{runtimeID})
		testPool.Exec(context.Background(), `DELETE FROM runtime_skill_discovery WHERE workspace_id = $1`, otherWorkspaceID)
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, otherWorkspaceID)
	})
	seedCatalogDiscovery(t, otherWorkspaceID, runtimeID, "foreign", "catalog-test-foreign", "~/.agents/skills/foreign")

	w := httptest.NewRecorder()
	req := withURLParams(
		newRequestAsUser(testUserID, http.MethodGet, "/api/skills/catalog", nil),
	)
	testHandler.ListSkillCatalog(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListSkillCatalog: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var entries []SkillCatalogEntry
	if err := json.NewDecoder(w.Body).Decode(&entries); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}

	byName := make(map[string]SkillCatalogEntry)
	for _, e := range entries {
		byName[e.Name] = e
	}

	// Source classification: workspace-authored, runtime-imported (config
	// origin), and plugin-contributed are three distinct sources.
	if e := byName["catalog-test-workspace"]; e.Kind != "skill" || e.Source != "workspace" || e.ID != wsSkillID {
		t.Fatalf("workspace entry = %+v, want kind=skill source=workspace id=%s", e, wsSkillID)
	}
	if e := byName["catalog-test-imported"]; e.Kind != "skill" || e.Source != "runtime" || e.ID != rtSkillID {
		t.Fatalf("runtime-imported entry = %+v, want kind=skill source=runtime id=%s", e, rtSkillID)
	}
	if e := byName["catalog-test-plugin"]; e.Kind != "skill" || e.Source != "plugin" || e.ID != pluginSkillID {
		t.Fatalf("plugin entry = %+v, want kind=skill source=plugin id=%s", e, pluginSkillID)
	}

	// Discovery sighting: metadata-only row, source=runtime. Its name
	// collides with an authored skill, so MatchingSkillID points at the row
	// an import would hit instead of promising a clean create.
	discovered := byName["catalog-test-collide"]
	if discovered.Kind != "discovery" || discovered.Source != "runtime" || discovered.Key != "local-only" {
		t.Fatalf("discovery entry = %+v, want kind=discovery source=runtime key=local-only", discovered)
	}
	if discovered.MatchingSkillID != collideSkillID {
		t.Fatalf("discovery matching_skill_id = %q, want the colliding skill %q", discovered.MatchingSkillID, collideSkillID)
	}

	// Isolation (judgment criteria in the doc comment above): the foreign
	// sighting exists in runtime_skill_discovery but must not surface here.
	if e, ok := byName["catalog-test-foreign"]; ok {
		t.Fatalf("foreign workspace row leaked into catalog: %+v", e)
	}
	var foreignRows int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM runtime_skill_discovery WHERE workspace_id = $1`, otherWorkspaceID).Scan(&foreignRows); err != nil || foreignRows != 1 {
		t.Fatalf("precondition broken: foreign discovery rows = %d (err=%v), want 1", foreignRows, err)
	}

	// The same on-disk skill seen by two runtimes collapses to one entry.
	rt2 := createRuntimeLocalSkillTestRuntime(t, testUserID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM runtime_skill_discovery WHERE runtime_id = $1`, rt2)
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, rt2)
	})
	seedCatalogDiscovery(t, testWorkspaceID, rt2, "local-only", "catalog-test-collide", "~/.agents/skills/local-only")

	w2 := httptest.NewRecorder()
	testHandler.ListSkillCatalog(w2, withURLParams(
		newRequestAsUser(testUserID, http.MethodGet, "/api/skills/catalog", nil),
	))
	if w2.Code != http.StatusOK {
		t.Fatalf("ListSkillCatalog (dedupe): expected 200, got %d", w2.Code)
	}
	var deduped []SkillCatalogEntry
	if err := json.NewDecoder(w2.Body).Decode(&deduped); err != nil {
		t.Fatalf("decode catalog (dedupe): %v", err)
	}
	sighted := 0
	for _, e := range deduped {
		if e.Kind == "discovery" && e.Name == "catalog-test-collide" {
			sighted++
		}
	}
	if sighted != 1 {
		t.Fatalf("deduped discovery entries = %d, want 1", sighted)
	}
}

// The sync trigger fans discovery requests out to every ONLINE runtime of the
// workspace only; offline runtimes are not pinged.
func TestSyncSkillCatalogTriggersOnlineRuntimes(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	online := createRuntimeLocalSkillTestRuntime(t, testUserID)
	offline := createRuntimeLocalSkillTestRuntime(t, testUserID)
	t.Cleanup(func() {
		cleanupSkillCatalogFixtures(t, testWorkspaceID, []string{online, offline})
	})
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent_runtime SET status = 'offline' WHERE id = $1`, offline); err != nil {
		t.Fatalf("flip runtime offline: %v", err)
	}

	w := httptest.NewRecorder()
	testHandler.SyncSkillCatalog(w, withURLParams(
		newRequestAsUser(testUserID, http.MethodPost, "/api/skills/catalog/sync", nil),
	))
	if w.Code != http.StatusOK {
		t.Fatalf("SyncSkillCatalog: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Triggered int `json:"triggered"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode sync response: %v", err)
	}
	// Leftover online runtimes from other tests sharing this workspace can
	// inflate the count; the per-runtime pending checks below are the
	// discriminating assertions.
	if resp.Triggered < 1 {
		t.Fatalf("triggered = %d, want at least the online fixture runtime", resp.Triggered)
	}

	store, ok := testHandler.LocalSkillListStore.(*InMemoryLocalSkillListStore)
	if !ok {
		t.Fatalf("unexpected list store type %T", testHandler.LocalSkillListStore)
	}
	pending, err := store.HasPending(context.Background(), online)
	if err != nil || !pending {
		t.Fatalf("online runtime HasPending = %v (err=%v), want true", pending, err)
	}
	pending, err = store.HasPending(context.Background(), offline)
	if err != nil || pending {
		t.Fatalf("offline runtime HasPending = %v (err=%v), want false", pending, err)
	}
}

// A user outside the workspace cannot read the catalog.
func TestListSkillCatalogRejectsNonMember(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	var outsiderID string
	email := fmt.Sprintf("skill-catalog-outsider-%d@multica.ai", time.Now().UnixNano())
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO "user" (name, email) VALUES ('Skill Catalog Outsider', $1) RETURNING id
	`, email).Scan(&outsiderID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, outsiderID)
	})

	w := httptest.NewRecorder()
	testHandler.ListSkillCatalog(w, withURLParams(
		newRequestAsUser(outsiderID, http.MethodGet, "/api/skills/catalog", nil),
	))
	if w.Code == http.StatusOK {
		t.Fatalf("non-member catalog read unexpectedly succeeded: %s", w.Body.String())
	}
}
