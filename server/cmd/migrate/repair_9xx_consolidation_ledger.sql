-- RUYI-359 Phase 2R: rewrite schema_migrations for the 9xx domain
-- consolidation and gap-free renumbering (single hop).
--
-- Design and rules: 9xx-consolidation.md. The consolidation rewrote the 72
-- original 900+ stems (largest 973, gaps 909/918) into 17 domain files
-- numbered contiguously from 900 (largest 916). This script rewrites the
-- ledger of a database that the ORIGINAL tree already brought to 973 so it
-- matches the new tree one-to-one, in ONE hop — no intermediate 29-stem
-- state. It only edits identity rows — it executes no schema DDL.
--
-- Stage 1 folds fork-era oauth ledger variants into the oauth group's
-- mainline sources 939/940 (1:1 rename, applied_at preserved). These four
-- stems never existed on mainline disk, so they are exempt from the
-- zero-residue gate by design; folding them first keeps Stage 2
-- single-shaped for every reachable starting state.
-- Stage 2 applies the three-state rule per group, keyed by a `fold` flag:
--   * fold = false (rename group: the target stem is new):
--       target row present and no source row present   -> no-op (already
--                                                         rewritten; idempotent
--                                                         re-entry / new tree)
--       target row absent and all k source rows present -> UPDATE the first
--                                                         (lowest-numbered)
--                                                         source to the target
--                                                         (applied_at kept),
--                                                         DELETE the rest
--       target row absent and no source row present    -> fresh database:
--                                                         group never applied
--       anything else (partial sources, or target and
--       sources both present)                          -> raise; the whole
--                                                         transaction rolls back
--   * fold = true (902 only: the target stem was applied as itself under the
--     old tree, its source rows are redundant identities):
--       target row present                             -> DELETE the source rows
--       target row absent (sources present or not)     -> raise; a missing
--                                                         902 row beside an
--                                                         applied 903 is broken
--                                                         history, not a
--                                                         rename target
-- Object guards: for every group whose target row is present (already or
-- after the rewrite), each table the group touches must exist and each index
-- it builds must be valid; otherwise raise. Repair the drift on the old
-- tree, then re-run. Guard lists reflect the FINAL schema at 973, not the
-- migration-time schema: the 910 group's proposal table and its indexes were
-- dropped again by the former 957 (legislation E1 removal), and the former
-- 950's proposal.transfer_state went with them — nothing of the proposal
-- domain survives to verify, so those groups carry empty guard lists.
--
-- Idempotent: a second run is a no-op. Phase 3 pipeline (snapshot -> run ->
-- verify -> zero-apply re-up -> rollback path) is in 9xx-consolidation.md §5.

BEGIN;
SET LOCAL lock_timeout = '5s';
LOCK TABLE schema_migrations IN SHARE ROW EXCLUSIVE MODE;

-- ---------------------------------------------------------------------------
-- Stage 1: oauth fork-era ledger variants -> mainline sources.
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
        ) AS m(old_version, mainline_version)
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
        IF EXISTS (SELECT 1 FROM schema_migrations WHERE version = r.mainline_version) THEN
            DELETE FROM schema_migrations WHERE version = r.old_version;
        ELSE
            UPDATE schema_migrations SET version = r.mainline_version WHERE version = r.old_version;
        END IF;
    END LOOP;
END $$;

-- ---------------------------------------------------------------------------
-- Stage 2: one group per target stem; sources are the original stems the
-- target's file now embodies (9xx-consolidation.md §1). Source arrays are
-- ascending; sources[1] donates the surviving applied_at.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    g record;
    tbl text;
    idx text;
    source_count int;
    expected int;
