package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestSkillVersionCreateEditRestoreKeepsHistory(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}

	var created SkillWithFilesResponse
	testutil.Call(t, testHandler.CreateSkill, newRequest(http.MethodPost, "/api/skills", map[string]any{
		"name": "version-history-test", "content": "first", "files": []map[string]string{{"path": "references/example.md", "content": "alpha"}},
	})).Want(http.StatusCreated).JSON(&created)
	id := created.ID
	dbfx.Cleanup(t, `DELETE FROM skill_version WHERE skill_id = $1`, id)
	dbfx.Cleanup(t, `DELETE FROM skill WHERE id = $1`, id)

	var versions []SkillVersionResponse
	testutil.Call(t, testHandler.ListSkillVersions,
		withURLParam(newRequest(http.MethodGet, "/api/skills/"+id+"/versions", nil), "id", id),
	).Want(http.StatusOK).JSON(&versions)
	if len(versions) != 1 || versions[0].Version != 1 || versions[0].Content != "" || versions[0].Files != nil {
		t.Fatalf("list must return one metadata-only version: %+v", versions)
	}
	var first SkillVersionResponse
	testutil.Call(t, testHandler.GetSkillVersion,
		withURLParams(newRequest(http.MethodGet, "/api/skills/"+id+"/versions/1", nil), "id", id, "version", "1"),
	).Want(http.StatusOK).JSON(&first)
	if first.Content != "first" || len(first.Files) != 1 || first.Files[0].Content != "alpha" {
		t.Fatalf("first version must snapshot primary content and supporting files: %+v", first)
	}

	testutil.Call(t, testHandler.UpdateSkill,
		withURLParam(newRequest(http.MethodPut, "/api/skills/"+id, map[string]any{
			"content": "second", "files": []map[string]string{{"path": "references/example.md", "content": "beta"}},
		}), "id", id),
	).Want(http.StatusOK)
	var result struct {
		Version int32 `json:"version"`
	}
	testutil.Call(t, testHandler.RestoreSkillVersion,
		withURLParams(newRequest(http.MethodPost, "/api/skills/"+id+"/versions/1/restore", nil), "id", id, "version", "1"),
	).Want(http.StatusOK).JSON(&result)
	if result.Version != 3 {
		t.Fatalf("restore returned version %d, want appended version 3", result.Version)
	}

	testutil.Call(t, testHandler.ListSkillVersions,
		withURLParam(newRequest(http.MethodGet, "/api/skills/"+id+"/versions", nil), "id", id),
	).Want(http.StatusOK).JSON(&versions)
	if len(versions) != 3 || versions[0].Version != 3 || versions[0].Source != "restore" {
		t.Fatalf("restoring must append a new version: %+v", versions)
	}
	if versions[1].Version != 2 || versions[2].Version != 1 {
		t.Fatalf("older versions must stay unchanged: %+v", versions)
	}
	var restored SkillVersionResponse
	testutil.Call(t, testHandler.GetSkillVersion,
		withURLParams(newRequest(http.MethodGet, "/api/skills/"+id+"/versions/3", nil), "id", id, "version", "3"),
	).Want(http.StatusOK).JSON(&restored)
	if restored.Content != "first" || len(restored.Files) != 1 || restored.Files[0].Content != "alpha" {
		t.Fatalf("restored version differs from v1: %+v", restored)
	}
	var second SkillVersionResponse
	testutil.Call(t, testHandler.GetSkillVersion,
		withURLParams(newRequest(http.MethodGet, "/api/skills/"+id+"/versions/2", nil), "id", id, "version", "2"),
	).Want(http.StatusOK).JSON(&second)
	if second.Content != "second" || second.Files[0].Content != "beta" || first.Content != "first" {
		t.Fatalf("historical snapshots changed after restore: v1=%+v v2=%+v", first, second)
	}
}

