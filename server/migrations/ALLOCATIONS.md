# 9xx Migration Allocation Registry

Cross-issue coordination ledger for the `server/migrations` 9xx number range
(this fork diverges from upstream; everything below 9xx can be taken by
upstream at any time — never use it).

Rules (see workspace/project instructions, RUYI-359):

- New migrations take the smallest free number across three sources: `main`
  disk, every environment's `schema_migrations` ledger, and this registry.
- `reserved` = allocated, not yet merged. `applied` = merged to main.
  `freed` = released, reusable.
- Register your number in the SAME PR that adds the migration files, so two
  branches grabbing the same number collide in git instead of in a database.
- One atomic schema deliverable per issue takes exactly ONE number (merge
  statements into one stem where safe).

| Number | Stem | Issue | Branch / PR | Status |
| ------ | ---- | ----- | ----------- | ------ |
| 918 | 918_issue_decisions | RUYI-345 | agent/agent-f70e39b85abd/beec1e804b72 | reserved |
| 920 | 920_project_revision | RUYI-354 | agent/agent-f70e39b85abd/ruyi-354 (Owner pinned 2026-10-03 08:59) | applied |
| 921 | 921_prompt_version_snapshot_scope | RUYI-285 | merged to main via PR #202 (rebuilt from superseded PR #183) | applied |
| 922 | 922_channel_capability_state | RUYI-400 | agent/agent-f70e39b85abd/ruyi-400 | reserved |
| 923 | 923_prompt_proposal_jev_advisory | RUYI-347 | agent/agent-f70e39b85abd/ruyi-347-1ff5e2ca3358 (PR #179) | reserved |
| 924 | 924_agent_resource_weight | RUYI-397 | agent/agent-f70e39b85abd/ruyi-397 | reserved |
| 925 | 925_oauth_management | RUYI-420 | agent/agent-f70e39b85abd/ruyi-420 | reserved |
| 926 | 926_activity_audit | RUYI-355 | agent/agent-f70e39b85abd/ruyi-355 | reserved |
| 929 | 929_quick_replies | RUYI-435 | agent/agent-f70e39b85abd/ruyi-435 | reserved |

Notes:

- 918 (RUYI-345): already applied to that issue's QA database ledger
  (`multica_ruyi345qa_dev`); RUYI-400 originally took 918 for
  `918_channel_capability_state` and yielded it (renumbered to 922,
  918/922 merged into one stem) before either branch merged.
- 903 conflict memo (pre-merge review input, NOT an occupation row): main
  disk has `903_execution_profile`, while the shared `multica` production
  ledger also contains `903_admin_audit_log_actor_index` — a pre-shrink
  legacy stem from before the RUYI-359 consolidation.
- 923 note (RUYI-347): pre-shrink legacy `923_runtime_profile_add_deerflow_zcode`
  rows persist in several historical experiment/QA database ledgers; leftovers
  from the RUYI-359 shrink (that stem lives at 904 on the consolidated tree),
  NOT an occupation — same treatment as the 920-923 leftovers note above.
- 922 renumber note (RUYI-355): RUYI-400 merged `922_channel_capability_state`
  first (commit `148cd0479`, 2026-10-03), so RUYI-355's audit stem (then
  registered at 922) yielded the number and renumbered to `925_activity_audit`
  before its PR (the branch-side 922 registration never reached main, so
  RUYI-400's three-source check could not see it). The shared `multica`
  production ledger still carries that stem's row at 922 from pre-merge
  verification on this issue; its disposition is pending Owner decision
  (RUYI-355 decision 3) and is NOT an active claim. RUYI-346's head branch (PR #178)
  still carries pre-shrink legacy stems in the 920-923 range; those are
  leftovers, NOT active-branch claims, and resolve on that branch's
  required rebase.
- 926 renumber note (RUYI-355): the audit stem registered at 925 collided
  with RUYI-420's `925_oauth_management` on main disk (2026-10-05 merge
  preflight); RUYI-355 yielded and renumbered to `926_activity_audit` in
  its conflict-resolution merge — same yield pattern as the 922→925
  renumber above. Environments whose `schema_migrations` ledger carries
  `925_activity_audit` must have that row rewritten to `926_activity_audit`
  per the renumber rule (version rewritten, `applied_at` preserved).
- 929 renumber note (RUYI-435): the quick-replies stem originally registered
  at 926 collided with RUYI-355's `926_activity_audit`, which merged to main
  first (PR #214, 2026-10-05); RUYI-435 yielded and renumbered to
  `929_quick_replies` at rebase (927/928 taken by open PRs #228/#231 at the
  time). The stem never reached any shared `schema_migrations` ledger before
  the renumber, so no ledger rewrite is needed anywhere.
