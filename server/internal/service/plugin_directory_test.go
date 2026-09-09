package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// The instance directory, exercised against real Postgres.
//
// Live rather than mocked because the property under test is an authorization
// rule expressed as a join: a version from another workspace is installable
// only while its package is listed and the version is not withdrawn. A fake
// Queries would assert the rule against a reimplementation of itself, and the
// SQL is the thing that can be wrong.

// directoryFixture is one publishing workspace with one package and one
// version, plus a second workspace standing in for the rest of the instance.
type directoryFixture struct {
	service   *service.PluginService
	publisher pgtype.UUID
	consumer  pgtype.UUID
	packageID pgtype.UUID
	versionID string
	userID    pgtype.UUID
}

func newDirectoryFixture(t *testing.T) directoryFixture {
	t.Helper()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set; skipping live-Postgres plugin directory test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	t.Cleanup(pool.Close)

	var schemaReady bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('plugin_package') IS NOT NULL`).Scan(&schemaReady); err != nil {
		t.Fatalf("check plugin schema: %v", err)
	}
	if !schemaReady {
		t.Skip("plugin migrations are not applied")
	}

	// Everything runs in a transaction that is never committed, so the test
	// leaves the database exactly as it found it and two runs cannot collide.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	queries := db.New(tx)

	suffix := time.Now().UnixNano()
	newWorkspace := func(label string) pgtype.UUID {
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id`,
			label, fmt.Sprintf("%s-%d", label, suffix)).Scan(&id); err != nil {
			t.Fatalf("seed workspace %s: %v", label, err)
		}
		parsed, err := util.ParseUUID(id)
		if err != nil {
			t.Fatalf("parse workspace id: %v", err)
		}
		return parsed
	}
	publisher := newWorkspace("plugin-directory-publisher")
	consumer := newWorkspace("plugin-directory-consumer")

	var userID string
	if err := tx.QueryRow(ctx, `INSERT INTO "user" (email, name) VALUES ($1, 'Directory Publisher') RETURNING id`,
		fmt.Sprintf("directory-%d@example.test", suffix)).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	parsedUser, err := util.ParseUUID(userID)
	if err != nil {
		t.Fatalf("parse user id: %v", err)
	}

	manifest, _ := json.Marshal(map[string]any{"manifest_version": 1})
	pkg, err := queries.CreatePluginPackage(ctx, db.CreatePluginPackageParams{
		WorkspaceID: publisher,
		PluginKey:   fmt.Sprintf("com.example.directory%d", suffix),
		Name:        "Directory",
		CreatedBy:   parsedUser,
	})
	if err != nil {
		t.Fatalf("create package: %v", err)
	}
	version, err := queries.CreatePluginPackageVersion(ctx, db.CreatePluginPackageVersionParams{
		PackageID:   pkg.ID,
		WorkspaceID: publisher,
		Version:     "1.0.0",
		Manifest:    manifest,
		Digest:      strings.Repeat("b", 64),
		SizeBytes:   1,
		PublishedBy: parsedUser,
	})
	if err != nil {
		t.Fatalf("create package version: %v", err)
	}

	return directoryFixture{
		service:   &service.PluginService{Queries: queries},
		publisher: publisher,
		consumer:  consumer,
		packageID: pkg.ID,
		versionID: util.UUIDToString(version.ID),
		userID:    parsedUser,
	}
}

// A package is private until its publisher says otherwise, and private means
// invisible to the rest of the instance — including to an install that already
// knows the version id, because a uuid in a URL is not consent.
func TestPrivatePackageIsNotInstallableElsewhere(t *testing.T) {
	fixture := newDirectoryFixture(t)
	ctx := context.Background()

	if _, err := fixture.service.VersionForWorkspace(ctx, fixture.consumer, fixture.versionID); err == nil {
		t.Fatal("a private package was installable from another workspace — publishing it must not expose it instance-wide")
	}

	packages, err := fixture.service.ListPublicPackages(ctx, fixture.consumer)
	if err != nil {
		t.Fatalf("list directory: %v", err)
	}
	for _, pkg := range packages {
		if pkg.ID == util.UUIDToString(fixture.packageID) {
			t.Fatal("a private package appeared on the instance directory")
		}
	}
}

