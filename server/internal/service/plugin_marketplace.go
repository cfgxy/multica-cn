package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/plugincontract"
)

// MarketplacePluginSummary is discovery metadata for one immutable Plugin
// version. It contains no bundle bytes, configuration values, or secrets.
type MarketplacePluginSummary struct {
	PackageID              string `json:"package_id"`
	VersionID              string `json:"version_id"`
	PluginKey              string `json:"plugin_key"`
	Name                   string `json:"name"`
	Description            string `json:"description,omitempty"`
	AuthorName             string `json:"author_name"`
	Version                string `json:"version"`
	Digest                 string `json:"digest"`
	PublisherWorkspaceID   string `json:"publisher_workspace_id"`
	PublisherWorkspaceSlug string `json:"publisher_workspace_slug"`
	ListedAt               string `json:"listed_at"`
	Installed              bool   `json:"installed"`
}

func (s *PluginService) ListMarketplacePlugins(ctx context.Context, workspaceID pgtype.UUID) ([]MarketplacePluginSummary, error) {
	rows, err := s.Queries.ListMarketplacePluginListings(ctx, workspaceID)
	if err != nil {
		return nil, &PluginError{Kind: PluginErrorUnavailable, Message: "list Plugin marketplace", Err: err}
	}
	return marketplaceSummaries(rows), nil
}

// marketplaceSummaries renders listing rows into catalog entries. A listed
// version whose manifest no longer parses is skipped rather than failing the
// request: the publish path validates every manifest, so an unreadable one here
// means a database, migration or operations fault, and one damaged row must not
// hide every healthy Plugin in the directory.
func marketplaceSummaries(rows []db.ListMarketplacePluginListingsRow) []MarketplacePluginSummary {
	plugins := make([]MarketplacePluginSummary, 0, len(rows))
	for _, row := range rows {
		manifest, _, err := plugincontract.ParseManifest(row.Manifest)
		if err != nil {
			slog.Warn("plugins: skipping a marketplace listing with an unreadable manifest",
				"version_id", uuidString(row.VersionID),
				"plugin_key", row.PluginKey,
				"error", err,
			)
			continue
		}
		plugins = append(plugins, MarketplacePluginSummary{
			PackageID:              uuidString(row.PackageID),
			VersionID:              uuidString(row.VersionID),
			PluginKey:              row.PluginKey,
			Name:                   manifest.Name,
			Description:            manifest.Description,
			AuthorName:             manifest.Author.Name,
			Version:                row.Version,
			Digest:                 row.Digest,
			PublisherWorkspaceID:   uuidString(row.PublisherWorkspaceID),
			PublisherWorkspaceSlug: row.PublisherWorkspaceSlug,
			ListedAt:               row.ListedAt.Time.UTC().Format(pluginTimeFormat),
			Installed:              row.Installed,
		})
	}
	return plugins
}

// ListPackageInMarketplace points the catalog at one already-published immutable
// version. The directory references the artifact rows; it does not copy them.
func (s *PluginService) ListPackageInMarketplace(ctx context.Context, workspaceID, userID pgtype.UUID, packageID, versionID string) error {
	parsedPackageID, err := util.ParseUUID(strings.TrimSpace(packageID))
	if err != nil {
		return pluginErrf(PluginErrorNotFound, "plugin package not found")
	}
	parsedVersionID, err := util.ParseUUID(strings.TrimSpace(versionID))
	if err != nil {
		return pluginErrf(PluginErrorNotFound, "published plugin version not found")
	}
	pkg, err := s.Queries.GetWorkspacePluginPackage(ctx, db.GetWorkspacePluginPackageParams{WorkspaceID: workspaceID, ID: parsedPackageID})
	if errors.Is(err, pgx.ErrNoRows) {
		return pluginErrf(PluginErrorNotFound, "plugin package not found")
	}
	if err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "load plugin package", Err: err}
	}

	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "begin marketplace listing", Err: err}
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.Queries.WithTx(tx)
	if err := lockPluginPublisherWorkspace(ctx, queries, workspaceID); err != nil {
		return err
	}
	if err := lockPluginPackageKey(ctx, queries, workspaceID, pkg.PluginKey); err != nil {
		return err
	}
	version, err := queries.GetWorkspacePluginPackageVersion(ctx, db.GetWorkspacePluginPackageVersionParams{WorkspaceID: workspaceID, ID: parsedVersionID})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && uuidString(version.PackageID) != uuidString(parsedPackageID)) {
		return pluginErrf(PluginErrorNotFound, "published plugin version not found")
	}
	if err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "load published plugin version", Err: err}
	}
	err = queries.UpsertMarketplacePluginListing(ctx, db.UpsertMarketplacePluginListingParams{
		PackageID:            parsedPackageID,
		VersionID:            parsedVersionID,
		PublisherWorkspaceID: workspaceID,
		ListedBy:             userID,
	})
	if err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "list Plugin in marketplace", Err: err}
	}
	if err := tx.Commit(ctx); err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "commit marketplace listing", Err: err}
	}
	return nil
}

