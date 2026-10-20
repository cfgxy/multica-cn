\set ON_ERROR_STOP on
\echo 'Retired: this legacy seed modifies the existing ruyi638-qa workspace.'
\echo 'Use .qa-local/ruyi-638-followup-seed.sql for isolated fixtures.'
\quit
BEGIN;

DO $$
DECLARE
    ws UUID;
    owner_id UUID;
    member_id UUID;
    runtime_id UUID := '63800000-0000-4000-8000-000000000001';
    agent_id UUID := '63800000-0000-4000-8000-000000000002';
    issue_ok UUID := '63800000-0000-4000-8000-000000000010';
    issue_fail UUID := '63800000-0000-4000-8000-000000000011';
    skill_lifecycle UUID := '63800000-0000-4000-8000-000000000030';
    skill_reference UUID := '63800000-0000-4000-8000-000000000031';
    proposal_draft UUID := '63800000-0000-4000-8000-000000000040';
    proposal_pending UUID := '63800000-0000-4000-8000-000000000041';
    proposal_rejected UUID := '63800000-0000-4000-8000-000000000042';
    autopilot_active UUID := '63800000-0000-4000-8000-000000000050';
    autopilot_paused UUID := '63800000-0000-4000-8000-000000000051';
    autopilot_archived UUID := '63800000-0000-4000-8000-000000000052';
