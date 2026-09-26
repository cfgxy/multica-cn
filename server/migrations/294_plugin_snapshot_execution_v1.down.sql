-- Migration 344 drops every plugin table created here and does not restore
-- it on the way down, so this rollback can run against a database where the
-- tables are already gone while its schema_migrations row still exists.
-- DROP TRIGGER IF EXISTS fails on a missing table, so guard on the relation
-- itself. agent_task_queue is not part of the plugin schema and survives 344.

DROP TRIGGER IF EXISTS trg_agent_task_queue_plugin_execution_manifest ON agent_task_queue;
DROP FUNCTION IF EXISTS pin_plugin_execution_manifest();
ALTER TABLE agent_task_queue DROP COLUMN IF EXISTS plugin_execution_manifest_id;

DO $$
BEGIN
    IF to_regclass('plugin_health') IS NOT NULL THEN
        DROP TRIGGER IF EXISTS trg_plugin_health_append_only ON plugin_health;
    END IF;
    IF to_regclass('plugin_execution_manifest') IS NOT NULL THEN
        DROP TRIGGER IF EXISTS trg_plugin_execution_manifest_append_only ON plugin_execution_manifest;
    END IF;
    IF to_regclass('plugin_capability_snapshot') IS NOT NULL THEN
        DROP TRIGGER IF EXISTS trg_plugin_capability_snapshot_append_only ON plugin_capability_snapshot;
    END IF;
    IF to_regclass('plugin_artifact_file') IS NOT NULL THEN
        DROP TRIGGER IF EXISTS trg_plugin_artifact_file_append_only ON plugin_artifact_file;
    END IF;
END
$$;

DROP TABLE IF EXISTS plugin_health;
DROP TABLE IF EXISTS plugin_execution_manifest;
DROP TABLE IF EXISTS plugin_capability_snapshot;
DROP TABLE IF EXISTS plugin_workspace_capability_state;
DROP TABLE IF EXISTS plugin_artifact_file;
