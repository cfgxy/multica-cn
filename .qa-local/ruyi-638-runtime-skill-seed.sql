\set ON_ERROR_STOP on
BEGIN;

DO $$
DECLARE
    fixture_ws UUID := '63800000-0000-4000-8000-000000000081';
    fixture_owner UUID := '63800000-0000-4000-8000-000000000083';
    fixture_runtime UUID := '63800000-0000-4000-8000-0000000000c0';
    fixture_agent UUID := '63800000-0000-4000-8000-0000000000c1';
    lifecycle_skill UUID := '63800000-0000-4000-8000-0000000000c3';
    reference_skill UUID := '63800000-0000-4000-8000-0000000000c4';
BEGIN
    IF current_database() <> 'multica_dev1' THEN
        RAISE EXCEPTION 'RUYI-638 runtime/Skill fixture can only run in multica_dev1';
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
          AND email = 'ruyi638-proposal-owner@multica.test'
          AND profile_description = 'RUYI-638 disposable proposal owner'
    ) THEN
        RAISE EXCEPTION 'RUYI-638 isolated workspace or non-super-admin owner is missing/mismatched';
    END IF;

    IF EXISTS (
        SELECT 1 FROM agent_runtime
        WHERE id = fixture_runtime
          AND (workspace_id IS DISTINCT FROM fixture_ws
            OR metadata->>'fixture_issue' IS DISTINCT FROM 'RUYI-638'
            OR metadata->>'fixture_kind' IS DISTINCT FROM 'runtime_skill')
    ) OR EXISTS (
        SELECT 1 FROM agent
        WHERE id = fixture_agent
          AND (workspace_id IS DISTINCT FROM fixture_ws
            OR runtime_id IS DISTINCT FROM fixture_runtime
            OR name IS DISTINCT FROM '[RUYI-638 QA] Runtime and Skill fixture agent')
    ) OR EXISTS (
        SELECT 1 FROM skill
        WHERE id IN (lifecycle_skill, reference_skill)
          AND (workspace_id IS DISTINCT FROM fixture_ws
            OR name NOT LIKE '[RUYI-638 QA]%')
    ) THEN
        RAISE EXCEPTION 'RUYI-638 runtime/Skill fixture ID is owned outside its dedicated scope';
    END IF;

    INSERT INTO agent_runtime (
        id, workspace_id, daemon_id, name, runtime_mode, provider, status,
        device_info, metadata, owner_id, visibility, last_seen_at
    ) VALUES (
        fixture_runtime, fixture_ws, 'ruyi638-runtime-skill-fixture',
        '[RUYI-638 QA] Runtime and Skill fixture', 'local', 'claude', 'offline',
        'No daemon is connected; isolated QA fixture only.',
        '{"fixture_issue":"RUYI-638","fixture_kind":"runtime_skill"}'::jsonb,
        fixture_owner, 'public', NULL
    ) ON CONFLICT (id) DO UPDATE SET
        name = EXCLUDED.name,
        status = 'offline',
        device_info = EXCLUDED.device_info,
        metadata = EXCLUDED.metadata,
        owner_id = fixture_owner,
        visibility = 'public',
        last_seen_at = NULL,
        updated_at = now()
    WHERE agent_runtime.workspace_id = fixture_ws
      AND agent_runtime.metadata->>'fixture_issue' = 'RUYI-638'
      AND agent_runtime.metadata->>'fixture_kind' = 'runtime_skill';

    INSERT INTO agent (
        id, workspace_id, name, runtime_mode, runtime_config, visibility,
        status, owner_id, description, runtime_id
    ) VALUES (
        fixture_agent, fixture_ws, '[RUYI-638 QA] Runtime and Skill fixture agent',
        'local', '{}'::jsonb, 'workspace', 'offline', fixture_owner,
        'Disposable offline agent for runtime and Skill management QA.', fixture_runtime
    ) ON CONFLICT (id) DO UPDATE SET
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
      AND agent.name = '[RUYI-638 QA] Runtime and Skill fixture agent';

    INSERT INTO skill (id, workspace_id, name, description, content, config, created_by)
    VALUES
        (lifecycle_skill, fixture_ws, '[RUYI-638 QA] Lifecycle Skill',
         'Two versions for detail, edit, and history checks.',
         'Disposable lifecycle Skill fixture.', '{}'::jsonb, fixture_owner),
        (reference_skill, fixture_ws, '[RUYI-638 QA] Reference Skill',
         'Second item for management-list checks.',
         'No external tools or credentials are used.', '{}'::jsonb, fixture_owner)
    ON CONFLICT (id) DO UPDATE SET
        name = EXCLUDED.name,
        description = EXCLUDED.description,
        content = EXCLUDED.content,
        config = EXCLUDED.config,
        created_by = fixture_owner,
        updated_at = now()
    WHERE skill.workspace_id = fixture_ws
      AND skill.name LIKE '[RUYI-638 QA]%';

    INSERT INTO skill_version (
        id, workspace_id, skill_id, version, name, description,
        content, config, files, source, author_user_id
    ) VALUES
        ('63800000-0000-4000-8000-0000000000c5', fixture_ws, lifecycle_skill, 1,
         '[RUYI-638 QA] Lifecycle Skill', 'Initial disposable fixture version',
         'Initial fixture content.', '{}'::jsonb, '[]'::jsonb, 'create', fixture_owner),
        ('63800000-0000-4000-8000-0000000000c6', fixture_ws, lifecycle_skill, 2,
         '[RUYI-638 QA] Lifecycle Skill', 'Edited disposable fixture version',
         'Edited fixture content for version-history checks.', '{}'::jsonb,
         '[{"path":"README.md","content":"RUYI-638 fixture file"}]'::jsonb,
         'edit', fixture_owner),
        ('63800000-0000-4000-8000-0000000000c7', fixture_ws, reference_skill, 1,
         '[RUYI-638 QA] Reference Skill', 'Initial disposable fixture version',
         'Reference fixture content.', '{}'::jsonb, '[]'::jsonb, 'create', fixture_owner)
    ON CONFLICT (id) DO UPDATE SET
        name = EXCLUDED.name,
        description = EXCLUDED.description,
        content = EXCLUDED.content,
        files = EXCLUDED.files
    WHERE skill_version.workspace_id = fixture_ws
      AND skill_version.skill_id IN (lifecycle_skill, reference_skill);

    INSERT INTO skill_file (skill_id, path, content)
    VALUES (lifecycle_skill, 'README.md', 'RUYI-638 fixture file')
    ON CONFLICT (skill_id, path) DO UPDATE SET
        content = EXCLUDED.content,
        updated_at = now();

    INSERT INTO agent_skill (agent_id, skill_id)
    VALUES (fixture_agent, lifecycle_skill), (fixture_agent, reference_skill)
    ON CONFLICT DO NOTHING;

    IF (SELECT count(*) FROM agent_runtime
        WHERE id = fixture_runtime AND workspace_id = fixture_ws
          AND metadata->>'fixture_issue' = 'RUYI-638'
          AND metadata->>'fixture_kind' = 'runtime_skill' AND status = 'offline') <> 1
       OR (SELECT count(*) FROM agent
        WHERE id = fixture_agent AND workspace_id = fixture_ws
          AND runtime_id = fixture_runtime AND owner_id = fixture_owner
          AND status = 'offline') <> 1
       OR (SELECT count(*) FROM skill
        WHERE id IN (lifecycle_skill, reference_skill) AND workspace_id = fixture_ws) <> 2
       OR (SELECT count(*) FROM skill_version
        WHERE id IN ('63800000-0000-4000-8000-0000000000c5',
                     '63800000-0000-4000-8000-0000000000c6',
                     '63800000-0000-4000-8000-0000000000c7')
          AND workspace_id = fixture_ws) <> 3
       OR (SELECT count(*) FROM agent_skill
        WHERE agent_id = fixture_agent AND skill_id IN (lifecycle_skill, reference_skill)) <> 2 THEN
        RAISE EXCEPTION 'RUYI-638 runtime/Skill fixture failed validation';
    END IF;