BEGIN
    IF current_database() <> 'multica_dev1' THEN
        RAISE EXCEPTION 'fixture can only run in multica_dev1';
    END IF;

    SELECT id INTO STRICT ws FROM workspace WHERE slug = 'ruyi638-qa';
    SELECT u.id INTO STRICT owner_id
    FROM member m
    JOIN "user" u ON u.id = m.user_id
    WHERE m.workspace_id = ws AND m.role = 'owner' AND u.is_super_admin IS TRUE;
    SELECT user_id INTO STRICT member_id
    FROM member WHERE workspace_id = ws AND role = 'member';

    INSERT INTO workspace (id, name, slug, description, settings)
    VALUES (
        '63800000-0000-4000-8000-000000000005',
        '[RUYI-638 QA] Admin list fixture', 'ruyi638-admin-list',
        'RUYI-638-only workspace-list fixture.', '{}'::jsonb
    ) ON CONFLICT (id) DO UPDATE SET
        name = EXCLUDED.name, description = EXCLUDED.description,
        settings = EXCLUDED.settings, updated_at = now();
    INSERT INTO member (workspace_id, user_id, role)
    VALUES ('63800000-0000-4000-8000-000000000005', owner_id, 'owner'),
           ('63800000-0000-4000-8000-000000000005', member_id, 'member')
    ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = EXCLUDED.role;

    INSERT INTO agent_runtime (
        id, workspace_id, daemon_id, name, runtime_mode, provider, status,
        device_info, metadata, owner_id, visibility, last_seen_at
    ) VALUES (
        runtime_id, ws, 'ruyi638-qa-offline-01', '[RUYI-638 QA] Offline runtime',
        'local', 'claude', 'offline', 'Fixture only; no daemon is connected',
        '{"fixture_issue":"RUYI-638","fixture":true}'::jsonb,
        owner_id, 'public', NULL
    ) ON CONFLICT (workspace_id, daemon_id, provider) WHERE profile_id IS NULL DO UPDATE SET
        name = EXCLUDED.name,
        status = 'offline',
        device_info = EXCLUDED.device_info,
        metadata = EXCLUDED.metadata,
        owner_id = EXCLUDED.owner_id,
        visibility = 'public',
        last_seen_at = NULL,
        updated_at = now();

    INSERT INTO agent (
        id, workspace_id, name, runtime_mode, runtime_config, visibility,
        status, owner_id, description, runtime_id
    ) VALUES (
        agent_id, ws, '[RUYI-638 QA] Offline fixture agent', 'local', '{}'::jsonb,
        'workspace', 'offline', owner_id,
        'RUYI-638 inert QA fixture; runtime has no connected daemon', runtime_id
    ) ON CONFLICT (id) DO UPDATE SET
        workspace_id = EXCLUDED.workspace_id,
        name = EXCLUDED.name,
        runtime_mode = EXCLUDED.runtime_mode,
        runtime_config = EXCLUDED.runtime_config,
        visibility = EXCLUDED.visibility,
        status = 'offline',
        owner_id = EXCLUDED.owner_id,
        description = EXCLUDED.description,
        runtime_id = EXCLUDED.runtime_id,
        updated_at = now();

    INSERT INTO issue (id, workspace_id, title, description, status, priority,
                       creator_type, creator_id, number, metadata)
    VALUES (
        issue_ok, ws, '[RUYI-638 QA] Stats completed fixture',
        'Synthetic usage fixture; safe to remove with the RUYI-638 cleanup script.',
        'done', 'medium', 'member', owner_id,
        COALESCE((SELECT number FROM issue WHERE id = issue_ok),
                 (SELECT COALESCE(MAX(number), 0) + 1 FROM issue WHERE workspace_id = ws)),
        '{"fixture_issue":"RUYI-638","fixture":true}'::jsonb
    ) ON CONFLICT (id) DO UPDATE SET status = 'done', updated_at = now();

    INSERT INTO issue (id, workspace_id, title, description, status, priority,
                       creator_type, creator_id, number, metadata)
    VALUES (
        issue_fail, ws, '[RUYI-638 QA] Stats failed fixture',
        'Synthetic failure-rate fixture; no agent execution is queued.',
        'blocked', 'low', 'member', owner_id,
        COALESCE((SELECT number FROM issue WHERE id = issue_fail),
                 (SELECT COALESCE(MAX(number), 0) + 1 FROM issue WHERE workspace_id = ws)),
        '{"fixture_issue":"RUYI-638","fixture":true}'::jsonb
    ) ON CONFLICT (id) DO UPDATE SET status = 'blocked', updated_at = now();

    INSERT INTO agent_task_queue (
        id, agent_id, issue_id, runtime_id, status, priority,
        started_at, completed_at, created_at, result
    ) VALUES (
        '63800000-0000-4000-8000-000000000020', agent_id, issue_ok, runtime_id,
        'completed', 0, now() - interval '18 minutes', now() - interval '8 minutes',
        now() - interval '20 minutes', '{"fixture_issue":"RUYI-638"}'::jsonb
    ) ON CONFLICT (id) DO UPDATE SET
        status = 'completed', started_at = EXCLUDED.started_at,
        completed_at = EXCLUDED.completed_at, result = EXCLUDED.result;

    INSERT INTO agent_task_queue (
        id, agent_id, issue_id, runtime_id, status, priority,
        started_at, completed_at, created_at, failure_reason, error
    ) VALUES (
        '63800000-0000-4000-8000-000000000021', agent_id, issue_fail, runtime_id,
        'failed', 0, now() - interval '2 days 18 minutes', now() - interval '2 days 8 minutes',
        now() - interval '2 days 20 minutes', 'agent_error',
        'RUYI-638 synthetic failure fixture'
    ) ON CONFLICT (id) DO UPDATE SET
        status = 'failed', started_at = EXCLUDED.started_at,
        completed_at = EXCLUDED.completed_at,
        failure_reason = EXCLUDED.failure_reason, error = EXCLUDED.error;

    INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens,
                            cache_read_tokens, cache_write_tokens, created_at)
    VALUES
        ('63800000-0000-4000-8000-000000000020', 'claude', 'claude-3-5-sonnet',
         4200, 900, 300, 120, now() - interval '15 minutes'),
        ('63800000-0000-4000-8000-000000000021', 'claude', 'claude-3-5-sonnet',
         1200, 600, 0, 0, now() - interval '2 days 15 minutes')
    ON CONFLICT (task_id, provider, model) DO UPDATE SET
        input_tokens = EXCLUDED.input_tokens,
        output_tokens = EXCLUDED.output_tokens,
        cache_read_tokens = EXCLUDED.cache_read_tokens,
        cache_write_tokens = EXCLUDED.cache_write_tokens,
        created_at = EXCLUDED.created_at;

    PERFORM rollup_task_usage_hourly_window(now() - interval '4 days', now() + interval '1 hour');

    INSERT INTO skill (id, workspace_id, name, description, content, config, created_by)
    VALUES (
        skill_lifecycle, ws, '[RUYI-638 QA] Lifecycle Skill',
        'Fixture with two versions for detail, edit, and history checks.',
        'Use the fixture only to verify the skill lifecycle.', '{}'::jsonb, owner_id
    ) ON CONFLICT (id) DO UPDATE SET
        name = EXCLUDED.name, description = EXCLUDED.description,
        content = EXCLUDED.content, config = EXCLUDED.config, updated_at = now();

    INSERT INTO skill (id, workspace_id, name, description, content, config, created_by)
    VALUES (
        skill_reference, ws, '[RUYI-638 QA] Reference Skill',
        'Second fixture item for management-list counts.',
        'No external tools or credentials are used.', '{}'::jsonb, owner_id
    ) ON CONFLICT (id) DO UPDATE SET
        name = EXCLUDED.name, description = EXCLUDED.description,
        content = EXCLUDED.content, config = EXCLUDED.config, updated_at = now();

    INSERT INTO skill_version (id, workspace_id, skill_id, version, name, description,
                               content, config, files, source, author_user_id)
    VALUES
        ('63800000-0000-4000-8000-000000000032', ws, skill_lifecycle, 1,
         '[RUYI-638 QA] Lifecycle Skill', 'Initial fixture version',
         'Initial fixture content.', '{}'::jsonb, '[]'::jsonb, 'create', owner_id),
        ('63800000-0000-4000-8000-000000000033', ws, skill_lifecycle, 2,
         '[RUYI-638 QA] Lifecycle Skill', 'Edited fixture version',
         'Edited fixture content for version-history checks.', '{}'::jsonb,
         '[{"path":"README.md","content":"RUYI-638 fixture file"}]'::jsonb,
         'edit', owner_id),
        ('63800000-0000-4000-8000-000000000034', ws, skill_reference, 1,
         '[RUYI-638 QA] Reference Skill', 'Initial fixture version',
         'Reference fixture content.', '{}'::jsonb, '[]'::jsonb, 'create', owner_id)
    ON CONFLICT (id) DO UPDATE SET
        name = EXCLUDED.name, description = EXCLUDED.description,
        content = EXCLUDED.content, files = EXCLUDED.files;

    INSERT INTO skill_file (skill_id, path, content)
    VALUES (skill_lifecycle, 'README.md', 'RUYI-638 fixture file')
    ON CONFLICT (skill_id, path) DO UPDATE SET content = EXCLUDED.content, updated_at = now();
    INSERT INTO agent_skill (agent_id, skill_id)
    VALUES (agent_id, skill_lifecycle), (agent_id, skill_reference)
    ON CONFLICT DO NOTHING;

    INSERT INTO prompt_proposal (
        id, workspace_id, carrier_scope, carrier_scope_id, target_section,
        change_kind, clause_name, clause_text,
        gate_answer_layer, gate_answer_retention, gate_answer_cost,
        gate_answer_conflict, gate_answer_dedup,
        status, source, created_by_type, created_by_id, problem, expected_effect
    ) VALUES
        (proposal_draft, ws, 'workspace', ws, '', 'add_clause',
         'ruyi638_qa_draft', '- RUYI-638 draft lifecycle fixture.',
         'Workspace', 'Keep while validating this QA fixture.', 'Negligible fixture-only cost.',
         'No production or shared-workspace conflict.', 'Unique RUYI-638 fixture clause.',
         'draft', 'manual', 'member', owner_id,
         'Draft state for lifecycle testing.', 'Allow the owner to submit or discard the fixture.'),
        (proposal_pending, ws, 'workspace', ws, '', 'add_clause',
         'ruyi638_qa_pending', '- RUYI-638 pending-owner lifecycle fixture.',
         'Workspace', 'Keep while validating this QA fixture.', 'Negligible fixture-only cost.',
         'No production or shared-workspace conflict.', 'Unique RUYI-638 fixture clause.',
         'pending_owner', 'manual', 'member', owner_id,
         'Pending-owner state for approve/reject checks.', 'Allow the owner to review this fixture.'),
        (proposal_rejected, ws, 'workspace', ws, '', 'add_clause',
         'ruyi638_qa_rejected', '- RUYI-638 rejected lifecycle fixture.',
         'Workspace', 'Retain as history during QA.', 'No ongoing cost.',
         'No production or shared-workspace conflict.', 'Unique RUYI-638 fixture clause.',
         'rejected', 'manual', 'member', owner_id,
         'Rejected state for history and filter checks.', 'No active behavior change.')
    ON CONFLICT (id) DO UPDATE SET
        status = EXCLUDED.status, clause_text = EXCLUDED.clause_text,
        updated_at = now();

    INSERT INTO autopilot (
        id, workspace_id, title, description, assignee_id, assignee_type,
        status, execution_mode, created_by_type, created_by_id
    ) VALUES
        (autopilot_active, ws, '[RUYI-638 QA] Active autopilot fixture',
         'Inert fixture; no enabled trigger and no run is scheduled.', agent_id, 'agent',
         'active', 'create_issue', 'member', owner_id),
        (autopilot_paused, ws, '[RUYI-638 QA] Paused autopilot fixture',
         'Paused lifecycle state fixture.', agent_id, 'agent',
         'paused', 'create_issue', 'member', owner_id),
        (autopilot_archived, ws, '[RUYI-638 QA] Archived autopilot fixture',
         'Archived lifecycle state fixture.', agent_id, 'agent',
         'archived', 'create_issue', 'member', owner_id)
    ON CONFLICT (id) DO UPDATE SET
        title = EXCLUDED.title, description = EXCLUDED.description,
        assignee_id = EXCLUDED.assignee_id, assignee_type = EXCLUDED.assignee_type,
        status = EXCLUDED.status, execution_mode = EXCLUDED.execution_mode,
        updated_at = now();

    INSERT INTO autopilot_trigger (
        id, autopilot_id, kind, enabled, cron_expression, timezone, label,
        created_by_type, created_by_id
    ) VALUES (
        '63800000-0000-4000-8000-000000000060', autopilot_active,
        'schedule', false, '0 9 * * *', 'UTC', '[RUYI-638 QA] disabled fixture trigger',
        'member', owner_id
    ) ON CONFLICT (id) DO UPDATE SET enabled = false, next_run_at = NULL;

    INSERT INTO autopilot_run (
        id, autopilot_id, source, status, triggered_at, completed_at,
        result, failure_reason
    ) VALUES
        ('63800000-0000-4000-8000-000000000070', autopilot_active,
         'manual', 'completed', now() - interval '1 day', now() - interval '1 day' + interval '2 minutes',
         '{"fixture_issue":"RUYI-638","fixture":true}'::jsonb, NULL),
        ('63800000-0000-4000-8000-000000000071', autopilot_active,
         'manual', 'failed', now() - interval '2 days', now() - interval '2 days' + interval '1 minute',
         NULL, 'RUYI-638 synthetic failure fixture')
    ON CONFLICT (id) DO UPDATE SET
        status = EXCLUDED.status, completed_at = EXCLUDED.completed_at,
        result = EXCLUDED.result, failure_reason = EXCLUDED.failure_reason;