func (s *PluginService) UnlistPackageFromMarketplace(ctx context.Context, workspaceID pgtype.UUID, packageID string) error {
	parsedPackageID, err := util.ParseUUID(strings.TrimSpace(packageID))
	if err != nil {
		return pluginErrf(PluginErrorNotFound, "plugin package not found")
	}
	pkg, err := s.Queries.GetWorkspacePluginPackage(ctx, db.GetWorkspacePluginPackageParams{WorkspaceID: workspaceID, ID: parsedPackageID})
	if errors.Is(err, pgx.ErrNoRows) {
		return pluginErrf(PluginErrorNotFound, "plugin package not found")
	}
	if err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "load plugin package", Err: err}
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "begin marketplace unlist", Err: err}
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.Queries.WithTx(tx)
	if err := lockPluginPublisherWorkspace(ctx, queries, workspaceID); err != nil {
		return err
	}
	if err := lockPluginPackageKey(ctx, queries, workspaceID, pkg.PluginKey); err != nil {
		return err
	}
	if err := queries.DeleteMarketplacePluginListingByPackage(ctx, parsedPackageID); err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "remove Plugin from marketplace", Err: err}
	}
	if err := tx.Commit(ctx); err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "commit marketplace unlist", Err: err}
	}
	return nil
}

func (s *PluginService) versionForInstall(ctx context.Context, workspaceID pgtype.UUID, versionID string) (db.PluginPackageVersion, error) {
	parsed, err := util.ParseUUID(strings.TrimSpace(versionID))
	if err != nil {
		return db.PluginPackageVersion{}, pluginErrf(PluginErrorNotFound, "published plugin version not found")
	}
	version, err := s.Queries.GetWorkspacePluginPackageVersion(ctx, db.GetWorkspacePluginPackageVersionParams{WorkspaceID: workspaceID, ID: parsed})
	if err == nil {
		return version, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.PluginPackageVersion{}, &PluginError{Kind: PluginErrorUnavailable, Message: "load published plugin version", Err: err}
	}
	if !featureflags.PluginsV1Enabled(ctx, s.FeatureFlags) || !featureflags.MarketplaceV1Enabled(ctx, s.FeatureFlags) {
		return db.PluginPackageVersion{}, pluginErrf(PluginErrorNotFound, "published plugin version not found")
	}
	version, err = s.Queries.GetMarketplacePluginVersion(ctx, parsed)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.PluginPackageVersion{}, pluginErrf(PluginErrorNotFound, "published plugin version not found")
	}
	if err != nil {
		return db.PluginPackageVersion{}, &PluginError{Kind: PluginErrorUnavailable, Message: "load marketplace plugin version", Err: err}
	}
	return version, nil
}

func (s *PluginService) requireVersionInstallable(ctx context.Context, queries *db.Queries, targetWorkspaceID pgtype.UUID, version db.PluginPackageVersion) error {
	if err := requireVersionStillPublished(ctx, queries, version.WorkspaceID, version.ID); err != nil {
		return err
	}
	if uuidString(targetWorkspaceID) == uuidString(version.WorkspaceID) {
		return nil
	}
	if !featureflags.PluginsV1Enabled(ctx, s.FeatureFlags) || !featureflags.MarketplaceV1Enabled(ctx, s.FeatureFlags) {
		return pluginErrf(PluginErrorNotFound, "published plugin version not found")
	}
	listed, err := queries.IsMarketplacePluginVersionListed(ctx, version.ID)
	if err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "re-check marketplace listing", Err: err}
	}
	if !listed {
		return pluginErrf(PluginErrorConflict, "this Plugin was removed from the marketplace while the install was being confirmed")
	}
	return nil
}
