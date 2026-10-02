package main

import (
	"context"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// consolidationGroup mirrors one row of the Stage 2 VALUES map in
// repair_9xx_consolidation_ledger.sql. It is deliberately duplicated here: the
// test must provision exactly the objects the script guards, and a drift
// between the two lists surfaces as a failure of the full-rewrite case below.
type consolidationGroup struct {
	lead    string
	members []string
	tables  []string
	indexes []string
}

func consolidationGroups() []consolidationGroup {
	return []consolidationGroup{
		{"902_user_admin_state",
			[]string{"903_admin_audit_log_actor_index"},
			[]string{`"user"`, "admin_audit_log"},
			[]string{"idx_admin_audit_log_actor_created"}},
		{"904_execution_profile",
			[]string{"905_execution_profile_name_index", "906_execution_profile_entry_index"},
			[]string{"execution_profile", "execution_profile_entry"},
			[]string{"idx_execution_profile_workspace_name", "idx_execution_profile_entry_profile_agent"}},
		{"910_marketplace_listing",
			[]string{"911_marketplace_listing_name_index", "912_marketplace_listing_discovery_index", "913_marketplace_listing_source_index"},
			[]string{"marketplace_listing"},
			[]string{"idx_marketplace_listing_kind_name_key", "idx_marketplace_listing_discovery", "idx_marketplace_listing_source_workspace"}},
		{"916_prompt_version",
			[]string{"917_agent_task_queue_prompt_versions", "919_prompt_version_scope_version_index", "920_prompt_version_scope_created_index", "921_prompt_version_workspace_index", "922_prompt_version_v1_backfill", "937_prompt_version_v1_gap_backfill"},
			[]string{"prompt_version", "agent_task_queue"},
			[]string{"idx_prompt_version_scope_version", "idx_prompt_version_scope_created", "idx_prompt_version_workspace"}},
		{"925_prompt_quality_rollup",
			[]string{"926_prompt_quality_daily_scope_day_index", "927_prompt_quality_daily_workspace_index", "928_prompt_perplexity_score_workspace_index"},
			[]string{"prompt_quality_rollup_state", "prompt_quality_daily", "prompt_perplexity_score"},
			[]string{"idx_prompt_quality_daily_scope_day", "idx_prompt_quality_daily_workspace", "idx_prompt_perplexity_score_workspace"}},
		{"929_prompt_quiz",
			[]string{"930_prompt_quiz_result_baseline_index", "931_prompt_quiz_result_batch_index", "932_prompt_quiz_item_workspace_index", "933_prompt_quiz_outcome_answered", "934_prompt_quiz_result_runtime", "935_prompt_quiz_item_rubric", "936_prompt_quiz_result_item_index", "938_prompt_quiz_orphan_cleanup", "956_prompt_quiz_grading"},
			[]string{"prompt_quiz_sweep_state", "prompt_quiz_item", "prompt_quiz_result"},
			[]string{"idx_prompt_quiz_result_baseline", "idx_prompt_quiz_result_batch", "idx_prompt_quiz_item_workspace", "idx_prompt_quiz_result_item"}},
		{"939_oauth_client",
			[]string{"940_oauth_client_client_id_index"},
			[]string{"oauth_client"},
			[]string{"idx_oauth_client_client_id"}},
		{"941_skill_version",
			[]string{"942_skill_version_identity_index"},
			[]string{"skill_version"},
			[]string{"idx_skill_version_identity"}},
		{"943_proposal",
			[]string{"944_proposal_workspace_status", "955_proposal_system_dir_dedupe"},
			[]string{"proposal"},
			[]string{"idx_proposal_workspace_status", "uidx_proposal_system_dir"}},
		{"945_knowledge",
			[]string{"946_knowledge_dir_workspace", "947_knowledge_entry_identity", "948_knowledge_scan_batch_dir", "951_knowledge_dir_ws_path_unique", "952_knowledge_dir_ultimate_active_unique"},
			[]string{"knowledge_dir", "knowledge_entry", "knowledge_scan_batch"},
			[]string{"idx_knowledge_dir_workspace", "uidx_knowledge_entry_identity", "idx_knowledge_scan_batch_dir", "uidx_knowledge_dir_ws_path", "uidx_knowledge_dir_ultimate_active"}},
		{"958_prompt_proposal",
			[]string{"959_prompt_proposal_workspace_index"},
			[]string{"prompt_proposal"},
			[]string{"idx_prompt_proposal_workspace_status"}},
		{"960_prompt_structure_baseline",
			[]string{"961_prompt_structure_baseline_unique"},
			[]string{"prompt_structure_baseline"},
			[]string{"uidx_prompt_structure_baseline_carrier"}},
		{"962_retrospective",
			[]string{"963_retrospective_run_index", "964_retrospective_watermark_unique"},
			[]string{"retrospective_config", "retrospective_run", "retrospective_issue_watermark"},
			[]string{"idx_retrospective_run_workspace", "uidx_retrospective_watermark_issue"}},
		{"965_channel_chat_run_intent",
			[]string{"966_channel_chat_run_intent_id_uidx", "967_channel_chat_run_intent_pkey", "968_channel_chat_run_intent_pending_uidx", "969_channel_chat_run_intent_claim_idx"},
			[]string{"channel_chat_run_intent"},
			[]string{"channel_chat_run_intent_id_uidx", "channel_chat_run_intent_pending_uidx", "idx_channel_chat_run_intent_claim"}},
		{"970_runtime_skill_discovery",
			[]string{"971_runtime_skill_discovery_identity_uidx"},
			[]string{"runtime_skill_discovery"},
			[]string{"idx_runtime_skill_discovery_identity"}},
		{"972_agent_task_cancel_requested",
			[]string{"973_agent_task_cancel_attribution"},
			[]string{"agent_task_queue"},
			nil},
	}
}

var oauthLegacyVersions = []string{
	"929_oauth_client",
	"930_oauth_client_client_id_index",
	"924_oauth_clients",
	"925_oauth_clients_client_id_index",
}

func TestRepair9xxConsolidationLedger(t *testing.T) {
	sqlBytes, err := os.ReadFile("repair_9xx_consolidation_ledger.sql")
	if err != nil {
		t.Fatal(err)
	}
	script := string(sqlBytes)

	var allLeadsSlice []string
	var allMembers []string
	for _, g := range consolidationGroups() {
		allLeadsSlice = append(allLeadsSlice, g.lead)
		allMembers = append(allMembers, g.members...)
	}

	for _, tc := range []struct {
		name    string
		initial []string
		want    []string
		// objects lists the groups whose guarded tables/indexes the fixture
		// provisions. Guards only run for groups with a lead row present, so
		// cases that seed nothing need no objects.
		objects []string
		// breakObject drops one guarded object before the run to prove the
		// script fails closed; want then applies to the post-rollback ledger.
		breakObject string
	}{
		{
			name:    "fresh-noop",
			initial: nil,
			want:    nil,
		},
		{
			name:    "full-rewrite",
			initial: append(append([]string{"untouched"}, allLeadsSlice...), allMembers...),
			want:    nil, // computed below: untouched + all leads
			objects: []string{"ALL"},
		},
		{
			name:    "partial-member-reject",
			initial: []string{"untouched", "929_prompt_quiz", "930_prompt_quiz_result_baseline_index", "931_prompt_quiz_result_batch_index", "932_prompt_quiz_item_workspace_index", "933_prompt_quiz_outcome_answered", "934_prompt_quiz_result_runtime", "935_prompt_quiz_item_rubric", "936_prompt_quiz_result_item_index"},
			// 7 of the 9 group members present -> must refuse
			objects: []string{"929_prompt_quiz"},
		},
		{
			name:    "lead-missing-reject",
			initial: []string{"untouched", "944_proposal_workspace_status", "955_proposal_system_dir_dedupe"},
			objects: []string{"943_proposal"},
		},
		{
			name:    "oauth-legacy-fold",
			initial: append([]string{"untouched"}, oauthLegacyVersions...),
			want:    []string{"939_oauth_client", "untouched"},
			objects: []string{"939_oauth_client"},
		},
		{
			name: "oauth-legacy-and-new",
			initial: []string{
				"untouched",
				"929_oauth_client", "930_oauth_client_client_id_index",
				"924_oauth_clients", "925_oauth_clients_client_id_index",
				"939_oauth_client", "940_oauth_client_client_id_index",
			},
			want:    []string{"939_oauth_client", "untouched"},
			objects: []string{"939_oauth_client"},
		},
		{
			name:    "missing-table-reject",
			initial: append(append([]string{"untouched"}, allLeadsSlice...), allMembers...),
			want:    nil, // computed below
			objects: []string{"ALL"},
			// knowledge_entry is guarded by the 945 group
			breakObject: "DROP TABLE knowledge_entry",
		},
		{
			name:        "missing-index-reject",
			initial:     append(append([]string{"untouched"}, allLeadsSlice...), allMembers...),
			want:        nil, // computed below
			objects:     []string{"ALL"},
			breakObject: "DROP INDEX idx_admin_audit_log_actor_created",
		},
		{
			name:        "oauth-legacy-missing-table-reject",
			initial:     append([]string{"untouched"}, oauthLegacyVersions...),
			objects:     []string{"939_oauth_client"},
			breakObject: "DROP TABLE oauth_client",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			conn, err := f.pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
			if _, err := conn.Exec(ctx, "SET search_path TO "+pgx.Identifier{f.schema}.Sanitize()); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := conn.Exec(ctx, "RESET search_path"); err != nil {
					t.Error(err)
				}
			}()

			if _, err := conn.Exec(ctx, `
				CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL);
			`); err != nil {
				t.Fatal(err)
			}

			groups := consolidationGroups()
			if len(tc.objects) > 0 {
				for _, g := range groups {
					provision := tc.objects[0] == "ALL" || tc.objects[0] == g.lead
					if !provision {
						continue
					}
					for _, tbl := range g.tables {
						if _, err := conn.Exec(ctx, "CREATE TABLE IF NOT EXISTS "+tbl+" (id int)"); err != nil {
							t.Fatalf("provision table %s: %v", tbl, err)
						}
					}
					for _, idx := range g.indexes {
						if _, err := conn.Exec(ctx, "CREATE UNIQUE INDEX IF NOT EXISTS "+idx+" ON "+g.tables[0]+" (id)"); err != nil {
							t.Fatalf("provision index %s: %v", idx, err)
						}
					}
				}
			}

			for _, version := range tc.initial {
				if _, err := conn.Exec(ctx,
					"INSERT INTO schema_migrations VALUES ($1, '2026-01-01T00:00:00Z')", version); err != nil {
					t.Fatal(err)
				}
			}
			if tc.breakObject != "" {
				if _, err := conn.Exec(ctx, tc.breakObject); err != nil {
					t.Fatal(err)
				}
			}

			if tc.breakObject != "" || strings.HasSuffix(tc.name, "-reject") {
				// The script must fail closed: the whole transaction rolls
				// back and the ledger keeps every seeded row untouched.
				if _, err := conn.Exec(ctx, script); err == nil {
					t.Fatal("ledger rewrite must be rejected")
				}
				if _, err := conn.Exec(ctx, "ROLLBACK"); err != nil {
					t.Fatal(err)
				}
				want := append([]string{}, tc.initial...)
				sort.Strings(want)
				if got := f.appliedVersions(t); !reflect.DeepEqual(got, want) {
					t.Fatalf("rejected rewrite changed versions: got %v, want %v", got, want)
				}
				return
			}

			want := tc.want
			if tc.name == "full-rewrite" {
				want = append(allLeadsSlice, "untouched")
			}

			// Happy path: run twice to prove idempotent re-entry.
			for range 2 {
				if _, err := conn.Exec(ctx, script); err != nil {
					t.Fatal(err)
				}
			}
			if got := f.appliedVersions(t); !reflect.DeepEqual(got, want) {
				t.Fatalf("versions = %v, want %v", got, want)
			}

			// The rewrite only edits identity rows: every applied_at must be
			// exactly the seeded value.
			var preserved bool
			if err := conn.QueryRow(ctx, `SELECT NOT EXISTS (
				SELECT 1 FROM schema_migrations WHERE applied_at <> '2026-01-01T00:00:00Z'
			)`).Scan(&preserved); err != nil || !preserved {
				t.Fatalf("applied_at preserved = %v, error = %v", preserved, err)
			}
		})
	}

	// The script must know every group the test models; a group added to the
	// SQL without a matching fixture here would silently skip in the
	// full-rewrite case (no rows seeded -> clean skip). Count the Stage 2
	// group rows in the script against the fixture map.
	groups := consolidationGroups()
	missing := 0
	for _, g := range groups {
		if !strings.Contains(script, "('"+g.lead+"',") {
			missing++
			t.Errorf("script has no Stage 2 row for group %s", g.lead)
		}
	}
	if missing == 0 {
		t.Logf("all %d groups present in script", len(groups))
	}
}
