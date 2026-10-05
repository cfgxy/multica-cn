package handler

import (
	"context"
	"sort"
	"testing"
)

type workspaceDeleteAction string

const (
	workspaceDelete       workspaceDeleteAction = "delete"
	workspaceDeleteDetach workspaceDeleteAction = "detach"
	workspaceDeleteKeep   workspaceDeleteAction = "keep"
	workspaceDeleteSettle workspaceDeleteAction = "settle"
)

// workspaceDeletionManifest is the schema coverage contract for workspace
// teardown. Adding a table requires an explicit ownership decision here; the
// handler deletion graph must then implement that decision before CI passes.
var workspaceDeletionManifest = map[string]workspaceDeleteAction{
	"activity_log": workspaceDelete,
	// Instance-level admin audit trail (RUYI-47): audit history survives
	// workspace teardown and keeps its workspace_id un-nulled so the trail
	// still shows which workspace an action affected; classified Settle
	// (not Keep) because the row is left in place with workspace_id intact
	// rather than being genuinely workspace-agnostic.
	"admin_audit_log":             workspaceDeleteSettle,
	"agent":                       workspaceDelete,
	"agent_builder_draft":         workspaceDelete,
	"agent_invocation_target":     workspaceDelete,
	"agent_runtime":               workspaceDelete,
	"agent_skill":                 workspaceDelete,
	"agent_task_queue":            workspaceDelete,
	"agent_to_label":              workspaceDelete,
	"agent_webhook":               workspaceDelete,
	"attachment":                  workspaceDelete,
	"autopilot":                   workspaceDelete,
	"autopilot_collaborator":      workspaceDelete,
	"autopilot_quota_period":      workspaceDelete,
	"autopilot_quota_reservation": workspaceDelete,
	"autopilot_rule_version":      workspaceDelete,
	"autopilot_run":               workspaceDelete,
	"autopilot_subscriber":        workspaceDelete,
	"autopilot_trigger":           workspaceDelete,
	"channel_binding_token":       workspaceDelete,
	// Capability probe verdicts (RUYI-400) are installation-scoped
	// diagnostics: DeleteWorkspace sweeps them through ws_installations.
	"channel_capability_state":        workspaceDelete,
	"channel_chat_context_generation": workspaceDelete,
	// Run-trigger intents (RUYI-304) own nothing outside the database: the
	// cascade deletes them directly instead of settling through a reconciler.
	"channel_chat_run_intent":         workspaceDelete,
	"channel_chat_session_binding":    workspaceDelete,
	"channel_inbound_audit":           workspaceDelete,
	"channel_inbound_message_dedup":   workspaceDelete,
	"channel_installation":            workspaceDelete,
	"channel_media_pending_object":    workspaceDeleteSettle,
	"channel_outbound_card_message":   workspaceDelete,
	"channel_outbound_message":        workspaceDelete,
	"channel_task_delivery":           workspaceDelete,
	"channel_user_binding":            workspaceDelete,
	"chat_draft_restore":              workspaceDelete,
	"chat_message":                    workspaceDelete,
	"chat_pinned_agent":               workspaceDelete,
	"chat_session":                    workspaceDelete,
	"client_usage_daily":              workspaceDeleteDetach,
	"comment":                         workspaceDelete,
	"comment_reaction":                workspaceDelete,
	"contact_sales_inquiry":           workspaceDeleteKeep,
	"daemon_connection":               workspaceDelete,
	"daemon_token":                    workspaceDelete,
	"dingtalk_group_presence":         workspaceDelete,
	"dingtalk_bot_identity":           workspaceDelete,
	"dingtalk_group_route":            workspaceDelete,
	"execution_profile":               workspaceDelete,
	"execution_profile_entry":         workspaceDelete,
	"feedback":                        workspaceDeleteDetach,
	"github_installation":             workspaceDelete,
	"github_pending_check_suite":      workspaceDelete,
	"github_pending_installation":     workspaceDeleteKeep,
	"github_pull_request":             workspaceDelete,
	"github_pull_request_check_run":   workspaceDelete,
	"github_pull_request_check_suite": workspaceDelete,
	"inbox_item":                      workspaceDelete,
	"issue":                           workspaceDelete,
	// Self-evolution surfaces (RUYI-265/RUYI-305): the prompt-legislation
	// pool, its structure baselines, the retrospective config/run/watermark
	// tables, registered knowledge directories, their read-only mirror
	// entries and the scan log all go with the workspace
	// (DeleteWorkspaceSelfEvolutionData).
	"knowledge_dir":                 workspaceDelete,
	"knowledge_entry":               workspaceDelete,
	"knowledge_scan_batch":          workspaceDelete,
	"prompt_proposal":               workspaceDelete,
	"prompt_structure_baseline":     workspaceDelete,
	"retrospective_config":          workspaceDelete,
	"retrospective_issue_watermark": workspaceDelete,
	"retrospective_run":             workspaceDelete,
	// Published marketplace listings (RUYI-99) are owned by the workspace that
	// published them and go with it. Not Settle: the tombstone reserves a name
	// so its owner can republish, and a deleted workspace has no owner left to
	// do so. Classified Delete rather than Keep because it carries
	// source_workspace_id, which the manifest treats as workspace-scoped.
	"marketplace_listing":                workspaceDelete,
	"issue_view":                         workspaceDelete,
	"issue_view_preference":              workspaceDelete,
	"issue_dependency":                   workspaceDelete,
	"issue_label":                        workspaceDelete,
	"issue_property":                     workspaceDelete,
	"issue_pull_request":                 workspaceDelete,
	"issue_reaction":                     workspaceDelete,
	"issue_source_context":               workspaceDelete,
	"issue_source_context_object_intent": workspaceDeleteSettle,
	"issue_status":                       workspaceDelete,
	"issue_subscriber":                   workspaceDelete,
	"issue_to_label":                     workspaceDelete,
	"issue_vcs_pull_request":             workspaceDelete,
	// Decision cards (RUYI-345) own nothing outside the database; the answer
	// echo is a plain comment row swept with the rest of comment.
	"issue_decisions":            workspaceDelete,
	"lark_binding_token":         workspaceDelete,
	"lark_chat_session_binding":  workspaceDelete,
	"lark_inbound_audit":         workspaceDelete,
	"lark_inbound_message_dedup": workspaceDelete,
	"lark_installation":          workspaceDelete,
	"lark_outbound_card_message": workspaceDelete,
	"lark_user_binding":          workspaceDelete,
	// RUYI-425 stage 3: the voice gateway's per-conversation log (design
	// §3.5). Rows reach the teardown through the live_session.workspace_id
	// CASCADE FK; agent/instance/user columns are historical UUIDs by design
	// (migration 926), so no explicit DELETE is needed — same shape as
	// agent_webhook.
	"live_session": workspaceDelete,
	// A published prompt version outlives the workspace it came from
	// (RUYI-100): other workspaces hold installs against it, and the catalog
	// only ever shows the publisher, never the source workspace. Keep, not
	// Settle — the table has source_workspace_id, which is withdrawal
	// authority, not a workspace_id ownership column.
	"marketplace_prompt_version": workspaceDeleteKeep,
	// The workspace's own install library goes with it.
	"workspace_prompt_install": workspaceDelete,
	"member":                   workspaceDelete,
	"agent_mcp_server":         workspaceDelete,
	"workspace_mcp_server":     workspaceDelete,
	"notification_preference":  workspaceDelete,
	// Pre-registered OAuth clients for the MCP authorization server
	// (RUYI-209). Deployment-level, like personal_access_token: a client is
	// registered by an operator against the whole installation and carries no
	// workspace_id, so deleting a workspace must not remove it — the access
	// tokens it mints are scoped by the user behind them, not by workspace.
	"oauth_client":           workspaceDeleteKeep,
	"personal_access_token":  workspaceDeleteKeep,
	"pinned_item":            workspaceDelete,
	"plugin_installation":    workspaceDelete,
	"plugin_hook_schedule":   workspaceDelete,
	"plugin_invocation":      workspaceDelete,
	"plugin_storage":         workspaceDelete,
	"plugin_secret":          workspaceDelete,
	"plugin_package":         workspaceDelete,
	"plugin_package_version": workspaceDelete,
	"plugin_package_file":    workspaceDelete,
	"project":                workspaceDelete,
	"project_resource":       workspaceDelete,
	// Version history for the four prompt tiers (RUYI-183) is audit data
	// owned by the workspace it was written in, not by the publishing flow
	// that marketplace_prompt_version serves — it goes with the workspace,
	// not Keep.
	"prompt_version": workspaceDelete,
	// Derived from prompt_version and agent_task_queue (RUYI-184); both are
	// workspace-owned, so the derivations are too.
	"prompt_quality_daily":        workspaceDelete,
	"prompt_perplexity_score":     workspaceDelete,
	"prompt_quality_rollup_state": workspaceDeleteKeep,
	// The quiz bank and its measurements (RUYI-185) are workspace-owned: the
	// questions are written in the workspace and the readings only mean anything
	// against its prompt versions, which are deleted here too.
	"prompt_quiz_item":   workspaceDelete,
	"prompt_quiz_result": workspaceDelete,
	// The sweep's single-row cursor has no workspace_id: it is the deployment's
	// scheduler state, not any workspace's data.
	"prompt_quiz_sweep_state": workspaceDeleteKeep,
	"quick_action":            workspaceDelete,
	"runtime_profile":         workspaceDelete,
	// Runtime instance credentials (RUYI-425 §4.5) carry only ciphertext and
	// are keyed by (runtime_instance_id, credential_key) with no workspace
	// column; DeleteWorkspaceRuntimesAndProjects sweeps them through the
	// workspace's runtime set in the same statement that deletes the
	// runtimes. Destroying the workspace destroys its secrets.
	"runtime_credential": workspaceDelete,
	// RUYI-288: runtime-local skill discovery summaries are workspace-scoped
	// metadata; the whole set goes away with the workspace.
	"runtime_skill_discovery":        workspaceDelete,
	"schema_migrations":              workspaceDeleteKeep,
	"seat_capacity_outbox":           workspaceDeleteSettle,
	"skill":                          workspaceDelete,
	"skill_file":                     workspaceDelete,
	"skill_version":                  workspaceDelete,
	"skill_to_label":                 workspaceDelete,
	"squad":                          workspaceDelete,
	"squad_member":                   workspaceDelete,
	"sys_cron_executions":            workspaceDeleteKeep,
	"task_message":                   workspaceDelete,
	"task_token":                     workspaceDelete,
	"task_usage":                     workspaceDelete,
	"task_usage_hourly":              workspaceDelete,
	"task_usage_hourly_dirty":        workspaceDelete,
	"task_usage_hourly_rollup_state": workspaceDeleteKeep,
	"user":                           workspaceDeleteKeep,
	"user_composio_connection":       workspaceDeleteKeep,
	"vcs_commit_status":              workspaceDelete,
	"vcs_connection":                 workspaceDelete,
	"vcs_pull_request":               workspaceDelete,
	"verification_code":              workspaceDeleteKeep,
	"webhook_delivery":               workspaceDelete,
	"workspace":                      workspaceDelete,
	"workspace_invitation":           workspaceDelete,
	"workspace_share_link":           workspaceDelete,
}

