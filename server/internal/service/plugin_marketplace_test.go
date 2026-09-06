package service

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func marketplaceListingRow(t *testing.T, versionSuffix string, manifest string) db.ListMarketplacePluginListingsRow {
	t.Helper()
	packageID, err := util.ParseUUID("11111111-1111-4111-8111-1111111111" + versionSuffix)
	if err != nil {
		t.Fatalf("parse package id: %v", err)
	}
	versionID, err := util.ParseUUID("22222222-2222-4222-8222-2222222222" + versionSuffix)
	if err != nil {
		t.Fatalf("parse version id: %v", err)
	}
	publisherID, err := util.ParseUUID("33333333-3333-4333-8333-3333333333" + versionSuffix)
	if err != nil {
		t.Fatalf("parse publisher id: %v", err)
	}
	return db.ListMarketplacePluginListingsRow{
		PackageID:              packageID,
		VersionID:              versionID,
		PublisherWorkspaceID:   publisherID,
		ListedAt:               pgtype.Timestamptz{Time: time.Unix(0, 0).UTC(), Valid: true},
		PluginKey:              "com.example.hello",
		Name:                   "mutable package name",
		Version:                "1.0.0",
		Manifest:               []byte(manifest),
		Digest:                 "sha256:" + versionSuffix,
		PublisherWorkspaceSlug: "publisher-" + versionSuffix,
	}
}

// A single unreadable manifest reaches the catalog only through a database,
// migration or operations fault, but failing the whole request would let one
// bad row poison the directory for every workspace in the deployment.
func TestMarketplaceSummariesSkipUnreadableManifestsAndKeepHealthyRows(t *testing.T) {
	rows := []db.ListMarketplacePluginListingsRow{
		marketplaceListingRow(t, "01", `{"manifest_version": 1, "key": "com.example.broken"}`),
		marketplaceListingRow(t, "02", testManifestJSON),
	}

	plugins := marketplaceSummaries(rows)

	if len(plugins) != 1 {
		t.Fatalf("catalog entries = %d, want 1: %+v", len(plugins), plugins)
	}
	healthy := plugins[0]
	if healthy.VersionID != uuidString(rows[1].VersionID) {
		t.Fatalf("version id = %q, want %q", healthy.VersionID, uuidString(rows[1].VersionID))
	}
	if healthy.PackageID != uuidString(rows[1].PackageID) {
		t.Fatalf("package id = %q, want %q", healthy.PackageID, uuidString(rows[1].PackageID))
	}
	// The catalog shows the immutable listed manifest, not the mutable package row.
	if healthy.Name != "Hello Panel" {
		t.Fatalf("name = %q, want the listed manifest name", healthy.Name)
	}
	if healthy.PluginKey != rows[1].PluginKey || healthy.Version != rows[1].Version || healthy.Digest != rows[1].Digest {
		t.Fatalf("identity fields drifted: %+v", healthy)
	}
	if healthy.PublisherWorkspaceSlug != rows[1].PublisherWorkspaceSlug {
		t.Fatalf("publisher slug = %q, want %q", healthy.PublisherWorkspaceSlug, rows[1].PublisherWorkspaceSlug)
	}
}

func TestMarketplaceSummariesReturnEmptyCatalogWhenEveryManifestIsUnreadable(t *testing.T) {
	rows := []db.ListMarketplacePluginListingsRow{
		marketplaceListingRow(t, "01", `not json`),
		marketplaceListingRow(t, "02", ``),
	}

	if plugins := marketplaceSummaries(rows); len(plugins) != 0 {
		t.Fatalf("catalog entries = %+v, want none", plugins)
	}
}
