package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/service"
)

func publishMarketplaceFixture(t *testing.T) (service.PluginPackageSummary, string) {
	t.Helper()
	publisherWorkspaceID := dbfx.Workspace(t, "Marketplace Publisher", "marketplace-publisher-"+uuid.NewString())
	dbfx.Member(t, publisherWorkspaceID, testUserID, "owner")
	published, err := testHandler.PluginService.PublishBundle(
		t.Context(),
		parseUUID(publisherWorkspaceID),
		parseUUID(testUserID),
		pluginBundleZip(t, packageManifest("1.0.0"), "console.log('marketplace');\n"),
	)
	if err != nil {
		t.Fatalf("publish marketplace fixture: %v", err)
	}
	dbfx.Cleanup(t, `
		WITH versions AS (
			SELECT id FROM plugin_package_version WHERE package_id = $1
		), deleted_installations AS (
			DELETE FROM plugin_installation WHERE package_version_id IN (SELECT id FROM versions)
		), deleted_listings AS (
			DELETE FROM marketplace_plugin_listing WHERE package_id = $1
		), deleted_files AS (
			DELETE FROM plugin_package_file WHERE version_id IN (SELECT id FROM versions)
		), deleted_versions AS (
			DELETE FROM plugin_package_version WHERE package_id = $1
		)
		DELETE FROM plugin_package WHERE id = $1
	`, published.ID)
	return published, publisherWorkspaceID
}

func TestMarketplacePluginCanBeListedBrowsedAndInstalledAcrossWorkspaces(t *testing.T) {
	withPluginMarketplaceFlags(t, testHandler, true)
	withHostCapabilities(t)
	cleanupPluginInstallations(t)

	published, publisherWorkspaceID := publishMarketplaceFixture(t)
	versionID := published.Versions[0].ID
	body, _ := json.Marshal(map[string]string{"version_id": versionID})
	listed := httptest.NewRecorder()
	testHandler.ListPluginPackageInMarketplace(listed, pluginHandlerRequest(
		http.MethodPut,
		"/plugins/packages/marketplace",
		body,
		map[string]string{"id": publisherWorkspaceID, "packageId": published.ID},
	))
	if listed.Code != http.StatusNoContent {
		t.Fatalf("list marketplace plugin: status=%d body=%s", listed.Code, listed.Body.String())
	}

	catalog := httptest.NewRecorder()
	testHandler.ListMarketplacePlugins(catalog, pluginHandlerRequest(
		http.MethodGet,
		"/marketplace/plugins",
		nil,
		map[string]string{"id": testWorkspaceID},
	))
	if catalog.Code != http.StatusOK {
		t.Fatalf("browse marketplace: status=%d body=%s", catalog.Code, catalog.Body.String())
	}
	var response struct {
		Plugins []service.MarketplacePluginSummary `json:"plugins"`
	}
	if err := json.Unmarshal(catalog.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode marketplace catalog: %v", err)
	}
	if len(response.Plugins) != 1 || response.Plugins[0].VersionID != versionID {
		t.Fatalf("marketplace catalog = %+v", response.Plugins)
	}
	if response.Plugins[0].PublisherWorkspaceID != publisherWorkspaceID {
		t.Fatalf("publisher workspace = %q, want %q", response.Plugins[0].PublisherWorkspaceID, publisherWorkspaceID)
	}
	var publisherWorkspaceSlug string
	if err := testPool.QueryRow(t.Context(), `SELECT slug FROM workspace WHERE id = $1`, publisherWorkspaceID).Scan(&publisherWorkspaceSlug); err != nil {
		t.Fatalf("read publisher workspace slug: %v", err)
	}
	if response.Plugins[0].PublisherWorkspaceSlug != publisherWorkspaceSlug {
		t.Fatalf("publisher workspace slug = %q, want %q", response.Plugins[0].PublisherWorkspaceSlug, publisherWorkspaceSlug)
	}

	installPublishedVersion(t, versionID)
	if got := dbfx.Count(t,
		`SELECT count(*) FROM plugin_installation WHERE workspace_id = $1 AND package_version_id = $2`,
		testWorkspaceID, versionID,
	); got != 1 {
		t.Fatalf("cross-workspace installations = %d, want 1", got)
	}
}

