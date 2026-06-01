# Agent skill necessity records

This document records why each built-in Multica skill exists and how to test its value. It is not a marketing catalog. Each section must show the failure mode that appears without the skill and the behavior that appears with the skill.

Use this document when you add a built-in skill, review whether a skill belongs in the runtime brief, or design an evaluation that compares agent behavior with and without a specific skill.

## How to write a skill necessity record

Each skill record must answer the same questions. Keep the examples concrete; a vague benefit is not enough to justify a built-in skill.

### Purpose

Describe the missing platform knowledge that the skill gives the agent. This is not the task name. It is the reason a general coding agent would otherwise act like it understands Multica while missing a product contract.

### Platform contract

Name the real Multica rule that the skill teaches. Link the rule to source code, API behavior, CLI behavior, database shape, or observed product behavior.

### Without this skill

Show the wrong output or wrong action the agent is likely to produce. Prefer a short prompt and a concrete bad response, command, PR title, status change, or comment body.

### Failure mode

Explain what breaks. Be specific about whether the failure is silent, visible, recoverable, or likely to poison later context.

### With this skill

Show the corrected behavior. Include the commands the agent must run, the text it must write, and the checks it must perform.

### Test scenario

Define an evaluation case. A useful test scenario includes:

- a prompt that can be run with and without the skill;
- the expected failure without the skill;
- the expected successful behavior with the skill;
- the observable pass criteria.

### Why this belongs in a skill

Explain why the knowledge belongs in an on-demand skill instead of the always-on runtime brief. Hard contracts that must be known before any skill can load stay in the brief. Longer workflows and product-specific methods belong in skills.

## `multica-mentioning`

`multica-mentioning` exists because Multica mentions are not plain Markdown. They are side-effecting links that notify members, enqueue agents, enqueue squad leaders, or create safe issue references.

### Purpose

The skill teaches the agent to build a mention link that actually fires. A general coding agent may treat `@Alice` or `mention://member/Alice` as a normal human-readable mention. Multica requires a real UUID in the link target.

### Platform contract

Multica mention links use this shape:

```md
[@Name](mention://<type>/<id>)
```

The `<id>` must be a real UUID for `member`, `agent`, `squad`, and `issue` mentions. The only exception is `mention://all/all`.

The source of truth is:

- `server/internal/util/mention.go:16`, which parses only UUID-shaped mention IDs or the literal `all`;
- `server/internal/handler/comment.go:884`, which enqueues mentioned agents and squad leaders;
- `server/internal/handler/comment.go:768`, which handles `@all` broadcast behavior.

### Without this skill

A prompt like this is enough to trigger the failure:

```text
Fix the bug, then @ Alice for review and ask GPT-Boy to continue the follow-up.
```

Without the skill, the agent may write:

```md
[@Alice](mention://member/Alice) please review.
[@GPT-Boy](mention://agent/GPT-Boy) please handle the follow-up.
```

It may also write a bare mention:

```md
@Alice please review.
```

Both outputs look plausible to a person, but they do not satisfy the Multica mention protocol.

### Failure mode

The failure is dangerous because it can be silent:

- the member may not receive a notification;
- the agent may not be enqueued;
- the squad leader may not be enqueued;
- the comment may show text that looks like a successful handoff;
- the original agent may stop because it believes it delegated the work.

This breaks the collaboration chain without creating an obvious error for the user.

### With this skill

With the skill, the agent must look up the correct entity before it writes the comment:

```bash
multica workspace member list --output json
multica agent list --output json
multica squad list --output json
```

It must use the right ID source for each mention type:

- `member` uses `member.user_id`;
- `agent` uses `agent.id`;
- `squad` uses `squad.id`;
- `issue` uses `issue.id`;
- `all` uses the literal `all`.

The corrected comment looks like this:

```md
[@Alice](mention://member/7f3a1b2c-0000-4000-8000-000000000abc) please review.
[@GPT-Boy](mention://agent/6d1f2a3b-0000-4000-8000-000000000def) please handle the follow-up.
```

If a name is ambiguous or missing, the agent must say it cannot resolve the mention instead of guessing.

### Test scenario

Use this prompt for an A/B evaluation:

```text
After finishing the work, notify Alice for review and ask GPT-Boy to handle the follow-up. The notification and handoff must actually trigger in Multica.
```

The run without the skill fails if the agent:

- writes a bare `@Alice` mention;
- uses a display name where a UUID belongs;
- uses a `member` ID where an `agent` ID belongs, or the reverse;
- writes the handoff without first looking up IDs.

The run with the skill passes if the agent:

- calls the relevant `multica ... list --output json` command;
- uses `user_id` for a member mention;
- uses `id` for an agent or squad mention;
- avoids guessing when the entity cannot be resolved.