END $$;

COMMIT;

SELECT json_build_object(
    'database', current_database(),
    'workspace', w.slug,
    'workspace_list_fixture', (SELECT count(*) FROM workspace WHERE id = '63800000-0000-4000-8000-000000000005' AND slug = 'ruyi638-admin-list'),
    'runtime_count', (SELECT count(*) FROM agent_runtime WHERE id = '63800000-0000-4000-8000-000000000001'),
    'agent_count', (SELECT count(*) FROM agent WHERE id = '63800000-0000-4000-8000-000000000002'),
    'issue_count', (SELECT count(*) FROM issue WHERE metadata->>'fixture_issue' = 'RUYI-638'),
    'terminal_task_count', (SELECT count(*) FROM agent_task_queue WHERE id IN ('63800000-0000-4000-8000-000000000020','63800000-0000-4000-8000-000000000021')),
    'usage_hourly_rows', (SELECT count(*) FROM task_usage_hourly WHERE runtime_id = '63800000-0000-4000-8000-000000000001'),
    'skills', (SELECT count(*) FROM skill WHERE id IN ('63800000-0000-4000-8000-000000000030','63800000-0000-4000-8000-000000000031')),
    'proposals', (SELECT json_object_agg(status, n) FROM (SELECT status,count(*) n FROM prompt_proposal WHERE id IN ('63800000-0000-4000-8000-000000000040','63800000-0000-4000-8000-000000000041','63800000-0000-4000-8000-000000000042') GROUP BY status) s),
    'autopilots', (SELECT json_object_agg(status, n) FROM (SELECT status,count(*) n FROM autopilot WHERE id IN ('63800000-0000-4000-8000-000000000050','63800000-0000-4000-8000-000000000051','63800000-0000-4000-8000-000000000052') GROUP BY status) a)
) FROM workspace w WHERE w.slug = 'ruyi638-qa';
