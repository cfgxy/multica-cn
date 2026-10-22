# ADR-010 Draft: Git Tasks on Local Snapshot Execution

- Status: Accepted for the narrow ordinary-repository rollout
- Issue: RUYI-845
- Baseline: `fdaa42a1122127791e6f61c58f9d9fb5154a749e`

## Context

The existing local snapshot worker mounts each declared resource root over itself in a private user/mount namespace. This isolates writes that resolve through that mount, but Git may resolve metadata outside the declared root. In particular, a linked worktree's `.git` file points to the common repository's worktree metadata. Git object writes, branch refs, reflogs, config, hooks, and worktree administration files therefore need an explicit isolation and delivery model; overlaying only the visible worktree directory is not sufficient.

The goal remains to keep Git snapshot execution deployment-gated and off by default. `execution_mode=snapshot` continues to be capability-gated at resource save and task claim; a Git resource that does not meet the narrow delivery contract runs through the established `worktree` path. RUYI-746 shadow scheduling is out of scope and must remain untouched.

## Evidence and Current Conclusion

See [the reproducible matrix and observed results](010-git-snapshot-matrix.md). The real same-path worker passed its existing userns/overlay probe and simple directory smoke test. In a fresh real-worker test, however, a standard linked worktree's `git commit` advanced a branch ref in its common repository outside the snapshotted worktree. The host worktree then showed the committed file as deleted. On this host, 20/20 fresh `git worktree add` samples emitted an absolute `gitdir:` pointer.

This proves same-path overlay of only the linked worktree is unsafe. It does not prove every Git topology is unsafe, nor establish that alternate-path mounts are categorically unsafe. The full shape/path matrix remains incomplete.

Owner selected the minimal path: recursively audit Git metadata at task initialization; use snapshot only when it can be isolated and delivered safely; otherwise make no Git metadata change and use the existing worktree backend. The implemented first slice is one ordinary repository at the resource root with one snapshot unit and a real `.git` directory. A real userns/overlay worker captures tracked, untracked, and binary changes into a ledger-owned Git bundle that an independent clone can fetch, check out, and fsck. Failed, cancelled, and delivered bundles are retained and reaped through the ledger.

## Decision

Support only the ordinary single-repository shape for this rollout. Snapshot initialization recursively audits Git metadata, but delivery eligibility is narrower: exactly one unit at the resource root, a real `.git` directory, no nested repository, no submodule, and no external common Git directory. The deployment gate is `MULTICA_ENABLE_GIT_SNAPSHOT=1`; it is off by default. The daemon startup userns/overlay probe must have advertised `local-snapshot-v1`, which is enforced both when saving a snapshot resource and when claiming its task.

The daemon emits one structured `local_directory: snapshot execution decision` record per snapshot task. It carries the actual `execution_mode`, the gate and capability verdicts, and a stable `downgrade_code`. The current codes are `git_snapshot_gate_disabled`, `git_metadata_audit_failed`, and `git_snapshot_unsupported_topology`; these intentionally do not carry local paths. This is the supported operator surface for distinguishing the requested resource mode from the backend that actually ran.

## Provisional Compatibility Boundary

- Supported today: non-Git snapshots, plus one ordinary Git repository at the resource root after the gate is enabled. Successful Git runs expose a ledger-owned bundle; failed and cancelled runs retain recovery artifacts until ledger GC reaps them.
- Worktree fallback: gate disabled; linked worktrees or any Git metadata outside scope; nested repositories; submodules; multiple units; subdirectory scopes; incomplete metadata resolution; and any other shape outside the ordinary-repository eligibility rule.
- Fail closed: if the daemon lacks the snapshot capability, task claim is cancelled rather than running in place. If a claimed snapshot task fails its Git audit or delivery eligibility, it uses the existing worktree backend. Never silently run `in_place` and never let an agent write through to pristine Git metadata.
- Defaults: Git snapshot gate off; no resource configuration migration; do not touch RUYI-746.

## Follow-up Work

1. Validate recovery/continuation behavior and concurrent ordinary-repository tasks under the enabled gate.
2. Add an explicit user-facing review/import flow for retained Git bundles before treating a bundle as a replacement for branch delivery.
3. Consider each unsupported topology independently; no broadening occurs without its own delivery, recovery, and pristine-preservation evidence.
4. Complete the rollout evidence: gate-off fallback, independent QA, and an authorized GTI canary.

## Reversal

The deployment gate stays off by default. Turning it off must immediately route new tasks through the existing worktree backend. Do not delete or overwrite pristine directories, existing worktrees, refs, or failed snapshot artifacts as part of fallback. Any captured Git artifacts remain ledger-owned until explicitly delivered or safely reaped under the selected retention policy.
