# RUYI-845 Git Snapshot Compatibility Matrix

Automated execution baseline: PR #473 implementation commit `2ee81c16685e3ce4bdc7c8d6500b4239aededdc0`. Every fixture is a disposable real Git repository under Go's test temporary directory; no project resource, daemon registration, Pool, GTI, or production database is involved.

## Supported Delivery Path

| Git shape | Gate / worker result | Pristine proof |
| --- | --- | --- |
| Ordinary single worktree | `MULTICA_ENABLE_GIT_SNAPSHOT=1` enters the real userns + overlay worker. It modifies tracked, untracked, and binary files, captures a bundle, imports it into an independent clone, and runs `git fsck`. | Host worktree remains clean; files and refs retain their original values. An interrupted worker still creates an importable bundle and a `failed` ledger entry with its upperdir retained. |

The worker smoke starts the built `multica __snapshot-worker` binary through the same namespace and serialized wrap contract used by the daemon. It is a real local worker execution, not a mock worker or GTI canary.

## Fail-Closed Topology Boundary

| Git shape | Gate-on outcome | Automated proof |
| --- | --- | --- |
| Standard linked worktree | Existing `worktree` backend | External common Git directory fails scope audit; no snapshot worker starts. |
| Nested Git repository | Existing `worktree` backend | Recursive audit sees both repositories, while narrow delivery eligibility rejects more than one Git root. |
| Submodule | Existing `worktree` backend | `.gitmodules` rejects snapshot delivery before any Git metadata repair or worker spawn. |
| Parent directory containing multiple Git roots | Existing `worktree` backend | Parent is not a single Git-root candidate; representative recursive audit sees both children and narrow delivery eligibility rejects it. |

`TestGitSnapshotUnsupportedTopologiesFallbackWithoutPristineWrites` creates each topology with real `git init`, `git worktree add`, and `git submodule add`. Each fixture contains a modified tracked file, untracked file, and binary payload. Before and after gate planning, audit, and fallback it compares:

- `git status --porcelain=v1 --untracked-files=all`;
- `git show-ref --head`;
- a SHA-256 tree digest for the worktree (including linked/submodule `.git` files);
- SHA-256 tree digests for each Git dir and common Git dir, including objects and refs.

Any drift fails the test. This makes the supported ordinary path and unsupported topologies materially distinct: unsupported shapes are not treated as snapshot-compatible and are not merely exercised through a mock.

## Execution Evidence

Run from repository root with injected agent variables removed and system Git first on `PATH`:

```sh
tmpdir=$(mktemp -d /tmp/ruyi845-snapshot.XXXXXX)
(cd server && go build -o "$tmpdir/multica" ./cmd/multica)
(cd server && MULTICA_SNAPSHOT_SMOKE_BIN="$tmpdir/multica" go test ./internal/daemon/execenv -run '^TestSnapshotWorker(CapturesGitArtifact|InterruptedAgentKeepsRecoverableArtifact)Smoke$' -count=1 -v)
(cd server && go test ./internal/daemon -run '^TestGitSnapshotUnsupportedTopologiesFallbackWithoutPristineWrites$' -count=1 -v)
rm -rf "$tmpdir"
```

The scoped runs passed locally on 2026-10-23. The full `./internal/daemon` package remains the release regression suite for this implementation.

## Residual Boundary

This matrix deliberately does not claim a GTI/Pool rollout, production daemon validation, or support for the rejected topologies. The minimal real-environment follow-up, if rollout is later requested, is one disposable ordinary Git repository on an isolated daemon with the deployment gate toggled on then off, verifying the same decision record and bundle import. It is not required to establish the current automated acceptance boundary.
