package handler

// Project creation must seed the project's v1 prompt_version baseline
// (RUYI-213). Migration 922 backfilled only the projects that existed when
// self-evolution shipped; projects created after it had no version history at
// all, so "view history" and "diff" had nothing to anchor on and the
// instructions the project launched with could not be recovered.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func cleanupProjectAndVersions(t *testing.T, projectID string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM prompt_version WHERE scope = 'project' AND scope_id = $1`, projectID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID)
	})
}

func projectBaseline(t *testing.T, projectID string) (content, sha, source string) {
	t.Helper()
	dbfx.QueryRow(t, `SELECT content, content_sha256, source FROM prompt_version
		WHERE scope = 'project' AND scope_id = $1 AND version = 1`, projectID).Scan(&content, &sha, &source)
	return content, sha, source
}

func TestCreateProjectSeedsPromptVersionV1(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const instructions = "本项目的 agent 指令基线。"

	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title":        "prompt baseline project",
		"instructions": instructions,
	}))
	created := decodeProject(t, w, http.StatusCreated)
	cleanupProjectAndVersions(t, created.ID)

	content, sha, source := projectBaseline(t, created.ID)
	if content != instructions {
		t.Fatalf("v1 content = %q, want the project's instructions %q", content, instructions)
	}
	if source != "import" {
		t.Fatalf("v1 source = %q, want %q", source, "import")
	}
	// The digest must describe the content actually stored; a mismatch would
	// make every later diff and integrity check read against a wrong hash.
	if sha != sha256Hex(instructions) {
		t.Fatalf("v1 content_sha256 = %q, want %q", sha, sha256Hex(instructions))
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM prompt_version WHERE scope = 'project' AND scope_id = $1`, created.ID); n != 1 {
		t.Fatalf("prompt_version rows = %d after create, want exactly the v1 baseline", n)
	}
}

// A project created without instructions still needs a baseline: the tier's
// history must start at v1 with empty content, not with no row at all.
func TestCreateProjectWithoutInstructionsSeedsEmptyV1(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "prompt baseline no instructions",
	}))
	created := decodeProject(t, w, http.StatusCreated)
	cleanupProjectAndVersions(t, created.ID)

	content, sha, _ := projectBaseline(t, created.ID)
	if content != "" {
		t.Fatalf("v1 content = %q, want empty", content)
	}
	if sha != sha256Hex("") {
		t.Fatalf("v1 content_sha256 = %q, want the digest of empty content", sha)
	}
}

// The bundled create path (project + resources in one transaction) is a
// separate branch in CreateProject and needs its own baseline assertion.
func TestCreateProjectWithResourcesSeedsPromptVersionV1(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const instructions = "带资源的项目指令。"

	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title":        "prompt baseline with resources",
		"instructions": instructions,
		"resources": []map[string]any{
			{"resource_type": "github_repo", "resource_ref": map[string]any{"url": "https://github.com/cfgxy/multica.git"}},
		},
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("create with resources: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	created := decodeProject(t, w, http.StatusCreated)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project_resource WHERE project_id = $1`, created.ID)
	})
	cleanupProjectAndVersions(t, created.ID)

	content, _, _ := projectBaseline(t, created.ID)
	if content != instructions {
		t.Fatalf("v1 content = %q, want the project's instructions %q", content, instructions)
	}
}

// Idempotency of the seed query itself: running it a second time against a
// project that already has a v1 must not insert a duplicate and must not
// overwrite the existing baseline with the tier's current content. This is the
// property the 933 gap-backfill migration relies on for re-runnability.
func TestSeedPromptVersionV1LeavesExistingBaselineUntouched(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const original = "创建时的指令。"

	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title":        "prompt baseline idempotency",
		"instructions": original,
	}))
	created := decodeProject(t, w, http.StatusCreated)
	cleanupProjectAndVersions(t, created.ID)

	// Instructions moved on since creation; a non-idempotent re-seed would
	// stamp this newer text over v1 and lose the original baseline.
	dbfx.Exec(t, `UPDATE project SET instructions = $2 WHERE id = $1`, created.ID, "后来改过的指令。")

	if err := seedPromptVersionV1(context.Background(), testHandler.Queries, promptVersionScopeProject,
		parseUUID(testWorkspaceID), parseUUID(created.ID), "后来改过的指令。"); err != nil {
		t.Fatalf("re-seed: %v", err)
	}

	content, _, _ := projectBaseline(t, created.ID)
	if content != original {
		t.Fatalf("v1 content = %q after re-seed, want the original baseline %q preserved", content, original)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM prompt_version WHERE scope = 'project' AND scope_id = $1`, created.ID); n != 1 {
		t.Fatalf("prompt_version rows = %d after re-seed, want the single baseline (no duplicate)", n)
	}
}
