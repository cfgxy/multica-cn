---
name: multica-skill-discovery
description: Use when the user describes a capability but does not know which skill URL to import. Teaches how to search for candidate skills, verify fit against the user's need, choose an importable URL, and then install through Multica's import path.
user-invocable: false
allowed-tools: Bash(multica *)
---

# Discovering skills before import

Use this skill when the user wants a capability but does not provide a specific
skill URL. Your job is to find candidates, verify the best fit, and then hand off
to the Multica import path.

discovery is not installation. The final installation step is still:

```bash
multica skill import --url <selected-url> --output json
```

## Start from the user need

Turn the user's request into a short search query. Keep the query close to the
capability, not the user's whole sentence.

Examples:

- "make better landing pages" → `landing page design`
- "help agents find existing skills" → `find skills`
- "generate frontend UI polish guidance" → `frontend design`

## Find candidates

Use Multica's structured skill search CLI first:

```bash
multica skill search <query> --output json
```

The command returns candidate objects with fields such as:

```json
{
  "name": "<skill-name>",
  "url": "https://clawhub.ai/<owner>/<skill>",
  "source": "clawhub.ai",
  "repo": null,
  "install_count": 123,
  "github_stars": null,
  "description": "..."
}
```

Treat the response as candidates, not a product decision. The CLI normalizes the
upstream search source so agents do not need to parse external human-readable
output. If search returns `upstream_unavailable` or no trustworthy candidates,
say that clearly instead of inventing a URL.

Do not stop at the first result. Search output is a candidate list, not a product
decision.

## Verify before import

You must verify before import. Compare candidates with the user's actual need.
Use these signals:

- content match in `SKILL.md`, not only the title;
- `install_count`;
- `github_stars` / `repo` when present;
- source reputation and owner/repo credibility;
- whether the skill is general enough or too project-specific;
- whether the URL is importable by `multica skill import`;
- whether the skill duplicates an existing workspace skill.

If a candidate looks good from the search result but the `SKILL.md` does not
match the user's intent, reject it and explain why.

## Import after choosing

After selecting the best candidate, import through Multica:

```bash
multica skill import --url <selected-url> --output json
```

Use `multica-skill-importing` for duplicate handling, returned fields, and agent
binding.

Do not use `npx skills add` as the final step; this is not `npx skills add`. That installs outside Multica and
will not create a managed workspace skill.

## Output to the user

Report the decision, not the whole search dump:

- selected skill name and URL;
- why it matched the user's request;
- any strong rejected alternatives if relevant;
- import result: `id`, `name`, `config.origin`, files count;
- whether it still needs to be bound to an agent.

If no candidate is trustworthy, say that. Do not import a weak match just to do
something.

## Incorrect → correct

Incorrect:

```text
I found the first result on skills.sh and installed it with npx skills add.
```

Correct:

```text
I searched for `frontend design`, compared the top candidates by install count,
source reputation, and SKILL.md content, selected the matching skills.sh URL, and
imported it with `multica skill import --url <selected-url> --output json`.
```

## Source of truth

- `multica skill search <query> --output json` / `GET /api/skills/search?q=...`
  are the supported structured discovery surfaces.
- `multica-skill-importing` defines the final Multica workspace import path.
- `POST /api/skills/import` and `multica skill import --url` are the supported
  Multica installation surfaces.
- Discovery returns candidates; it does not replace the workspace import API.
