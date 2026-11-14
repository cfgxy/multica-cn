# Issue delivery examples

## Link a delivery PR

```text
Fix login redirect                  # incorrect: no issue key to link
MUL-2759: fix login redirect        # links the issue
Closes MUL-2759                    # standalone body line records close intent
```

## Stage a serial child chain

Do not start dependent work before its inputs exist. Promote each later stage
only after its stated dependencies and the current stage barrier are satisfied.

```bash
multica issue create --title "Step 1" --parent <issue-id> --assignee <agent> --stage 1 --status todo
multica issue create --title "Step 2" --parent <issue-id> --assignee <agent> --stage 2 --status backlog
multica issue create --title "Step 3" --parent <issue-id> --assignee <agent> --stage 3 --status backlog
```

## Preserve a managed local environment for a dependency

```bash
multica worktree retain --env-root <managed-env-root> --owner <responsible-owner> --reason <remaining-work> --version <full-commit-sha-or-sha256> --evidence <evidence-and-rebuild-inputs> --until <RFC3339-time>
multica worktree retain --env-root <managed-env-root> --release
```

The active `MULTICA_TASK_ID` and `MULTICA_WORKSPACE_ID` must match the root's
owner. Record authorized unfinished work, responsible owner, immutable candidate,
evidence/rebuild inputs, and an expiry. GC honors active holds for task data,
artifacts, and local snapshot trees. Expiry or release restores normal GC
eligibility; release does not delete the environment immediately. Stop or release
it as soon as its dependency ends.

A local hold does not renew or release external VM, AVD, container, or service
leases. Use the owning resource system and record those handles separately.
