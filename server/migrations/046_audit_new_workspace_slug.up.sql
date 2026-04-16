-- Audit existing workspace slugs against the newly-added "new-workspace"
-- reserved slug. The frontend wires /new-workspace as the global workspace
-- creation page (replacing the old /onboarding flow); reserving the slug
-- prevents a workspace from being created with slug = "new-workspace" that
-- would shadow that route.
--
-- Keep this slug in sync with:
--  - server/internal/handler/workspace_reserved_slugs.go
--  - packages/core/paths/reserved-slugs.ts

DO $$
DECLARE
  conflict_count INT;
BEGIN
  SELECT COUNT(*) INTO conflict_count
  FROM workspace
  WHERE slug = 'new-workspace';

  IF conflict_count > 0 THEN
    RAISE EXCEPTION 'Found % workspace(s) with slug "new-workspace" that collides with the global route. Rename or delete before deploying.', conflict_count;
  END IF;
END $$;
