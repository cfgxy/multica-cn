---
name: multica-mentioning
description: Use when writing an issue comment that needs to @mention someone — notify a person, trigger another agent, or hand work to a squad. Covers how to build a mention link that actually fires and what each mention type does.
user-invocable: false
allowed-tools: Bash(multica *)
---

# Mentioning & Delegating

This skill covers HOW to build a mention that works. WHETHER to mention at all
— loop avoidance, staying silent on acknowledgements — is already in your
runtime brief's Mentions section; follow that and do not repeat it here.

## The one rule that breaks mentions

A mention link needs a REAL UUID. Writing `[@Alice](mention://member/Alice)`
does NOTHING: a name is not a UUID, so the link silently fails — no
notification, no trigger, no error. Always look up the UUID first.

## Step 1 — look up the UUID with `--output json`

- a person → `multica workspace member list --output json` → use `user_id`
- an agent → `multica agent list --output json` → use `id`
- a squad  → `multica squad list --output json` → use `id`

Match by display name. If the name is ambiguous or absent, do not guess —
say so in your comment instead of emitting a broken link.

## Step 2 — build the link; type and id source MUST match

Format: `[@Name](mention://<type>/<uuid>)`

| To…                  | type     | uuid from      | What it triggers                          |
| -------------------- | -------- | -------------- | ----------------------------------------- |
| notify a person      | `member` | member.user_id | sends them a notification (no run)        |
| make an agent work   | `agent`  | agent.id       | enqueues a run for that agent             |
| hand work to a squad | `squad`  | squad.id       | enqueues the squad LEADER, who delegates  |
| reference an issue   | `issue`  | issue.id       | nothing — a plain link, always safe       |

Using the wrong `type` for an id points at the wrong entity or fails silently.

**`@all` is the exception** — it uses the literal `all`, never a UUID:
`[@all](mention://all/all)`. It broadcasts to everyone on the issue; it does
NOT make any specific agent run, and it also suppresses the assignee's
automatic on-comment trigger. Use it to announce, not to request work.

## What does NOT happen (so the result doesn't surprise you)

- A wrong/missing UUID, or a bare `@name`, silently does nothing.
- `@member` never makes a person "run" — it only notifies them.
- Even a correct mention may not fire if the target agent already has a
  pending task on this issue, is archived, or is private and you cannot
  access it. That is expected — do not retry in a loop.

## Incorrect → Correct

Incorrect: `@alice please review`
  → plain text, no link, nobody is notified.

Incorrect: `[@Alice](mention://member/Alice) please review`
  → "Alice" is not a UUID, the link is silently dead.

Correct:
  1. `multica workspace member list --output json`  → Alice's user_id = 7f3a…
  2. `[@Alice](mention://member/7f3a…) please review`

## Source of truth

These behaviors are hard-coded in the Multica backend. If a mention does not
behave as described, check the source rather than guessing:

- `server/internal/util/mention.go:16` — the mention regex. The id must be a
  hex UUID (or the literal `all`); a name silently fails to parse.
- `server/internal/handler/comment.go:884` — `enqueueMentionedAgentTasks`:
  how `@agent` enqueues a run and `@squad` enqueues the leader, plus the
  guards (already-pending dedup, archived, private) that make a valid mention
  no-op.
- `server/internal/handler/comment.go:768` — `@all` is a broadcast that
  suppresses the assignee's auto-trigger.
