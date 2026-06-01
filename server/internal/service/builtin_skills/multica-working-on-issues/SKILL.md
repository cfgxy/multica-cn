---
name: multica-working-on-issues
description: Use when working on a Multica issue or issue comment — read the triggering context, perform and verify the work, report results, link PRs to issues, use metadata/status carefully, and create sub-issues without accidentally starting the wrong work.
user-invocable: false
allowed-tools: Bash(multica *), Bash(git *), Bash(gh *)
---

# Working on Multica issues

This skill covers the Multica issue execution loop: understand the trigger, do
real work, verify it, and leave the issue in a state the next person or agent can
trust.

Do not use this skill to learn how to build mention links. For mentions, load
`multica-mentioning`.

## The invariant

If you perform actual work for an issue, the result must be visible on the issue.
Terminal output, local files, and agent logs are not delivery. Post a concise
issue comment after the work is done.

If the triggering comment is only an acknowledgement, thanks, or sign-off and
you did no work, stay silent. Do not post "no action needed".

## Start from the trigger, not from memory

1. Read the issue:

```bash
multica issue get <issue-id> --output json
```

2. Read pinned metadata:

```bash
multica issue metadata list <issue-id> --output json
```

3. If this run came from a comment, read that conversation thread first:

```bash
multica issue comment list <issue-id> --thread <trigger-comment-id> --tail 30 --output json
```

4. Use recent threads only when the current thread does not contain enough
context:

```bash
multica issue comment list <issue-id> --recent 20 --output json
```

Do not answer an old comment just because it appears in the history. Focus on
the trigger that started this run.

## Reply only after doing the requested work

A result comment should state outcome and evidence, not narrate every command.
Good comments answer: what changed, what was verified, and what remains blocked.

Use the trigger comment as the parent when replying to a triggered comment:

```bash
multica issue comment add <issue-id> --parent <trigger-comment-id> --content "..."
```

For multiline comments, use `--content-stdin` or `--content-file` so quotes and
code blocks survive intact.

## PR linking and close intent

Multica links GitHub PRs to issues when the PR title, body, or branch contains a
routable issue key such as `MUL-2759`. Include the issue key when you create a PR
or branch for issue work.

Examples that link:

```text
MUL-2759: add built-in issue working skill
agent/matt/mul-2759-working-on-issues
```

Close intent is stricter. Use adjacent GitHub-style close syntax only when merge
should move the issue to `done`:

```text
Closes MUL-2759
Fixes MUL-2759
Resolves MUL-2759
```

Do not use close syntax for exploratory work, partial fixes, draft PRs, or PRs
that should not complete the issue.

## Verify linked PRs through Multica

When a task depends on PR state, do not guess from memory, branch names, GitHub
search, or stale metadata. Query Multica's issue ↔ PR link table through the CLI:

```bash
multica issue pull-requests <issue-id> --output json
```

Use the result to confirm:

- whether the issue is actually linked to a PR;
- PR number, URL, title, state, draft/merged status, and checks if present;
- whether `pr_url` / `pr_number` metadata is missing or stale;
- whether a result comment or status change can accurately say the work is in
  review, merged, blocked, or still unlinked.

If the command returns no linked PRs after you opened one, fix the PR title/body
or branch to include the issue key (for example `MUL-2759`) instead of claiming
that the issue is linked.

## Metadata is a high-signal scratchpad

Read metadata on entry. Write it only when the value will likely be re-read by a
future run on the same issue.

Usually valid keys:

- `pr_url`
- `pr_number`
- `pipeline_status`
- `deploy_url`
- `external_issue_url`
- `waiting_on`
- `blocked_reason`
- `decision`

Do not write logs, summaries, files touched, timestamps, attempts, or temporary
notes. Put those in the result comment if they matter.

Use:

```bash
multica issue metadata set <issue-id> --key pr_url --value <url>
multica issue metadata delete <issue-id> --key <stale-key>
```

## Status changes are side effects

Do not change status just to look done. Status changes can trigger or cancel
work.

Guidelines:

- Use `blocked` only when there is a real blocker that outlasts this run; also
  explain it in a comment and consider `blocked_reason` metadata.
- Use `in_review` when the deliverable is waiting for review, commonly after a
  PR is opened.
- Use `done` only when the issue is actually complete. If a PR should close it
  on merge, prefer close syntax in the PR instead of manually marking done early.
- Do not change status for a pure answer unless the user explicitly asked.
- Do not set `cancelled` unless a user requested cancellation.

## Sub-issues: `todo` starts work, `backlog` parks work

Choosing status on creation controls whether assigned agents run immediately.

Parallel children:

```bash
multica issue create --title "..." --parent <issue-id> --assignee <agent> --status todo
```

Serial follow-up children:

```bash
multica issue create --title "Step 2: ..." --parent <issue-id> --assignee <agent> --status backlog
```

Only promote the next serial issue when the previous step is truly complete:

```bash
multica issue status <child-id> todo
```

Using `todo` for every serial step starts too much work at once.

## Attachments and platform data

Use the `multica` CLI for Multica resources. Do not fetch Multica resource URLs
with curl, wget, or direct HTTP. If an issue or comment has attachments and you
need them, inspect the attachment CLI help and use the authenticated CLI path.

## Incorrect → correct

Incorrect:

```text
I fixed it locally.
```

Correct:

```text
Fixed the login redirect and opened PR: https://github.com/org/repo/pull/123
Verified with `go test ./internal/service -run TestLoginRedirect`.
```

Incorrect PR title:

```text
Fix login redirect
```

Correct PR title:

```text
MUL-2759: fix login redirect
```

Incorrect serial children:

```bash
multica issue create --title "Step 2" --parent MUL-2759 --status todo
multica issue create --title "Step 3" --parent MUL-2759 --status todo
```

Correct serial children:

```bash
multica issue create --title "Step 2" --parent MUL-2759 --status backlog
multica issue create --title "Step 3" --parent MUL-2759 --status backlog
```

## Source of truth

- `server/cmd/multica/cmd_issue.go:104` — `multica issue pull-requests` exposes
  linked PR lookup to agents through the CLI.
- `server/cmd/multica/cmd_issue.go:522` — the CLI calls
  `GET /api/issues/<id>/pull-requests`.
- `server/cmd/server/router.go:480` — the API route is registered.
- `server/internal/handler/github.go:466` — the API loads the issue and lists
  PRs through `ListPullRequestsByIssue`.
- `server/internal/handler/github.go:727` — issue identifiers in PR title, body,
  or branch create issue ↔ PR links.
- `server/internal/handler/github.go:736` — adjacent close keywords such as
  `Closes MUL-123` record close intent.
- `server/internal/handler/issue.go:2523` — moving an assigned issue out of
  `backlog` enqueues work.
- `server/internal/handler/issue_child_done.go:15` — a child issue entering `done`
  notifies the parent.
