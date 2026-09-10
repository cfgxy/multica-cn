-- The instance-wide public directory for plugins.
--
-- Publishing has been workspace-private: plugin_package rows are scoped to one
-- workspace and GetWorkspacePluginPackageVersion refuses a version id from any
-- other, so a plugin written in one workspace could not be installed in the one
-- next door. That was the right default while there was nowhere to review or
-- report a listing, and it is what this migration relaxes — deliberately, one
-- step, and no further than the instance boundary. Nothing here reaches an
-- external registry.
--
-- The model is three layers, and the reason they are three is that each has a
-- different lifetime:
--
--   * the artifact (plugin_package_version) is immutable and is never
--     un-published while anything runs it,
--   * the listing (this migration) controls who can DISCOVER it, and changes
--     freely,
--   * the installation is one workspace's decision to run one artifact, and
--     survives the listing being withdrawn.
--
-- Collapsing withdrawal into deletion would break the middle promise: taking a
-- plugin off the directory has to stop new installs without stopping the
-- workspaces already running it, or every publisher's takedown becomes an
-- outage for their users.

-- Who may find this package.
--
-- Default 'private' so nothing already published becomes instance-visible by
-- the act of running this migration. Making an existing package public is an
-- explicit act by its publisher, and it should be, because 'public' is the
-- point at which other workspaces' administrators start seeing it on a consent
-- screen.
ALTER TABLE plugin_package
    ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'private'
        CHECK (visibility IN ('private', 'public'));

-- Withdrawal, recorded on the version rather than the package.
--
-- Per version because that is the granularity a publisher actually needs: a
-- release that shipped a bug has to stop being installable while the release
-- before it stays available. A package-level flag would force "withdraw
-- everything or nothing", and publishers would answer it by deleting the bad
-- version, which is the one thing the immutability rule forbids.
--
-- A withdrawn version is still served to the installations that consented to
-- it. This column gates discovery and NEW installs, nothing else.
ALTER TABLE plugin_package_version
    ADD COLUMN IF NOT EXISTS withdrawn_at TIMESTAMPTZ;

-- Who withdrew it, kept when the withdrawal is lifted so the directory can show
-- a version's history rather than only its current state.
ALTER TABLE plugin_package_version
    ADD COLUMN IF NOT EXISTS withdrawn_by UUID;