### Why this belongs in a skill

The always-on brief must state that mentions are side effects and that agents must avoid loops. The long how-to for resolving IDs and constructing the links belongs in a skill because it is only needed when the agent writes a mention.

## `multica-working-on-issues`

`multica-working-on-issues` exists because a Multica issue is both the work item and the coordination surface. Finishing the code is not enough; the agent must close the loop through comments, PR linking, metadata, status, and sub-issue semantics.

### Purpose

The skill teaches the agent to move a triggered issue from context intake to a verifiable handoff. It covers how to read the triggering context, decide whether to reply, ship changes, link PRs, write high-signal metadata, update status only when appropriate, and create sub-issues without accidentally starting the wrong agents.

### Platform contract

The relevant platform contracts are spread across CLI commands and backend behavior:

- `multica issue get`, `multica issue comment list`, and `multica issue metadata list` provide the working context.
- `multica issue comment add` is the visible delivery surface for issue work.
- `server/internal/handler/github.go:490` links PRs to issues when the PR title, body, or branch contains an issue identifier such as `MUL-2759`.
- `server/internal/handler/github.go:501` only treats close keywords such as `Closes MUL-2759` as close intent when the keyword is adjacent to the issue identifier.
- `server/internal/handler/issue.go:2446` treats `backlog` as a parking lot and enqueues work when an assigned issue moves out of `backlog`.
- `server/internal/handler/issue.go:2474` sends parent notifications when a child issue moves to `done`.

### Without this skill

A prompt like this exercises the failure:

```text
Handle this issue. Fix the code, open a PR, report back, and create the next two steps as serial sub-issues.
```

Without the skill, the agent may produce plausible but broken behavior:

- it reads only the issue title and misses the triggering comment thread;
- it opens a PR titled `Fix login redirect` with no `MUL-2759` identifier;
- it finishes code but never posts an issue comment, so the user cannot see the result in Multica;
- it sets the issue to `done` from a comment-triggered follow-up even though the work only needs a reply;
- it writes temporary notes such as files touched or run timestamps into issue metadata;
- it creates serial sub-issues with `--status todo`, which starts all assigned agents immediately;
- it claims an issue is linked to a PR without verifying the link or recording the PR URL.

### Failure mode

These failures break the work loop rather than a single command:

- the user cannot tell what the agent did because no result comment exists;
- the PR is not visible from the issue because it lacks the issue identifier;
- the issue status stops representing reality;
- future agents read noisy metadata and make worse decisions;
- serial work runs concurrently because later sub-issues were not parked in `backlog`;
- follow-up agents or humans have to reconstruct the state manually.

### With this skill

With the skill, the agent must run the issue as a closed loop:

1. Read the issue and pinned metadata with `multica issue get` and `multica issue metadata list`.
2. Read the triggering conversation with `multica issue comment list --thread` and use `--recent` only when cross-thread context is needed.
3. Decide whether real work is needed. If the trigger is only an acknowledgement and the agent produces no work, stay silent.
4. Do the requested work and verify it with real commands.
5. When opening a PR, include the issue identifier in the title, body, or branch, for example `MUL-2759`.
6. Use adjacent close syntax such as `Closes MUL-2759` only when merge should move the issue to `done`.
7. Report the result with `multica issue comment add` after doing the work.
8. Write metadata only for facts that future agents will read repeatedly, such as `pr_url`, `deploy_url`, `waiting_on`, `blocked_reason`, or `decision`.
9. For sub-issues, use `todo` for parallel work and `backlog` for later serial steps.
10. Do not claim that the issue has a linked PR unless the agent created that PR, read a recorded `pr_url`, or can query the link through the CLI when that command exists.

### Test scenario

Use this prompt for an A/B evaluation:

```text
Handle MUL-2759. Make a small code change, open a PR, report back on the issue, and create two serial follow-up sub-issues.
```

The run without the skill fails if the agent:

- skips the triggering thread;
- opens a PR without `MUL-2759` in the title, body, or branch;
- omits the final issue comment;
- writes run logs or temporary notes into metadata;
- creates both serial sub-issues as `todo`;
- changes issue status without a clear status contract.

The run with the skill passes if the agent:

- reads the issue, metadata, and trigger thread;
- reports real execution output through `multica issue comment add`;
- includes `MUL-2759` in the PR title, body, or branch;
- uses close syntax only when the issue should be closed on merge;
- keeps metadata small and durable;
- parks later serial sub-issues in `backlog`.

### Why this belongs in a skill

The brief must keep the hard contracts: use the `multica` CLI, do not access Multica APIs directly, and post a result comment when work is performed. The long issue workflow belongs in a skill because it is a task-specific method. It is needed when the agent works on an issue, but it does not need to consume prompt space for every possible task type.
