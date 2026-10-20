\set ON_ERROR_STOP on
BEGIN;

DO $$
DECLARE
    fixture_ws UUID := '63800000-0000-4000-8000-000000000081';
    fixture_owner UUID := '63800000-0000-4000-8000-000000000083';
    fixture_member UUID := '63800000-0000-4000-8000-000000000082';
    fixture_agent UUID := '63800000-0000-4000-8000-000000000090';
    active_autopilot UUID := '63800000-0000-4000-8000-000000000091';
    archived_autopilot UUID := '63800000-0000-4000-8000-000000000096';
BEGIN
    IF current_database() <> 'multica_dev1' THEN
        RAISE EXCEPTION 'RUYI-638 follow-up fixtures can only run in multica_dev1';
    END IF;

    IF EXISTS (
        SELECT 1 FROM workspace
        WHERE id = fixture_ws
          AND (slug <> 'ruyi638-proposal-fixture' OR name <> '[RUYI-638 QA] Proposal fixture workspace')
    ) OR EXISTS (
        SELECT 1 FROM workspace WHERE slug = 'ruyi638-proposal-fixture' AND id <> fixture_ws
    ) THEN
        RAISE EXCEPTION 'RUYI-638 proposal workspace identity collision';
    END IF;
    IF EXISTS (
        SELECT 1 FROM "user" WHERE id = fixture_owner
          AND (email <> 'ruyi638-proposal-owner@multica.test'
            OR profile_description <> 'RUYI-638 disposable proposal owner')
    ) OR EXISTS (
        SELECT 1 FROM "user" WHERE email = 'ruyi638-proposal-owner@multica.test' AND id <> fixture_owner
    ) OR EXISTS (
        SELECT 1 FROM "user" WHERE id = fixture_member
          AND (email <> 'ruyi638-web-member@multica.test'
            OR profile_description <> 'RUYI-638 disposable fixture identity')
    ) OR EXISTS (
        SELECT 1 FROM "user" WHERE email = 'ruyi638-web-member@multica.test' AND id <> fixture_member
    ) THEN
        RAISE EXCEPTION 'RUYI-638 disposable identity collision';
    END IF;
    IF EXISTS (SELECT 1 FROM member WHERE user_id IN (fixture_owner, fixture_member) AND workspace_id <> fixture_ws) THEN
        RAISE EXCEPTION 'RUYI-638 disposable identities are unexpectedly members of another workspace';
    END IF;

    INSERT INTO workspace (id, name, slug, description, context, settings)
    VALUES (
        fixture_ws, '[RUYI-638 QA] Proposal fixture workspace',
        'ruyi638-proposal-fixture',
        'Disposable workspace for RUYI-638 approval and parity fixtures.',
        E'# RUYI-638 disposable fixture workspace\n\nConfiguration written here is removed by the paired fixture cleanup.\n',
        '{}'::jsonb
    ) ON CONFLICT (id) DO NOTHING;
    INSERT INTO "user" (
        id, name, email, timezone, onboarded_at, onboarding_questionnaire, profile_description
    ) VALUES
        (fixture_member, 'RUYI-638 Web Comparison Member', 'ruyi638-web-member@multica.test',
         'Asia/Shanghai', now(), '{"fixture_issue":"RUYI-638"}'::jsonb,
         'RUYI-638 disposable fixture identity'),
        (fixture_owner, 'RUYI-638 Proposal Owner', 'ruyi638-proposal-owner@multica.test',
         'Asia/Shanghai', now(), '{"fixture_issue":"RUYI-638"}'::jsonb,
         'RUYI-638 disposable proposal owner')
    ON CONFLICT (id) DO NOTHING;
    INSERT INTO member (workspace_id, user_id, role)
    VALUES (fixture_ws, fixture_owner, 'owner'), (fixture_ws, fixture_member, 'member')
    ON CONFLICT (workspace_id, user_id) DO NOTHING;

    INSERT INTO prompt_proposal (
        id, workspace_id, carrier_scope, carrier_scope_id, target_section,
        change_kind, clause_name, clause_text,
        gate_answer_layer, gate_answer_retention, gate_answer_cost,
        gate_answer_conflict, gate_answer_dedup,
        status, source, created_by_type, created_by_id, problem, expected_effect
    ) VALUES (
        '63800000-0000-4000-8000-000000000041', fixture_ws, 'workspace', fixture_ws, '',
        'add_clause', 'ruyi638_qa_approval',
        '- RUYI-638 disposable approval fixture clause.',
        'Workspace', 'Stored only in this disposable workspace.',
        'No external cost; this is local fixture content.',
        'No production or shared-workspace effect.',
        'Unique RUYI-638 fixture clause; removed by paired cleanup.',
        'pending_owner', 'manual', 'member', fixture_member,
        'Disposable proposal for the approve lifecycle.',
        'Approval writes are confined to this disposable workspace.'
    ) ON CONFLICT (id) DO NOTHING;

    IF NOT EXISTS (
        SELECT 1 FROM workspace
        WHERE id = fixture_ws AND slug = 'ruyi638-proposal-fixture'
          AND name = '[RUYI-638 QA] Proposal fixture workspace'
    ) OR NOT EXISTS (
        SELECT 1 FROM "user"
        WHERE id = fixture_owner AND email = 'ruyi638-proposal-owner@multica.test'
          AND profile_description = 'RUYI-638 disposable proposal owner'
          AND onboarded_at IS NOT NULL AND is_super_admin IS FALSE
    ) OR NOT EXISTS (
        SELECT 1 FROM "user"
        WHERE id = fixture_member AND email = 'ruyi638-web-member@multica.test'
          AND profile_description = 'RUYI-638 disposable fixture identity'
          AND onboarded_at IS NOT NULL AND is_super_admin IS FALSE
    ) THEN
        RAISE EXCEPTION 'disposable workspace or onboarding-complete identities failed validation';
    END IF;
    IF (SELECT count(*) FROM member WHERE workspace_id = fixture_ws) <> 2
       OR NOT EXISTS (SELECT 1 FROM member WHERE workspace_id = fixture_ws AND user_id = fixture_owner AND role = 'owner')
       OR NOT EXISTS (SELECT 1 FROM member WHERE workspace_id = fixture_ws AND user_id = fixture_member AND role = 'member') THEN
        RAISE EXCEPTION 'proposal workspace membership is not the expected isolated owner/member pair';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM prompt_proposal
        WHERE id = '63800000-0000-4000-8000-000000000041'
          AND workspace_id = fixture_ws AND carrier_scope = 'workspace'
          AND carrier_scope_id = fixture_ws AND status = 'pending_owner'
          AND audit_log = '[]'::jsonb
    ) THEN
        RAISE EXCEPTION 'isolated pending-owner proposal fixture is missing, changed, or already used';
    END IF;
    IF EXISTS (SELECT 1 FROM agent WHERE id = fixture_agent
               AND (workspace_id <> fixture_ws OR name <> '[RUYI-638 QA] Inert autopilot agent'))
       OR EXISTS (SELECT 1 FROM autopilot WHERE id = active_autopilot
                  AND (workspace_id <> fixture_ws OR title <> '[RUYI-638 QA] Active history fixture'))
       OR EXISTS (SELECT 1 FROM autopilot WHERE id = archived_autopilot
                  AND (workspace_id <> fixture_ws OR title <> '[RUYI-638 QA] Archived visibility fixture'))
       OR EXISTS (SELECT 1 FROM autopilot_run WHERE id IN (
                    '63800000-0000-4000-8000-000000000092','63800000-0000-4000-8000-000000000093'
                  ) AND autopilot_id <> active_autopilot)
       OR EXISTS (SELECT 1 FROM autopilot_trigger WHERE id = '63800000-0000-4000-8000-000000000094'
                  AND autopilot_id <> active_autopilot) THEN
        RAISE EXCEPTION 'RUYI-638 fixture ID is owned outside its dedicated workspace';
    END IF;

    INSERT INTO agent (
        id, workspace_id, name, runtime_mode, runtime_config, visibility,
        status, owner_id, description
    ) VALUES (
        fixture_agent, fixture_ws, '[RUYI-638 QA] Inert autopilot agent',
        'local', '{}'::jsonb, 'workspace', 'offline', fixture_owner,
        'Disposable fixture agent; no runtime or credentials are attached.'
    ) ON CONFLICT (id) DO UPDATE SET
        workspace_id = EXCLUDED.workspace_id,
        name = EXCLUDED.name,
        runtime_mode = EXCLUDED.runtime_mode,
        runtime_config = EXCLUDED.runtime_config,
        visibility = EXCLUDED.visibility,
        status = EXCLUDED.status,
        owner_id = EXCLUDED.owner_id,
        description = EXCLUDED.description,
        updated_at = now();

    INSERT INTO autopilot (
        id, workspace_id, title, description, assignee_id, assignee_type,
        status, execution_mode, created_by_type, created_by_id, last_run_at
    ) VALUES
        (active_autopilot, fixture_ws, '[RUYI-638 QA] Active history fixture',
         'Inert fixture with terminal run history; no trigger is enabled.', fixture_agent, 'agent',
         'active', 'create_issue', 'member', fixture_owner, now() - interval '1 day'),
        (archived_autopilot, fixture_ws, '[RUYI-638 QA] Archived visibility fixture',
         'Archived fixture for the default list-filter comparison.', fixture_agent, 'agent',
         'archived', 'create_issue', 'member', fixture_owner, NULL)
    ON CONFLICT (id) DO UPDATE SET
        workspace_id = EXCLUDED.workspace_id,
        title = EXCLUDED.title,
        description = EXCLUDED.description,
        assignee_id = EXCLUDED.assignee_id,
        assignee_type = EXCLUDED.assignee_type,
        status = EXCLUDED.status,
        execution_mode = EXCLUDED.execution_mode,
        created_by_type = EXCLUDED.created_by_type,
        created_by_id = EXCLUDED.created_by_id,
        last_run_at = EXCLUDED.last_run_at,
        updated_at = now();

    INSERT INTO autopilot_trigger (
        id, autopilot_id, kind, enabled, cron_expression, timezone, label,
        created_by_type, created_by_id
    ) VALUES (
        '63800000-0000-4000-8000-000000000094', active_autopilot,
        'schedule', false, '0 9 * * *', 'UTC', '[RUYI-638 QA] disabled fixture trigger',
        'member', fixture_owner
    ) ON CONFLICT (id) DO UPDATE SET
        autopilot_id = EXCLUDED.autopilot_id,
        enabled = false,
        next_run_at = NULL,
        updated_at = now();

    INSERT INTO autopilot_run (
        id, autopilot_id, source, status, triggered_at, completed_at,
        result, failure_reason
    ) VALUES
        ('63800000-0000-4000-8000-000000000092', active_autopilot,
         'manual', 'completed', now() - interval '1 day', now() - interval '1 day' + interval '2 minutes',
         '{"fixture_issue":"RUYI-638","test_operation":true}'::jsonb, NULL),
        ('63800000-0000-4000-8000-000000000093', active_autopilot,
         'manual', 'failed', now() - interval '2 days', now() - interval '2 days' + interval '1 minute',
         NULL, 'RUYI-638 synthetic failure fixture')
    ON CONFLICT (id) DO UPDATE SET
        autopilot_id = EXCLUDED.autopilot_id,
        status = EXCLUDED.status,
        triggered_at = EXCLUDED.triggered_at,
        completed_at = EXCLUDED.completed_at,
        result = EXCLUDED.result,
        failure_reason = EXCLUDED.failure_reason;

    UPDATE autopilot
    SET last_run_at = (
            SELECT max(triggered_at) FROM autopilot_run WHERE autopilot_id = active_autopilot
        ),
        updated_at = now()
    WHERE id = active_autopilot AND workspace_id = fixture_ws;
END $$;

COMMIT;

SELECT json_build_object(
    'database', current_database(),
    'isolated_workspace_members', (SELECT count(*) FROM member WHERE workspace_id = '63800000-0000-4000-8000-000000000081'),
    'disposable_onboarded_identities', (SELECT count(*) FROM "user" WHERE id IN ('63800000-0000-4000-8000-000000000082','63800000-0000-4000-8000-000000000083') AND onboarded_at IS NOT NULL),
    'pending_workspace_proposal', (SELECT count(*) FROM prompt_proposal WHERE id = '63800000-0000-4000-8000-000000000041' AND workspace_id = '63800000-0000-4000-8000-000000000081' AND status = 'pending_owner'),
    'active_fixture_last_run_at', (SELECT last_run_at FROM autopilot WHERE id = '63800000-0000-4000-8000-000000000091'),
    'active_fixture_run_statuses', (SELECT json_agg(status ORDER BY status) FROM autopilot_run WHERE autopilot_id = '63800000-0000-4000-8000-000000000091'),
    'enabled_fixture_triggers', (SELECT count(*) FROM autopilot_trigger WHERE autopilot_id IN ('63800000-0000-4000-8000-000000000091','63800000-0000-4000-8000-000000000096') AND enabled)
);
