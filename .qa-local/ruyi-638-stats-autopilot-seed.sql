\set ON_ERROR_STOP on
BEGIN;

DO $$
DECLARE
    fixture_ws UUID := '63800000-0000-4000-8000-000000000081';
    fixture_owner UUID := '63800000-0000-4000-8000-000000000083';
    fixture_runtime UUID := '63800000-0000-4000-8000-0000000000a0';
    fixture_agent UUID := '63800000-0000-4000-8000-0000000000a1';
    paused_autopilot UUID := '63800000-0000-4000-8000-000000000097';
BEGIN
    IF current_database() <> 'multica_dev1' THEN
        RAISE EXCEPTION 'RUYI-638 stats/paused fixture can only run in multica_dev1';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM workspace
        WHERE id = fixture_ws AND slug = 'ruyi638-proposal-fixture'
          AND name = '[RUYI-638 QA] Proposal fixture workspace'
    ) OR NOT EXISTS (
        SELECT 1 FROM member
        WHERE workspace_id = fixture_ws AND user_id = fixture_owner AND role = 'owner'
    ) OR NOT EXISTS (
        SELECT 1 FROM "user"
        WHERE id = fixture_owner AND is_super_admin IS FALSE
    ) OR NOT EXISTS (
        SELECT 1 FROM agent_runtime
        WHERE id = fixture_runtime AND workspace_id = fixture_ws
          AND metadata->>'fixture_issue' = 'RUYI-638'
    ) OR NOT EXISTS (
        SELECT 1 FROM agent
        WHERE id = fixture_agent AND workspace_id = fixture_ws
          AND runtime_id = fixture_runtime
          AND name = '[RUYI-638 QA] Runtime fixture agent'
    ) THEN
        RAISE EXCEPTION 'RUYI-638 disposable workspace, non-super-admin owner, runtime, or agent is missing/mismatched';
    END IF;

    IF EXISTS (
        SELECT 1 FROM autopilot
        WHERE id = paused_autopilot
          AND (workspace_id <> fixture_ws
            OR title <> '[RUYI-638 QA] Paused status fixture'
            OR assignee_id <> fixture_agent)
    ) OR EXISTS (
        SELECT 1 FROM task_usage_hourly
        WHERE (provider = 'ruyi638_fixture' AND model = 'ruyi638-usage-v1')
          AND (workspace_id <> fixture_ws
            OR runtime_id <> fixture_runtime
            OR agent_id <> fixture_agent)
    ) THEN
        RAISE EXCEPTION 'RUYI-638 fixture identifier/usage marker is already owned outside its dedicated workspace/runtime/agent';
    END IF;

    INSERT INTO task_usage_hourly (
        bucket_hour, workspace_id, runtime_id, agent_id, project_id,
        provider, model, input_tokens, output_tokens,
        cache_read_tokens, cache_write_tokens, task_count, event_count,
        cost_usd_ticks, uncosted_input_tokens, uncosted_output_tokens,
        uncosted_cache_read_tokens, uncosted_cache_write_tokens, updated_at
    ) VALUES
        (date_trunc('hour', now()), fixture_ws, fixture_runtime, fixture_agent, NULL,
         'ruyi638_fixture', 'ruyi638-usage-v1', 65000, 12000,
         4000, 1000, 2, 4, 0, 65000, 12000, 4000, 1000, now()),
        (date_trunc('hour', now() - interval '1 day'), fixture_ws, fixture_runtime, fixture_agent, NULL,
         'ruyi638_fixture', 'ruyi638-usage-v1', 32000, 6000,
         2000, 500, 1, 2, 0, 32000, 6000, 2000, 500, now())
    ON CONFLICT ON CONSTRAINT uq_task_usage_hourly_key DO UPDATE SET
        input_tokens = EXCLUDED.input_tokens,
        output_tokens = EXCLUDED.output_tokens,
        cache_read_tokens = EXCLUDED.cache_read_tokens,
        cache_write_tokens = EXCLUDED.cache_write_tokens,
        task_count = EXCLUDED.task_count,
        event_count = EXCLUDED.event_count,
        cost_usd_ticks = EXCLUDED.cost_usd_ticks,
        uncosted_input_tokens = EXCLUDED.uncosted_input_tokens,
        uncosted_output_tokens = EXCLUDED.uncosted_output_tokens,
        uncosted_cache_read_tokens = EXCLUDED.uncosted_cache_read_tokens,
        uncosted_cache_write_tokens = EXCLUDED.uncosted_cache_write_tokens,
        updated_at = EXCLUDED.updated_at
    WHERE task_usage_hourly.workspace_id = fixture_ws
      AND task_usage_hourly.runtime_id = fixture_runtime
      AND task_usage_hourly.agent_id = fixture_agent
      AND task_usage_hourly.provider = 'ruyi638_fixture'
      AND task_usage_hourly.model = 'ruyi638-usage-v1';

    INSERT INTO autopilot (
        id, workspace_id, title, description, assignee_id, assignee_type,
        status, execution_mode, created_by_type, created_by_id, last_run_at
    ) VALUES (
        paused_autopilot, fixture_ws, '[RUYI-638 QA] Paused status fixture',
        'Disposable paused-state fixture; no trigger or run is configured.',
        fixture_agent, 'agent', 'paused', 'run_only', 'member', fixture_owner, NULL
    ) ON CONFLICT (id) DO UPDATE SET
        description = EXCLUDED.description,
        status = 'paused',
        execution_mode = EXCLUDED.execution_mode,
        last_run_at = NULL,
        updated_at = now()
    WHERE autopilot.workspace_id = fixture_ws
      AND autopilot.title = '[RUYI-638 QA] Paused status fixture'
      AND autopilot.assignee_id = fixture_agent;

    IF (SELECT count(*) FROM task_usage_hourly
        WHERE workspace_id = fixture_ws AND runtime_id = fixture_runtime
          AND agent_id = fixture_agent AND provider = 'ruyi638_fixture'
          AND model = 'ruyi638-usage-v1') <> 2
       OR NOT EXISTS (SELECT 1 FROM autopilot
          WHERE id = paused_autopilot AND workspace_id = fixture_ws
            AND title = '[RUYI-638 QA] Paused status fixture'
            AND assignee_id = fixture_agent AND status = 'paused')
       OR EXISTS (SELECT 1 FROM autopilot_trigger WHERE autopilot_id = paused_autopilot)
       OR EXISTS (SELECT 1 FROM autopilot_run WHERE autopilot_id = paused_autopilot) THEN
        RAISE EXCEPTION 'RUYI-638 stats/paused fixture failed validation or unexpectedly has trigger/run rows';
    END IF;
