-- RUYI-425 stage 1: dual-slot agent runtimes + capability + secret foundations
-- (design doc 2026-10-04-RUYI-423-统一AgentContext架构设计.md §4.1/§4.4/§7.1).
--
-- agent.voice_runtime_id mirrors its text-slot sibling exactly: a plain UUID
-- column with a RESTRICT foreign key to agent_runtime(id), the same shape
-- migration 004 gave agent.runtime_id (constraint agent_runtime_id_fkey).
-- Deleting a voice runtime instance is therefore the application layer's job,
-- same as today's runtime-delete flow: detach agents first, then delete.
-- New-table columns keep the app-layer style (see migration 120's
-- agent_runtime.profile_id) — runtime_credential below has no FKs and the
-- only index it adds is its own composite PRIMARY KEY.
--
-- The existing agent.runtime_id column KEEPS its name and gains the text-slot
-- semantics by convention — no rename, no data copy, zero backfill: every
-- existing row is a text binding today and stays one (design §7.1).
ALTER TABLE agent
    ADD COLUMN voice_runtime_id UUID,
    ADD CONSTRAINT agent_voice_runtime_id_fkey
        FOREIGN KEY (voice_runtime_id) REFERENCES agent_runtime(id) ON DELETE RESTRICT;

-- runtime_profile.capabilities: the Type-layer capability declaration
-- (design §4.4). '{}' (the default on pre-existing rows) resolves to the
-- protocol family's baseline via agent.ResolveCapabilities; a non-empty
-- object is authoritative. Row shape is the agent.Capabilities struct:
-- {"text":bool,"realtime_voice":bool,"tools":bool}.
ALTER TABLE runtime_profile
    ADD COLUMN capabilities JSONB NOT NULL DEFAULT '{}';

-- agent_runtime.registration_source: how the instance was born (design §4.1).
-- Every instance that exists today was registered by a daemon probing local
-- CLI binaries, so 'daemon_discovered' is the correct default for all
-- pre-existing rows. API-backed voice instances (future rows) are registered
-- as 'manual'. The registration queries are intentionally left untouched:
-- INSERT picks up this default and ON CONFLICT DO UPDATE does not list the
-- column, so daemon re-registration never rewrites how an instance was born.
ALTER TABLE agent_runtime
    ADD COLUMN registration_source TEXT NOT NULL DEFAULT 'daemon_discovered';

-- agent_runtime.credential_ref: the ONLY credential carrier on the DB row —
-- a pointer into the server-side secret store, never a plaintext key
-- (design §4.5). Shape: '<instance-uuid>:<credential-key>', matching the
-- secrets_refs ref format in the Agent Context schema (design §3.3).
ALTER TABLE agent_runtime
    ADD COLUMN credential_ref TEXT;

-- Server-side secret store for runtime instance credentials (design §4.5,
-- reused at-rest encryption: secretbox AES-256-GCM, same construction as the
-- Lark/VCS/plugin boxes). One row per (instance, credential key); plaintext
-- never touches this table or any other.
CREATE TABLE runtime_credential (
    runtime_instance_id UUID NOT NULL,
    credential_key TEXT NOT NULL,
    secret_encrypted BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (runtime_instance_id, credential_key)
);

-- protocol_family whitelist: add the first voice family, 'gemini_live'
-- (design §4.4 baseline: {realtime_voice, tools}). NOT VALID preserves
-- historical-row tolerance for unknown families (a workspace restored from
-- an older dump) while enforcing the widened whitelist for new rows — same
-- pattern as migrations 403/441/904.
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
        'zcode',
        'gemini_live'
    )) NOT VALID;
