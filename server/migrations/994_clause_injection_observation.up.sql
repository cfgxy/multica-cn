-- Preserve the carrier identity and degradation state independently of
-- clause-level rows, which may be absent when a snapshot cannot be parsed.
CREATE TABLE clause_injection_observation (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    task_id UUID NOT NULL,
    carrier_scope TEXT NOT NULL CHECK (carrier_scope IN ('workspace', 'project', 'squad', 'agent')),
    carrier_scope_id UUID NOT NULL,
    degraded BOOLEAN NOT NULL DEFAULT false,
    degradation_reason TEXT NOT NULL CHECK (degradation_reason IN ('kill_switch', 'query_error', 'parse_error')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
