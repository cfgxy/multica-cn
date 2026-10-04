# Worker Supervision — systemd Transient Units (RUYI-349)

## Problem

A daemon restart or SIGKILL killed every running task worker: workers were
direct children of the daemon process, so the kernel tore them down with it.
Users lost mid-run output, sessions, and minutes-to-hours of agent work on
every daemon upgrade or crash.

## Decision

Each task worker of a supervised provider runs inside a **systemd transient
user unit** (`systemd-run --user`, unit `multica-run-<run_id>.service`,
KillMode=control-group) with a **resident launcher** as its parent:

```
daemon ── systemd-run ──> multica-run-<run_id>.service
                             └─ launcher (this binary, __worker-launcher)
                                  └─ worker (claude …)
```

- The launcher owns the worker's stdio: stdout/stderr stream into per-run
  append-only log files; the daemon tails them with a persisted read offset
  plus a line-hash ring, so a daemon crash between delivering a line and
  saving the cursor re-delivers nothing (dedup on recovery).
- Stdin control frames travel over a per-run Unix socket
  (`control.sock`); the daemon reconnects after a restart and the launcher
  replays the exit event to a connection served before the control socket
  closes with the exit. A reconnect that misses that window learns the exit
  from `manifest.json`.
- A per-run `manifest.json` (atomic replace) is the sole source of run
  identity: run id, task id, unit, PIDs, exit evidence. Nothing lives only
  in daemon memory.
- Cancel uses `systemctl kill --kill-who=all --signal=SIGKILL` on the
  cgroup; the supervisor writes the exit record only after the unit is
  provably inactive. `cancelled` is never fabricated without evidence.

### Reconciliation matrix (daemon startup)

| manifest | unit active | server in-flight | decision | action |
| --- | --- | --- | --- | --- |
| nil | yes | — | Quarantine | record, never kill what we cannot classify |
| yes | yes | yes | Resume | leave running; next claim reenters it |
| yes | yes | no | StopOrphan | cgroup kill + exit evidence |
| yes | no | — | ConvergeExit | mark consumed (`ConvergedAt`) |
| yes | no | — + lock held | Quarantine | conflicting evidence, no fabrication |
| yes | no | — | Lost | worker provably dead, exit unrecorded |

### Reattach

Run ids derive deterministically from `taskID-attempt(-generation)`, so any
daemon derives the same id for the same task. A manifest without an exit
record means a previous daemon launched this worker and died first: the new
daemon's Execute reenters the worker — no prompt rewrite, stdout resumes
from the persisted offset. Generation suffixes (`-2`, `-3`) step past
exited manifests for server-side retries.

### Legacy fallback

`pkg/agent` routes **every** provider through `workerSession`. Legacy
semantics (direct `exec.Cmd` child, byte-identical to pre-RUYI-349) is the
default; the daemon injects `Supervision` only for whitelisted providers on
hosts where the systemd user bus answers. Guard tests pin direct
`StdoutPipe`/`startOwnedProcessTree`/`Wait` usage to the session
implementation (plus the listed probe exemptions).

## Phase 1 scope

Whitelist: **claude only**. The bidirectional ACP family (hermes, kimi,
kiro, mcode, zcode, qoder, dim, …) and codex app-server own RPC session
state machines that cannot re-enter a mid-turn without protocol-level
state recovery — Phase 2. Their code paths already route through the
session abstraction; only the runtime whitelist gates them.

Server-side absorption (RUYI-326): the daemon advertises the
`worker-supervisor-v1` capability over the existing `X-Client-Capabilities`
channel (persisted in `agent_runtime.metadata.capabilities`, read
fail-closed via `runtimeHasCapability`), and the sweeper converges
`cancel_requested` rows on offline runtimes after a **5-minute SLA**
instead of the 3h reconnect grace — the stop was accepted; only the ack
was missing. Task-result re-delivery after a daemon death stays with the
existing RUYI-225 recovery paths, which now consult supervised-worker
liveness before failing a task.

## Consequences

- Daemon restart: supervised workers keep running; output converges from
  logs; cancel during the restart window detaches instead of killing.
- New failure surface: a wedged launcher is bounded by systemd's unit
  lifecycle; the reconciler's Quarantine path exists precisely so unknown
  evidence is recorded, never destroyed.
- Run stores live under `~/.multica/supervisor-runs` (override:
  `MULTICA_SUPERVISOR_RUNS_DIR`); retention GC is a Phase 2 item.