// Listing is the act that opens a package to the instance, and it is what makes
// a cross-workspace install possible at all.
func TestListedPackageIsInstallableElsewhere(t *testing.T) {
	fixture := newDirectoryFixture(t)
	ctx := context.Background()

	listed, err := fixture.service.SetPackageVisibility(ctx, fixture.publisher, util.UUIDToString(fixture.packageID), true)
	if err != nil {
		t.Fatalf("list package: %v", err)
	}
	if listed.Visibility != "public" {
		t.Fatalf("visibility is %q after listing, want \"public\"", listed.Visibility)
	}

	version, err := fixture.service.VersionForWorkspace(ctx, fixture.consumer, fixture.versionID)
	if err != nil {
		t.Fatalf("install a listed version from another workspace: %v", err)
	}
	if util.UUIDToString(version.ID) != fixture.versionID {
		t.Fatalf("resolved version %s, want %s", util.UUIDToString(version.ID), fixture.versionID)
	}

	packages, err := fixture.service.ListPublicPackages(ctx, fixture.consumer)
	if err != nil {
		t.Fatalf("list directory: %v", err)
	}
	found := false
	for _, pkg := range packages {
		if pkg.ID == util.UUIDToString(fixture.packageID) {
			found = true
			if pkg.PublisherWorkspaceID != util.UUIDToString(fixture.publisher) {
				t.Fatalf("directory entry names publisher %q, want %q", pkg.PublisherWorkspaceID, util.UUIDToString(fixture.publisher))
			}
		}
	}
	if !found {
		t.Fatal("a listed package is missing from the instance directory")
	}
}

// Withdrawal stops new installs from elsewhere without deleting anything, and
// it is reversible. The publisher keeps access throughout: withdrawal is a
// signal to other workspaces, not a lock on their own artifact.
func TestWithdrawalStopsNewInstallsAndIsReversible(t *testing.T) {
	fixture := newDirectoryFixture(t)
	ctx := context.Background()

	if _, err := fixture.service.SetPackageVisibility(ctx, fixture.publisher, util.UUIDToString(fixture.packageID), true); err != nil {
		t.Fatalf("list package: %v", err)
	}
	if _, err := fixture.service.VersionForWorkspace(ctx, fixture.consumer, fixture.versionID); err != nil {
		t.Fatalf("precondition: listed version must install elsewhere: %v", err)
	}

	withdrawn, err := fixture.service.SetVersionWithdrawn(ctx, fixture.publisher, fixture.userID, fixture.versionID, true)
	if err != nil {
		t.Fatalf("withdraw version: %v", err)
	}
	if len(withdrawn.Versions) != 1 || withdrawn.Versions[0].WithdrawnAt == "" {
		t.Fatalf("withdrawal was not reported back to the publisher: %+v", withdrawn.Versions)
	}

	if _, err := fixture.service.VersionForWorkspace(ctx, fixture.consumer, fixture.versionID); err == nil {
		t.Fatal("a withdrawn version was still installable from another workspace")
	}
	// The artifact is untouched — this is what lets the workspaces already
	// running it keep loading their surfaces.
	if _, err := fixture.service.VersionForWorkspace(ctx, fixture.publisher, fixture.versionID); err != nil {
		t.Fatalf("withdrawal removed the publisher's own access to the artifact: %v", err)
	}

	restored, err := fixture.service.SetVersionWithdrawn(ctx, fixture.publisher, fixture.userID, fixture.versionID, false)
	if err != nil {
		t.Fatalf("restore version: %v", err)
	}
	if len(restored.Versions) != 1 || restored.Versions[0].WithdrawnAt != "" {
		t.Fatalf("restoring did not clear the withdrawal: %+v", restored.Versions)
	}
	if _, err := fixture.service.VersionForWorkspace(ctx, fixture.consumer, fixture.versionID); err != nil {
		t.Fatalf("a restored version is not installable again: %v", err)
	}
}

// Only the publisher decides a listing. An administrator elsewhere on the
// instance must not be able to unlist someone else's plugin, or withdraw a
// version out from under its users.
func TestOnlyThePublisherChangesAListing(t *testing.T) {
	fixture := newDirectoryFixture(t)
	ctx := context.Background()

	if _, err := fixture.service.SetPackageVisibility(ctx, fixture.consumer, util.UUIDToString(fixture.packageID), true); err == nil {
		t.Fatal("another workspace listed a package it does not publish")
	}
	if _, err := fixture.service.SetVersionWithdrawn(ctx, fixture.consumer, fixture.userID, fixture.versionID, true); err == nil {
		t.Fatal("another workspace withdrew a version it does not publish")
	}
}
