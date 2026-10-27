# Manifests only the host rejects

Every file here **passes** `packages/plugin-sdk/manifest.schema.json` and is
still refused when you publish it. That is not a gap in the schema — each rule
below needs an equality between sibling values, a sum, a comparison against
another list, or a computation, and a JSON Schema can state none of those.

They exist so the boundary is testable from both sides:
`packages/plugin-sdk/manifest.schema.test.ts` asserts the schema accepts them,
and `server/pkg/plugincontract/host_only_rules_test.go` asserts the host does
not. A rule that quietly moved into the schema, or out of the host, breaks one
of those two suites.

| File | Rule the host enforces |
| --- | --- |
| `contributes-over-the-total-limit.json` | Surfaces, hooks and resources may total 64. The schema can bound each array; it cannot add three lengths. |
| `skill-entry-does-not-match-its-key.json` | A skill's `entry` must be `skills/<its own key>/SKILL.md`. The schema fixes the shape, not the equality between two sibling fields. |
| `hook-url-outside-the-net-scopes.json` | A hook's `transport.url` host must be covered by a `net:` scope, so the consent screen describes where data actually goes. |
| `schedule-more-often-than-five-minutes.json` | A cron that can fire more often than every five minutes is refused. Deciding that means enumerating occurrences. |

The event→read-scope rule used to sit here. It does not belong: events and
scopes are both closed enums, so "subscribing to `comment.created` requires
`comments:read`" is one `if`/`then` over `contains`, and the schema now states
it. Its sample lives with the rest of the schema-caught ones, one directory up.
A rule this directory keeps has to be one a JSON Schema genuinely cannot reach —
otherwise the boundary drifts into a place to park work.

Passing the schema means the shape is right. It has never meant the publish will
succeed, and these files are what makes that statement checkable rather than a
sentence in a README.