func TestMarketplaceListingKeepsTheListedVersionNameAfterARename(t *testing.T) {
	withPluginMarketplaceFlags(t, testHandler, true)
	withHostCapabilities(t)

	published, publisherWorkspaceID := publishMarketplaceFixture(t)
	listedVersionID := published.Versions[0].ID
	if err := testHandler.PluginService.ListPackageInMarketplace(
		t.Context(), parseUUID(publisherWorkspaceID), parseUUID(testUserID), published.ID, listedVersionID,
	); err != nil {
		t.Fatalf("list marketplace fixture: %v", err)
	}

	renamedManifest := replaceOnce(t, packageManifest("2.0.0"), `"name": "Published Panel"`, `"name": "Renamed Panel"`)
	if _, err := testHandler.PluginService.PublishBundle(
		t.Context(),
		parseUUID(publisherWorkspaceID),
		parseUUID(testUserID),
		pluginBundleZip(t, renamedManifest, "console.log('renamed');\n"),
	); err != nil {
		t.Fatalf("publish renamed version: %v", err)
	}
	var currentPackageName string
	if err := testPool.QueryRow(t.Context(), `SELECT name FROM plugin_package WHERE id = $1`, published.ID).Scan(&currentPackageName); err != nil {
		t.Fatalf("read renamed package: %v", err)
	}
	if currentPackageName != "Renamed Panel" {
		t.Fatalf("current package name = %q, want Renamed Panel", currentPackageName)
	}

	catalog, err := testHandler.PluginService.ListMarketplacePlugins(t.Context(), parseUUID(testWorkspaceID))
	if err != nil {
		t.Fatalf("list marketplace: %v", err)
	}
	if len(catalog) != 1 {
		t.Fatalf("marketplace catalog = %+v, want one listing", catalog)
	}
	if catalog[0].VersionID != listedVersionID || catalog[0].Name != "Published Panel" {
		t.Fatalf("listed immutable version = %+v, want version %s named Published Panel", catalog[0], listedVersionID)
	}
}

