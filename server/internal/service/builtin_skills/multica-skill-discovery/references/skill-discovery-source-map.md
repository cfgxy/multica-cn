# Skill discovery — source map

Every contract in `SKILL.md` traced to server source. Paths are relative to the
repo root. Line numbers re-derived on branch `agent/howard/219a700a`.

## CLI: `multica skill search <query>`

- `server/cmd/multica/cmd_skill.go:66-71` — `skillSearchCmd` (`Use: "search <query>"`,
  `Args: exactArgs(1)`, `RunE: runSkillSearch`).
- `server/cmd/multica/cmd_skill.go:472-510` — `runSkillSearch`:
  - `:478-481` trims the arg and returns `query is required` when empty.
  - `:487` builds the path `"/api/skills/search?q=" + url.QueryEscape(query)`.
  - `:488` issues `GetJSON` into `[]map[string]any`.
  - `:492-495` prints raw JSON when `--output json`.
  - `:497-508` table mode columns: NAME, URL, SOURCE, INSTALLS, DESCRIPTION
    (no repo / github_stars column — they are never useful).

## Route: `GET /api/skills/search`

- `server/cmd/server/router.go:620` — `r.Get("/search", h.SearchSkills)` inside
  the `r.Route("/api/skills", ...)` block (`:617`).

## Handler: `SearchSkills`

- `server/internal/handler/skill.go:280-297`:
  - `:281-285` reads `q`, trims it, writes HTTP 400 `query is required` when blank.
  - `:287-288` 30s HTTP client; delegates to `searchClawHubSkills(httpClient, query)`.
  - `:289-295` on error writes HTTP 502 with body
    `{"code":"upstream_unavailable","error": err.Error()}`.
  - `:296` on success writes HTTP 200 with the candidate array.

## Candidate struct: `SkillSearchCandidateResponse`

- `server/internal/handler/skill.go:89-97`:

```go
type SkillSearchCandidateResponse struct {
	Name         string  `json:"name"`
	URL          string  `json:"url"`
	Source       string  `json:"source"`
	Repo         *string `json:"repo"`
	InstallCount *int64  `json:"install_count"`
	GitHubStars  *int64  `json:"github_stars"`
	Description  string  `json:"description"`
}
```

`Repo` and `GitHubStars` are pointers, so when unset they serialize to JSON
`null`.

## Dead-field proof: `repo` and `github_stars` are never assigned

- `server/internal/handler/skill.go:781-819` — `searchClawHubSkills`. The
  per-result candidate is built at `:802-807` setting **only** Name, URL, Source,
  Description:

```go
candidate := SkillSearchCandidateResponse{
	Name:        result.DisplayName,
	URL:         buildClawHubSkillURL(result.OwnerHandle, result.Slug),
	Source:      "clawhub.ai",
	Description: result.Summary,
}
```

  - `:808-810` fills `Name` from the slug only when blank.
  - `:811-814` conditionally sets `InstallCount` (only for `i < clawHubSearchStatsLimit`).
  - No line in the function ever assigns `candidate.Repo` or
    `candidate.GitHubStars`. Both stay nil → JSON `null` on every result.
- `server/internal/handler/skill.go:805` — `Source` is the hardcoded literal
  `"clawhub.ai"`.
- `server/internal/handler/skill.go:804`, `:821-826` —
  `buildClawHubSkillURL` always returns a `https://clawhub.ai/...` URL, so `url`
  is always a clawhub.ai URL (never skills.sh / github.com).
- `server/internal/handler/skill.go:618` — `clawHubSearchStatsLimit = 10` (the
  install-count fetch cutoff).
- `server/internal/handler/skill.go:828-849` — `fetchClawHubInstallCount`
  returns only an install count from clawhub stats; nothing assigns stars to the
  candidate.

### Test pinning the dead fields

- `server/internal/handler/skill_search_test.go:92-97` —
  `TestSearchSkillsReturnsNormalizedClawHubCandidates` asserts `repo` is null and
  `github_stars` is null even though the upstream `/skills/react` detail payload
  returns `"stars": 3` (`:40-44`). It also asserts `install_count == 62` (`:98`)
  and `source == "clawhub.ai"` (`:89-91`). This is the live proof that upstream
  stars are deliberately not surfaced as `github_stars`.

## Upstream-unavailable contract

- `server/internal/handler/skill.go:289-295` — HTTP 502 + `upstream_unavailable`
  code (handler, quoted above).
- `server/internal/handler/skill.go:781-790` — `searchClawHubSkills` returns an
  error on transport failure or any non-200 upstream status, which is what
  triggers the 502.
- `server/internal/handler/skill_search_test.go:118-141` —
  `TestSearchSkillsUpstreamUnavailableReturnsStructuredError` asserts a 502 with
  `code == "upstream_unavailable"` when upstream returns 502.

## ClawHub upstream types (for reference)

- `server/internal/handler/skill.go:616` — `clawHubAPIBase = "https://clawhub.ai/api/v1"`.
- `server/internal/handler/skill.go:620-647` — `clawhubSearchResponse`,
  `clawhubSearchResult` (`slug`, `displayName`, `summary`, `ownerHandle`),
  `clawhubSkillStats` (`installsAllTime`, `installsCurrent`), `clawhubSkill`.
  Upstream `stars` is not modeled into any field that reaches the candidate.

## Post-import content verification

- `server/cmd/multica/cmd_skill.go:33-38` — `skillGetCmd` (`Use: "get <id>"`).
- `server/cmd/multica/cmd_skill.go:119` — `skill get` defaults `--output` to `json`.
- `server/cmd/multica/cmd_skill.go:251-279` — `runSkillGet` calls
  `GET /api/skills/<id>` and prints the skill (body + files) as JSON. This is the
  first point at which full skill content is inspectable — after import, not
  during search.

## Import handoff

- `server/cmd/multica/cmd_skill.go:60-64` — `skillImportCmd`.
- `server/cmd/multica/cmd_skill.go:412-445` — `runSkillImport` POSTs
  `/api/skills/import` with the selected `--url`. This is the only path that
  creates a workspace skill. See the importing skill for duplicate handling and
  agent binding.
