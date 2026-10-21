# RUYI-845 Git Snapshot Compatibility Matrix

Baseline under test: `fdaa42a1122127791e6f61c58f9d9fb5154a749e` (`origin/main`, 2026-10-21 23:35:24 +0800). All fixtures below are disposable under `/tmp`; no project resource or RUYI-746 state was changed.

## Matrix

| Git shape | Same-path view | Alternate-path view | Evidence / status |
|---|---|---|---|
| Ordinary single worktree | Existing userns overlay probe and directory smoke pass; Git `status`/checkout/commit/fsck isolation not yet exercised | Not run | Partial; no Git isolation conclusion |
| Standard linked worktree | **Fail** when only linked worktree is overlaid: commit advances common repo branch ref outside the mounted root | Not run | Real production `__snapshot-worker` test; see output below |
| Nested Git repository | Not run in the expanded matrix | Not run | Earlier RUYI-669 same-path nested-Git result is historical evidence only; rerun required |
| Submodule | Not run in the expanded matrix | Not run | Must test parent scope and submodule-only scope separately because `.git` may resolve outside the submodule directory |
| Parent directory containing multiple Git roots | Not run | Not run | Must verify each root's metadata isolation and child-scope routing |

| Cross-cutting check | Result |
|---|---|
| Absolute linked-worktree `gitdir:` incidence | 20/20 fresh `git worktree add` samples on this host had an absolute pointer into the common repository |
| `status`, `checkout`, `commit`, `fsck` across all shapes | Incomplete; the linked-worktree `commit` case already demonstrates a write-through |
| Untracked/binary visibility and persistence | Existing directory smoke tests only a regular text write; Git matrix incomplete |
| Cancel/crash recovery, concurrent tasks, runtime brief injection, and GC | Existing unit tests cover parts of snapshot lifecycle; Git-topology-specific end-to-end checks not run |
| Pristine preservation | Violated for linked-worktree common ref in same-path single-root scope; other shapes not established |

## Reproduction

Commands were run from the issue worktree on Linux with unprivileged user namespaces enabled. The project binary was built from the stated baseline as `/tmp/multica-ruyi845-snapshot` and the test used only temporary Git repositories.

1. Build the actual worker entrypoint:

   ```sh
   (cd server && go build -o /tmp/multica-ruyi845-snapshot ./cmd/multica)
   ```

2. Verify the real namespace capability and existing directory smoke tests:

   ```sh
   (cd server && env -u MULTICA_AGENT_NAME -u MULTICA_AGENT_ID -u MULTICA_TASK_ID -u MULTICA_WORKSPACE_ID -u MULTICA_TASK_SLOT -u MULTICA_TASK_WORKSPACES_ROOT -u MULTICA_TASK_CONFIG_ROOT -u MULTICA_REPO_CHECKOUT_MODE -u MULTICA_TOKEN -u MULTICA_SERVER_URL -u MULTICA_DAEMON_PORT -u MULTICA_GIT_PUSH_GUARD -u MULTICA_GH_PR_GUARD MULTICA_SNAPSHOT_SMOKE_BIN=/tmp/multica-ruyi845-snapshot go test ./internal/daemon/execenv -run 'Snapshot.*Smoke' -count=1 -v)
   ```

   Result: `TestProbeSnapshotSupportSmoke` and `TestSnapshotWorkerSmoke` passed. The former reported `snapshot-probe: OK (userns overlay same-path mount verified)`.

3. Generate 20 linked worktrees with `git worktree add --detach`, then read each worktree's `.git` file. Result: `linked_worktrees=20 absolute_gitdir=20 relative_gitdir=0`; sample pointer: `/tmp/.../repo/.git/worktrees/wt-1`.

4. For the write-through reproduction, initialize a repository, commit a base commit, and run `git worktree add -b agent/test <tmp>/wt`. Prepare empty `alias`, `upper`, and `work` directories. Build worker spec JSON with the actual uid/gid and one unit whose `target=<tmp>/wt`, with those three paths and `cwd=<tmp>/wt`. Run:

   ```sh
   unshare -Urnm /tmp/multica-ruyi845-snapshot __snapshot-worker "$spec" -- /bin/sh -c 'printf data > tracked.txt; git add tracked.txt; git -c user.name=Agent -c user.email=agent@example.invalid commit -qm snapshot-commit; git status --short; git rev-parse HEAD'
   ```

   Observed: the worker succeeded; after namespace exit the pristine host worktree reported `D tracked.txt`, `refs/heads/agent/test` equaled the new snapshot commit, `upper/.git/objects` contained zero files, and the common repo object directory contained five files. The `.git` file pointed to `<tmp>/repo/.git/worktrees/wt`. This is a confirmed same-path write-through, not an inference from the earlier spike.

## Remaining Work Before ADR Acceptance

Run each untested topology under both same-path and alternate-path views, including status/checkout/commit/fsck and pristine-byte/ref/object comparisons. Record task cancellation/crash, concurrency, runtime brief injection, and ledger/GC behavior with Git present. Until then, this document is an evidence-bearing draft, not an accepted support matrix.
