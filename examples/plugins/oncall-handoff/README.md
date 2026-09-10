# On-call Handoff

The example for the three parts of the plugin contract the panel examples do
not reach: a **`modal` surface**, a **`manual` trigger**, and a **write-only
secret**.

## What it shows

### A modal surface

`contributes.surfaces[0].type` is `modal`, not `issue_panel`. The host mounts it
in a dialog opened from the issue menu instead of inline on the issue. Nothing
else changes: same sandboxed iframe with an opaque origin, same `MessagePort`
bridge, same CSP derived from the granted `net:` scopes.

A modal is not a way to interrupt somebody. It opens because a person picked it;
a plugin has no way to open one on its own.

### A manual trigger

`file_handoff` declares `"triggers": ["ui", "manual"]`, which are two different
call sites for the same hook:

- **`ui`** — the modal calls it through the host bridge. The surface never
  fetches `rota.example.com` itself; the host makes the call, signs it, rate
  limits it, holds it to the granted `net:` domains, and records an invocation
  the administrator can read afterwards.
- **`manual`** — the same hook appears directly in the issue menu, for the case
  where somebody wants to file a handoff without opening the form. Declaring the
  trigger is the whole opt-in; the host renders the entry.

The `roster` hook declares only `agent`, so it never appears in a menu — it is
offered to agents as MCP tools, and only after an administrator approves the
individual tools.

### Write-only secrets

`rota_token` and `roster_credential` are `type: "secret"`. An administrator sets
them once in **Settings → Plugins**; the host encrypts them and never returns
them. Concretely, in this example:

| Where | Does the secret appear? |
| --- | --- |
| The settings form, after saving | No — it shows "configured", never the value |
| `context.config` in `ui/main.js` | No — secret-typed fields are stripped |
| The `config` object in an http hook body | No — same stripping, server side |
| `Authorization` on the `mcp` transport | Yes, and only for `roster_credential` |

That last row is the single exception, and it is why the field is named
`roster_credential`: Multica attaches `<hookKey>_credential` as the
`Authorization` header when it speaks to that hook's MCP server. There is no
other path by which a stored secret reaches an endpoint.

So how does `server/handler.mjs` authenticate to the rota service? It reads
`ROTA_TOKEN` from **its own** environment. The plugin's credential for the
plugin's own service is something the author already has; sending it back to
them over the wire would hand every value to whoever holds the endpoint, and buy
nothing.

Both the surface and the handler assert this rather than describe it — each
refuses to run if a secret-typed field turns up where it should not be.

## Running it

```sh
# 1. Generate a dev certificate (see deploy-sentinel/README.md for details)
openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
  -keyout dev-key.pem -out dev-cert.pem \
  -subj "/CN=127.0.0.1" -addext "subjectAltName=IP:127.0.0.1"

# 2. Run the handler with its own copy of the rota token
MULTICA_SIGNING_SECRET=whsec_… ROTA_TOKEN=… node server/handler.mjs
```

Then zip this folder — the manifest plus every file it names — and upload it in
**Settings → Plugins**. While iterating, `MULTICA_PLUGIN_DIR` publishes straight
from disk instead.

Point `transport.url` at `https://127.0.0.1:8789/hooks/handoff` and set
`MULTICA_PLUGIN_DEV_CA` to `dev-cert.pem` for local development. That variable
changes *which* certificate Multica trusts; it never turns verification off.

`ui/main.js` is one file with no `import`: the host serves the entry inside one
generated document with no module graph, so a bare specifier has nowhere to
resolve. Bundle your dependencies in.
