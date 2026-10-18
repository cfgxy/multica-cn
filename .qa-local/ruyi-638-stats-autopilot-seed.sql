\set ON_ERROR_STOP on
BEGIN;

DO $$
DECLARE
    fixture_ws UUID := '63800000-0000-4000-8000-000000000081';
    fixture_owner UUID := '63800000-0000-4000-8000-000000000083';
    fixture_runtime UUID := '63800000-0000-4000-8000-0000000000a0';
    fixture_agent UUID := '63800000-0000-4000-8000-0000000000a1';
    runtime_issue UUID := '63800000-0000-4000-8000-0000000000b0';
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
    ) THEN
        RAISE EXCEPTION 'RUYI-638 disposable workspace or non-super-admin owner is missing/mismatched';
    END IF;

    IF EXISTS (
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
        SELECT 1 FROM task_usage_hourly
        WHERE (provider = 'ruyi638_fixture' AND model = 'ruyi638-usage-v1')
          AND (workspace_id <> fixture_ws
            OR runtime_id <> fixture_runtime
            OR agent_id <> fixture_agent)
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
    ) THEN
        RAISE EXCEPTION 'RUYI-638 fixture identifier/usage marker is already owned outside its dedicated workspace/runtime/agent';
    END IF;

    INSERT INTO agent_runtime (
        id, workspace_id, daemon_id, name, runtime_mode, provider, status,
        device_info, metadata, owner_id, visibility, last_seen_at
    ) VALUES (
        fixture_runtime, fixture_ws, 'ruyi638-stats-fixture',
        '[RUYI-638 QA] Runtime stats fixture', 'local', 'claude', 'offline',
        'No daemon is connected; this runtime exists only for isolated statistics.',
        '{"fixture_issue":"RUYI-638","fixture_kind":"runtime_stats"}'::jsonb,
        fixture_owner, 'public', NULL
    ) ON CONFLICT (id) DO UPDATE SET
        workspace_id = EXCLUDED.workspace_id,
        daemon_id = EXCLUDED.daemon_id,
        name = EXCLUDED.name,
        runtime_mode = EXCLUDED.runtime_mode,
        provider = EXCLUDED.provider,
        status = 'offline',
        device_info = EXCLUDED.device_info,
        metadata = EXCLUDED.metadata,
        owner_id = fixture_owner,
        visibility = 'public',
        last_seen_at = NULL,
        updated_at = now()
    WHERE agent_runtime.workspace_id = fixture_ws
      AND agent_runtime.metadata->>'fixture_issue' = 'RUYI-638'
      AND agent_runtime.metadata->>'fixture_kind' = 'runtime_stats';

    INSERT INTO agent (
        id, workspace_id, name, runtime_mode, runtime_config, visibility,
        status, owner_id, description, runtime_id
    ) VALUES (
        fixture_agent, fixture_ws, '[RUYI-638 QA] Runtime fixture agent',
        'local', '{}'::jsonb, 'workspace', 'offline', fixture_owner,
        'RUYI-638 isolated runtime stats fixture agent', fixture_runtime
    ) ON CONFLICT (id) DO UPDATE SET
        workspace_id = EXCLUDED.workspace_id,
        name = EXCLUDED.name,
        runtime_mode = EXCLUDED.runtime_mode,
        runtime_config = EXCLUDED.runtime_config,
        visibility = EXCLUDED.visibility,
        status = 'offline',
        owner_id = fixture_owner,
        description = EXCLUDED.description,
        runtime_id = fixture_runtime,
        updated_at = now()
    WHERE agent.workspace_id = fixture_ws
      AND agent.runtime_id = fixture_runtime
      AND agent.name = '[RUYI-638 QA] Runtime fixture agent'
      AND agent.description = 'RUYI-638 isolated runtime stats fixture agent';

    INSERT INTO issue (
        id, workspace_id, title, description, status, priority,
        creator_type, creator_id, number, metadata
    ) VALUES (
        runtime_issue, fixture_ws, '[RUYI-638 QA] Runtime stats fixture issue',
        'Dedicated issue for three terminal runtime-stat fixture tasks.',
        'done', 'low', 'member', fixture_owner,
        COALESCE((SELECT number FROM issue WHERE id = runtime_issue),
                 (SELECT COALESCE(MAX(number), 0) + 1 FROM issue WHERE workspace_id = fixture_ws)),
        '{"fixture_issue":"RUYI-638","fixture_kind":"runtime_stats"}'::jsonb
    ) ON CONFLICT (id) DO UPDATE SET
        description = EXCLUDED.description,
        status = 'done',
        metadata = EXCLUDED.metadata,
        updated_at = now()
    WHERE issue.workspace_id = fixture_ws
      AND issue.title = '[RUYI-638 QA] Runtime stats fixture issue'
      AND issue.metadata->>'fixture_issue' = 'RUYI-638';

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

    INSERT INTO agent_task_queue (
        id, agent_id, issue_id, runtime_id, status, priority,
        started_at, completed_at, created_at, result
    ) VALUES
        ('63800000-0000-4000-8000-0000000000b1', fixture_agent, runtime_issue,
         fixture_runtime, 'completed', 0, now() - interval '30 minutes',
         now() - interval '20 minutes', now() - interval '31 minutes',
         '{"fixture_issue":"RUYI-638","fixture_kind":"runtime_stats"}'::jsonb),
        ('63800000-0000-4000-8000-0000000000b2', fixture_agent, runtime_issue,
         fixture_runtime, 'completed', 0, now() - interval '15 minutes',
         now() - interval '10 minutes', now() - interval '16 minutes',
         '{"fixture_issue":"RUYI-638","fixture_kind":"runtime_stats"}'::jsonb),
        ('63800000-0000-4000-8000-0000000000b3', fixture_agent, runtime_issue,
         fixture_runtime, 'completed', 0, now() - interval '25 hours 10 minutes',
         now() - interval '25 hours', now() - interval '25 hours 11 minutes',
         '{"fixture_issue":"RUYI-638","fixture_kind":"runtime_stats"}'::jsonb)
    ON CONFLICT (id) DO UPDATE SET
        status = 'completed',
        priority = 0,
        started_at = EXCLUDED.started_at,
        completed_at = EXCLUDED.completed_at,
        created_at = EXCLUDED.created_at,
        result = EXCLUDED.result
    WHERE agent_task_queue.agent_id = fixture_agent
      AND agent_task_queue.runtime_id = fixture_runtime
      AND agent_task_queue.issue_id = runtime_issue
      AND agent_task_queue.result->>'fixture_issue' = 'RUYI-638'
      AND agent_task_queue.result->>'fixture_kind' = 'runtime_stats';

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
       OR EXISTS (SELECT 1 FROM autopilot_run WHERE autopilot_id = paused_autopilot)
       OR (SELECT count(*) FROM agent_task_queue
           WHERE id IN ('63800000-0000-4000-8000-0000000000b1',
                        '63800000-0000-4000-8000-0000000000b2',
                        '63800000-0000-4000-8000-0000000000b3')
             AND agent_id = fixture_agent AND runtime_id = fixture_runtime
             AND issue_id = runtime_issue AND status = 'completed'
             AND started_at IS NOT NULL AND completed_at IS NOT NULL
             AND result->>'fixture_issue' = 'RUYI-638'
             AND result->>'fixture_kind' = 'runtime_stats') <> 3 THEN
        RAISE EXCEPTION 'RUYI-638 stats/paused fixture failed validation or unexpectedly has trigger/run rows';
    END IF;
