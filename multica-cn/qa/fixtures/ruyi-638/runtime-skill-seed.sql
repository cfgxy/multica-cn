\set ON_ERROR_STOP on
BEGIN;

DO $$
DECLARE
    fixture_ws UUID := '63800000-0000-4000-8000-000000000081';
    fixture_owner UUID := '63800000-0000-4000-8000-000000000083';
    fixture_runtime UUID := '63800000-0000-4000-8000-0000000000a0';
    fixture_agent UUID := '63800000-0000-4000-8000-0000000000a1';
    fixture_skill UUID := '63800000-0000-4000-8000-0000000000a2';
    reference_skill UUID := '63800000-0000-4000-8000-0000000000a3';
BEGIN
    IF current_database() <> 'multica_dev1' THEN
        RAISE EXCEPTION 'RUYI-638 runtime/Skill fixtures can only run in multica_dev1';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM workspace
        WHERE id = fixture_ws AND slug = 'ruyi638-proposal-fixture'
          AND name = '[RUYI-638 QA] Proposal fixture workspace'
    ) OR NOT EXISTS (
        SELECT 1 FROM member WHERE workspace_id = fixture_ws
          AND user_id = fixture_owner AND role = 'owner'
    ) THEN
        RAISE EXCEPTION 'RUYI-638 disposable workspace/owner is missing or mismatched';
    END IF;
    IF EXISTS (SELECT 1 FROM agent_runtime WHERE id = fixture_runtime AND
        (workspace_id <> fixture_ws OR name <> '[RUYI-638 QA] Offline runtime fixture'))
       OR EXISTS (SELECT 1 FROM agent WHERE id = fixture_agent AND
        (workspace_id <> fixture_ws OR name <> '[RUYI-638 QA] Runtime fixture agent' OR runtime_id <> fixture_runtime))
       OR EXISTS (SELECT 1 FROM skill WHERE id IN (fixture_skill, reference_skill)
        AND workspace_id <> fixture_ws)
       OR EXISTS (SELECT 1 FROM skill WHERE workspace_id = fixture_ws
        AND id IN (fixture_skill, reference_skill)
        AND name NOT LIKE '[RUYI-638 QA]%') THEN
        RAISE EXCEPTION 'RUYI-638 fixture identifier is already owned by unexpected data';
    END IF;

    INSERT INTO agent_runtime (
        id, workspace_id, daemon_id, name, runtime_mode, provider, status,
        device_info, metadata, owner_id, visibility, last_seen_at
    ) VALUES (
        fixture_runtime, fixture_ws, 'ruyi638-disposable-runtime',
        '[RUYI-638 QA] Offline runtime fixture', 'local', 'claude', 'offline',
        'Disposable offline runtime; no daemon or credentials are connected.',
        '{"fixture_issue":"RUYI-638","fixture":true}'::jsonb,
        fixture_owner, 'public', NULL
    ) ON CONFLICT (id) DO NOTHING;

    INSERT INTO agent (
        id, workspace_id, name, runtime_mode, runtime_config, visibility,
        status, owner_id, description, runtime_id
    ) VALUES (
        fixture_agent, fixture_ws, '[RUYI-638 QA] Runtime fixture agent',
        'local', '{}'::jsonb, 'workspace', 'offline', fixture_owner,
        'RUYI-638 disposable runtime and Skill lifecycle fixture.', fixture_runtime
    ) ON CONFLICT (id) DO NOTHING;

    INSERT INTO skill (id, workspace_id, name, description, content, config, created_by)
    VALUES
        (fixture_skill, fixture_ws, '[RUYI-638 QA] Lifecycle Skill fixture',
         'Disposable Skill for edit, version history and delete checks.',
         'No external tools, credentials, or production data are used.', '{}'::jsonb, fixture_owner),
        (reference_skill, fixture_ws, '[RUYI-638 QA] Reference Skill fixture',
         'Second disposable Skill for list and association checks.',
         'Read-only fixture content.', '{}'::jsonb, fixture_owner)
    ON CONFLICT (id) DO NOTHING;

    INSERT INTO skill_version (
        id, workspace_id, skill_id, version, name, description, content,
        config, files, source, author_user_id
    ) VALUES
        ('63800000-0000-4000-8000-0000000000a4', fixture_ws, fixture_skill, 1,
         '[RUYI-638 QA] Lifecycle Skill fixture', 'Initial disposable version',
         'Initial fixture content.', '{}'::jsonb, '[]'::jsonb, 'create', fixture_owner),
        ('63800000-0000-4000-8000-0000000000a5', fixture_ws, reference_skill, 1,
         '[RUYI-638 QA] Reference Skill fixture', 'Initial disposable version',
         'Reference fixture content.', '{}'::jsonb, '[]'::jsonb, 'create', fixture_owner)
    ON CONFLICT (id) DO NOTHING;

    INSERT INTO skill_file (skill_id, path, content)
    VALUES (fixture_skill, 'README.md', 'RUYI-638 disposable Skill fixture.')
    ON CONFLICT (skill_id, path) DO NOTHING;
    INSERT INTO agent_skill (agent_id, skill_id)
    VALUES (fixture_agent, fixture_skill), (fixture_agent, reference_skill)
    ON CONFLICT (agent_id, skill_id) DO NOTHING;

    IF NOT EXISTS (SELECT 1 FROM agent_runtime WHERE id = fixture_runtime
        AND workspace_id = fixture_ws AND status = 'offline'
        AND metadata->>'fixture_issue' = 'RUYI-638')
       OR NOT EXISTS (SELECT 1 FROM agent WHERE id = fixture_agent
        AND workspace_id = fixture_ws AND runtime_id = fixture_runtime)
       OR (SELECT count(*) FROM skill WHERE id IN (fixture_skill, reference_skill)
        AND workspace_id = fixture_ws) <> 2 THEN
        RAISE EXCEPTION 'RUYI-638 runtime/Skill fixtures failed post-insert validation';
    END IF;
END $$;

COMMIT;

SELECT json_build_object(
    'database', current_database(),
    'fixture_workspace', 'ruyi638-proposal-fixture',
    'runtime_rows', (SELECT count(*) FROM agent_runtime WHERE id = '63800000-0000-4000-8000-0000000000a0'),
    'agent_rows', (SELECT count(*) FROM agent WHERE id = '63800000-0000-4000-8000-0000000000a1'),
    'skill_rows', (SELECT count(*) FROM skill WHERE id IN ('63800000-0000-4000-8000-0000000000a2','63800000-0000-4000-8000-0000000000a3')),
    'skill_version_rows', (SELECT count(*) FROM skill_version WHERE skill_id IN ('63800000-0000-4000-8000-0000000000a2','63800000-0000-4000-8000-0000000000a3'))
);
