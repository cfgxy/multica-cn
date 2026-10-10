-- Dropping visibility returns every package to workspace-private discovery,
-- which is the pre-migration behavior. Installations are untouched: they name a
-- version id and never consulted the listing after the install completed.
ALTER TABLE plugin_package_version DROP COLUMN IF EXISTS withdrawn_by;
ALTER TABLE plugin_package_version DROP COLUMN IF EXISTS withdrawn_at;
ALTER TABLE plugin_package DROP COLUMN IF EXISTS visibility;
