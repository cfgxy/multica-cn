# Deliberately invalid manifests

Each file here is rejected by `packages/plugin-sdk/manifest.schema.json`, and by
the host parser for the same reason. They exist so the schema is testable
against failure and not only against success: a schema that accepts everything
still passes a suite that only ever feeds it the official demos.

The file name states the rule being broken.

| File | Rule |
| --- | --- |
| `unknown-scope.json` | The scope list is closed. `storage:admin` is not one of them. |
| `enum-without-options.json` | An `enum` config field must declare `options`. |
| `secret-with-options.json` | `options` belongs to `enum` fields only. |
| `event-hook-without-events.json` | Declaring the `event` trigger requires an `events` list. |
| `schedule-on-mcp-transport.json` | A scheduled hook runs over the `http` transport. |
| `surface-entry-not-a-script.json` | The host renders the surface document, so `entry` is a `.js`/`.mjs` script. |
| `unknown-field.json` | Unknown manifest fields are rejected rather than ignored, so a typo is an error and not a silently dropped setting. |
| `empty-contributes.json` | A plugin that contributes nothing has nothing to install. |
| `skill-entry-not-under-skills.json` | A skill's `entry` lives at `skills/<key>/SKILL.md`; anything else is not a skill the host will read. |
| `event-without-the-matching-read-scope.json` | Subscribing to an event requires the read scope for the same content. Events and scopes are both closed enums, so the schema states it as one conditional per read scope. |

Rules the schema cannot express live one directory down, in `host-only/`. Those
files PASS this schema and are still refused at publish — cross-field equality,
a summed limit, a host compared against another list, and a computed cron
interval are things a JSON Schema cannot state. Passing this schema means the
shape is right; `host-only/README.md` is the list of what it does not promise.
