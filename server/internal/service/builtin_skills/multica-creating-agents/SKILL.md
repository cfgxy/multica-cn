---
name: multica-creating-agents
description: Use when a user asks to create, configure, or validate a Multica agent. Teaches the decision flow for building useful agents: define the job, write runtime instructions, choose runtime/model/reasoning settings, handle secrets safely, bind skills additively, and verify behavior with a low-risk task.
user-invocable: false
allowed-tools: Bash(multica *)
---

# Creating useful Multica agents

This is not a parameter manual. Use it to decide what an agent should be, how to
encode that into Multica's current agent creation path, and how to prove the
agent is actually usable after creation.

## Decision flow

1. Define the job first.
   - What result should this agent produce?
   - What inputs will it receive: issues, chat, comments, repos, attachments, or
     external tools?
   - What should it refuse, escalate, or leave to a human?
2. Pick the runtime that can execute that job.
   - Use a runtime the workspace can actually claim. `runtime_id` is required.
   - Use `model` and `thinking_level` only when the runtime/provider supports
     those choices. Empty values mean runtime defaults.
3. Write the behavior contract.
   - `description` is a catalog summary. Keep it short and human-readable; the
     API caps it at 255 Unicode code points.
   - `instructions` is the runtime behavior contract. Put durable persona,
     responsibilities, boundaries, output rules, and escalation rules here.
4. Add only the operational surface the job needs.
   - Use skills for reusable workflows and tool contracts.
   - Use env only for values the process must read at execution time.
   - Use custom CLI args only for runtime-specific switches that are not already
     first-class Multica fields.
5. Verify with a low-risk task before calling the agent ready.

## Create the agent

Minimum CLI shape:

```bash
multica agent create --name <name> --runtime-id <runtime-id> \
  --description "<short catalog summary>" \
  --instructions "<runtime behavior contract>" \
  --output json
```

If the agent needs a specific model, prefer the first-class field:

```bash
multica agent create --name <name> --runtime-id <runtime-id> \
  --model <model-id> \
  --instructions "<runtime behavior contract>" \
  --output json
```

Prefer `--model` over model flags in `--custom-args`. `custom_args` is a JSON
array for runtime CLI switches; model selection is already a persisted agent
field and some providers reject model flags inside custom args.

`runtime_config` is JSON stored on the agent. Use it for runtime-specific config
only when the runtime contract actually reads it. Do not use it as a general
notes field.

The HTTP create body uses the same concepts:

```json
{
  "name": "<name>",
  "description": "<short catalog summary>",
  "instructions": "<runtime behavior contract>",
  "runtime_id": "<runtime-id>",
  "runtime_config": {},
  "model": "<model-id>",
  "thinking_level": "<provider-level reasoning token>",
  "custom_args": []
}
```

## Secrets and env

`custom_env` is secret material. Do not put secrets directly on a shell command
line unless the user explicitly accepts shell history and `ps` exposure.

Prefer one of the secret-safe input modes:

```bash
multica agent create --name <name> --runtime-id <runtime-id> \
  --custom-env-stdin \
  --instructions "<runtime behavior contract>" \
  --output json
```

```bash
multica agent create --name <name> --runtime-id <runtime-id> \
  --custom-env-file <path-to-0600-json> \
  --instructions "<runtime behavior contract>" \
  --output json
```

After creation, env changes go through the dedicated env command, not generic
agent update:

```bash
multica agent env set <agent-id> --custom-env-stdin --output json
multica agent env get <agent-id> --output json
```

Agent list/get/create/update responses expose only metadata such as
`has_custom_env` and `custom_env_key_count`, not plaintext values. Reading values
uses the dedicated endpoint and is owner/admin-only; agent actors are denied.

## Bind skills additively

Creating an agent does not automatically bind workspace skills. Bind known skill
ids after the agent exists:

```bash
multica agent skills add <agent-id> --skill-ids <skill-id> --output json
multica agent skills list <agent-id> --output json
```

Use `add` for normal work because it preserves existing assignments. Use `set`
only when the user explicitly wants to replace the entire skill list.

At task claim time, the daemon receives workspace-bound skills first and then the
platform built-ins. The daemon materializes skill content/files for the provider;
that is why binding by skill id matters more than copying instructions into the
agent prompt.

## Validate before declaring success

1. Inspect the persisted agent:

```bash
multica agent get <agent-id> --output json
```

Check at least: `description`, `instructions`, `runtime_id`, `model`,
`thinking_level`, `custom_args`, `has_custom_env`, `custom_env_key_count`, and
`skills`.

2. If skills were bound, verify the final assignment list:

```bash
multica agent skills list <agent-id> --output json
```

3. Run a low-risk task. Use a small issue or chat prompt that exercises the
agent's real job without touching production data. The test should prove:

- it follows `instructions`, not just its `description`;
- it can access the expected workspace context/repo if needed;
- it loads the bound skill when the task calls for it;
- it does not need missing secrets or unsupported runtime switches;
- it reports blockers instead of inventing results.

## Do / don't / consequences

Do:

- write the job and boundaries before choosing knobs;
- keep `description` short and searchable;
- put behavior, output format, escalation, and refusal rules in `instructions`;
- choose `model` and `thinking_level` from the runtime/provider's supported set;
- pass secrets through `--custom-env-stdin` or `--custom-env-file`;
- bind skills with additive `multica agent skills add`;
- verify with `multica agent get <agent-id> --output json` and a low-risk task.

Don't:

- treat description as prompt instructions;
- stuff reusable workflows into one giant instruction block when a skill should
  be bound;
- use `custom_args` for first-class fields such as model selection;
- pass real secrets with `--custom-env` on the command line;
- use `agent skills set` unless replacing every assignment is intended;
- declare success from creation output alone.

Consequences:

- weak instructions create a named shell with no reliable operating contract;
- wrong runtime/model/reasoning choices produce claim-time or execution-time
  failures;
- unsafe env handling leaks secrets through shell history or process lists;
- destructive skill replacement removes capabilities the agent already needed;
- no low-risk validation means the first real user task becomes the test.

## Source of truth

- `server/cmd/multica/cmd_agent.go:158` defines create flags including
  `description`, `instructions`, `runtime-id`, `runtime-config`, `model`,
  `custom-args`, and secret-safe custom env input modes.
- `server/cmd/multica/cmd_agent.go:409` builds the manual create request body and
  posts it to `/api/agents`.
- `server/internal/handler/agent.go:565` defines the create request fields:
  `description`, `instructions`, `runtime_config`, `custom_env`, `custom_args`,
  `model`, and `thinking_level`.
- `server/internal/handler/agent.go:669` validates `thinking_level` against the
  runtime provider; unknown literal values return 400.
- `server/internal/handler/agent.go:688` persists default `{}` env/config and
  `[]` args when omitted.
- `server/internal/handler/agent.go:33` returns env metadata but not plaintext
  `custom_env` values on agent resources.
- `server/internal/handler/daemon.go:1109` builds claim responses with fresh
  agent instructions, skills, `custom_env`, `custom_args`, `model`, and
  `thinking_level`.
- `server/internal/service/task.go:1684` loads workspace-bound agent skills and
  their files for execution.
- `server/internal/service/builtin_skills.go:10` embeds platform built-in skills;
  `server/internal/service/builtin_skills.go:45` loads each `SKILL.md` plus
  supporting files.
- `server/pkg/db/generated/agent.sql.go` shows the persisted agent columns for
  `runtime_config`, `instructions`, `custom_env`, `custom_args`, `model`, and
  `thinking_level`.
