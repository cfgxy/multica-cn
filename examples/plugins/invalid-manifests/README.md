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

Two host rules are cross-field and deliberately NOT expressible here — the
schema cannot see them, so passing it is a statement about shape only:

- A hook's `transport.url` host must be covered by a `net:` scope.
- Subscribing to an event requires the scope that reading the same content
  through the Action API would have required.
