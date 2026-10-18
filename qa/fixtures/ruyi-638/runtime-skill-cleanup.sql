\set ON_ERROR_STOP on
BEGIN;

DO $$
DECLARE
    fixture_ws UUID := '63800000-0000-4000-8000-000000000081';
    fixture_runtime UUID := '63800000-0000-4000-8000-0000000000a0';
    fixture_agent UUID := '63800000-0000-4000-8000-0000000000a1';
    fixture_skill UUID := '63800000-0000-4000-8000-0000000000a2';
    reference_skill UUID := '63800000-0000-4000-8000-0000000000a3';
BEGIN
    IF current_database() <> 'multica_dev1' THEN
        RAISE EXCEPTION 'RUYI-638 runtime/Skill cleanup can only run in multica_dev1';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM workspace
        WHERE id = fixture_ws AND slug = 'ruyi638-proposal-fixture'
          AND name = '[RUYI-638 QA] Proposal fixture workspace'
    ) THEN
        RAISE EXCEPTION 'refusing cleanup: dedicated RUYI-638 workspace identity mismatch';
    END IF;
    IF EXISTS (SELECT 1 FROM agent_runtime WHERE id = fixture_runtime
        AND (workspace_id <> fixture_ws OR metadata->>'fixture_issue' <> 'RUYI-638'))
       OR EXISTS (SELECT 1 FROM agent WHERE id = fixture_agent
        AND (workspace_id <> fixture_ws OR runtime_id <> fixture_runtime))
       OR EXISTS (SELECT 1 FROM skill WHERE id IN (fixture_skill, reference_skill)
        AND workspace_id <> fixture_ws) THEN
        RAISE EXCEPTION 'refusing cleanup: fixture identifier is owned outside its dedicated workspace';
    END IF;
    IF EXISTS (SELECT 1 FROM agent
        WHERE (runtime_id = fixture_runtime OR voice_runtime_id = fixture_runtime)
          AND id <> fixture_agent) THEN
        RAISE EXCEPTION 'refusing cleanup: runtime is now referenced by a non-fixture agent';
    END IF;
    IF EXISTS (SELECT 1 FROM agent_skill
        WHERE skill_id IN (fixture_skill, reference_skill) AND agent_id <> fixture_agent) THEN
        RAISE EXCEPTION 'refusing cleanup: a fixture Skill is now assigned to a non-fixture agent';
    END IF;
    IF EXISTS (SELECT 1 FROM agent_task_queue WHERE agent_id = fixture_agent)
       OR EXISTS (SELECT 1 FROM task_usage WHERE task_id IN (
           SELECT id FROM agent_task_queue WHERE agent_id = fixture_agent
       )) THEN
        RAISE EXCEPTION 'refusing cleanup: fixture agent has execution/usage records requiring explicit review';
    END IF;

    DELETE FROM agent_skill WHERE agent_id = fixture_agent
      AND skill_id IN (fixture_skill, reference_skill);
    DELETE FROM skill_file WHERE skill_id IN (fixture_skill, reference_skill);
    DELETE FROM skill_version WHERE skill_id IN (fixture_skill, reference_skill)
      AND workspace_id = fixture_ws;
    DELETE FROM skill WHERE workspace_id = fixture_ws
      AND id IN (fixture_skill, reference_skill);
    DELETE FROM agent WHERE workspace_id = fixture_ws AND id = fixture_agent
      AND runtime_id = fixture_runtime;
    DELETE FROM agent_runtime WHERE workspace_id = fixture_ws AND id = fixture_runtime
      AND metadata->>'fixture_issue' = 'RUYI-638';
END $$;

COMMIT;

SELECT json_build_object(
    'database', current_database(),
    'remaining_runtime', (SELECT count(*) FROM agent_runtime WHERE id = '63800000-0000-4000-8000-0000000000a0'),
    'remaining_agent', (SELECT count(*) FROM agent WHERE id = '63800000-0000-4000-8000-0000000000a1'),
    'remaining_skills', (SELECT count(*) FROM skill WHERE id IN ('63800000-0000-4000-8000-0000000000a2','63800000-0000-4000-8000-0000000000a3')),
    'remaining_skill_versions', (SELECT count(*) FROM skill_version WHERE skill_id IN ('63800000-0000-4000-8000-0000000000a2','63800000-0000-4000-8000-0000000000a3'))
);
