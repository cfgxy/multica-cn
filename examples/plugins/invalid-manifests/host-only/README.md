# Manifests only the host rejects

Every file here **passes** `packages/plugin-sdk/manifest.schema.json` and is
still refused when you publish it. That is not a gap in the schema — each rule
below is cross-field or computed, and a JSON Schema can state neither.

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
| `event-without-the-matching-read-scope.json` | Subscribing to an event requires the scope that reading the same content through the Action API would have required — an event pushes the same body. |
| `schedule-more-often-than-five-minutes.json` | A cron that can fire more often than every five minutes is refused. Deciding that means enumerating occurrences. |

Passing the schema means the shape is right. It has never meant the publish will
succeed, and these files are what makes that statement checkable rather than a
sentence in a README.
