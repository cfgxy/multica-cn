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
| 920 | 920_channel_capability_state | RUYI-400 | agent/agent-f70e39b85abd/ruyi-400 | reserved |

Notes:

- 918 (RUYI-345): already applied to that issue's QA database ledger
  (`multica_ruyi345qa_dev`); RUYI-400 originally took 918 for
  `918_channel_capability_state` and yielded it (renumbered to 920,
  918/920 merged into one stem) before either branch merged.
- 903 conflict memo (pre-merge review input, NOT an occupation row): main
  disk has `903_execution_profile`, while the shared `multica` production
  ledger also contains `903_admin_audit_log_actor_index` — a pre-shrink
  legacy stem from before the RUYI-359 consolidation.
