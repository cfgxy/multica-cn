-- RUYI-359: rewrite schema_migrations for the 9xx consolidation.
--
-- Design and rules: 9xx-consolidation.md §6. The consolidation merged 16 groups
-- of one-statement 9xx migrations into their lead files (43 member stems
-- retired); this script rewrites the ledger of a database that the OLD tree
-- already brought to 973 so it matches the NEW tree one-to-one. It only edits
-- identity rows — it executes no schema DDL.
--
-- Stage 1 folds fork-era oauth ledger variants into 939/940 (1:1 rename,
-- applied_at preserved; same semantics as repair_oauth_migration_ledger.sql).
-- Stage 2 applies the three-state rule per group (9xx-consolidation.md §6.1):
--     lead row present and all k member rows present  -> delete the member rows
--     lead row present and no member row present      -> no-op (already
--                                                        rewritten; idempotent
--                                                        re-entry / new tree)
--     anything else (lead missing, members partial)   -> raise; the whole
--                                                        transaction rolls back
-- Object guards (§6.3): for every group whose lead row is present, each table
-- the group touches must exist and each member-built index must be valid;
-- otherwise raise. Repair the drift on the old tree, then re-run.
--
-- Idempotent: a second run is a no-op. Phase 3 pipeline (snapshot -> run ->
-- verify -> zero-apply re-up -> rollback path) is in 9xx-consolidation.md §6.4.
BEGIN;
SET LOCAL lock_timeout = '5s';
LOCK TABLE schema_migrations IN SHARE ROW EXCLUSIVE MODE;

-- ---------------------------------------------------------------------------
-- Stage 1: oauth fork-era ledger variants. These stems never existed on
-- mainline disk, so they are exempt from the zero-residue gate by design.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    r record;
BEGIN
    FOR r IN
        SELECT *
        FROM (VALUES
            ('929_oauth_client',                  '939_oauth_client'),
            ('930_oauth_client_client_id_index',  '940_oauth_client_client_id_index'),
            ('924_oauth_clients',                 '939_oauth_client'),
            ('925_oauth_clients_client_id_index', '940_oauth_client_client_id_index')
        ) AS m(old_version, new_version)
    LOOP
        IF NOT EXISTS (SELECT 1 FROM schema_migrations WHERE version = r.old_version) THEN
            CONTINUE;
        END IF;
        IF to_regclass('oauth_client') IS NULL THEN
            RAISE EXCEPTION 'oauth_client table missing while % is recorded; repair schema drift before rewriting the ledger', r.old_version;
        END IF;
        IF NOT EXISTS (
            SELECT 1 FROM pg_index
            WHERE indexrelid = to_regclass('idx_oauth_client_client_id') AND indisvalid
        ) THEN
            RAISE EXCEPTION 'idx_oauth_client_client_id missing or invalid while % is recorded; repair schema drift before rewriting the ledger', r.old_version;
        END IF;
        IF EXISTS (SELECT 1 FROM schema_migrations WHERE version = r.new_version) THEN
            DELETE FROM schema_migrations WHERE version = r.old_version;
        ELSE
            UPDATE schema_migrations SET version = r.new_version WHERE version = r.old_version;
        END IF;
    END LOOP;
END $$;

-- ---------------------------------------------------------------------------
-- Stage 2: fold each consolidated group's member rows into its lead.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    g record;
    tbl text;
    idx text;
    member_count int;
    expected int;
