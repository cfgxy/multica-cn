---
name: multica-skill-discovery
description: Use when a user describes a capability but does not name a specific skill URL to import. Documents the facts of Multica's skill search surface — what the search returns (metadata-only candidates from clawhub.ai), which candidate fields are populated versus always-null, how upstream outage is reported, and the handoff to the import path. Discovery is candidate search, not installation; full content is only visible after import.
user-invocable: false
allowed-tools: Bash(multica *)
---

# Skill discovery in Multica

Discovery turns a capability description into a list of importable skill
candidates. It is candidate search over remote metadata. It does not install
anything, and it does not fetch or preview remote skill content.

## Quick start

```bash
multica skill search <query> --output json
```

This returns a JSON array of candidate objects. Pick one candidate's `url` and
hand it to the import skill:

```bash
multica skill import --url <selected-url> --output json
```

## Core model

Three facts define the discovery surface:

1. **Discovery is metadata-only candidate search.** Each result is a small
   metadata record (name, url, source, install_count, description), not the
   skill body. The search path never downloads a `SKILL.md`.
2. **discovery is not installation.** `multica skill search` only lists
   candidates. The skill is created in the workspace only by
   `multica skill import` (see the importing skill).
3. **There is no remote content preview during search.** You cannot inspect what
   a candidate skill actually does from the search result.
   **full content verification happens after import** by reading the imported
   workspace skill.

## The search CLI and API

`multica skill search <query> --output json` (CLI subcommand `skill search`)
calls `GET /api/skills/search?q=...`, URL-escaping the query. The server handler
delegates to a clawhub.ai search and normalizes the upstream response into a
flat candidate array, so agents never parse external human-readable output.

Empty query is rejected:

- CLI: `runSkillSearch` returns `query is required` before any request.
- Server: the handler writes HTTP 400 `query is required` when `q` is blank.

## Candidate field contract

Every candidate is a `SkillSearchCandidateResponse`. The JSON shape is fixed,
but only a subset of fields ever carries a value:

| Field | Status today | Notes |
| --- | --- | --- |
| `name` | populated | display name; falls back to the slug when blank |
| `url` | populated | always a `clawhub.ai` URL, built from owner handle + slug |
| `source` | populated | hardcoded literal `"clawhub.ai"` |
| `description` | populated | upstream summary; may be empty string |
| `install_count` | sometimes populated | only fetched for the first results (stats limit); otherwise null |
| `repo` | **always null** | the search handler never assigns it |
| `github_stars` | **always null** | the search handler never assigns it; upstream `stars` is deliberately ignored |

`repo` and `github_stars` are dead fields. The search handler builds each
candidate with only Name/URL/Source/Description plus a conditional InstallCount
and never sets Repo or GitHubStars, so both serialize to `null` on every result.
A handler test pins this: it asserts `github_stars` stays null even when the
upstream payload carries a `stars` value. **Do not rank or justify a selection
on `repo` or `github_stars`** — they convey nothing. Rank on `install_count`,
`source`/`url`, and `description` instead.

Because `source` is the hardcoded literal `clawhub.ai` and every `url` is built
as a clawhub.ai URL, a search result is never a `skills.sh` or `github.com`
candidate. (Those URLs are still importable directly via the import skill — they
are simply not search *results*.)

## Upstream-unavailable handling

The search depends on a live external upstream. When that upstream cannot be
reached or returns a non-200, the server responds with:

- HTTP 502
- JSON body `{"code":"upstream_unavailable","error":"..."}`

A handler test pins the `upstream_unavailable` code on a 502. When search fails
this way, report the outage. There is no local fallback list to fall back to.

## Handoff to import

A candidate's `url` is the input to the import path:

```bash
multica skill import --url <selected-url> --output json
```

Import is the only operation that creates a workspace skill. After import, read
the installed skill's full body and files to confirm fit:

```bash
multica skill get <skill-id> --output json
```

This is where content becomes inspectable —
**full content verification happens after import**, not during search.

Import is in-platform. It is **not `npx skills add`** or any other local
installer; those install outside Multica and do not create a managed workspace
skill. Use the importing skill for duplicate handling, returned fields, and
agent binding.

## References

`references/skill-discovery-source-map.md` maps every contract above to
`file:line` in the server source, quotes the candidate struct, and shows the
proof that `repo`/`github_stars` are never assigned.
