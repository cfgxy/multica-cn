-- RUYI-46: per-project agent instructions (project.instructions), injected as
-- the `## Project Instructions` brief section right after Workspace Context.
-- Plain user text, same storage semantics as workspace.context; NULL when the
-- project lead hasn't set one and the section is not rendered.
--
-- Renumbered 441 → 451 (RUYI-75): prefix 441 collides with the upstream
-- 441_runtime_profile_add_codearts. DDL is idempotent so databases that
-- already applied the old 441 stem re-run this as 451 harmlessly.
ALTER TABLE project ADD COLUMN IF NOT EXISTS instructions TEXT;