func TestSkillVersionFileChangeIsRecorded(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	var created SkillWithFilesResponse
	testutil.Call(t, testHandler.CreateSkill, newRequest(http.MethodPost, "/api/skills", map[string]any{
		"name": "version-file-test", "content": "body",
	})).Want(http.StatusCreated).JSON(&created)
	dbfx.Cleanup(t, `DELETE FROM skill_version WHERE skill_id = $1`, created.ID)
	dbfx.Cleanup(t, `DELETE FROM skill WHERE id = $1`, created.ID)

	var file SkillFileResponse
	testutil.Call(t, testHandler.UpsertSkillFile,
		withURLParam(newRequest(http.MethodPut, "/api/skills/"+created.ID+"/files", map[string]string{
			"path": "references/usage.md", "content": "new file",
		}), "id", created.ID),
	).Want(http.StatusOK).JSON(&file)
	if got := dbfx.Count(t, `SELECT count(*) FROM skill_version WHERE skill_id = $1`, created.ID); got != 2 {
		t.Fatalf("file upsert version count = %d, want 2", got)
	}
	testutil.Call(t, testHandler.DeleteSkillFile,
		withURLParams(newRequest(http.MethodDelete, "/api/skills/"+created.ID+"/files/"+file.ID, nil),
			"id", created.ID, "fileId", file.ID),
	).Want(http.StatusNoContent)
	var v SkillVersionResponse
	testutil.Call(t, testHandler.GetSkillVersion,
		withURLParams(newRequest(http.MethodGet, "/api/skills/"+created.ID+"/versions/3", nil),
			"id", created.ID, "version", "3"),
	).Want(http.StatusOK).JSON(&v)
	if v.Version != 3 || len(v.Files) != 0 {
		t.Fatalf("file deletion must produce a clean v3 snapshot: %+v", v)
	}
}

func TestSkillVersionRestoreRequiresOwner(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	var created SkillWithFilesResponse
	testutil.Call(t, testHandler.CreateSkill, newRequest(http.MethodPost, "/api/skills", map[string]any{
		"name": "version-owner-test", "content": "original",
	})).Want(http.StatusCreated).JSON(&created)
	dbfx.Cleanup(t, `DELETE FROM skill_version WHERE skill_id = $1`, created.ID)
	dbfx.Cleanup(t, `DELETE FROM skill WHERE id = $1`, created.ID)

	memberID := dbfx.User(t, "Skill Version Member", "skill-version-member@multica.ai")
	dbfx.Member(t, testWorkspaceID, memberID, "member")
	req := withURLParams(newRequest(http.MethodPost, "/api/skills/"+created.ID+"/versions/1/restore", nil),
		"id", created.ID, "version", "1")
	req.Header.Set("X-User-ID", memberID)
	testutil.Call(t, testHandler.RestoreSkillVersion, req).Want(http.StatusForbidden)
	if got := dbfx.Count(t, `SELECT count(*) FROM skill_version WHERE skill_id = $1`, created.ID); got != 1 {
		t.Fatalf("unauthorized restore changed history: %d versions", got)
	}
}

func TestSkillDeleteRemovesVersionHistory(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	var created SkillWithFilesResponse
	testutil.Call(t, testHandler.CreateSkill, newRequest(http.MethodPost, "/api/skills", map[string]any{
		"name": "version-delete-test", "content": "first",
	})).Want(http.StatusCreated).JSON(&created)
	dbfx.Cleanup(t, `DELETE FROM skill_version WHERE skill_id = $1`, created.ID)
	dbfx.Cleanup(t, `DELETE FROM skill WHERE id = $1`, created.ID)

	testutil.Call(t, testHandler.DeleteSkill,
		withURLParam(newRequest(http.MethodDelete, "/api/skills/"+created.ID, nil), "id", created.ID),
	).Want(http.StatusNoContent)
	if got := dbfx.Count(t, `SELECT count(*) FROM skill_version WHERE skill_id = $1`, created.ID); got != 0 {
		t.Fatalf("deleted skill left %d versions", got)
	}
}

func TestSkillVersionsAreWorkspaceScoped(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	var created SkillWithFilesResponse
	testutil.Call(t, testHandler.CreateSkill, newRequest(http.MethodPost, "/api/skills", map[string]any{
		"name": "version-cross-workspace-test", "content": "private",
	})).Want(http.StatusCreated).JSON(&created)
	dbfx.Cleanup(t, `DELETE FROM skill_version WHERE skill_id = $1`, created.ID)
	dbfx.Cleanup(t, `DELETE FROM skill WHERE id = $1`, created.ID)

	otherWorkspaceID := dbfx.Workspace(t, "Other Skill Workspace", "other-skill-workspace")
	for _, tc := range []struct {
		method  string
		path    string
		handler http.HandlerFunc
	}{
		{http.MethodGet, "/api/skills/" + created.ID + "/versions", testHandler.ListSkillVersions},
		{http.MethodGet, "/api/skills/" + created.ID + "/versions/1", testHandler.GetSkillVersion},
		{http.MethodPost, "/api/skills/" + created.ID + "/versions/1/restore", testHandler.RestoreSkillVersion},
	} {
		req := newRequest(tc.method, tc.path, nil)
		req.Header.Set("X-Workspace-ID", otherWorkspaceID)
		req = withURLParams(req, "id", created.ID, "version", "1")
		testutil.Call(t, tc.handler, req).Want(http.StatusNotFound)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM skill_version WHERE skill_id = $1`, created.ID); got != 1 {
		t.Fatalf("cross-workspace requests changed history: %d versions", got)
	}
}