END $$;

COMMIT;

SELECT json_build_object(
    'database', current_database(),
    'workspace_id', '63800000-0000-4000-8000-000000000081',
    'runtime_rows', (SELECT count(*) FROM agent_runtime
        WHERE id = '63800000-0000-4000-8000-0000000000c0'
          AND workspace_id = '63800000-0000-4000-8000-000000000081'
          AND metadata->>'fixture_issue' = 'RUYI-638'
          AND metadata->>'fixture_kind' = 'runtime_skill'),
    'agent_rows', (SELECT count(*) FROM agent
        WHERE id = '63800000-0000-4000-8000-0000000000c1'
          AND workspace_id = '63800000-0000-4000-8000-000000000081'),
    'skills', (SELECT count(*) FROM skill
        WHERE id IN ('63800000-0000-4000-8000-0000000000c3',
                     '63800000-0000-4000-8000-0000000000c4')
          AND workspace_id = '63800000-0000-4000-8000-000000000081'),
    'skill_versions', (SELECT count(*) FROM skill_version
        WHERE id IN ('63800000-0000-4000-8000-0000000000c5',
                     '63800000-0000-4000-8000-0000000000c6',
                     '63800000-0000-4000-8000-0000000000c7')
          AND workspace_id = '63800000-0000-4000-8000-000000000081'),
    'agent_skill_links', (SELECT count(*) FROM agent_skill
        WHERE agent_id = '63800000-0000-4000-8000-0000000000c1'
          AND skill_id IN ('63800000-0000-4000-8000-0000000000c3',
                           '63800000-0000-4000-8000-0000000000c4'))
);
