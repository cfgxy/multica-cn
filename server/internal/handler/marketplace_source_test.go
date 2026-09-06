package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
)

// requireCatalogSourceCheck gates the two tests that reach the public internet
// to fetch the catalog's real skill sources.
func requireCatalogSourceCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("MULTICA_RUN_CATALOG_SOURCE_CHECK") != "1" {
		t.Skip("set MULTICA_RUN_CATALOG_SOURCE_CHECK=1 to fetch the catalog's real skill sources over the network")
	}
}

// TestMarketplaceCatalogSkillSourcesFetch drives the real import fetcher
// against every curated skill source. It is the only check that can catch the
// failure this test file was added for: the upstream repository moved its
// document skills out of document-skills/<name>, the catalog kept the old
// paths, and every marketplace skill install returned 502 while the listing
// still advertised them. No amount of offline shape checking sees that — only
// asking the upstream does.
//
// It reaches the public internet, so it is opt-in rather than part of the
// default suite; the offline structural guard is
// TestMarketplaceCatalog_SkillSourcesAreFetchableTreeURLs in internal/service.
func TestMarketplaceCatalogSkillSourcesFetch(t *testing.T) {
	requireCatalogSourceCheck(t)
	items, err := service.MarketplaceCatalog()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	var checked int
	for _, item := range items {
		if item.Kind != service.MarketplaceKindSkill {
			continue
		}
		checked++
		t.Run(item.Name, func(t *testing.T) {
			imported, status, msg := fetchSkillFromURL(context.Background(), item.SourceURL)
			if msg != "" {
				t.Fatalf("fetching %s failed with %d: %s", item.SourceURL, status, msg)
			}
			if strings.TrimSpace(imported.content) == "" {
				t.Fatalf("%s resolved but its SKILL.md is empty", item.SourceURL)
			}
		})
	}
	if checked == 0 {
		t.Fatal("catalog carries no skill entries to fetch")
	}
}

// firstCatalogSkill returns a curated skill entry to install end to end.
func firstCatalogSkill(t *testing.T) service.MarketplaceItem {
	t.Helper()
	items, err := service.MarketplaceCatalog()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	for _, item := range items {
		if item.Kind == service.MarketplaceKindSkill {
			return item
		}
	}
	t.Fatal("catalog carries no skill entries")
	return service.MarketplaceItem{}
}

// TestInstallMarketplaceSkill_RealSourceClosesTheLoop installs a curated skill
// through the public install endpoint against its real upstream source. The
// stale-path defect this covers passed every offline test in the suite and
// only showed up here: the install returned 502 because the source 404'd, and
// the conflict path stayed permanently unreachable behind it.
//
// A second install of the same entry must reach the on_conflict path and
// answer 409 — the marketplace installer hands finishSkillImport
// importOnConflictFail, so an already-installed skill is a conflict, not
// another upstream failure wearing the same status code.
func TestInstallMarketplaceSkill_RealSourceClosesTheLoop(t *testing.T) {
	requireCatalogSourceCheck(t)
	if testHandler == nil {
		t.Skip("database not available")
	}
	item := firstCatalogSkill(t)
	t.Cleanup(func() {
		ctx := context.Background()
		testPool.Exec(ctx, `DELETE FROM skill_file WHERE skill_id IN (SELECT id FROM skill WHERE workspace_id = $1 AND name = $2)`, testWorkspaceID, item.Name)
		testPool.Exec(ctx, `DELETE FROM skill WHERE workspace_id = $1 AND name = $2`, testWorkspaceID, item.Name)
	})

	code, raw := installMarketplaceForTest(t, map[string]any{"key": item.Key}, nil)
	if code != http.StatusCreated {
		t.Fatalf("installing %s from %s: expected 201, got %d: %s", item.Key, item.SourceURL, code, raw)
	}
	var result SkillImportResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode install response: %v", err)
	}
	if result.Status != "created" || result.Skill == nil {
		t.Fatalf("install did not create a skill: %s", raw)
	}
	// The catalog names the entry; a bundle that renamed itself would leave the
	// listing unable to report the install it just performed.
	if result.Skill.Name != item.Name {
		t.Fatalf("installed skill is named %q, want the catalog name %q", result.Skill.Name, item.Name)
	}
	if strings.TrimSpace(result.Skill.Content) == "" {
		t.Fatal("installed skill has no SKILL.md content")
	}

	// The listing must now report it, which is what turns the install button
	// into a link to the installed thing.
	_, items, listRaw := listMarketplaceForTest(t, "kind=skill&q="+item.Name, nil)
	var reported bool
	for _, listed := range items {
		if listed.Key == item.Key && listed.Installed {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("listing does not report %s as installed: %s", item.Key, listRaw)
	}

	code, raw = installMarketplaceForTest(t, map[string]any{"key": item.Key}, nil)
	if code != http.StatusConflict {
		t.Fatalf("re-installing %s: expected 409 from the conflict path, got %d: %s", item.Key, code, raw)
	}

	var duplicates int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM skill WHERE workspace_id = $1 AND name = $2`,
		testWorkspaceID, item.Name).Scan(&duplicates); err != nil {
		t.Fatalf("count skills: %v", err)
	}
	if duplicates != 1 {
		t.Fatalf("the conflicting re-install left %d rows named %q", duplicates, item.Name)
	}
}
