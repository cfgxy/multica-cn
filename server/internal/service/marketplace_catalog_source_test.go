package service

import (
	"net/url"
	"strings"
	"testing"
)

// A curated skill entry is only useful if the import path can still find a
// SKILL.md behind it. The upstream repository moved every document skill from
// document-skills/<name> to skills/<name>, and because nothing here checked
// the shape, four entries kept pointing at a directory that had stopped
// existing — the catalog listed them, the install fetched a 404, and the user
// saw a 502.
//
// This is the offline half of that guard: it pins the structural contract the
// fetcher relies on, so a hand-edit that drops the ref or the directory fails
// the build. The reachability half lives in
// TestMarketplaceCatalogSkillSourcesFetch (handler package), which drives the
// real fetcher against the network.
func TestMarketplaceCatalog_SkillSourcesAreFetchableTreeURLs(t *testing.T) {
	items, err := MarketplaceCatalog()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	var checked int
	for _, item := range items {
		if item.Kind != MarketplaceKindSkill {
			continue
		}
		checked++
		parsed, err := url.Parse(item.SourceURL)
		if err != nil {
			t.Errorf("item %q has an unparseable source_url %q: %v", item.Key, item.SourceURL, err)
			continue
		}
		if parsed.Scheme != "https" {
			t.Errorf("item %q source_url is not https: %q", item.Key, item.SourceURL)
		}
		host := strings.ToLower(parsed.Hostname())
		// detectImportSource only routes these three hosts; anything else is
		// rejected before a single request goes out.
		switch host {
		case "github.com", "clawhub.ai", "skills.sh":
		default:
			t.Errorf("item %q source_url host %q is not an import source the fetcher supports", item.Key, host)
			continue
		}
		if host != "github.com" {
			continue
		}
		// github.com/{owner}/{repo}/tree/{ref}/{path...}: the fetcher needs the
		// directory segments to build the raw SKILL.md URL, and a repo-root URL
		// would only work for a single-skill repository.
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) < 5 || parts[2] != "tree" {
			t.Errorf("item %q source_url is not a github tree URL with a ref and a directory: %q", item.Key, item.SourceURL)
			continue
		}
		dir := parts[len(parts)-1]
		// The catalog renames the imported bundle to item.Name, so a directory
		// that no longer matches is the signal that the entry was moved or
		// repointed without the listing being updated with it.
		if dir != item.Name {
			t.Errorf("item %q points at directory %q but installs as %q", item.Key, dir, item.Name)
		}
	}
	if checked == 0 {
		t.Fatal("catalog carries no skill entries to check")
	}
}
