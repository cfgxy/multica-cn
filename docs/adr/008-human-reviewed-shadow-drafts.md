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
   `--ephemeral`, `--ignore-user-config`, and `--ignore-rules`, inside a
   bubblewrap mount namespace. The namespace exposes the Codex runtime and
   system certificates/resolver, a per-invocation provider-auth home, a
   read-only issue-snapshot directory, and only the draft output file. Host
   project files, user home contents, and audit files are not mounted into the
   Codex view.
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

The model provider connection is required for inference. The outer bubblewrap
namespace deliberately retains host networking so Codex can reach its
provider; it limits filesystem visibility but does not filter network traffic.
The Codex CLI's `read-only` sandbox is the separate boundary for
model-invoked tools, including their additional network access. A real Codex
run must verify that provider inference succeeds while tool execution cannot
read outside the snapshot context or make additional network requests.

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

`scripts/shadow-draft.test.sh` substitutes a fake Codex binary on the same
bubblewrap launch path. It verifies that an unmounted file outside the snapshot
cannot be read, the snapshot and provider-auth home are the only task data
visible, platform variables do not reach the child, and the read-only,
ephemeral, and config-isolation options are present. A successful invocation
produces a hash-linked ledger entry. The fixture also freezes time for repeated
invocations, checks that artifact paths and hashes remain independently
traceable, and covers empty output and child exit codes 42 and 124. A hanging
fake process verifies that the deadline records a 124 channel error and
terminates the process group. This is synthetic process and file-namespace
evidence; real Codex interruption, provider communication, and the CLI
read-only sandbox's network restriction remain QA requirements.
