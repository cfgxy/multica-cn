-- Reverse the identity split before restoring the pre-907 whitelist.
--
-- Order matters: the rows have to leave the two new families first, otherwise
-- a rolled-back deployment is left with profiles a pre-907 daemon cannot map
-- to any backend (it would refuse to register them) and a pre-907 API cannot
-- edit. Sending them back to 'kimi' restores exactly the shim state 907 up
-- migrated away from — the same profile ids, so agent bindings survive the
-- round trip in both directions.
--
-- Profiles created directly in the new families after 907 (no kimi shim ever
-- existed for them) are folded into the same shim shape: it is the only
-- representation the older schema has, and it keeps them launchable rather
-- than orphaned.
--
-- The pre-fold family is recorded first. Re-applying 907 recovers a folded
-- row's identity from the command basename, which only works for rows
-- launching the standard bridge command; the API allows any single-token
-- command, so ('zcode', 'company-zcode-wrapper') would otherwise be stranded
-- on 'kimi' by an up -> down -> up cycle with no field left to recover it
-- from. The table is dropped by the up migration once it has been consumed.
DROP TABLE IF EXISTS runtime_profile_family_907_backup;

CREATE TABLE runtime_profile_family_907_backup AS
SELECT id AS profile_id, protocol_family
FROM runtime_profile
WHERE protocol_family IN ('deerflow', 'zcode');

-- No index or key is declared on the backup: profile_id inherits uniqueness
-- from runtime_profile.id, the table is read exactly once by a full scan in
-- the up migration, and the repository forbids non-concurrent index builds in
-- a migration.

UPDATE runtime_profile
SET protocol_family = 'kimi',
    updated_at = now()
WHERE protocol_family IN ('deerflow', 'zcode');

ALTER TABLE runtime_profile DROP CONSTRAINT IF EXISTS runtime_profile_protocol_family_check;

ALTER TABLE runtime_profile ADD CONSTRAINT runtime_profile_protocol_family_check
    CHECK (protocol_family IN (
        'claude',
        'codebuddy',
        'codex',
        'copilot',
        'opencode',
        'codearts',
        'openclaw',
        'hermes',
        'pi',
        'cursor',
        'kimi',
        'reasonix',
        'dsh',
        'kiro',
        'antigravity',
        'qoder',
        'qoderclicn',
        'traecli',
        'deveco',
        'grok',
        'qwen',
        'qwenpaw',
        'mcode',
        'dim',
        'zeroclaw'
    )) NOT VALID;