func TestMarketplaceRelistingNewVersionRefreshesListedAtAndSortOrder(t *testing.T) {
	withPluginMarketplaceFlags(t, testHandler, true)
	withHostCapabilities(t)

	first, firstWorkspaceID := publishMarketplaceFixture(t)
	second, secondWorkspaceID := publishMarketplaceFixture(t)
	firstVersionID := first.Versions[0].ID
	secondVersionID := second.Versions[0].ID
	if err := testHandler.PluginService.ListPackageInMarketplace(
		t.Context(), parseUUID(firstWorkspaceID), parseUUID(testUserID), first.ID, firstVersionID,
	); err != nil {
		t.Fatalf("list first marketplace package: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), `
		UPDATE marketplace_plugin_listing
		SET created_at = now() - interval '1 hour'
		WHERE package_id = $1
	`, first.ID); err != nil {
		t.Fatalf("age first marketplace listing: %v", err)
	}
	if err := testHandler.PluginService.ListPackageInMarketplace(
		t.Context(), parseUUID(secondWorkspaceID), parseUUID(testUserID), second.ID, secondVersionID,
	); err != nil {
		t.Fatalf("list second marketplace package: %v", err)
	}

	var listedBefore time.Time
	if err := testPool.QueryRow(t.Context(), `SELECT created_at FROM marketplace_plugin_listing WHERE package_id = $1`, first.ID).Scan(&listedBefore); err != nil {
		t.Fatalf("read first listing time: %v", err)
	}
	updated, err := testHandler.PluginService.PublishBundle(
		t.Context(), parseUUID(firstWorkspaceID), parseUUID(testUserID),
		pluginBundleZip(t, packageManifest("2.0.0"), "console.log('marketplace v2');\n"),
	)
	if err != nil {
		t.Fatalf("publish updated marketplace package: %v", err)
	}
	updatedVersionID := updated.Versions[0].ID
	if err := testHandler.PluginService.ListPackageInMarketplace(
		t.Context(), parseUUID(firstWorkspaceID), parseUUID(testUserID), first.ID, updatedVersionID,
	); err != nil {
		t.Fatalf("re-list updated marketplace package: %v", err)
	}

	var listedAfter time.Time
	if err := testPool.QueryRow(t.Context(), `SELECT created_at FROM marketplace_plugin_listing WHERE package_id = $1`, first.ID).Scan(&listedAfter); err != nil {
		t.Fatalf("read refreshed listing time: %v", err)
	}
	if !listedAfter.After(listedBefore) {
		t.Fatalf("refreshed listing time = %s, want after %s", listedAfter, listedBefore)
	}
	catalog, err := testHandler.PluginService.ListMarketplacePlugins(t.Context(), parseUUID(testWorkspaceID))
	if err != nil {
		t.Fatalf("list marketplace after re-list: %v", err)
	}
	if len(catalog) != 2 || catalog[0].PackageID != first.ID || catalog[0].VersionID != updatedVersionID {
		t.Fatalf("marketplace order after re-list = %+v, want updated first package first", catalog)
	}
}

func TestMarketplaceRejectsUnlistedCrossWorkspaceVersion(t *testing.T) {
	withPluginMarketplaceFlags(t, testHandler, true)
	withHostCapabilities(t)
	cleanupPluginInstallations(t)

	published, _ := publishMarketplaceFixture(t)
	body, _ := json.Marshal(map[string]any{
		"version_id":     published.Versions[0].ID,
		"granted_scopes": []string{"issues:read"},
	})
	recorder := httptest.NewRecorder()
	testHandler.InstallPlugin(recorder, pluginHandlerRequest(
		http.MethodPost,
		"/plugins",
		body,
		map[string]string{"id": testWorkspaceID},
	))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("install unlisted version: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

// Mirrors the operations fault QA reproduced: a listed version whose stored
// manifest is valid JSON but semantically invalid must not take the catalog
// down with it.
func TestMarketplaceCatalogSkipsDamagedManifestAndKeepsHealthyListings(t *testing.T) {
	withPluginMarketplaceFlags(t, testHandler, true)
	withHostCapabilities(t)
	cleanupPluginInstallations(t)

	damaged, damagedWorkspaceID := publishMarketplaceFixture(t)
	if err := testHandler.PluginService.ListPackageInMarketplace(
		t.Context(), parseUUID(damagedWorkspaceID), parseUUID(testUserID), damaged.ID, damaged.Versions[0].ID,
	); err != nil {
		t.Fatalf("list damaged fixture: %v", err)
	}
	healthy, healthyWorkspaceID := publishMarketplaceFixture(t)
	healthyVersionID := healthy.Versions[0].ID
	if err := testHandler.PluginService.ListPackageInMarketplace(
		t.Context(), parseUUID(healthyWorkspaceID), parseUUID(testUserID), healthy.ID, healthyVersionID,
	); err != nil {
		t.Fatalf("list healthy fixture: %v", err)
	}
	if _, err := testPool.Exec(t.Context(),
		`UPDATE plugin_package_version SET manifest = $2 WHERE id = $1`,
		damaged.Versions[0].ID, []byte(`{"manifest_version": 1, "key": "com.example.broken"}`),
	); err != nil {
		t.Fatalf("damage stored manifest: %v", err)
	}

	recorder := httptest.NewRecorder()
	testHandler.ListMarketplacePlugins(recorder, pluginHandlerRequest(
		http.MethodGet,
		"/marketplace/plugins",
		nil,
		map[string]string{"id": testWorkspaceID},
	))
	if recorder.Code != http.StatusOK {
		t.Fatalf("browse marketplace with a damaged listing: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Plugins []service.MarketplacePluginSummary `json:"plugins"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode marketplace catalog: %v", err)
	}
	var healthyEntries int
	for _, plugin := range response.Plugins {
		if plugin.VersionID == damaged.Versions[0].ID {
			t.Fatalf("damaged listing was rendered: %+v", plugin)
		}
		if plugin.VersionID == healthyVersionID {
			healthyEntries++
			if plugin.Name != "Published Panel" {
				t.Fatalf("healthy listing name = %q, want the listed manifest name", plugin.Name)
			}
		}
	}
	if healthyEntries != 1 {
		t.Fatalf("healthy listing entries = %d, want 1: %+v", healthyEntries, response.Plugins)
	}
}

func TestMarketplaceFlagOffLeavesExistingPluginFlowAndHidesCatalog(t *testing.T) {
	withPluginMarketplaceFlags(t, testHandler, false)
	withHostCapabilities(t)
	cleanupPluginInstallations(t)

	published := publishUploadedBundle(t, packageManifest("1.0.0"), "console.log('local');\n")
	installPublishedVersion(t, published.Versions[0].ID)

	recorder := httptest.NewRecorder()
	testHandler.ListMarketplacePlugins(recorder, pluginHandlerRequest(
		http.MethodGet,
		"/marketplace/plugins",
		nil,
		map[string]string{"id": testWorkspaceID},
	))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("marketplace with flag off: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMarketplaceUnlistSerializesWithInstall(t *testing.T) {
	withPluginMarketplaceFlags(t, testHandler, true)
	withHostCapabilities(t)
	cleanupPluginInstallations(t)

	published, publisherWorkspaceID := publishMarketplaceFixture(t)
	versionID := published.Versions[0].ID
	if err := testHandler.PluginService.ListPackageInMarketplace(
		t.Context(), parseUUID(publisherWorkspaceID), parseUUID(testUserID), published.ID, versionID,
	); err != nil {
		t.Fatalf("list marketplace fixture: %v", err)
	}

	// Hold the same transaction-scoped lock as unlist, remove the listing without
	// committing, and prove install reaches and waits on that lock. Once unlist
	// commits, install must re-check the catalog and reject the withdrawn version.
	unlistTx, err := testPool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin controlled unlist: %v", err)
	}
	defer unlistTx.Rollback(t.Context())
	var unlistPID int
	if err := unlistTx.QueryRow(t.Context(), `SELECT pg_backend_pid()`).Scan(&unlistPID); err != nil {
		t.Fatalf("read controlled unlist pid: %v", err)
	}
	if _, err := unlistTx.Exec(t.Context(), `SELECT id FROM workspace WHERE id = $1 FOR KEY SHARE`, publisherWorkspaceID); err != nil {
		t.Fatalf("lock publisher workspace: %v", err)
	}
	if _, err := unlistTx.Exec(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, publisherWorkspaceID+":"+published.PluginKey); err != nil {
		t.Fatalf("lock marketplace package: %v", err)
	}
	if _, err := unlistTx.Exec(t.Context(), `DELETE FROM marketplace_plugin_listing WHERE package_id = $1`, published.ID); err != nil {
		t.Fatalf("stage marketplace unlist: %v", err)
	}

	installResult := make(chan error, 1)
	go func() {
		_, installErr := testHandler.PluginService.InstallPlugin(
			t.Context(), parseUUID(testWorkspaceID), parseUUID(testUserID), versionID, []string{"issues:read"},
		)
		installResult <- installErr
	}()
	waitForMarketplaceLockWaiter(t, unlistPID)
	select {
	case installErr := <-installResult:
		t.Fatalf("install completed before the unlist lock was released: %v", installErr)
	default:
	}
	if err := unlistTx.Commit(t.Context()); err != nil {
		t.Fatalf("commit controlled unlist: %v", err)
	}
	select {
	case installErr := <-installResult:
		var pluginErr *service.PluginError
		if !errors.As(installErr, &pluginErr) || pluginErr.Kind != service.PluginErrorConflict {
			t.Fatalf("install after committed unlist = %v, want marketplace conflict", installErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("install stayed blocked after the unlist lock was released")
	}
	if installed := dbfx.Count(t,
		`SELECT count(*) FROM plugin_installation WHERE workspace_id = $1 AND package_version_id = $2`,
		testWorkspaceID, versionID,
	); installed != 0 {
		t.Fatalf("install committed after unlist won the lock: installed=%d", installed)
	}

	// Re-list, then hold the package lock externally and prove the real unlist
	// method also waits for it before deleting discovery state.
	if err := testHandler.PluginService.ListPackageInMarketplace(
		t.Context(), parseUUID(publisherWorkspaceID), parseUUID(testUserID), published.ID, versionID,
	); err != nil {
		t.Fatalf("re-list marketplace fixture: %v", err)
	}
	blockerTx, err := testPool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin lock blocker: %v", err)
	}
	defer blockerTx.Rollback(t.Context())
	var blockerPID int
	if err := blockerTx.QueryRow(t.Context(), `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		t.Fatalf("read lock blocker pid: %v", err)
	}
	if _, err := blockerTx.Exec(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, publisherWorkspaceID+":"+published.PluginKey); err != nil {
		t.Fatalf("hold marketplace package lock: %v", err)
	}
	unlistResult := make(chan error, 1)
	go func() {
		unlistResult <- testHandler.PluginService.UnlistPackageFromMarketplace(
			t.Context(), parseUUID(publisherWorkspaceID), published.ID,
		)
	}()
	waitForMarketplaceLockWaiter(t, blockerPID)
	select {
	case unlistErr := <-unlistResult:
		t.Fatalf("unlist completed before the package lock was released: %v", unlistErr)
	default:
	}
	if err := blockerTx.Commit(t.Context()); err != nil {
		t.Fatalf("release lock blocker: %v", err)
	}
	select {
	case unlistErr := <-unlistResult:
		if unlistErr != nil {
			t.Fatalf("unlist after lock release: %v", unlistErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("unlist stayed blocked after the package lock was released")
	}
	if listed := dbfx.Count(t, `SELECT count(*) FROM marketplace_plugin_listing WHERE version_id = $1`, versionID); listed != 0 {
		t.Fatalf("unlist left %d marketplace listing(s)", listed)
	}
}

func waitForMarketplaceLockWaiter(t *testing.T, holderPID int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		err := testPool.QueryRow(t.Context(), `
			SELECT EXISTS (
				SELECT 1
				FROM pg_locks holder
				JOIN pg_locks waiter
				  ON waiter.locktype = holder.locktype
				 AND waiter.database IS NOT DISTINCT FROM holder.database
				 AND waiter.classid IS NOT DISTINCT FROM holder.classid
				 AND waiter.objid IS NOT DISTINCT FROM holder.objid
				 AND waiter.objsubid IS NOT DISTINCT FROM holder.objsubid
				WHERE holder.pid = $1
				  AND holder.locktype = 'advisory'
				  AND holder.granted
				  AND NOT waiter.granted
			)
		`, holderPID).Scan(&waiting)
		if err != nil {
			t.Fatalf("inspect marketplace lock waiter: %v", err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("operation never reached the held marketplace package lock")
}

func TestMarketplacePublisherWorkspaceDeleteIsBlockedWhileExternallyInstalled(t *testing.T) {
	withPluginMarketplaceFlags(t, testHandler, true)
	withHostCapabilities(t)
	cleanupPluginInstallations(t)

	published, publisherWorkspaceID := publishMarketplaceFixture(t)
	versionID := published.Versions[0].ID
	if err := testHandler.PluginService.ListPackageInMarketplace(
		t.Context(), parseUUID(publisherWorkspaceID), parseUUID(testUserID), published.ID, versionID,
	); err != nil {
		t.Fatalf("list marketplace fixture: %v", err)
	}
	installPublishedVersion(t, versionID)

	recorder := httptest.NewRecorder()
	testHandler.DeleteWorkspace(recorder, pluginHandlerRequest(
		http.MethodDelete, "/workspaces", nil, map[string]string{"id": publisherWorkspaceID},
	))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("delete publisher workspace: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM workspace WHERE id = $1`, publisherWorkspaceID); got != 1 {
		t.Fatalf("publisher workspace count = %d, want 1", got)
	}
}

func TestMarketplaceInstallFailsAfterTargetWorkspaceDeleteCommits(t *testing.T) {
	withPluginMarketplaceFlags(t, testHandler, true)
	withHostCapabilities(t)

	published, publisherWorkspaceID := publishMarketplaceFixture(t)
	versionID := published.Versions[0].ID
	if err := testHandler.PluginService.ListPackageInMarketplace(
		t.Context(), parseUUID(publisherWorkspaceID), parseUUID(testUserID), published.ID, versionID,
	); err != nil {
		t.Fatalf("list marketplace fixture: %v", err)
	}
	targetWorkspaceID := dbfx.Workspace(t, "Marketplace Install Target", "marketplace-install-target-"+uuid.NewString())
	dbfx.Member(t, targetWorkspaceID, testUserID, "owner")

	deleteTx, err := testPool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin target workspace delete: %v", err)
	}
	deleteCommitted := false
	defer func() {
		if !deleteCommitted {
			_ = deleteTx.Rollback(t.Context())
		}
	}()
	deletePID := holderBackendPID(t, t.Context(), deleteTx)
	if _, err := deleteTx.Exec(t.Context(), `SELECT id FROM workspace WHERE id = $1 FOR UPDATE`, targetWorkspaceID); err != nil {
		t.Fatalf("lock target workspace for delete: %v", err)
	}

	type installResponse struct {
		code int
		body string
	}
	installResult := make(chan installResponse, 1)
	go func() {
		body, _ := json.Marshal(map[string]any{"version_id": versionID, "granted_scopes": []string{"issues:read"}})
		recorder := httptest.NewRecorder()
		testHandler.InstallPlugin(recorder, pluginHandlerRequest(
			http.MethodPost, "/plugins", body, map[string]string{"id": targetWorkspaceID},
		))
		installResult <- installResponse{code: recorder.Code, body: recorder.Body.String()}
	}()
	if !waitForWaiterBlockedBy(t, deletePID, 5*time.Second) {
		t.Fatal("install returned without waiting for the target workspace delete lock")
	}
	select {
	case response := <-installResult:
		t.Fatalf("install completed before target workspace delete committed: status=%d body=%s", response.code, response.body)
	default:
	}

	if _, err := deleteTx.Exec(t.Context(), `DELETE FROM member WHERE workspace_id = $1`, targetWorkspaceID); err != nil {
		t.Fatalf("delete target workspace members: %v", err)
	}
	if _, err := deleteTx.Exec(t.Context(), `DELETE FROM workspace WHERE id = $1`, targetWorkspaceID); err != nil {
		t.Fatalf("delete target workspace: %v", err)
	}
	if err := deleteTx.Commit(t.Context()); err != nil {
		t.Fatalf("commit target workspace delete: %v", err)
	}
	deleteCommitted = true

	select {
	case response := <-installResult:
		if response.code != http.StatusConflict {
			t.Fatalf("install after target workspace delete: status=%d body=%s, want 409", response.code, response.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("install stayed blocked after target workspace delete committed")
	}
	if installed := dbfx.Count(t,
		`SELECT count(*) FROM plugin_installation WHERE workspace_id = $1 AND package_version_id = $2`,
		targetWorkspaceID, versionID,
	); installed != 0 {
		t.Fatalf("install wrote %d installation(s) after target workspace deletion", installed)
	}
}

func TestMarketplaceInstallAndPublisherWorkspaceDeleteRaceLeavesNoDanglingInstallation(t *testing.T) {
	withPluginMarketplaceFlags(t, testHandler, true)
	withHostCapabilities(t)

	for attempt := range 6 {
		cleanupPluginInstallations(t)
		published, publisherWorkspaceID := publishMarketplaceFixture(t)
		versionID := published.Versions[0].ID
		if err := testHandler.PluginService.ListPackageInMarketplace(
			t.Context(), parseUUID(publisherWorkspaceID), parseUUID(testUserID), published.ID, versionID,
		); err != nil {
			t.Fatalf("list marketplace fixture: %v", err)
		}

		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			body, _ := json.Marshal(map[string]any{"version_id": versionID, "granted_scopes": []string{"issues:read"}})
			testHandler.InstallPlugin(httptest.NewRecorder(), pluginHandlerRequest(
				http.MethodPost, "/plugins", body, map[string]string{"id": testWorkspaceID},
			))
		}()
		go func() {
			defer wait.Done()
			testHandler.DeleteWorkspace(httptest.NewRecorder(), pluginHandlerRequest(
				http.MethodDelete, "/workspaces", nil, map[string]string{"id": publisherWorkspaceID},
			))
		}()
		wait.Wait()

		dangling := dbfx.Count(t, `
			SELECT count(*) FROM plugin_installation i
			WHERE i.workspace_id = $1
			  AND i.package_version_id = $2
			  AND NOT EXISTS (SELECT 1 FROM plugin_package_version v WHERE v.id = i.package_version_id)
		`, testWorkspaceID, versionID)
		if dangling != 0 {
			t.Fatalf("attempt %d left %d dangling marketplace installation(s)", attempt, dangling)
		}
	}
}
