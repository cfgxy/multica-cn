-- Instance-local discovery for immutable Plugin versions. The listing points at
-- existing package/version rows; it never copies bundle bytes, manifests, config,
-- or secrets. Relationships are enforced transactionally by PluginService.
CREATE TABLE marketplace_plugin_listing (
    package_id UUID NOT NULL,
    version_id UUID NOT NULL,
    publisher_workspace_id UUID NOT NULL,
    listed_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
