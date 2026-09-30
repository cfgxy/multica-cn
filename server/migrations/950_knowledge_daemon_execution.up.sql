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
