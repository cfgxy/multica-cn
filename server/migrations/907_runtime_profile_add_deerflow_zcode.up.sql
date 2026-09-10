-- Add DeerFlow (deerflow-acp) and ZCode (zcode-acp) as first-party protocol
-- families. Both bridges were previously configured as runtime profiles over
-- the 'kimi' family, which made their tasks report as Kimi everywhere and
-- forced kimi's ACP behaviour onto backends that do not share it.
--
-- NOT VALID preserves historical-row tolerance while enforcing the expanded
-- whitelist for new rows. Existing profiles that still carry
-- protocol_family='kimi' with a deerflow/zcode command remain valid and keep
-- running on the kimi backend; migrating them to their own family is an
-- explicit user action, not a silent rewrite.
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