func TestWorkspaceDeletionManifestCoversPublicSchema(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}

	rows, err := testPool.Query(context.Background(), `
SELECT tablename
FROM pg_tables
WHERE schemaname = 'public'
ORDER BY tablename
`)
	if err != nil {
		t.Fatalf("list public tables: %v", err)
	}
	defer rows.Close()

	actual := make(map[string]struct{}, len(workspaceDeletionManifest))
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatalf("scan public table: %v", err)
		}
		actual[table] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate public tables: %v", err)
	}

	var unclassified, missing []string
	for table := range actual {
		if _, ok := workspaceDeletionManifest[table]; !ok {
			unclassified = append(unclassified, table)
		}
	}
	for table := range workspaceDeletionManifest {
		if _, ok := actual[table]; !ok {
			missing = append(missing, table)
		}
	}
	sort.Strings(unclassified)
	sort.Strings(missing)
	if len(unclassified) > 0 || len(missing) > 0 {
		t.Fatalf("workspace deletion manifest drift: unclassified=%v missing=%v", unclassified, missing)
	}

	workspaceColumns, err := testPool.Query(context.Background(), `
SELECT table_name
FROM information_schema.columns
WHERE table_schema = 'public'
  AND column_name = 'workspace_id'
`)
	if err != nil {
		t.Fatalf("list workspace_id columns: %v", err)
	}
	defer workspaceColumns.Close()

	withWorkspaceID := make(map[string]struct{})
	for workspaceColumns.Next() {
		var table string
		if err := workspaceColumns.Scan(&table); err != nil {
			t.Fatalf("scan workspace_id table: %v", err)
		}
		withWorkspaceID[table] = struct{}{}
	}
	if err := workspaceColumns.Err(); err != nil {
		t.Fatalf("iterate workspace_id tables: %v", err)
	}

	for table, action := range workspaceDeletionManifest {
		_, hasWorkspaceID := withWorkspaceID[table]
		switch action {
		case workspaceDeleteKeep:
			if hasWorkspaceID {
				t.Errorf("KEEP table %s gained workspace_id; classify its teardown behavior", table)
			}
		case workspaceDeleteDetach, workspaceDeleteSettle:
			if !hasWorkspaceID {
				t.Errorf("%s table %s lost workspace_id; update its teardown selector", action, table)
			}
		}
	}
}