END $$;

COMMIT;

SELECT json_build_object(
    'database', current_database(),
    'workspace_id', '63800000-0000-4000-8000-000000000081',
    'usage_marker', 'ruyi638_fixture/ruyi638-usage-v1',
    'usage_rows', (SELECT count(*) FROM task_usage_hourly
        WHERE workspace_id = '63800000-0000-4000-8000-000000000081'
          AND runtime_id = '63800000-0000-4000-8000-0000000000a0'
          AND agent_id = '63800000-0000-4000-8000-0000000000a1'
          AND provider = 'ruyi638_fixture' AND model = 'ruyi638-usage-v1'),
    'usage_tokens', (SELECT json_build_object(
        'input', sum(input_tokens), 'output', sum(output_tokens),
        'cache_read', sum(cache_read_tokens), 'cache_write', sum(cache_write_tokens),
        'tasks', sum(task_count), 'events', sum(event_count)
    ) FROM task_usage_hourly
        WHERE workspace_id = '63800000-0000-4000-8000-000000000081'
          AND runtime_id = '63800000-0000-4000-8000-0000000000a0'
          AND agent_id = '63800000-0000-4000-8000-0000000000a1'
          AND provider = 'ruyi638_fixture' AND model = 'ruyi638-usage-v1'),
    'paused_autopilot_id', '63800000-0000-4000-8000-000000000097',
    'paused_autopilot_rows', (SELECT count(*) FROM autopilot
        WHERE id = '63800000-0000-4000-8000-000000000097'
          AND workspace_id = '63800000-0000-4000-8000-000000000081' AND status = 'paused'),
    'fixture_triggers', (SELECT count(*) FROM autopilot_trigger
        WHERE autopilot_id = '63800000-0000-4000-8000-000000000097'),
    'fixture_runs', (SELECT count(*) FROM autopilot_run
        WHERE autopilot_id = '63800000-0000-4000-8000-000000000097')
);