BEGIN
    FOR g IN
        SELECT *
        FROM (VALUES
            ('902_user_admin_state',
             ARRAY['903_admin_audit_log_actor_index'],
             true,
             ARRAY['"user"', 'admin_audit_log'],
             ARRAY['idx_admin_audit_log_actor_created']),
            ('903_execution_profile',
             ARRAY['904_execution_profile', '905_execution_profile_name_index', '906_execution_profile_entry_index'],
             false,
             ARRAY['execution_profile', 'execution_profile_entry'],
             ARRAY['idx_execution_profile_workspace_name', 'idx_execution_profile_entry_profile_agent']),
            ('904_runtime_profile_add_deerflow_zcode',
             ARRAY['923_runtime_profile_add_deerflow_zcode'],
             false,
             ARRAY['runtime_profile'],
             ARRAY[]::text[]),
            ('905_agent_session_context_gate',
             ARRAY['907_agent_session_context_gate'],
             false,
             ARRAY['agent'],
             ARRAY[]::text[]),
            ('906_task_usage',
             ARRAY['908_task_usage_context_tokens', '915_task_usage_run_stats', '924_task_message_is_error'],
             false,
             ARRAY['task_usage', 'task_message'],
             ARRAY[]::text[]),
            ('907_marketplace',
             ARRAY['910_marketplace_listing', '911_marketplace_listing_name_index', '912_marketplace_listing_discovery_index', '913_marketplace_listing_source_index', '914_marketplace_prompt'],
             false,
             ARRAY['marketplace_listing', 'marketplace_prompt_version', 'workspace_prompt_install'],
             ARRAY['idx_marketplace_listing_kind_name_key', 'idx_marketplace_listing_discovery', 'idx_marketplace_listing_source_workspace', 'idx_marketplace_prompt_version_discovery', 'idx_marketplace_prompt_version_source', 'idx_marketplace_prompt_version_idempotency']),
            ('908_prompt',
             ARRAY['916_prompt_version', '917_agent_task_queue_prompt_versions', '919_prompt_version_scope_version_index', '920_prompt_version_scope_created_index', '921_prompt_version_workspace_index', '922_prompt_version_v1_backfill', '925_prompt_quality_rollup', '926_prompt_quality_daily_scope_day_index', '927_prompt_quality_daily_workspace_index', '928_prompt_perplexity_score_workspace_index', '929_prompt_quiz', '930_prompt_quiz_result_baseline_index', '931_prompt_quiz_result_batch_index', '932_prompt_quiz_item_workspace_index', '933_prompt_quiz_outcome_answered', '934_prompt_quiz_result_runtime', '935_prompt_quiz_item_rubric', '936_prompt_quiz_result_item_index', '937_prompt_version_v1_gap_backfill', '938_prompt_quiz_orphan_cleanup', '956_prompt_quiz_grading', '958_prompt_proposal', '959_prompt_proposal_workspace_index', '960_prompt_structure_baseline', '961_prompt_structure_baseline_unique'],
             false,
             ARRAY['prompt_version', 'agent_task_queue', 'prompt_quality_rollup_state', 'prompt_quality_daily', 'prompt_perplexity_score', 'prompt_quiz_sweep_state', 'prompt_quiz_item', 'prompt_quiz_result', 'prompt_proposal', 'prompt_structure_baseline'],
             ARRAY['idx_prompt_version_scope_version', 'idx_prompt_version_scope_created', 'idx_prompt_version_workspace', 'idx_prompt_quality_daily_scope_day', 'idx_prompt_quality_daily_workspace', 'idx_prompt_perplexity_score_workspace', 'idx_prompt_quiz_result_baseline', 'idx_prompt_quiz_result_batch', 'idx_prompt_quiz_item_workspace', 'idx_prompt_quiz_result_item', 'idx_prompt_proposal_workspace_status', 'uidx_prompt_structure_baseline_carrier']),
            ('909_oauth_client',
             ARRAY['939_oauth_client', '940_oauth_client_client_id_index'],
             false,
             ARRAY['oauth_client'],
             ARRAY['idx_oauth_client_client_id']),
            ('910_proposal',
             ARRAY['943_proposal', '944_proposal_workspace_status', '955_proposal_system_dir_dedupe'],
             false,
             ARRAY[]::text[],
             ARRAY[]::text[]),
            ('911_knowledge',
             ARRAY['945_knowledge', '946_knowledge_dir_workspace', '947_knowledge_entry_identity', '948_knowledge_scan_batch_dir', '950_knowledge_daemon_execution', '951_knowledge_dir_ws_path_unique', '952_knowledge_dir_ultimate_active_unique', '953_knowledge_dir_daemon_idx', '954_project_resource_local_dir_daemon_idx'],
             false,
             ARRAY['knowledge_dir', 'knowledge_entry', 'knowledge_scan_batch', 'project_resource'],
             ARRAY['idx_knowledge_dir_workspace', 'uidx_knowledge_entry_identity', 'idx_knowledge_scan_batch_dir', 'uidx_knowledge_dir_ws_path', 'uidx_knowledge_dir_ultimate_active', 'idx_knowledge_dir_daemon', 'idx_project_resource_local_dir_daemon']),
            ('912_issue_run_suppressed',
             ARRAY['949_issue_run_suppressed'],
             false,
             ARRAY['issue'],
             ARRAY[]::text[]),
            ('913_skill',
             ARRAY['941_skill_version', '942_skill_version_identity_index', '957_legislation_e1_removal', '970_runtime_skill_discovery', '971_runtime_skill_discovery_identity_uidx'],
             false,
             ARRAY['skill_version', 'runtime_skill_discovery'],
             ARRAY['idx_skill_version_identity', 'idx_runtime_skill_discovery_identity']),
            ('914_retrospective',
             ARRAY['962_retrospective', '963_retrospective_run_index', '964_retrospective_watermark_unique'],
             false,
             ARRAY['retrospective_config', 'retrospective_run', 'retrospective_issue_watermark'],
             ARRAY['idx_retrospective_run_workspace', 'uidx_retrospective_watermark_issue']),
            ('915_channel_chat_run_intent',
             ARRAY['965_channel_chat_run_intent', '966_channel_chat_run_intent_id_uidx', '967_channel_chat_run_intent_pkey', '968_channel_chat_run_intent_pending_uidx', '969_channel_chat_run_intent_claim_idx'],
             false,
             ARRAY['channel_chat_run_intent'],
             ARRAY['channel_chat_run_intent_pkey', 'channel_chat_run_intent_pending_uidx', 'idx_channel_chat_run_intent_claim']),
            ('916_agent_task_queue',
             ARRAY['972_agent_task_cancel_requested', '973_agent_task_cancel_attribution'],
             false,
             ARRAY['agent_task_queue'],
             ARRAY[]::text[])
        ) AS t(target, sources, fold, tables, indexes)
    LOOP
        expected := array_length(g.sources, 1);
        SELECT count(*) INTO source_count
        FROM schema_migrations
        WHERE version = ANY (g.sources);

        IF EXISTS (SELECT 1 FROM schema_migrations WHERE version = g.target) THEN
            IF g.fold THEN
                -- The target row is the old tree's own 902 row: just retire
                -- the redundant member identities.
                DELETE FROM schema_migrations WHERE version = ANY (g.sources);
            ELSE
                IF source_count > 0 THEN
                    RAISE EXCEPTION 'group %: target row present alongside % source row(s); ambiguous double state, refusing the rewrite', g.target, source_count;
                END IF;
                -- already rewritten (or the new tree applied it): fall
                -- through to the object guards.
            END IF;
        ELSE
            IF source_count = 0 THEN
                CONTINUE; -- fresh database: this group was never applied
            END IF;
            IF g.fold THEN
                RAISE EXCEPTION 'group %: target row missing while source rows are recorded; broken history, repair the database before rewriting the ledger', g.target;
            END IF;
            IF source_count <> expected THEN
                RAISE EXCEPTION 'group %: partial source set (% of % present); refusing a half-applied history', g.target, source_count, expected;
            END IF;
            UPDATE schema_migrations SET version = g.target
            WHERE version = g.sources[1];
            DELETE FROM schema_migrations WHERE version = ANY (g.sources[2:expected]);
        END IF;

        -- Object guards: the target row claims these objects exist. Validate
        -- them on every pass (first rewrite and idempotent re-entry alike).
        FOREACH tbl IN ARRAY g.tables LOOP
            IF to_regclass(tbl) IS NULL THEN
                RAISE EXCEPTION 'group %: table % missing; repair schema drift before rewriting the ledger', g.target, tbl;
            END IF;
        END LOOP;
        FOREACH idx IN ARRAY g.indexes LOOP
            IF NOT EXISTS (
                SELECT 1 FROM pg_index
                WHERE indexrelid = to_regclass(idx) AND indisvalid
            ) THEN
                RAISE EXCEPTION 'group %: index % missing or invalid; repair schema drift before rewriting the ledger', g.target, idx;
            END IF;
        END LOOP;
    END LOOP;
END $$;
COMMIT;

-- ---------------------------------------------------------------------------
-- Phase 3 verification queries (run after this script, per §5):
--
--   -- 17 canonical stems all present:
--   SELECT count(*) FROM schema_migrations WHERE version IN (
--       '900_agent_webhooks', '901_project_instructions', '902_user_admin_state',
--       '903_execution_profile', '904_runtime_profile_add_deerflow_zcode',
--       '905_agent_session_context_gate', '906_task_usage', '907_marketplace',
--       '908_prompt', '909_oauth_client', '910_proposal', '911_knowledge',
--       '912_issue_run_suppressed', '913_skill', '914_retrospective',
--       '915_channel_chat_run_intent', '916_agent_task_queue');  -- expect 17
--   -- 72 original stems zero residue:
--   SELECT version FROM schema_migrations
--   WHERE version IN (/* full list in 9xx-consolidation.md §1 */);  -- expect 0 rows
--   -- New-tree zero-apply: run `migrate up` with the new tree; readiness must
--   -- pass with zero applied migrations.
-- ---------------------------------------------------------------------------
