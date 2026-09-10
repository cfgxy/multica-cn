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
