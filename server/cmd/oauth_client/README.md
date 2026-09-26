# OAuth client registration runbook

The MCP authorization server (RUYI-209) does not implement Dynamic Client
Registration. Clients are pre-registered rows in `oauth_clients`, and this
command is their only writer. See
`docs/adr/001-mcp-oauth-behind-nextjs-proxy.md` §3.6.

From `server/`:

```bash
go run ./cmd/oauth_client create \
  --name "ChatGPT" \
  --redirect-uri "https://chatgpt.com/connector_platform_oauth_redirect"
```

The command prints `client_id` and `client_secret` to stdout once. Only the
SHA-256 hash of the secret is stored, so the plaintext cannot be read back —
paste it into the consumer's advanced OAuth settings immediately. Rotation is
creating a new client and deleting the old one.

`--redirect-uri` is repeatable and is matched byte for byte at the authorize
endpoint: register the exact string the client sends, including any trailing
slash. Entries must be absolute, must use `https` unless the host is
`localhost` / `127.0.0.1`, and must not carry a fragment.

Other subcommands:

```bash
go run ./cmd/oauth_client list
go run ./cmd/oauth_client delete --client-id <id>
```

Deleting a client is the first-version revocation mechanism: already-issued
access tokens stay valid until they expire, since they are self-contained JWTs
the resource server verifies by signature (ADR §6).

`DATABASE_URL` selects the database, defaulting to the local development
instance.
