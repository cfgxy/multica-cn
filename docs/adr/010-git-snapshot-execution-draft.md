# ADR-010 Draft: Git Tasks on Local Snapshot Execution

- Status: Draft; blocked on Owner decision
- Issue: RUYI-845
- Baseline: `fdaa42a1122127791e6f61c58f9d9fb5154a749e`

## Context

The existing local snapshot worker mounts each declared resource root over itself in a private user/mount namespace. This isolates writes that resolve through that mount, but Git may resolve metadata outside the declared root. In particular, a linked worktree's `.git` file points to the common repository's worktree metadata. Git object writes, branch refs, reflogs, config, hooks, and worktree administration files therefore need an explicit isolation and delivery model; overlaying only the visible worktree directory is not sufficient.

The goal remains to keep user resources configured as `execution_mode=worktree`, with snapshot execution deployment-gated and off by default. RUYI-746 shadow scheduling is out of scope and must remain untouched.

## Evidence and Current Conclusion

See [the reproducible matrix and observed results](010-git-snapshot-matrix.md). The real same-path worker passed its existing userns/overlay probe and simple directory smoke test. In a fresh real-worker test, however, a standard linked worktree's `git commit` advanced a branch ref in its common repository outside the snapshotted worktree. The host worktree then showed the committed file as deleted. On this host, 20/20 fresh `git worktree add` samples emitted an absolute `gitdir:` pointer.

This proves same-path overlay of only the linked worktree is unsafe. It does not prove every Git topology is unsafe, nor establish that alternate-path mounts are categorically unsafe. The full shape/path matrix remains incomplete. Until the Owner selects an isolation/delivery contract and the remaining cells are exercised, no Git snapshot shape is approved for rollout. Existing worktree remains the supported fallback.

## Decision Required

Choose the Git metadata boundary and delivery model before Stage B:

1. Restrict support to repositories whose complete Git metadata is inside the declared snapshot root; reject external common Git directories and unsupported layouts before task claim. Nested repositories, submodules, and parent-directory multi-repository resources are supported only when the snapshot scope captures every Git directory they resolve to.
2. Extend same-path snapshot scopes to cover every resolved common Git directory and linked-worktree administration path, then implement branch/ref delivery and replay against that captured set.
3. Use an alternate-path view only with a complete, tested gate or rewrite strategy for Git metadata paths, and define how that rewritten view maps to an independently reviewable branch/commit.

These options change supported resource layouts and Git artifact ownership. Do not implement one implicitly.

## Provisional Compatibility Boundary

- Supported today: existing `worktree` execution. Non-Git snapshot behavior remains as already implemented.
- Not approved for Git snapshot: linked worktrees whose common Git directory is outside the mounted set; unrecognized `.git` indirection; any resource shape for which all Git metadata roots cannot be proven and isolated.
- Fail closed: if the deployment gate, daemon capability, path-resolution proof, or delivery capture is unavailable, use existing worktree execution or reject before claim. Never silently run `in_place` and never let an agent write through to pristine Git metadata.
- Defaults: snapshot gate off; no resource configuration migration; do not touch RUYI-746.

## Stage B Minimum Split (conditional on decision)

1. Detection and fail-closed claim gate: resolve `.git` files/directories, common dirs, nested repos, submodules, and declared roots; publish the exact supported-shape contract and reason for rejection.
2. Isolation primitive: implement the selected mount topology and mount-order/path alias rules; prove pristine bytes, refs, objects, config, and worktree administration state remain unchanged until delivery.
3. Git delivery lifecycle: anchor the starting commit and refs; capture tracked, untracked, binary, and agent-committed changes; emit an independently reviewable branch/commit or explicitly selected equivalent; preserve failed/cancelled artifacts; replay user changes with conflicts surfaced; make GC ledger-driven.
4. Execution and observability: deployment-only default-off gate, startup capability probe, task/run mode and downgrade reason; preserve existing `worktree` behavior when disabled.
5. Entrypoints and topology: add only the supported Desktop/Web/CLI/MCP resource/scope controls; test parent multi-repository overlap routing and primary selection; reject undeclared/ambiguous write sets.
6. Rollout: isolated integration test, GTI canary, gate-off fallback test, independent QA, then a separately authorized rollout decision.

No Stage B work should start until the decision card is resolved and the remaining Stage A matrix closes the chosen path's evidence gaps.

## Reversal

The deployment gate stays off by default. Turning it off must immediately route new tasks through the existing worktree backend. Do not delete or overwrite pristine directories, existing worktrees, refs, or failed snapshot artifacts as part of fallback. Any captured Git artifacts remain ledger-owned until explicitly delivered or safely reaped under the selected retention policy.
