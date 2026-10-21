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
        RAISE EXCEPTION 'RUYI-638 follow-up cleanup can only run in multica_dev1';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM workspace
        WHERE id = fixture_ws AND slug = 'ruyi638-proposal-fixture'
          AND name = '[RUYI-638 QA] Proposal fixture workspace'
    ) THEN
        RAISE EXCEPTION 'refusing cleanup: dedicated RUYI-638 workspace identity mismatch';
    END IF;
    IF EXISTS (
        SELECT 1 FROM member
        WHERE workspace_id = fixture_ws
          AND user_id NOT IN (fixture_owner, fixture_member)
    ) THEN
        RAISE EXCEPTION 'refusing cleanup: dedicated workspace contains an unexpected member';
    END IF;
    IF EXISTS (
        SELECT 1 FROM member
        WHERE user_id IN (fixture_owner, fixture_member)
          AND workspace_id <> fixture_ws
    ) THEN
        RAISE EXCEPTION 'refusing cleanup: a disposable identity is now attached to another workspace';
    END IF;

    -- The approved proposal audit trail survives teardown as a marked ops event.
    INSERT INTO audit_event (
        id, workspace_id, domain, event_type, occurred_at,
        actor_type, actor_id, details
    )
    SELECT
        md5(p.id::text || p.audit_log::text)::uuid,
        fixture_ws,
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
    WHERE p.workspace_id = fixture_ws
      AND p.id = '63800000-0000-4000-8000-000000000041'
      AND p.status IN ('approved', 'enacted', 'gate_failed')
      AND jsonb_array_length(p.audit_log) > 0
    ON CONFLICT (id) DO NOTHING;

    DELETE FROM autopilot_run WHERE autopilot_id IN (active_autopilot, archived_autopilot);
    DELETE FROM autopilot_trigger WHERE autopilot_id IN (active_autopilot, archived_autopilot);
    DELETE FROM autopilot WHERE workspace_id = fixture_ws AND id IN (active_autopilot, archived_autopilot)
      AND title LIKE '[RUYI-638 QA]%';
    DELETE FROM agent WHERE workspace_id = fixture_ws AND id = fixture_agent
      AND name = '[RUYI-638 QA] Inert autopilot agent';

    DELETE FROM prompt_version WHERE workspace_id = fixture_ws;
    DELETE FROM prompt_structure_baseline WHERE workspace_id = fixture_ws;
    DELETE FROM clause_injection WHERE workspace_id = fixture_ws;
    DELETE FROM clause_quality_daily WHERE workspace_id = fixture_ws;
    DELETE FROM clause_binding WHERE clause_id IN (SELECT id FROM clause WHERE workspace_id = fixture_ws);
    DELETE FROM clause_version WHERE clause_id IN (SELECT id FROM clause WHERE workspace_id = fixture_ws);
    DELETE FROM clause WHERE workspace_id = fixture_ws;
    DELETE FROM prompt_proposal WHERE workspace_id = fixture_ws;

    DELETE FROM member WHERE workspace_id = fixture_ws AND user_id IN (fixture_owner, fixture_member);
    DELETE FROM workspace WHERE id = fixture_ws
      AND slug = 'ruyi638-proposal-fixture'
      AND name = '[RUYI-638 QA] Proposal fixture workspace';
    DELETE FROM verification_code WHERE email IN (
        'ruyi638-web-member@multica.test',
        'ruyi638-proposal-owner@multica.test'
    );
    DELETE FROM "user" WHERE id = fixture_member
      AND email = 'ruyi638-web-member@multica.test'
      AND profile_description = 'RUYI-638 disposable fixture identity'
      AND NOT EXISTS (SELECT 1 FROM member WHERE user_id = fixture_member);
    DELETE FROM "user" WHERE id = fixture_owner
      AND email = 'ruyi638-proposal-owner@multica.test'
      AND profile_description = 'RUYI-638 disposable proposal owner'
      AND NOT EXISTS (SELECT 1 FROM member WHERE user_id = fixture_owner);
END $$;

COMMIT;

SELECT json_build_object(
    'database', current_database(),
    'remaining_isolated_workspace', (SELECT count(*) FROM workspace WHERE id = '63800000-0000-4000-8000-000000000081'),
    'remaining_fixture_identities', (SELECT count(*) FROM "user" WHERE id IN ('63800000-0000-4000-8000-000000000082','63800000-0000-4000-8000-000000000083')),
    'preserved_approval_audit_events', (SELECT count(*) FROM audit_event WHERE workspace_id = '63800000-0000-4000-8000-000000000081' AND event_type = 'ops.fixture_proposal_audit_preserved' AND details->>'fixture_issue' = 'RUYI-638'),
    'remaining_fixture_autopilots', (SELECT count(*) FROM autopilot WHERE id IN ('63800000-0000-4000-8000-000000000091','63800000-0000-4000-8000-000000000096'))
);