BEGIN
    FOR g IN
        SELECT *
        FROM (VALUES
            ('902_user_admin_state',
             ARRAY['903_admin_audit_log_actor_index'],
             ARRAY['"user"', 'admin_audit_log'],
             ARRAY['idx_admin_audit_log_actor_created']),
            ('904_execution_profile',
             ARRAY['905_execution_profile_name_index', '906_execution_profile_entry_index'],
             ARRAY['execution_profile', 'execution_profile_entry'],
             ARRAY['idx_execution_profile_workspace_name', 'idx_execution_profile_entry_profile_agent']),
            ('910_marketplace_listing',
             ARRAY['911_marketplace_listing_name_index', '912_marketplace_listing_discovery_index', '913_marketplace_listing_source_index'],
             ARRAY['marketplace_listing'],
             ARRAY['idx_marketplace_listing_kind_name_key', 'idx_marketplace_listing_discovery', 'idx_marketplace_listing_source_workspace']),
            ('916_prompt_version',
             ARRAY['917_agent_task_queue_prompt_versions', '919_prompt_version_scope_version_index', '920_prompt_version_scope_created_index', '921_prompt_version_workspace_index', '922_prompt_version_v1_backfill', '937_prompt_version_v1_gap_backfill'],
             ARRAY['prompt_version', 'agent_task_queue'],
             ARRAY['idx_prompt_version_scope_version', 'idx_prompt_version_scope_created', 'idx_prompt_version_workspace']),
            ('925_prompt_quality_rollup',
             ARRAY['926_prompt_quality_daily_scope_day_index', '927_prompt_quality_daily_workspace_index', '928_prompt_perplexity_score_workspace_index'],
             ARRAY['prompt_quality_rollup_state', 'prompt_quality_daily', 'prompt_perplexity_score'],
             ARRAY['idx_prompt_quality_daily_scope_day', 'idx_prompt_quality_daily_workspace', 'idx_prompt_perplexity_score_workspace']),
            ('929_prompt_quiz',
             ARRAY['930_prompt_quiz_result_baseline_index', '931_prompt_quiz_result_batch_index', '932_prompt_quiz_item_workspace_index', '933_prompt_quiz_outcome_answered', '934_prompt_quiz_result_runtime', '935_prompt_quiz_item_rubric', '936_prompt_quiz_result_item_index', '938_prompt_quiz_orphan_cleanup', '956_prompt_quiz_grading'],
             ARRAY['prompt_quiz_sweep_state', 'prompt_quiz_item', 'prompt_quiz_result'],
             ARRAY['idx_prompt_quiz_result_baseline', 'idx_prompt_quiz_result_batch', 'idx_prompt_quiz_item_workspace', 'idx_prompt_quiz_result_item']),
            ('939_oauth_client',
             ARRAY['940_oauth_client_client_id_index'],
             ARRAY['oauth_client'],
             ARRAY['idx_oauth_client_client_id']),
            ('941_skill_version',
             ARRAY['942_skill_version_identity_index'],
             ARRAY['skill_version'],
             ARRAY['idx_skill_version_identity']),
            ('943_proposal',
             ARRAY['944_proposal_workspace_status', '955_proposal_system_dir_dedupe'],
             ARRAY['proposal'],
             ARRAY['idx_proposal_workspace_status', 'uidx_proposal_system_dir']),
            ('945_knowledge',
             ARRAY['946_knowledge_dir_workspace', '947_knowledge_entry_identity', '948_knowledge_scan_batch_dir', '951_knowledge_dir_ws_path_unique', '952_knowledge_dir_ultimate_active_unique'],
             ARRAY['knowledge_dir', 'knowledge_entry', 'knowledge_scan_batch'],
             ARRAY['idx_knowledge_dir_workspace', 'uidx_knowledge_entry_identity', 'idx_knowledge_scan_batch_dir', 'uidx_knowledge_dir_ws_path', 'uidx_knowledge_dir_ultimate_active']),
            ('958_prompt_proposal',
             ARRAY['959_prompt_proposal_workspace_index'],
             ARRAY['prompt_proposal'],
             ARRAY['idx_prompt_proposal_workspace_status']),
            ('960_prompt_structure_baseline',
             ARRAY['961_prompt_structure_baseline_unique'],
             ARRAY['prompt_structure_baseline'],
             ARRAY['uidx_prompt_structure_baseline_carrier']),
            ('962_retrospective',
             ARRAY['963_retrospective_run_index', '964_retrospective_watermark_unique'],
             ARRAY['retrospective_config', 'retrospective_run', 'retrospective_issue_watermark'],
             ARRAY['idx_retrospective_run_workspace', 'uidx_retrospective_watermark_issue']),
            ('965_channel_chat_run_intent',
             ARRAY['966_channel_chat_run_intent_id_uidx', '967_channel_chat_run_intent_pkey', '968_channel_chat_run_intent_pending_uidx', '969_channel_chat_run_intent_claim_idx'],
             ARRAY['channel_chat_run_intent'],
             ARRAY['channel_chat_run_intent_id_uidx', 'channel_chat_run_intent_pending_uidx', 'idx_channel_chat_run_intent_claim']),
            ('970_runtime_skill_discovery',
             ARRAY['971_runtime_skill_discovery_identity_uidx'],
             ARRAY['runtime_skill_discovery'],
             ARRAY['idx_runtime_skill_discovery_identity']),
            ('972_agent_task_cancel_requested',
             ARRAY['973_agent_task_cancel_attribution'],
             ARRAY['agent_task_queue'],
             ARRAY[]::text[])
        ) AS t(lead, members, tables, member_indexes)
    LOOP
        expected := array_length(g.members, 1);
        SELECT count(*) INTO member_count
        FROM schema_migrations
        WHERE version = ANY (g.members);

        IF NOT EXISTS (SELECT 1 FROM schema_migrations WHERE version = g.lead) THEN
            IF member_count > 0 THEN
                RAISE EXCEPTION 'group %: lead row missing but % of % member rows present; bring the database to 973 on the old tree before rewriting the ledger', g.lead, member_count, expected;
            END IF;
            CONTINUE; -- fresh database: this group was never applied
        END IF;

        -- Object guards: the lead row claims these objects exist. Validate
        -- them on every pass (first rewrite and idempotent re-entry alike).
        FOREACH tbl IN ARRAY g.tables LOOP
            IF to_regclass(tbl) IS NULL THEN
                RAISE EXCEPTION 'group %: table % missing; repair schema drift before rewriting the ledger', g.lead, tbl;
            END IF;
        END LOOP;
        FOREACH idx IN ARRAY g.member_indexes LOOP
            IF NOT EXISTS (
                SELECT 1 FROM pg_index
                WHERE indexrelid = to_regclass(idx) AND indisvalid
            ) THEN
                RAISE EXCEPTION 'group %: index % missing or invalid; repair schema drift before rewriting the ledger', g.lead, idx;
            END IF;
        END LOOP;

        IF member_count = 0 THEN
            CONTINUE; -- already rewritten (or the new tree applied it)
        END IF;
        IF member_count <> expected THEN
            RAISE EXCEPTION 'group %: partial member set (% of % present); refusing a half-merged rewrite', g.lead, member_count, expected;
        END IF;

        DELETE FROM schema_migrations WHERE version = ANY (g.members);
    END LOOP;
END $$;
COMMIT;

-- ---------------------------------------------------------------------------
-- Phase 3 verification queries (run after this script, per §6.4 step 3):
--
--   -- 29 new-canonical stems all present:
--   SELECT count(*) FROM schema_migrations WHERE version LIKE '9__%';  -- expect 29 (+ 'untouched'-era 9xx pre-fork rows if any)
--   -- 43 member stems zero residue:
--   SELECT version FROM schema_migrations
--   WHERE version IN ('903_admin_audit_log_actor_index', ...);  -- full list in 9xx-consolidation.md §1; expect 0 rows
--   -- row delta: ledger went from 72 to 29 nine-hundreds (minus 4 legacy oauth
--   -- variants folded in Stage 1 where applicable).
--   -- New-tree zero-apply: run `migrate up` with the new tree; readiness must
--   -- pass with zero applied migrations.
-- ---------------------------------------------------------------------------
