-- Down for 925_agent_context_dual_slot (RUYI-425 stage 1).
--
-- Restores the pre-925 shape: single runtime slot, no capability column, no
-- registration source, no credential_ref, no server-side credential store,
-- and the 27-family protocol whitelist (no 'gemini_live'). Rollback
-- semantics follow design doc §7.5: voice bindings are detached and
-- voice-family profiles (and their instances and credentials) are removed —
-- the only rows a pre-925 schema cannot represent. Text-slot bindings,
-- profiles, instances and agents survive the round trip untouched.
--
-- Order matters. Voice bindings must leave agent.voice_runtime_id before the
-- instances they point at are deleted — the column carries the same
-- ON DELETE RESTRICT foreign key as the text slot (migration 004), so the
-- database itself refuses to delete a referenced instance. Voice-family rows
-- must leave agent_runtime and runtime_profile before the narrower CHECK
-- returns (a pre-925 daemon would refuse to register a 'gemini_live' profile
-- it cannot map to any backend). Credentials must go before their table is
-- dropped; column drops come last so the cleanup statements above can still
-- reference the columns they inspect.

-- Detach every voice binding. The column is being dropped wholesale, so
-- there is nothing to preserve: any surviving value would either dangle
-- after the instance deletes below or, worse, block them via RESTRICT.
UPDATE agent SET voice_runtime_id = NULL;

-- Remove stored credentials for every voice-family instance, plus any
-- orphaned credential rows (plain UUIDs, no DB FK, so app-layer cleanup is
-- the house pattern). Plaintext never existed on these rows; the encrypted
-- payloads are simply destroyed.
DELETE FROM runtime_credential
WHERE runtime_instance_id IN (
    SELECT ar.id
    FROM agent_runtime ar
    JOIN runtime_profile rp ON ar.profile_id = rp.id
    WHERE rp.protocol_family = 'gemini_live'
);

DELETE FROM runtime_credential rc
WHERE NOT EXISTS (
    SELECT 1 FROM agent_runtime ar WHERE ar.id = rc.runtime_instance_id
);

-- A text-slot reference to a voice instance can only exist as malformed
-- app-layer state (the capability gate forbids it); clear it rather than
-- let RESTRICT fail the instance delete behind a confusing error.
UPDATE agent
SET runtime_id = NULL
WHERE runtime_id IN (
    SELECT ar.id
    FROM agent_runtime ar
    JOIN runtime_profile rp ON ar.profile_id = rp.id
    WHERE rp.protocol_family = 'gemini_live'
);

-- Delete the voice-family instances, then their profiles. No tasks can
-- reference them: voice dispatch does not exist at this stage, so a
-- gemini_live instance has never executed a task.
DELETE FROM agent_runtime
WHERE profile_id IN (
    SELECT id FROM runtime_profile WHERE protocol_family = 'gemini_live'
);

DELETE FROM runtime_profile
WHERE protocol_family = 'gemini_live';

-- Restore the 27-family whitelist (the post-904 state). NOT VALID, same as
-- every other family migration's down.
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
        'zeroclaw',
        'deerflow',
        'zcode'
    )) NOT VALID;

DROP TABLE runtime_credential;

ALTER TABLE agent_runtime DROP COLUMN credential_ref;
ALTER TABLE agent_runtime DROP COLUMN registration_source;

ALTER TABLE runtime_profile DROP COLUMN capabilities;

ALTER TABLE agent DROP COLUMN voice_runtime_id;
