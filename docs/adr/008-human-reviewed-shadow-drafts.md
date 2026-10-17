# ADR 008: Human-Reviewed Shadow Drafts

- Date: 2026-10-17
- Status: accepted for the RUYI-746 two-week pilot

## Context

RUYI-746 requires `deepseek-v4-flash` to propose drafts for real dispatch work,
while a human Leader remains the only party allowed to comment, mention, change
task status, create pull requests, or deploy. The prior lab harness proved
draft formatting and accounting only. It had no remote, deployment, or real
dispatch integration and must not be presented as a production pilot.

Normal Multica agent tasks receive a task-scoped `MULTICA_TOKEN`. That token is
needed for ordinary agent work but is not an acceptable boundary for this
pilot: prompt instructions alone do not prevent a model from using a platform
write API.

## Decision

Use `scripts/shadow-draft.sh` as the first pilot entry point.

1. A human exports a minimal issue snapshot and explicitly invokes the script
   at an eligible real dispatch point.
2. The script starts Codex with `deepseek-v4-flash`, `--sandbox read-only`,
   `--ephemeral`, `--ignore-user-config`, and `--ignore-rules`. Codex runs with
   a per-invocation working directory containing only a read-only copy of the
   issue snapshot; no project directory or additional directory is granted.
3. The child gets a fresh `CODEX_HOME` containing only provider authentication.
   It does not inherit any `MULTICA_*` variable, project config, MCP server,
   skill, task state, or platform credential.
4. The runner accepts a deadline from 1 to 600 seconds, defaulting to 120.
   It starts Codex in a dedicated process group, sends TERM at the deadline,
   then sends KILL after two seconds. A deadline expiry is recorded as
   `channel_error` with exit code 124, and any draft is removed. Before it
   starts, the runner requires `setsid`, `bwrap`, the Codex CLI options used
   below, and a successful bubblewrap user/network namespace probe. Missing
   prerequisites reject the run before a model invocation.
5. The child writes no platform record. The parent writes a local append-only
   ledger with snapshot and draft SHA256 values, output paths, and a
   `draft_created` or `channel_error` outcome. Each invocation atomically
   reserves its own audit subdirectory; ledger paths are relative to the
   audit root. A zero process exit without a nonempty draft records
   `channel_error` and returns exit code 1.
6. The Leader reads the draft and performs any subsequent platform action
   manually. A draft is not an adoption record until that manual review is
   recorded separately.

The model provider connection is required for inference. The Codex CLI's
read-only sandbox is the boundary for model-invoked tools; this runner does not
claim that provider traffic is blocked. A real Codex run must verify that tool
execution cannot read outside the snapshot context or make additional network
requests. The shell fixture verifies the enforced CLI arguments and temporary
working directory, but it does not substitute for that QA evidence.

## Consequences

The mechanism deliberately has no automatic event subscription, no automatic
issue scan, and no daily scheduled write. This keeps its authority narrower
than a normal agent task during the two-week pilot. A failed or timed-out model
call is a `channel_error`, not a zero-candidate result and not a pilot sample.

The script still needs a real deployment of the reviewed commit to the
hp-server Multica checkout before it can be used as a production-pilot
artifact. Deployment remains a human release action; this ADR grants neither
deployment authority nor model platform write access.

## Verification

`scripts/shadow-draft.test.sh` substitutes a fake Codex binary and verifies
that `MULTICA_TOKEN` and `MULTICA_SERVER_URL` do not reach the child, the
read-only/ephemeral/config-isolation flags and snapshot-only working directory
are present, and a successful invocation produces a hash-linked ledger entry.
It also freezes time for repeated invocations, checks all artifact paths and
hashes remain independently traceable, and covers empty output and child
exit codes 42 and 124. A hanging fake process with a child verifies that the
deadline records a 124 channel error and terminates the process group. This is
synthetic process evidence; real Codex interruption, provider communication,
and file/network isolation remain QA requirements. The fake executable locates
its fixtures without allowing test environment variables through the
production isolation boundary.
