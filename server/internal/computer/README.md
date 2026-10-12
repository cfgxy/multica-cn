# Self-hosted Computer provisioning

The personal **Settings → My environments** page manages Linux accounts and
Multica daemons. Instance administrators manage the machine registry, bindings
and audit at **/admin**, outside any workspace. This feature is disabled until an operator supplies the settings
below. Workspace administrator roles do not grant access to another human's
credentials. Every saved Multica PAT must belong to the authenticated human.

Server configuration:

- `MULTICA_COMPUTER_SECRET_KEY`: a base64-encoded 32-byte encryption key. Preserve
  this separately from database backups; changing it requires re-entering credentials.
- `MULTICA_INSTANCE_ADMIN_IDS`: comma-separated human UUIDs allowed into Admin
  Area and its APIs. Workspace owner/admin roles do not grant this access. This
  phase uses deployment configuration, not a self-service grant endpoint.
- `MULTICA_COMPUTER_OPERATOR_IDS`: legacy bootstrap fallback, used only when
  `MULTICA_INSTANCE_ADMIN_IDS` is absent. Setting the new variable explicitly
  empty disables all instance administrators, even if the old list is populated.
  Change configuration and restart the backend to grant/revoke access. Choose
  the initial human UUID from the actual authenticated account; no automatic
  first-user or workspace-owner promotion is performed.
- `MULTICA_COMPUTER_SSH_KEY`: absolute path to the operator's private SSH key.
  Mount it read-only in the server container. Prepopulate that server user's SSH
  `known_hosts` from independently verified host keys; unknown or changed hosts fail.
- `MULTICA_COMPUTER_CLI_PATH`: trusted Linux executable built from this branch for
  the target architecture. It includes configurable per-user health ports. In the
  standard backend image this can point to `/app/multica` (check the image workdir).
- `MULTICA_COMPUTER_SERVER_URL`: fixed URL reachable from the target Computer.
- `MULTICA_COMPUTER_STATE_DIR`: private persistent directory owned by the backend
  service account, mode 0700. Contains per-machine/account locks and failed PAM
  attempt counters. Do not share it with employees or place it in their homes.

The server needs OpenSSH client access. Target Computers need Debian/Ubuntu
(with `apt-get`), Python 3, PAM (`pam_unix.so`), `sudo`, `runuser`, user
management tools and systemd. The registered SSH operator requires passwordless
sudo. The connection check rejects machines without `apt-get`. New accounts
require successful installation of zsh, htop, curl and git; a package installation
failure fails provisioning. System packages are installed before creating the
account, and failures during password or shell setup delete the new account.
Installer process groups are terminated on timeout, including their children.
Account creation allows eight minutes for installation and cleanup.

New accounts receive a private `.zshrc` with basic history, completion and the
managed runtime paths, so the first login does not open `zsh-newuser-install`.
Existing startup files from `/etc/skel` are preserved. Shell initialization uses
local configuration only; it does not download oh-my-zsh or other shell plugins.
Reusing an existing account does not change its shell configuration.

Administrators can install vendor CLIs from the Computer runtime panel after
entering a target Linux username. npm-based runtimes require Node.js and npm in
`/usr/local/bin`, `/usr/bin` or `/bin`; installation checks these prerequisites
and uses the target user's `~/.local` prefix. Installs and version checks use the
same user and PATH, without reading login-shell or nvm configuration. Managed
daemon units include `~/.local/bin`, `~/.kimi-code/bin` and `~/.grok/bin`; use Upgrade
runtime for existing bindings to apply this unit change. A failed SSH or version
check is reported separately from a missing executable.

Grok and Kimi receive the requested version (or `latest`) through their installer
arguments. Oh-My-Pi uses the official installer with `--binary` and a release tag,
so it does not depend on an independently installed Bun interpreter. Installers are downloaded
completely over HTTPS before execution as the target user; any download or
installer failure fails the operation. The installed command must also pass
`--version` before success is recorded. These upstream installers remain trusted
network dependencies; there is no pinned checksum or independent signature
verification in this workflow.

Before registering a target, its operator must install the fixed PAM service once
(outside request handling), owned by root and not writable by other users:

```sh
printf 'auth required pam_unix.so\naccount required pam_unix.so\n' > /tmp/multica-provision.pam
sudo install -o root -g root -m 0644 /tmp/multica-provision.pam /etc/pam.d/multica-provision
rm /tmp/multica-provision.pam
```

Authentication rejects a missing, symlinked, writable, or unexpected policy file;
it never creates or replaces the service while handling a request.

Existing accounts are reused only after PAM authentication and account checks.
System users, the SSH operator, privileged group members and sudo users are
refused. Five failed checks lock that username for 15 minutes after the last
counted attempt. Infrastructure failures do not count as wrong passwords.
Passwords travel on SSH stdin and are never stored in the database or logs.

Secrets are encrypted in the database and written with mode 0600 as the target
uid. Git identity is managed through an included file; unrelated Git helpers and
Multica JSON fields remain. Each binding uses its own `computer-<id>` CLI profile
and private workspace directory; the default CLI profile is not replaced.
Managed services use `UMask=0077` so newly created code is not world-readable. SSH Git auth requires a private key and independently
verified `known_hosts` entries. Environment keys are provider-allowlisted.

Operations run asynchronously on one bastion. A unique Computer/username binding
prevents concurrent jobs and ownership changes. A process interrupted by restart
shows `interrupted` after 20 minutes and requires explicit acknowledgment before a new retry; passwords are deliberately
not persisted for automatic replay. A managed daemon is reported ready only after
its registration matches owner, workspace, daemon ID and a recent heartbeat.
Port binding failure leaves a failed operation, never a false success.

Uninstall stops/disables the service and removes its managed unit/executable. It
keeps the Linux account, code, credential files and historical runtime registration.
Use the existing runtime deletion UI to delete registration records after unbinding
agents; provisioning does not bypass those dependency checks.

Before enabling on a real host, validate on an explicitly designated nonproduction
Computer/account: PAM match/mismatch/lockout, interrupted creation, symlink defense,
GitLab authentication and author identity, multiple users/Computers, daemon restart,
key synchronization and uninstall. Never aim the tests at the production operator.

The legacy POST /api/computers remains compatible and uses the same instance
admin check and audit transaction. Admin APIs never expose employee credentials.
Disabling a Computer prevents new provision/sync/upgrade operations, but existing
jobs/services keep running and owners may remove their own daemon. A connection
cannot be changed once any account binding exists; register another Computer.
Bindings show the latest 500 entries and audit shows the latest 200 entries.

Operation and asset metadata are persisted in PostgreSQL. Linux User details separate account observations, systemd service observations, CLI probes and runtime registrations. See [the operation contract](../../../docs/design/computer-remote-operations.md) for async receipts, serialization, recovery, archival and explicit Linux account deletion.

## Runtime download cache

Managed runtime installations require `MULTICA_COMPUTER_SERVER_URL` to be reachable
from the target Computer. The backend supplies temporary, operation-scoped
registry/installer URLs and caches supported public upstream downloads centrally.
`MULTICA_RUNTIME_CACHE_DIR` overrides the default `runtime-cache` directory under
`MULTICA_COMPUTER_STATE_DIR`. Keep that directory on persistent storage and do not
share it between backend processes. Administrators manage versions and cached
files in Admin Area → Runtime cache. See
[the runtime cache guide](../../../docs/engineering/runtime-cache.md) for limits,
provider coverage, endpoint behavior, and rollback notes.
