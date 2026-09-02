-- RUYI-46: per-project agent instructions (project.instructions), injected as
-- the `## Project Instructions` brief section right after Workspace Context.
-- Plain user text, same storage semantics as workspace.context; NULL when the
-- project lead hasn't set one and the section is not rendered.
ALTER TABLE project ADD COLUMN instructions TEXT;
