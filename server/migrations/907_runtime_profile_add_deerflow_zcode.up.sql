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

-- Restore the identities a previous rollback folded onto 'kimi'.
--
-- The down migration records the family of every row it collapses, because a
-- profile in one of the new families is not required to launch the standard
-- bridge command: the API accepts any single-token command, so
-- ('zcode', 'company-zcode-wrapper') is a legal row whose identity the
-- basename rewrite below cannot reconstruct. Without this step an
-- up -> down -> up cycle would strand exactly those rows on 'kimi' forever.
--
-- The table only exists on a deployment that has rolled 907 back at least
-- once, hence the to_regclass guard; it is dropped once consumed, so the
-- restore never replays against a later, deliberate family change.
DO $$
BEGIN
    IF to_regclass('runtime_profile_family_907_backup') IS NOT NULL THEN
        UPDATE runtime_profile p
        SET protocol_family = b.protocol_family,
            updated_at = now()
        FROM runtime_profile_family_907_backup b
        WHERE p.id = b.profile_id
          AND p.protocol_family = 'kimi';

        DROP TABLE runtime_profile_family_907_backup;
    END IF;
END $$;

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
-- The basename is taken across both path separators and with a Windows
-- executable suffix removed, because all three shapes are legal existing
-- profiles: the daemon is supported on Windows, where an absolute override
-- reads 'C:\tools\zcode-acp', an npm-installed bridge is entered through a
-- '.cmd' shim and a pip console script through a '.exe' launcher.
-- validateRuntimeProfileCommandName accepts every one of them — it only
-- rejects whitespace and NUL — so a Unix-only match would leave real Windows
-- shims running on kimiBackend. Comparison is case-folded for the same
-- reason: Windows paths do not preserve case meaningfully.
UPDATE runtime_profile
SET protocol_family = 'deerflow',
    updated_at = now()
WHERE protocol_family = 'kimi'
  AND regexp_replace(
          lower(regexp_replace(command_name, '^.*[/\\]', '')),
          '\.(exe|cmd|bat|ps1)$', ''
      ) = 'deerflow-acp';

UPDATE runtime_profile
SET protocol_family = 'zcode',
    updated_at = now()
WHERE protocol_family = 'kimi'
  AND regexp_replace(
          lower(regexp_replace(command_name, '^.*[/\\]', '')),
          '\.(exe|cmd|bat|ps1)$', ''
      ) = 'zcode-acp';
