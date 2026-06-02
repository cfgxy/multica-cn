# Squad Source Map

This file records source evidence for `multica-squads/SKILL.md`.

Use this when the task requires exact source paths, edge-case behavior, tests, or contract verification.

## Object Model

### DB shape

Source:

```text
server/migrations/084_squad.up.sql
server/migrations/088_squad_instructions.up.sql
server/pkg/db/queries/squad.sql
packages/core/types/squad.ts
```

Key facts:

- `squad` stores `name`, `description`, `leader_id`, `creator_id`, archive metadata, and `instructions`.
- `squad_member` stores `member_type`, `member_id`, and `role`.
- `member_type` is constrained to `agent` or `member`.
- issue `assignee_type` supports `squad`.

## CLI

Source:

```text
server/cmd/multica/cmd_squad.go
```

Commands:

```bash
multica squad list
multica squad get <squad-id>
multica squad create
multica squad update <squad-id>
multica squad delete <squad-id>
multica squad activity <issue-id> <outcome>

multica squad member list <squad-id>
multica squad member add <squad-id>
multica squad member remove <squad-id>
multica squad member set-role <squad-id>
```

Use `--help` for exact flags before writes.

## Create / Update

Source:

```text
server/internal/handler/squad.go
```

Contracts:

- create requires `leader_id`;
- leader must be workspace agent;
- archived leader is rejected;
- leader is auto-added as member with role `leader`;
- updating `leader_id` auto-adds new leader as member if missing.

## Leader Briefing

Source:

```text
server/internal/handler/squad_briefing.go
server/internal/handler/daemon.go
```

Contracts:

- squad leader tasks append briefing to leader agent instructions;
- briefing includes operating protocol, roster, and optional instructions;
- `instructions` section appears only when non-empty;
- archived agent members are skipped from roster;
- no traced behavior injects `instructions` into every squad member.

## Issue Assignment

Source:

```text
server/internal/handler/issue.go
server/internal/handler/squad.go
server/internal/service/task.go
```

Contracts:

- `assignee_type="squad"` routes to `squad.leader_id`;
- backlog assignment does not immediately enqueue;
- moving out of backlog can enqueue leader;
- assignee change cancels existing issue tasks first;
- private leader access is checked;
- pending task dedup is applied.

## Comment / Mention

Source:

```text
server/internal/handler/comment.go
server/internal/handler/squad.go
server/internal/service/task.go
```

Contracts:

- commenting on a squad-assigned issue can wake the leader;
- explicit `mention://squad/<id>` resolves squad and enqueues leader;
- squad mention does not fan out to members;
- leader task uses `is_leader_task=true`;
- leader self-trigger loops are guarded.

## Autopilot

Source:

```text
server/internal/service/autopilot.go
```

Contracts:

- squad autopilot resolves executable agent from `squad.leader_id`;
- readiness/admission checks target the leader;
- archived squad fails closed / skips dispatch;
- `create_issue` keeps the issue assigned to the squad;
- `run_only` creates task directly for leader.

## Child-done Parent Trigger

Source:

```text
server/internal/handler/issue_child_done.go
```

Contracts:

- when child issue completes and parent is assigned to squad, parent squad leader can be triggered;
- routing is leader-only;
- loop guards skip same squad, same effective leader, and shared-leader cross-squad cases.

## Private Leader Access

Source:

```text
server/internal/handler/agent_access.go
```

Contracts:

- public leaders pass;
- agent-to-agent traffic is allowed;
- private leader access for members is limited to owner/admin or agent owner;
- system triggers are treated like agent triggers for squad leader enqueue.

## Tests

Relevant test groups:

```text
server/internal/handler/squad_assign_trigger_test.go
server/internal/handler/squad_comment_trigger_test.go
server/internal/handler/squad_briefing_test.go
server/internal/handler/squad_private_leader_test.go
server/internal/handler/autopilot_private_leader_test.go
server/internal/handler/squad_no_action_test.go
```

Verification command:

```bash
go test ./internal/handler -run 'Test.*Squad|Test.*squad|Test.*Autopilot.*Squad|Test.*ChildDone.*Squad'
```
