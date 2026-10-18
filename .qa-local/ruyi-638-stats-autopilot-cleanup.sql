\set ON_ERROR_STOP on
BEGIN;

DO $$
DECLARE
    fixture_ws UUID := '63800000-0000-4000-8000-000000000081';
    fixture_runtime UUID := '63800000-0000-4000-8000-0000000000a0';
    fixture_agent UUID := '63800000-0000-4000-8000-0000000000a1';
    paused_autopilot UUID := '63800000-0000-4000-8000-000000000097';
BEGIN
    IF current_database() <> 'multica_dev1' THEN
        RAISE EXCEPTION 'RUYI-638 stats/paused cleanup can only run in multica_dev1';
    END IF;
    IF EXISTS (
        SELECT 1 FROM workspace
        WHERE id = fixture_ws
          AND (slug <> 'ruyi638-proposal-fixture'
            OR name <> '[RUYI-638 QA] Proposal fixture workspace')
    ) OR EXISTS (
        SELECT 1 FROM agent_runtime
        WHERE id = fixture_runtime
          AND (workspace_id <> fixture_ws
            OR metadata->>'fixture_issue' <> 'RUYI-638')
    ) OR EXISTS (
        SELECT 1 FROM agent
        WHERE id = fixture_agent
          AND (workspace_id <> fixture_ws OR runtime_id <> fixture_runtime
            OR name <> '[RUYI-638 QA] Runtime fixture agent')
    ) OR EXISTS (
        SELECT 1 FROM autopilot
        WHERE id = paused_autopilot
          AND (workspace_id <> fixture_ws
            OR title <> '[RUYI-638 QA] Paused status fixture'
            OR assignee_id <> fixture_agent)
    ) OR EXISTS (
        SELECT 1 FROM task_usage_hourly
        WHERE provider = 'ruyi638_fixture' AND model = 'ruyi638-usage-v1'
          AND (workspace_id <> fixture_ws
            OR runtime_id <> fixture_runtime OR agent_id <> fixture_agent)
    ) THEN
        RAISE EXCEPTION 'refusing cleanup: RUYI-638 fixture identity or ownership boundary mismatch';
    END IF;

    DELETE FROM task_usage_hourly
    WHERE workspace_id = fixture_ws AND runtime_id = fixture_runtime
      AND agent_id = fixture_agent
      AND provider = 'ruyi638_fixture' AND model = 'ruyi638-usage-v1';

    DELETE FROM autopilot
    WHERE id = paused_autopilot AND workspace_id = fixture_ws
      AND title = '[RUYI-638 QA] Paused status fixture'
      AND assignee_id = fixture_agent;
END $$;

COMMIT;

SELECT json_build_object(
    'database', current_database(),
    'remaining_fixture_usage_rows', (SELECT count(*) FROM task_usage_hourly
        WHERE workspace_id = '63800000-0000-4000-8000-000000000081'
          AND runtime_id = '63800000-0000-4000-8000-0000000000a0'
          AND agent_id = '63800000-0000-4000-8000-0000000000a1'
          AND provider = 'ruyi638_fixture' AND model = 'ruyi638-usage-v1'),
    'remaining_paused_autopilot_rows', (SELECT count(*) FROM autopilot
        WHERE id = '63800000-0000-4000-8000-000000000097'
          AND workspace_id = '63800000-0000-4000-8000-000000000081'),
    'workspace_retained_for_qa', (SELECT count(*) FROM workspace
        WHERE id = '63800000-0000-4000-8000-000000000081'
          AND slug = 'ruyi638-proposal-fixture'),
    'unrelated_usage_markers', (SELECT count(*) FROM task_usage_hourly
        WHERE provider = 'ruyi638_fixture' AND model = 'ruyi638-usage-v1'
          AND (workspace_id <> '63800000-0000-4000-8000-000000000081'
            OR runtime_id <> '63800000-0000-4000-8000-0000000000a0'
            OR agent_id <> '63800000-0000-4000-8000-0000000000a1'))
);
