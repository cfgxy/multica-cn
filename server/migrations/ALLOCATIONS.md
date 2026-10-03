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
| 918 | 918_channel_capability_state | RUYI-400 | agent/agent-f70e39b85abd/ruyi-400 | reserved |
| 920 | 920_channel_capability_state_unique | RUYI-400 | agent/agent-f70e39b85abd/ruyi-400 | reserved |
