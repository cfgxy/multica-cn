# RUYI-638 Runtime and Skill Fixtures

These scripts extend only the disposable `ruyi638-proposal-fixture` workspace seeded by the RUYI-638 follow-up fixture. They are intended for `multica_dev1` and must not be used against production, the shared `multica` database, another slot, or another workspace.

Run `runtime-skill-seed.sql` after the proposal fixture exists. It creates one offline runtime, one inert offline agent attached to it, and two Skills with initial version rows. No daemon, runtime credentials, execution, or external integration is configured.

After QA, run `runtime-skill-cleanup.sql` before the follow-up workspace cleanup. It removes only the fixed runtime/agent/Skill IDs in that disposable workspace. It refuses cleanup if the runtime has acquired a non-fixture agent reference or the fixture agent has execution/usage records. Skill versions created during lifecycle checks are removed with the two fixture Skills. Existing accounts, workspaces, proposals, audit events, and unrelated Issue resources are outside this cleanup.

The seed and cleanup scripts are prepared only; this delivery does not execute them or perform QA. Cleanup does not remove files, runtime profiles, daemon processes, or data created by an unapproved execution path.