END $$;

COMMIT;

SELECT json_build_object(
    'database', current_database(),
    'workspace_id', '63800000-0000-4000-8000-000000000081',
    'usage_marker', 'ruyi638_fixture/ruyi638-usage-v1',
    'runtime_fixture_task_rows', (SELECT count(*) FROM agent_task_queue
        WHERE id IN ('63800000-0000-4000-8000-0000000000b1',
                     '63800000-0000-4000-8000-0000000000b2',
                     '63800000-0000-4000-8000-0000000000b3')
          AND agent_id = '63800000-0000-4000-8000-0000000000a1'
          AND runtime_id = '63800000-0000-4000-8000-0000000000a0'
          AND issue_id = '63800000-0000-4000-8000-0000000000b0'
          AND result->>'fixture_issue' = 'RUYI-638'
          AND result->>'fixture_kind' = 'runtime_stats'),
    'runtime_fixture_windows', json_build_object(
        '1d', (SELECT count(*) FROM agent_task_queue
            WHERE id IN ('63800000-0000-4000-8000-0000000000b1',
                         '63800000-0000-4000-8000-0000000000b2')
              AND started_at IS NOT NULL AND completed_at >= now() - interval '1 day'),
        '30d', (SELECT count(*) FROM agent_task_queue
            WHERE id IN ('63800000-0000-4000-8000-0000000000b1',
                         '63800000-0000-4000-8000-0000000000b2',
                         '63800000-0000-4000-8000-0000000000b3')
              AND started_at IS NOT NULL AND completed_at >= now() - interval '30 days')),
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
