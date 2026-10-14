-- Voice provider pool + binding-free voice config model (RUYI-693, stage D2).
--
-- Owner decision 2026-10-11 10:48 (RUYI-686 决策卡 2/3): voice configuration
-- moves from per-agent runtime binding to a resolution chain —
-- agent override (provider_id+model+advanced triple) → workspace default
-- (workspace.settings "voice" key) → official vanilla fallback. The gate
-- falls back only on missing/invalid credentials; unreachable never blocks
-- initiation (RUYI-619 semantics, same as the instance badge).
--
-- House rules honored here: no new database foreign keys — workspace_id,
-- agent.voice_provider_id and voice_provider.source_runtime_id are plain
-- UUIDs whose integrity lives in the application layer (workspace teardown
-- sweeps voice_provider in DeleteWorkspaceRuntimesAndProjects; the provider
-- delete path clears agent overrides and settings references itself). The
-- workspace index is a plain CREATE INDEX, not CONCURRENTLY: its table is
-- created in this same file, so the whole file is one implicit transaction
-- on an empty table (902/914 consolidation precedent) and a concurrent
-- build would only add failure modes. Single-issue single-number: table,
-- columns, and the additive data migration share this one stem.
--
-- Additive by design (读向兼容): the legacy manual agent_runtime rows, their
-- bindings (agent.voice_runtime_id) and their runtime_credential secrets are
-- left untouched — the migrated voice_provider rows carry COPIES of the
-- secret ciphertext (same MULTICA_RUNTIME_CREDENTIAL_SECRET_KEY box, so the
-- bytes decrypt identically), and the legacy gate keeps working during the
-- compat window. The down migration removes structures only; migrated data
-- is not rewound (the chain reads forward-only state).

CREATE TABLE voice_provider (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Owning workspace. Plain UUID; cleanup on workspace delete is the
    -- application layer's job (DeleteWorkspaceRuntimesAndProjects).
    workspace_id UUID NOT NULL,
    display_name TEXT NOT NULL,
    -- Provider family behind the pool entry. Whitelist enforced as a
    -- NOT-VALID table constraint below (same pattern as the runtime_profile
    -- protocol_family check in 930): unknown historical rows stay readable,
    -- new rows must declare a known type. 'chatgpt' is reserved for the
    -- OpenAI Realtime family; the gateway only serves 'gemini' today and
    -- the resolution chain falls back to vanilla for anything else.
    provider_type TEXT NOT NULL,
    -- Pointer into the shared runtime_credential secret store, shape
    -- '<voice-provider-uuid>:<credential-key>' — the same credential_ref
    -- contract agent_runtime uses. No plaintext key ever touches this row.
    credential_ref TEXT,
    -- Bag carrying the credential_probe badge (same key and four-state
    -- semantics as the instance badge: ok/invalid/unreachable/skipped).
    metadata JSONB NOT NULL DEFAULT '{}',
    enabled BOOLEAN NOT NULL DEFAULT true,
    -- Provenance for rows seeded from legacy manual agent_runtime instances:
    -- the migrated-from instance id, or NULL for rows born in the pool API.
    -- Lets the agent-override backfill below join through it and lets the
    -- delete path reason about the compat window.
    source_runtime_id UUID,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_voice_provider_workspace ON voice_provider(workspace_id);

ALTER TABLE voice_provider ADD CONSTRAINT voice_provider_provider_type_check
    CHECK (provider_type IN ('gemini', 'chatgpt')) NOT VALID;

-- Agent-level override triple (Owner decision: provider_id+model+advanced,
-- level-wise atomic — a set override replaces the whole triple, never a
-- field-wise merge with the workspace default). All nullable: NULL
-- voice_provider_id means "no override, fall through to the workspace
-- default". No index: reference checks run on the rare provider-delete
-- admin path only (same trade as runtime_profile.profile_id since 120).
ALTER TABLE agent
    ADD COLUMN voice_provider_id UUID,
    ADD COLUMN voice_model TEXT,
    ADD COLUMN voice_advanced JSONB;

-- Seed the pool from every legacy manual voice instance. enabled mirrors
-- the instance's metadata.disabled flag so a disabled instance migrates to
-- a disabled provider (chain behavior identical before and after).
INSERT INTO voice_provider (
    workspace_id, display_name, provider_type, credential_ref,
    metadata, enabled, source_runtime_id, created_at, updated_at
)
SELECT
    rt.workspace_id,
    rt.name,
    'gemini',
    rt.credential_ref,
    COALESCE(rt.metadata, '{}'::jsonb),
    NOT COALESCE((rt.metadata->>'disabled')::boolean, false),
    rt.id,
    rt.created_at,
    now()
FROM agent_runtime rt
WHERE rt.registration_source = 'manual'
  AND rt.provider = 'gemini_live';

-- Copy the instance secrets so both projections decrypt the same keys
-- independently: the ciphertext is portable across rows of the same box,
-- and the legacy rows keep their own copies (delete paths stay decoupled).
INSERT INTO runtime_credential (runtime_instance_id, credential_key, secret_encrypted, created_at, updated_at)
SELECT
    vp.id,
    rc.credential_key,
    rc.secret_encrypted,
    rc.created_at,
    rc.updated_at
FROM runtime_credential rc
JOIN voice_provider vp ON vp.source_runtime_id = rc.runtime_instance_id
ON CONFLICT DO NOTHING;

-- Backfill agent overrides so bound agents keep their exact voice config
-- through the chain: provider id, model (leading "models/" stripped — the
-- gateway prepends the prefix itself) and advanced land as the triple.
-- Agents bound to non-migrated instances keep only the legacy binding; the
-- gateway's legacy bridge still serves them during the compat window.
UPDATE agent
SET voice_provider_id = vp.id,
    voice_model = NULLIF(
        CASE
            WHEN rt.metadata->>'model' LIKE 'models/%'
                THEN substring(rt.metadata->>'model' from 8)
            ELSE rt.metadata->>'model'
        END, ''),
    voice_advanced = rt.metadata->'advanced',
    updated_at = now()
FROM voice_provider vp
JOIN agent_runtime rt ON rt.id = vp.source_runtime_id
WHERE agent.voice_runtime_id = rt.id
  AND agent.voice_provider_id IS NULL;
