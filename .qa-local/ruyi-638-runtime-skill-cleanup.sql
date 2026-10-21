\set ON_ERROR_STOP on
BEGIN;

DO $$
DECLARE
    fixture_ws UUID := '63800000-0000-4000-8000-000000000081';
    fixture_runtime UUID := '63800000-0000-4000-8000-0000000000c0';
    fixture_agent UUID := '63800000-0000-4000-8000-0000000000c1';
    lifecycle_skill UUID := '63800000-0000-4000-8000-0000000000c3';
    reference_skill UUID := '63800000-0000-4000-8000-0000000000c4';
BEGIN
    IF current_database() <> 'multica_dev1' THEN
        RAISE EXCEPTION 'RUYI-638 runtime/Skill cleanup can only run in multica_dev1';
    END IF;
    IF EXISTS (
        SELECT 1 FROM workspace
        WHERE id = fixture_ws
          AND (slug <> 'ruyi638-proposal-fixture'
            OR name <> '[RUYI-638 QA] Proposal fixture workspace')
    ) OR EXISTS (
        SELECT 1 FROM agent_runtime
        WHERE id = fixture_runtime
          AND (workspace_id IS DISTINCT FROM fixture_ws
            OR metadata->>'fixture_issue' IS DISTINCT FROM 'RUYI-638'
            OR metadata->>'fixture_kind' IS DISTINCT FROM 'runtime_skill')
    ) OR EXISTS (
        SELECT 1 FROM agent
        WHERE id = fixture_agent
          AND (workspace_id IS DISTINCT FROM fixture_ws
            OR runtime_id IS DISTINCT FROM fixture_runtime
            OR name IS DISTINCT FROM '[RUYI-638 QA] Runtime and Skill fixture agent')
    ) OR EXISTS (
        SELECT 1 FROM skill
        WHERE id IN (lifecycle_skill, reference_skill)
          AND (workspace_id IS DISTINCT FROM fixture_ws
            OR name NOT LIKE '[RUYI-638 QA]%')
    ) OR EXISTS (
        SELECT 1 FROM skill_version
        WHERE id IN ('63800000-0000-4000-8000-0000000000c5',
                     '63800000-0000-4000-8000-0000000000c6',
                     '63800000-0000-4000-8000-0000000000c7')
          AND (workspace_id IS DISTINCT FROM fixture_ws
            OR skill_id NOT IN (lifecycle_skill, reference_skill))
    ) OR EXISTS (
        SELECT 1 FROM agent_task_queue WHERE agent_id = fixture_agent
    ) OR EXISTS (
        SELECT 1 FROM autopilot WHERE assignee_id = fixture_agent
    ) THEN
        RAISE EXCEPTION 'refusing cleanup: RUYI-638 runtime/Skill ownership boundary mismatch or fixture in use';
    END IF;

    DELETE FROM agent_skill
    WHERE agent_id = fixture_agent AND skill_id IN (lifecycle_skill, reference_skill);
    DELETE FROM skill_file WHERE skill_id IN (lifecycle_skill, reference_skill);
    DELETE FROM skill_version
    WHERE id IN ('63800000-0000-4000-8000-0000000000c5',
                 '63800000-0000-4000-8000-0000000000c6',
                 '63800000-0000-4000-8000-0000000000c7')
      AND workspace_id = fixture_ws
      AND skill_id IN (lifecycle_skill, reference_skill);
    DELETE FROM skill
    WHERE id IN (lifecycle_skill, reference_skill)
      AND workspace_id = fixture_ws AND name LIKE '[RUYI-638 QA]%';
    DELETE FROM agent
    WHERE id = fixture_agent AND workspace_id = fixture_ws
      AND runtime_id = fixture_runtime
      AND name = '[RUYI-638 QA] Runtime and Skill fixture agent'
      AND NOT EXISTS (SELECT 1 FROM agent_task_queue WHERE agent_id = fixture_agent)
      AND NOT EXISTS (SELECT 1 FROM autopilot WHERE assignee_id = fixture_agent);
    DELETE FROM agent_runtime
    WHERE id = fixture_runtime AND workspace_id = fixture_ws
      AND metadata->>'fixture_issue' = 'RUYI-638'
      AND metadata->>'fixture_kind' = 'runtime_skill'
      AND NOT EXISTS (SELECT 1 FROM agent WHERE runtime_id = fixture_runtime)
      AND NOT EXISTS (SELECT 1 FROM agent_task_queue WHERE runtime_id = fixture_runtime);
END $$;

COMMIT;

SELECT json_build_object(
    'database', current_database(),
    'remaining_runtime', (SELECT count(*) FROM agent_runtime
        WHERE id = '63800000-0000-4000-8000-0000000000c0'
          AND metadata->>'fixture_issue' = 'RUYI-638'
          AND metadata->>'fixture_kind' = 'runtime_skill'),
    'remaining_agent', (SELECT count(*) FROM agent
        WHERE id = '63800000-0000-4000-8000-0000000000c1'
          AND name = '[RUYI-638 QA] Runtime and Skill fixture agent'),
    'remaining_skills', (SELECT count(*) FROM skill
        WHERE id IN ('63800000-0000-4000-8000-0000000000c3',
                     '63800000-0000-4000-8000-0000000000c4')),
    'remaining_skill_versions', (SELECT count(*) FROM skill_version
        WHERE id IN ('63800000-0000-4000-8000-0000000000c5',
                     '63800000-0000-4000-8000-0000000000c6',
                     '63800000-0000-4000-8000-0000000000c7')),
    'remaining_agent_skill_links', (SELECT count(*) FROM agent_skill
        WHERE agent_id = '63800000-0000-4000-8000-0000000000c1'
          AND skill_id IN ('63800000-0000-4000-8000-0000000000c3',
                           '63800000-0000-4000-8000-0000000000c4')),
    'workspace_retained_for_qa', (SELECT count(*) FROM workspace
        WHERE id = '63800000-0000-4000-8000-000000000081'
          AND slug = 'ruyi638-proposal-fixture')
);
