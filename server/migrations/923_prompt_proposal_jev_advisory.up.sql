-- RUYI-347 batch A: warn-only jev advisory sidecar on the proposal pool.
-- NULL = the advisory layer is disabled or the row predates a check; the
-- fail-closed gate semantics (gate_errors/gate_warnings) are untouched.
ALTER TABLE prompt_proposal ADD COLUMN jev_advisory JSONB;
