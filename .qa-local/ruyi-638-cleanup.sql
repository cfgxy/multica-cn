\set ON_ERROR_STOP on
BEGIN;
DO $$
DECLARE
    ws UUID;
    proposal_ws UUID := '63800000-0000-4000-8000-000000000081';
    web_member_id UUID := '63800000-0000-4000-8000-000000000082';
BEGIN
    IF current_database() <> 'multica_dev1' THEN
        RAISE EXCEPTION 'fixture cleanup can only run in multica_dev1';
    END IF;
    SELECT id INTO ws FROM workspace WHERE slug = 'ruyi638-qa';
    IF ws IS NULL THEN
        RAISE NOTICE 'RUYI-638 QA workspace is absent; nothing to clean';
    END IF;

    -- Keep the proposal's approval trail after removing its disposable carrier.
    -- audit_event is append-only and workspace teardown deliberately settles it.
    INSERT INTO audit_event (
        id, workspace_id, domain, event_type, occurred_at,
        actor_type, actor_id, details
    )
    SELECT
        md5(p.id::text || p.audit_log::text)::uuid,
        proposal_ws,
        'ops',
        'ops.fixture_proposal_audit_preserved',
        now(),
        'member',
        COALESCE(
            (
                SELECT (entry->>'actor_id')::uuid
                FROM jsonb_array_elements(p.audit_log) WITH ORDINALITY AS trail(entry, ord)
                WHERE entry->>'action' = 'approved'
                ORDER BY ord DESC LIMIT 1
            ),
            p.created_by_id
        ),
        jsonb_build_object(
            'fixture_issue', 'RUYI-638',
            'test_operation', true,
            'operation', 'proposal_approval_audit_preserved_during_fixture_cleanup',
            'proposal_id', p.id,
            'proposal_audit_log', p.audit_log
        )
    FROM prompt_proposal p
    WHERE p.workspace_id = proposal_ws
      AND p.id = '63800000-0000-4000-8000-000000000041'
      AND p.status IN ('approved', 'enacted', 'gate_failed')
      AND jsonb_array_length(p.audit_log) > 0
    ON CONFLICT (id) DO NOTHING;

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

    DELETE FROM prompt_version WHERE workspace_id = proposal_ws;
    DELETE FROM prompt_structure_baseline WHERE workspace_id = proposal_ws;
    DELETE FROM clause_injection WHERE workspace_id = proposal_ws;
    DELETE FROM clause_quality_daily WHERE workspace_id = proposal_ws;
    DELETE FROM clause_binding WHERE clause_id IN (
        SELECT id FROM clause WHERE workspace_id = proposal_ws
    );
    DELETE FROM clause_version WHERE clause_id IN (
        SELECT id FROM clause WHERE workspace_id = proposal_ws
    );
    DELETE FROM clause WHERE workspace_id = proposal_ws;
    DELETE FROM prompt_proposal WHERE workspace_id = proposal_ws AND id IN (
        '63800000-0000-4000-8000-000000000040',
        '63800000-0000-4000-8000-000000000041',
        '63800000-0000-4000-8000-000000000042'
    );
    DELETE FROM member WHERE workspace_id = proposal_ws;
    DELETE FROM workspace WHERE id = proposal_ws
      AND slug = 'ruyi638-proposal-fixture'
      AND name = '[RUYI-638 QA] Proposal fixture workspace';
    DELETE FROM verification_code WHERE email = 'ruyi638-web-member@multica.test';
    DELETE FROM "user" WHERE id = web_member_id
      AND email = 'ruyi638-web-member@multica.test'
      AND profile_description = 'RUYI-638 disposable fixture identity'
      AND NOT EXISTS (SELECT 1 FROM member WHERE user_id = web_member_id);
END $$;
COMMIT;

SELECT json_build_object(
    'database', current_database(),
    'remaining_proposal_workspace', (SELECT count(*) FROM workspace WHERE id = '63800000-0000-4000-8000-000000000081'),
    'remaining_disposable_identity', (SELECT count(*) FROM "user" WHERE id = '63800000-0000-4000-8000-000000000082'),
    'preserved_proposal_audit_events', (SELECT count(*) FROM audit_event WHERE workspace_id = '63800000-0000-4000-8000-000000000081' AND event_type = 'ops.fixture_proposal_audit_preserved' AND details->>'fixture_issue' = 'RUYI-638'),
    'remaining_workspace_fixture', (SELECT count(*) FROM workspace WHERE id = '63800000-0000-4000-8000-000000000005'),
    'remaining_runtime', (SELECT count(*) FROM agent_runtime WHERE id = '63800000-0000-4000-8000-000000000001'),
    'remaining_agent', (SELECT count(*) FROM agent WHERE id = '63800000-0000-4000-8000-000000000002'),
    'remaining_fixture_issues', (SELECT count(*) FROM issue WHERE metadata->>'fixture_issue' = 'RUYI-638'),
    'remaining_usage_rollups', (SELECT count(*) FROM task_usage_hourly WHERE runtime_id = '63800000-0000-4000-8000-000000000001'),
    'remaining_skills', (SELECT count(*) FROM skill WHERE id IN ('63800000-0000-4000-8000-000000000030','63800000-0000-4000-8000-000000000031')),
    'remaining_proposals', (SELECT count(*) FROM prompt_proposal WHERE id IN ('63800000-0000-4000-8000-000000000040','63800000-0000-4000-8000-000000000041','63800000-0000-4000-8000-000000000042')),
    'remaining_autopilots', (SELECT count(*) FROM autopilot WHERE id IN ('63800000-0000-4000-8000-000000000050','63800000-0000-4000-8000-000000000051','63800000-0000-4000-8000-000000000052'))
);
