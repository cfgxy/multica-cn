\set ON_ERROR_STOP on
BEGIN;

DO $$
DECLARE
    fixture_ws UUID := '63800000-0000-4000-8000-000000000081';
    fixture_runtime UUID := '63800000-0000-4000-8000-0000000000a0';
    fixture_agent UUID := '63800000-0000-4000-8000-0000000000a1';
    runtime_issue UUID := '63800000-0000-4000-8000-0000000000b0';
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
          AND (workspace_id IS DISTINCT FROM fixture_ws
            OR metadata->>'fixture_issue' IS DISTINCT FROM 'RUYI-638'
            OR metadata->>'fixture_kind' IS DISTINCT FROM 'runtime_stats')
    ) OR EXISTS (
        SELECT 1 FROM agent
        WHERE id = fixture_agent
          AND (workspace_id IS DISTINCT FROM fixture_ws
            OR runtime_id IS DISTINCT FROM fixture_runtime
            OR name IS DISTINCT FROM '[RUYI-638 QA] Runtime fixture agent'
            OR description IS DISTINCT FROM 'RUYI-638 isolated runtime stats fixture agent')
    ) OR EXISTS (
        SELECT 1 FROM autopilot
        WHERE id = paused_autopilot
          AND (workspace_id <> fixture_ws
            OR title <> '[RUYI-638 QA] Paused status fixture'
            OR assignee_id <> fixture_agent)
    ) OR EXISTS (
        SELECT 1 FROM issue
        WHERE id = runtime_issue
          AND (workspace_id IS DISTINCT FROM fixture_ws
            OR title IS DISTINCT FROM '[RUYI-638 QA] Runtime stats fixture issue'
            OR metadata->>'fixture_issue' IS DISTINCT FROM 'RUYI-638')
    ) OR EXISTS (
        SELECT 1 FROM agent_task_queue
        WHERE id IN (
            '63800000-0000-4000-8000-0000000000b1',
            '63800000-0000-4000-8000-0000000000b2',
            '63800000-0000-4000-8000-0000000000b3'
        ) AND (agent_id IS DISTINCT FROM fixture_agent
          OR runtime_id IS DISTINCT FROM fixture_runtime
          OR issue_id IS DISTINCT FROM runtime_issue
          OR result->>'fixture_issue' IS DISTINCT FROM 'RUYI-638'
          OR result->>'fixture_kind' IS DISTINCT FROM 'runtime_stats')
    ) OR EXISTS (
        SELECT 1 FROM agent_task_queue
        WHERE agent_id = fixture_agent
          AND id NOT IN (
              '63800000-0000-4000-8000-0000000000b1',
              '63800000-0000-4000-8000-0000000000b2',
              '63800000-0000-4000-8000-0000000000b3'
          )
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

    DELETE FROM agent_task_queue
    WHERE id IN (
        '63800000-0000-4000-8000-0000000000b1',
        '63800000-0000-4000-8000-0000000000b2',
        '63800000-0000-4000-8000-0000000000b3'
    ) AND agent_id = fixture_agent AND runtime_id = fixture_runtime
      AND issue_id = runtime_issue
      AND result->>'fixture_issue' = 'RUYI-638'
      AND result->>'fixture_kind' = 'runtime_stats';

    DELETE FROM autopilot
    WHERE id = paused_autopilot AND workspace_id = fixture_ws
      AND title = '[RUYI-638 QA] Paused status fixture'
      AND assignee_id = fixture_agent;

    DELETE FROM issue
    WHERE id = runtime_issue AND workspace_id = fixture_ws
      AND title = '[RUYI-638 QA] Runtime stats fixture issue'
      AND metadata->>'fixture_issue' = 'RUYI-638'
      AND NOT EXISTS (SELECT 1 FROM agent_task_queue WHERE issue_id = runtime_issue);

    DELETE FROM agent
    WHERE id = fixture_agent AND workspace_id = fixture_ws
      AND runtime_id = fixture_runtime
      AND name = '[RUYI-638 QA] Runtime fixture agent'
      AND description = 'RUYI-638 isolated runtime stats fixture agent'
      AND NOT EXISTS (SELECT 1 FROM agent_task_queue WHERE agent_id = fixture_agent)
      AND NOT EXISTS (SELECT 1 FROM autopilot WHERE assignee_id = fixture_agent);

    DELETE FROM agent_runtime
    WHERE id = fixture_runtime AND workspace_id = fixture_ws
      AND metadata->>'fixture_issue' = 'RUYI-638'
      AND metadata->>'fixture_kind' = 'runtime_stats'
      AND NOT EXISTS (SELECT 1 FROM agent WHERE runtime_id = fixture_runtime)
      AND NOT EXISTS (SELECT 1 FROM agent_task_queue WHERE runtime_id = fixture_runtime);

END $$;

COMMIT;

SELECT json_build_object(
    'database', current_database(),
    'remaining_fixture_usage_rows', (SELECT count(*) FROM task_usage_hourly
        WHERE workspace_id = '63800000-0000-4000-8000-000000000081'
          AND runtime_id = '63800000-0000-4000-8000-0000000000a0'
          AND agent_id = '63800000-0000-4000-8000-0000000000a1'
          AND provider = 'ruyi638_fixture' AND model = 'ruyi638-usage-v1'),
    'remaining_fixture_runtime_tasks', (SELECT count(*) FROM agent_task_queue
        WHERE id IN ('63800000-0000-4000-8000-0000000000b1',
                     '63800000-0000-4000-8000-0000000000b2',
                     '63800000-0000-4000-8000-0000000000b3')
          AND agent_id = '63800000-0000-4000-8000-0000000000a1'
          AND runtime_id = '63800000-0000-4000-8000-0000000000a0'
          AND issue_id = '63800000-0000-4000-8000-0000000000b0'
          AND result->>'fixture_issue' = 'RUYI-638'
          AND result->>'fixture_kind' = 'runtime_stats'),
    'remaining_fixture_runtime_issue', (SELECT count(*) FROM issue
        WHERE id = '63800000-0000-4000-8000-0000000000b0'
          AND workspace_id = '63800000-0000-4000-8000-000000000081'
          AND title = '[RUYI-638 QA] Runtime stats fixture issue'),
    'remaining_fixture_agent', (SELECT count(*) FROM agent
        WHERE id = '63800000-0000-4000-8000-0000000000a1'
          AND workspace_id = '63800000-0000-4000-8000-000000000081'
          AND name = '[RUYI-638 QA] Runtime fixture agent'),
    'remaining_fixture_runtime', (SELECT count(*) FROM agent_runtime
        WHERE id = '63800000-0000-4000-8000-0000000000a0'
          AND workspace_id = '63800000-0000-4000-8000-000000000081'
          AND metadata->>'fixture_issue' = 'RUYI-638'
          AND metadata->>'fixture_kind' = 'runtime_stats'),
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
