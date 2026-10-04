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
	target  string
	sources []string
	fold    bool
	tables  []string
	indexes []string
}

func consolidationGroups() []consolidationGroup {
	return []consolidationGroup{
		{"902_user_admin_state",
			[]string{"903_admin_audit_log_actor_index"},
			true,
			[]string{`"user"`, "admin_audit_log"},
			[]string{"idx_admin_audit_log_actor_created"}},
		{"903_execution_profile",
			[]string{"904_execution_profile", "905_execution_profile_name_index", "906_execution_profile_entry_index"},
			false,
			[]string{"execution_profile", "execution_profile_entry"},
			[]string{"idx_execution_profile_workspace_name", "idx_execution_profile_entry_profile_agent"}},
		{"904_runtime_profile_add_deerflow_zcode",
			[]string{"923_runtime_profile_add_deerflow_zcode"},
			false,
			[]string{"runtime_profile"},
			nil},
		{"905_agent_session_context_gate",
			[]string{"907_agent_session_context_gate"},
			false,
			[]string{"agent"},
			nil},
		{"906_task_usage",
			[]string{"908_task_usage_context_tokens", "915_task_usage_run_stats", "924_task_message_is_error"},
			false,
			[]string{"task_usage", "task_message"},
			nil},
		{"907_marketplace",
			[]string{"910_marketplace_listing", "911_marketplace_listing_name_index", "912_marketplace_listing_discovery_index", "913_marketplace_listing_source_index", "914_marketplace_prompt"},
			false,
			[]string{"marketplace_listing", "marketplace_prompt_version", "workspace_prompt_install"},
			[]string{"idx_marketplace_listing_kind_name_key", "idx_marketplace_listing_discovery", "idx_marketplace_listing_source_workspace", "idx_marketplace_prompt_version_discovery", "idx_marketplace_prompt_version_source", "idx_marketplace_prompt_version_idempotency"}},
		{"908_prompt",
			[]string{"916_prompt_version", "917_agent_task_queue_prompt_versions", "919_prompt_version_scope_version_index", "920_prompt_version_scope_created_index", "921_prompt_version_workspace_index", "922_prompt_version_v1_backfill", "925_prompt_quality_rollup", "926_prompt_quality_daily_scope_day_index", "927_prompt_quality_daily_workspace_index", "928_prompt_perplexity_score_workspace_index", "929_prompt_quiz", "930_prompt_quiz_result_baseline_index", "931_prompt_quiz_result_batch_index", "932_prompt_quiz_item_workspace_index", "933_prompt_quiz_outcome_answered", "934_prompt_quiz_result_runtime", "935_prompt_quiz_item_rubric", "936_prompt_quiz_result_item_index", "937_prompt_version_v1_gap_backfill", "938_prompt_quiz_orphan_cleanup", "956_prompt_quiz_grading", "958_prompt_proposal", "959_prompt_proposal_workspace_index", "960_prompt_structure_baseline", "961_prompt_structure_baseline_unique"},
			false,
			[]string{"prompt_version", "agent_task_queue", "prompt_quality_rollup_state", "prompt_quality_daily", "prompt_perplexity_score", "prompt_quiz_sweep_state", "prompt_quiz_item", "prompt_quiz_result", "prompt_proposal", "prompt_structure_baseline"},
			[]string{"idx_prompt_version_scope_version", "idx_prompt_version_scope_created", "idx_prompt_version_workspace", "idx_prompt_quality_daily_scope_day", "idx_prompt_quality_daily_workspace", "idx_prompt_perplexity_score_workspace", "idx_prompt_quiz_result_baseline", "idx_prompt_quiz_result_batch", "idx_prompt_quiz_item_workspace", "idx_prompt_quiz_result_item", "idx_prompt_proposal_workspace_status", "uidx_prompt_structure_baseline_carrier"}},
		{"909_oauth_client",
			[]string{"939_oauth_client", "940_oauth_client_client_id_index"},
			false,
			[]string{"oauth_client"},
			[]string{"idx_oauth_client_client_id"}},
		{"910_proposal",
			[]string{"943_proposal", "944_proposal_workspace_status", "955_proposal_system_dir_dedupe"},
			false,
			nil,
			nil},
		{"911_knowledge",
			[]string{"945_knowledge", "946_knowledge_dir_workspace", "947_knowledge_entry_identity", "948_knowledge_scan_batch_dir", "950_knowledge_daemon_execution", "951_knowledge_dir_ws_path_unique", "952_knowledge_dir_ultimate_active_unique", "953_knowledge_dir_daemon_idx", "954_project_resource_local_dir_daemon_idx"},
			false,
			[]string{"knowledge_dir", "knowledge_entry", "knowledge_scan_batch", "project_resource"},
			[]string{"idx_knowledge_dir_workspace", "uidx_knowledge_entry_identity", "idx_knowledge_scan_batch_dir", "uidx_knowledge_dir_ws_path", "uidx_knowledge_dir_ultimate_active", "idx_knowledge_dir_daemon", "idx_project_resource_local_dir_daemon"}},
		{"912_issue_run_suppressed",
			[]string{"949_issue_run_suppressed"},
			false,
			[]string{"issue"},
			nil},
		{"913_skill",
			[]string{"941_skill_version", "942_skill_version_identity_index", "957_legislation_e1_removal", "970_runtime_skill_discovery", "971_runtime_skill_discovery_identity_uidx"},
			false,
			[]string{"skill_version", "runtime_skill_discovery"},
			[]string{"idx_skill_version_identity", "idx_runtime_skill_discovery_identity"}},
		{"914_retrospective",
			[]string{"962_retrospective", "963_retrospective_run_index", "964_retrospective_watermark_unique"},
			false,
			[]string{"retrospective_config", "retrospective_run", "retrospective_issue_watermark"},
			[]string{"idx_retrospective_run_workspace", "uidx_retrospective_watermark_issue"}},
		{"915_channel_chat_run_intent",
			[]string{"965_channel_chat_run_intent", "966_channel_chat_run_intent_id_uidx", "967_channel_chat_run_intent_pkey", "968_channel_chat_run_intent_pending_uidx", "969_channel_chat_run_intent_claim_idx"},
			false,
			[]string{"channel_chat_run_intent"},
			[]string{"channel_chat_run_intent_pkey", "channel_chat_run_intent_pending_uidx", "idx_channel_chat_run_intent_claim"}},
		{"916_agent_task_queue",
			[]string{"972_agent_task_cancel_requested", "973_agent_task_cancel_attribution"},
			false,
			[]string{"agent_task_queue"},
			nil},
	}
}

