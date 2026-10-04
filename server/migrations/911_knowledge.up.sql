-- RUYI-359 Phase 2R domain consolidation: merges the former migrations
-- 945, 950, 953, 954 into one atomic migration (renumbered to 911_knowledge) on the gap-free
-- 900+ ladder. Statement bodies are unchanged except CREATE/DROP INDEX lost
-- the CONCURRENTLY keyword: every target is created earlier in this same
-- file or by an earlier migration, and the whole file runs as one implicit
-- transaction (914 precedent); existing environments converge via the
-- ledger rewrite and never re-run these files. Original-stem -> new-stem
-- mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.

-- >>> absorbed from 945.up.sql (RUYI-359 consolidation)

-- bd memories unified collection (RUYI-265, phase-4 spec §K). The platform
-- keeps a READ-ONLY mirror of bd memories from registered directories; the
-- source bd files are never written. The only write path in the whole flow
-- is the Owner adoption transfer into the workspace's ultimate knowledge
-- directory, which itself is confirmed by reading the entry back from that
-- bd before the adoption is recorded (G1: 转移完成才算采纳).
CREATE TABLE knowledge_dir (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    -- ultimate: the workspace's single adoption target (one per workspace);
    -- candidate_cli: added through the server management channel;
    -- candidate_auto: discovered from a project's registered local resource
    -- directories. All are scanned read-only, ultimate included.
    kind TEXT NOT NULL CHECK (kind IN ('ultimate', 'candidate_cli', 'candidate_auto')),
    path TEXT NOT NULL,
    project_id UUID,
    label TEXT NOT NULL DEFAULT '',
    -- Last server-side verification of the daemon hosting the directory and
    -- of the access to the path: ok | unreachable | no_access.
    health_state TEXT NOT NULL DEFAULT 'ok',
    health_note TEXT NOT NULL DEFAULT '',
    removed BOOLEAN NOT NULL DEFAULT FALSE,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE knowledge_entry (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    dir_id UUID NOT NULL,
    key TEXT NOT NULL,
    content TEXT NOT NULL,
    content_sha256 TEXT NOT NULL,
    -- synced | source_deleted (key vanished from the source dir) |
    -- source_removed (the source dir was unregistered) — entries are never
    -- physically deleted so the mirror stays retrievable.
    mirror_state TEXT NOT NULL DEFAULT 'synced',
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_confirmed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- pending | adopted | failed. 'adopted' is only written after the entry
    -- was transferred into the ultimate bd AND read back from it; the
    -- ultimate bd remains the only authority for adopted content and the
    -- provenance columns below are mirror metadata, not a second authority.
    adoption_state TEXT NOT NULL DEFAULT 'pending',
    adopted_at TIMESTAMPTZ,
    adopted_by UUID,
    ultimate_dir_id UUID,
    adopted_from_dir_id UUID,
    adopted_from_key TEXT,
    adoption_error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE knowledge_scan_batch (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    dir_id UUID NOT NULL,
    -- scheduled | manual | initial
    trigger_source TEXT NOT NULL,
    -- noop (zero changes — still logged) | changed | failed
    result TEXT NOT NULL,
    added INT NOT NULL DEFAULT 0,
    updated INT NOT NULL DEFAULT 0,
    removed INT NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);

COMMENT ON TABLE knowledge_dir IS 'Registered bd memories directories: read-only candidate sources plus one per-workspace ultimate adoption target.';
COMMENT ON TABLE knowledge_entry IS 'Read-only mirror of bd memories entries; adoption_state=adopted only after transfer into the ultimate bd confirmed by read-back.';
COMMENT ON TABLE knowledge_scan_batch IS 'Per-directory scan batch log; zero-change batches are logged too so incremental ingestion stays recomputable.';

-- >>> absorbed from 946.up.sql (RUYI-359 consolidation)

CREATE INDEX idx_knowledge_dir_workspace
ON knowledge_dir (workspace_id);

-- >>> absorbed from 947.up.sql (RUYI-359 consolidation)

CREATE UNIQUE INDEX uidx_knowledge_entry_identity
ON knowledge_entry (dir_id, key);

-- >>> absorbed from 948.up.sql (RUYI-359 consolidation)

CREATE INDEX idx_knowledge_scan_batch_dir
ON knowledge_scan_batch (dir_id, started_at DESC);

-- >>> absorbed from 951.up.sql (RUYI-359 consolidation)

-- RUYI-289: auto-discovery now registers candidate directories from daemons
-- instead of the single server process, so the (workspace_id, path) duplicate
-- check that used to be an application-level count query needs a database
-- backstop: two daemons discovering the same directory concurrently must not
-- produce two rows. Unregistered (removed) paths may re-register, hence the
-- partial index.
CREATE UNIQUE INDEX uidx_knowledge_dir_ws_path
ON knowledge_dir (workspace_id, path)
WHERE removed = FALSE;

-- >>> absorbed from 952.up.sql (RUYI-359 consolidation)

-- RUYI-289: the ultimate knowledge directory is now auto-designated by
-- daemons (bd init + register on a daemon-managed path) instead of only by
-- the Owner through the management channel. Multiple daemons can race to
-- designate one for the same workspace; the existing single-ultimate rule
-- becomes a database constraint so exactly one registration wins and the
-- losers get a conflict instead of a second row.
CREATE UNIQUE INDEX uidx_knowledge_dir_ultimate_active
ON knowledge_dir (workspace_id)
WHERE kind = 'ultimate' AND removed = FALSE;

-- >>> from former migration 950 (RUYI-359 Phase 2R)

-- RUYI-289: scan execution moves from the server process (containers have no
-- bd and no host mounts) to the daemon on the bd host. The daemon claims work
-- through a pull loop, so every knowledge_dir row records which daemon hosts
-- the path ('' = not yet claimed — the first daemon that scans it binds it),
-- and a manual rescan requested through the UI is a flag the loop picks up
-- rather than a synchronous server-side bd exec.
ALTER TABLE knowledge_dir ADD COLUMN daemon_id TEXT NOT NULL DEFAULT '';
ALTER TABLE knowledge_dir ADD COLUMN scan_requested BOOLEAN NOT NULL DEFAULT FALSE;

-- Adoption transfer (bd remember + read-back) moves to the daemon too, so the
-- Owner's adopt decision only QUEUES the transfer: the proposal carries the
-- in-flight marker until the daemon reports the outcome. '' = idle,
-- 'transferring' = queued/being executed by a daemon. The proposal status
-- itself only becomes 'adopted' after the daemon confirmed the transfer.
ALTER TABLE proposal ADD COLUMN transfer_state TEXT NOT NULL DEFAULT '';

-- >>> from former migration 953 (RUYI-359 Phase 2R)

-- RUYI-289: the daemon pull loop asks for every knowledge directory assigned
-- to it (plus unclaimed ones) each cycle; this index keeps that lookup off a
-- sequential scan of knowledge_dir.
CREATE INDEX idx_knowledge_dir_daemon
ON knowledge_dir (daemon_id);

-- >>> from former migration 954 (RUYI-359 Phase 2R)

-- RUYI-289: auto-discovery roots are the local_directory project resources
-- pinned to a daemon; the daemon_id lives inside the resource_ref JSONB. The
-- discovery plan is computed per daemon each knowledge cycle, so index the
-- extracted daemon_id for that one shape.
CREATE INDEX idx_project_resource_local_dir_daemon
ON project_resource ((resource_ref->>'daemon_id'))
WHERE resource_type = 'local_directory';
