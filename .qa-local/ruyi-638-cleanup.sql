\set ON_ERROR_STOP on
BEGIN;
DO $$
DECLARE
    ws UUID;
BEGIN
    IF current_database() <> 'multica_dev1' THEN
        RAISE EXCEPTION 'fixture cleanup can only run in multica_dev1';
    END IF;
    SELECT id INTO ws FROM workspace WHERE slug = 'ruyi638-qa';
    IF ws IS NULL THEN
        RAISE NOTICE 'RUYI-638 QA workspace is absent; nothing to clean';
        RETURN;
    END IF;

    DELETE FROM task_usage_hourly WHERE runtime_id = '63800000-0000-4000-8000-000000000001';
    DELETE FROM task_usage WHERE task_id IN (
        '63800000-0000-4000-8000-000000000020',
        '63800000-0000-4000-8000-000000000021'
    );
    DELETE FROM agent_task_queue WHERE id IN (
        '63800000-0000-4000-8000-000000000020',
        '63800000-0000-4000-8000-000000000021'
    );
    DELETE FROM issue WHERE workspace_id = ws AND id IN (
        '63800000-0000-4000-8000-000000000010',
        '63800000-0000-4000-8000-000000000011'
    ) AND metadata->>'fixture_issue' = 'RUYI-638';

    DELETE FROM autopilot_run WHERE autopilot_id IN (
        '63800000-0000-4000-8000-000000000050',
        '63800000-0000-4000-8000-000000000051',
        '63800000-0000-4000-8000-000000000052'
    );
    DELETE FROM autopilot_trigger WHERE autopilot_id IN (
        '63800000-0000-4000-8000-000000000050',
        '63800000-0000-4000-8000-000000000051',
        '63800000-0000-4000-8000-000000000052'
    );
    DELETE FROM autopilot WHERE workspace_id = ws AND id IN (
        '63800000-0000-4000-8000-000000000050',
        '63800000-0000-4000-8000-000000000051',
        '63800000-0000-4000-8000-000000000052'
    ) AND title LIKE '[RUYI-638 QA]%';

    DELETE FROM prompt_proposal WHERE workspace_id = ws AND id IN (
        '63800000-0000-4000-8000-000000000040',
        '63800000-0000-4000-8000-000000000041',
        '63800000-0000-4000-8000-000000000042'
    );
    DELETE FROM agent_skill WHERE agent_id = '63800000-0000-4000-8000-000000000002';
    DELETE FROM skill_file WHERE skill_id IN (
        '63800000-0000-4000-8000-000000000030',
        '63800000-0000-4000-8000-000000000031'
    );
    DELETE FROM skill_version WHERE skill_id IN (
        '63800000-0000-4000-8000-000000000030',
        '63800000-0000-4000-8000-000000000031'
    );
    DELETE FROM skill WHERE workspace_id = ws AND id IN (
        '63800000-0000-4000-8000-000000000030',
        '63800000-0000-4000-8000-000000000031'
    ) AND name LIKE '[RUYI-638 QA]%';

    DELETE FROM agent WHERE workspace_id = ws
      AND id = '63800000-0000-4000-8000-000000000002'
      AND name = '[RUYI-638 QA] Offline fixture agent';
    DELETE FROM agent_runtime WHERE workspace_id = ws
      AND id = '63800000-0000-4000-8000-000000000001'
      AND metadata->>'fixture_issue' = 'RUYI-638';

    DELETE FROM member WHERE workspace_id = '63800000-0000-4000-8000-000000000005';
    DELETE FROM workspace WHERE id = '63800000-0000-4000-8000-000000000005'
      AND slug = 'ruyi638-admin-list'
      AND name = '[RUYI-638 QA] Admin list fixture';
END $$;
COMMIT;

SELECT json_build_object(
    'database', current_database(),
    'remaining_workspace_fixture', (SELECT count(*) FROM workspace WHERE id = '63800000-0000-4000-8000-000000000005'),
    'remaining_runtime', (SELECT count(*) FROM agent_runtime WHERE id = '63800000-0000-4000-8000-000000000001'),
    'remaining_agent', (SELECT count(*) FROM agent WHERE id = '63800000-0000-4000-8000-000000000002'),
    'remaining_fixture_issues', (SELECT count(*) FROM issue WHERE metadata->>'fixture_issue' = 'RUYI-638'),
    'remaining_usage_rollups', (SELECT count(*) FROM task_usage_hourly WHERE runtime_id = '63800000-0000-4000-8000-000000000001'),
    'remaining_skills', (SELECT count(*) FROM skill WHERE id IN ('63800000-0000-4000-8000-000000000030','63800000-0000-4000-8000-000000000031')),
    'remaining_proposals', (SELECT count(*) FROM prompt_proposal WHERE id IN ('63800000-0000-4000-8000-000000000040','63800000-0000-4000-8000-000000000041','63800000-0000-4000-8000-000000000042')),
    'remaining_autopilots', (SELECT count(*) FROM autopilot WHERE id IN ('63800000-0000-4000-8000-000000000050','63800000-0000-4000-8000-000000000051','63800000-0000-4000-8000-000000000052'))
);
