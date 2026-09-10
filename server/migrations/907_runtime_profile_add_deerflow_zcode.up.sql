-- Add DeerFlow (deerflow-acp) and ZCode (zcode-acp) as first-party protocol
-- families. Both bridges were previously configured as runtime profiles over
-- the 'kimi' family, which made their tasks report as Kimi everywhere and
-- forced kimi's ACP behaviour onto backends that do not share it.
--
-- NOT VALID preserves historical-row tolerance for families this whitelist
-- does not know about (a workspace restored from an older dump), while
-- enforcing the expanded whitelist for new rows.
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

-- Move the existing shim profiles onto their real identity.
--
-- Leaving them on 'kimi' is not a compatibility option: the daemon registers
-- each profile under its protocol_family, so such a profile keeps running on
-- kimiBackend and keeps reporting as Kimi in the UI, the daemon log and the
-- runtime metrics — the exact defect this migration exists to remove. The
-- rewrite is targeted at the rows whose identity is decidable from the
-- command they launch, keyed on the executable basename so a per-machine
-- absolute path override is covered too. A 'kimi' profile launching anything
-- else is a real Kimi profile and is left alone.
--
-- The row is updated in place: id, workspace_id, display_name, fixed_args and
-- visibility are untouched, so agent_runtime.profile_id and every agent bound
-- through it keep pointing at the same profile.
UPDATE runtime_profile
SET protocol_family = 'deerflow',
    updated_at = now()
WHERE protocol_family = 'kimi'
  AND regexp_replace(command_name, '^.*/', '') = 'deerflow-acp';

UPDATE runtime_profile
SET protocol_family = 'zcode',
    updated_at = now()
WHERE protocol_family = 'kimi'
  AND regexp_replace(command_name, '^.*/', '') = 'zcode-acp';
