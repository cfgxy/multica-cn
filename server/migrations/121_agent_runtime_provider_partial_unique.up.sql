-- Custom Runtime, PR2 compatibility stage. See MUL-3284 / GitHub #3667.
--
-- Migration 120 added agent_runtime.profile_id and the custom-runtime partial
-- unique index on (workspace_id, daemon_id, profile_id) WHERE profile_id IS NOT
-- NULL, while deliberately retaining the legacy UNIQUE (workspace_id, daemon_id,
-- provider) constraint. Old server builds still use:
--
--   ON CONFLICT (workspace_id, daemon_id, provider)
--
-- That statement requires a non-partial unique/exclusion arbiter on exactly
-- those columns at plan time. Dropping the legacy constraint in the same
-- release as profile-aware registration would break rolling deploys and
-- rollbacks: any old API pod that registers a daemon runtime after this
-- migration lands would fail before it can execute the DO UPDATE path.
--
-- This migration is therefore intentionally additive. It prepares the built-in
-- runtime partial unique index used by the new profile-aware upsert, but keeps
-- the legacy full constraint in place for old binaries. The full constraint
-- still prevents built-in and custom runtimes of the same provider from
-- coexisting on one daemon during this compatibility stage; a later, separately
-- released migration may drop agent_runtime_workspace_id_daemon_id_provider_key
-- after every running server build has stopped using the old conflict target.
--
-- During this stage the same-provider built-in + custom combination on a single
-- daemon is therefore unsupported and is enforced from two layers (MUL-3373):
--   1. The daemon (appendProfileRuntimes in server/internal/daemon/daemon.go)
--      skips a custom profile whose protocol_family already matches a built-in
--      runtime collected in the same Register batch, so the colliding row is
--      never sent.
--   2. The server (DaemonRegister handler in
--      server/internal/handler/daemon.go) treats a 23505 on this legacy
--      constraint coming from UpsertAgentRuntimeWithProfile as a soft-skip
--      rather than a 500, so an older daemon that has not yet picked up the
--      client-side guard does not fail the entire register batch.
-- When the legacy constraint is dropped in a follow-up migration both guards
-- become no-ops and built-in + custom of the same provider can coexist.

CREATE UNIQUE INDEX IF NOT EXISTS agent_runtime_workspace_daemon_provider_key
    ON agent_runtime (workspace_id, daemon_id, provider)
    WHERE profile_id IS NULL;