// passThroughStems are the original stems whose ledger rows survive untouched
// (identity files in the consolidation).
var passThroughStems = []string{"900_agent_webhooks", "901_project_instructions", "902_user_admin_state"}

// postConsolidationStems are 9xx migrations added after the RUYI-359
// consolidation; they extend the on-disk set without belonging to any
// consolidation group, so the repair script must leave their ledger rows
// untouched.
var postConsolidationStems = []string{
	"975_issue_decisions",
	"976_issue_decisions_issue_idx",
}

// finalStems are the 17 canonical 9xx stems of the consolidated tree, in
// migration order.
func finalStems() []string {
	stems := passThroughStems[:2:2] // 900, 901
	for _, g := range consolidationGroups() {
		stems = append(stems, g.target)
	}
	return stems
}

// originalStems are the 72 stems the pre-consolidation tree defined at
// fb42ab58b46de24dcae64459647f5671d88d57be.
func originalStems() []string {
	stems := append([]string{}, passThroughStems...)
	for _, g := range consolidationGroups() {
		stems = append(stems, g.sources...)
	}
	return stems
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

	// The fixture map must partition the original stems exactly: every
	// source is claimed once, pass-through stems are never sources, and the
	// on-disk 9xx files are exactly the canonical targets.
	groups := consolidationGroups()
	seen := map[string]bool{}
	for _, g := range groups {
		for _, s := range g.sources {
			if seen[s] {
				t.Fatalf("source %s claimed by more than one group", s)
			}
			seen[s] = true
		}
		if g.fold && len(g.sources) != 1 {
			t.Fatalf("fold group %s must have exactly one source", g.target)
		}
	}
	for _, p := range passThroughStems {
		if seen[p] {
			t.Fatalf("pass-through stem %s must not be a rewrite source", p)
		}
	}
	if len(seen)+len(passThroughStems) != 72 {
		t.Fatalf("sources+pass-through = %d, want 72 original stems", len(seen)+len(passThroughStems))
	}
	entries, err := os.ReadDir("../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	onDisk := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "9") && strings.HasSuffix(name, ".up.sql") {
			onDisk[strings.TrimSuffix(name, ".up.sql")] = true
		}
	}
	wantDisk := map[string]bool{}
	for _, f := range append(finalStems(), postConsolidationStems...) {
		wantDisk[f] = true
	}
	if !reflect.DeepEqual(onDisk, wantDisk) {
		t.Fatalf("on-disk 9xx stems %v do not match canonical targets %v", onDisk, wantDisk)
	}

	var allSources []string
	for _, g := range groups {
		allSources = append(allSources, g.sources...)
	}

	for _, tc := range []struct {
		name    string
		initial []string
		want    []string
		// objects lists the groups whose guarded tables/indexes the fixture
		// provisions. Guards only run for groups with a target row present,
		// so cases that seed nothing need no objects.
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
			initial: append(append([]string{"untouched"}, originalStems()...), oauthLegacyVersions...),
			want:    nil, // computed below: untouched + all 17 canonical stems
			objects: []string{"ALL"},
		},
		{
			name: "new-tree-idempotent",
			initial: append([]string{"untouched"}, finalStems()...),
			want:    nil, // computed below: unchanged
			objects: []string{"ALL"},
		},
		{
			name: "post-consolidation-tree-idempotent",
			initial: append(append([]string{"untouched"}, finalStems()...), postConsolidationStems...),
			want:    nil, // computed below: unchanged
			objects: []string{"ALL"},
		},
		{
			name: "partial-sources-reject",
			initial: []string{"untouched",
				"916_prompt_version", "917_agent_task_queue_prompt_versions", "919_prompt_version_scope_version_index",
				"920_prompt_version_scope_created_index", "921_prompt_version_workspace_index", "922_prompt_version_v1_backfill",
				"925_prompt_quality_rollup"},
			// 7 of the 25 group sources present -> must refuse
			objects: nil,
		},
		{
			name:    "fold-target-missing-reject",
			initial: []string{"untouched", "903_admin_audit_log_actor_index"},
			objects: nil,
		},
		{
			name:    "rename-target-with-sources-reject",
			initial: []string{"untouched", "909_oauth_client", "939_oauth_client", "940_oauth_client_client_id_index"},
			objects: nil,
		},
		{
			name:    "oauth-legacy-fold",
			initial: append([]string{"untouched"}, oauthLegacyVersions...),
			want:    []string{"909_oauth_client", "untouched"},
			objects: []string{"909_oauth_client"},
		},
		{
			name: "oauth-legacy-and-new",
			initial: []string{
				"untouched",
				"929_oauth_client", "930_oauth_client_client_id_index",
				"924_oauth_clients", "925_oauth_clients_client_id_index",
				"939_oauth_client", "940_oauth_client_client_id_index",
			},
			want:    []string{"909_oauth_client", "untouched"},
			objects: []string{"909_oauth_client"},
		},
		{
			name:        "missing-table-reject",
			initial:     append(append([]string{"untouched"}, originalStems()...), oauthLegacyVersions...),
			want:        nil, // computed below
			objects:     []string{"ALL"},
			breakObject: "DROP TABLE knowledge_entry",
		},
		{
			name:        "missing-index-reject",
			initial:     append(append([]string{"untouched"}, originalStems()...), oauthLegacyVersions...),
			want:        nil, // computed below
			objects:     []string{"ALL"},
			breakObject: "DROP INDEX idx_admin_audit_log_actor_created",
		},
		{
			name:        "oauth-legacy-missing-table-reject",
			initial:     append([]string{"untouched"}, oauthLegacyVersions...),
			objects:     []string{"909_oauth_client"},
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

			if len(tc.objects) > 0 {
				for _, g := range groups {
					provision := tc.objects[0] == "ALL" || tc.objects[0] == g.target
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
				want = append(finalStems(), "untouched")
			}
			if strings.HasSuffix(tc.name, "-idempotent") {
				want = append([]string{}, tc.initial...)
			}
			// Sort a copy so the comparison is order-insensitive; append from a
			// nil slice keeps nil distinct from the empty slice (DeepEqual).
			var sortedWant []string
			sortedWant = append(sortedWant, want...)
			sort.Strings(sortedWant)
			want = sortedWant

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
	missing := 0
	for _, g := range groups {
		if !strings.Contains(script, "('"+g.target+"',") {
			missing++
			t.Errorf("script has no Stage 2 row for group %s", g.target)
		}
	}
	if missing == 0 {
		t.Logf("all %d groups present in script", len(groups))
	}
}
