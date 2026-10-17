# RUYI-810: Codex command guard PATH

## Decision

The Codex backend writes executable `gh` and `git` shims into `command-guards` under the user's Multica cache. It prepends that directory to the environment of every Codex process it starts and sets `BASH_ENV` to a generated startup file in that directory. Codex's default `/bin/bash -lc` shell reads login profiles that may replace `PATH`; Bash then sources `BASH_ENV` for the non-interactive command and restores the shim directory to the front. If the daemon already supplied `BASH_ENV`, the generated file sources it before restoring the shim path. The shims pass arguments unchanged to the existing RUYI-401/403 guard scripts and remove their own directory from `PATH` before delegation, so each guard resolves the native CLI instead of recursively finding the shim. A private child-process marker keeps the startup file from re-adding the shim while a shim is running a guard or read-only native Git command.

The defaults follow the existing installers:

- `$HOME/.config/gh-guard/gh-pr-guard`.
- `$HOME/.config/git-push-guard/git-push-guard`.

When the gh guard is absent or not executable, the shim refuses the command. When the git guard is absent or not executable, the shim delegates only `git status` to the native executable and refuses every other Git subcommand. This keeps the explicitly allowed read-only status check available while preventing protocol write commands and configured aliases from falling through to native Git. Guard decisions and canonical repository policy remain owned by the existing scripts when the guard is present.

## Options

| Option | Reliability | Maintenance and host fit | Security boundary |
| --- | --- | --- | --- |
| Shell rc functions | Fails in non-interactive shells and depends on login-shell details | Low setup cost, but each shell needs its own rc behavior | Functions are bypassed by `command`, scripts, and absolute paths |
| Codex shell environment policy | Can control inherited variables, but does not create executable wrappers or make a missing guard fail closed by itself | Codex-specific; profile and version behavior must be checked | Environment filtering is not command authorization |
| Daemon or launcher PATH injection with private shims (selected) | Applies to every Codex process launched by this backend and survives Codex profile changes | Central code path; current shim implementation targets POSIX hosts with Bash | PATH controls name resolution only; absolute paths, alternate environments, and untrusted PATH changes can bypass it |
| Service-manager PATH configuration | Stable after service restart for all daemon children | Depends on systemd, launchd, or another host manager and affects more than Codex | Same PATH-only limitation |
| Server-side Git/GitHub write API or OS execution isolation | Strongest enforcement option | Requires a separate architecture and operational surface | Can make direct CLI bypass unavailable when all writes are mediated |

The chosen approach is narrow to Codex processes and does not depend on `.bashrc`, profile startup files, or interactive shell functions. It does not claim to be an unbypassable security boundary.

## Verification and operations

The package tests use temporary homes, fake native binaries, and fake guards to verify login-shell command resolution after a profile replaces `PATH`, argument-preserving delegation, no recursive resolution, and missing-guard rejection (including `send-pack` and a configured alias) without writing to GitHub or a remote Git repository. With a missing Git guard, only `git status` is delegated to native Git and all global options such as `-C <repo>` are retained. Existing guard tests remain responsible for canonical repository, upstream, and protected-branch policy.

For a host installation, the two existing guard files and their canonical repository configuration must be installed at the defaults above. Guard paths are derived from the daemon OS user's home and cannot be overridden by an agent profile. Restarting the daemon starts new Codex processes with the injected PATH and `BASH_ENV`. A newly started Codex task should report the generated shim first in `command -v` and `type -a`, and the native executable later in `type -a`, even when a login profile changes PATH. The shims and startup file live under the user's Multica cache and are replaced atomically on launch. This applies to Bash non-interactive shells; other shells and operating systems remain outside the implementation scope.

Rollback is a daemon rollback to the previous build. That build does not prepend the shim directory, so the generated files under the user's Multica cache are inert and can be removed. The system `git` and `gh` binaries and shell rc files are not modified.

## Known limits

- PATH shims do not stop direct calls such as `/usr/bin/git push`, `command -p git push`, a replaced PATH, or another client that bypasses Codex.
- The guard scripts must be installed and executable. The Git shim fails closed for every command except `status` when its guard is missing; that exception is a command-resolution policy, not a general Git authorization boundary.
- Bash is required for the current shim scripts. Windows hosts need a separate native wrapper implementation before this behavior can be claimed there.
- Replacing the host shell, profile-specific command environment, or daemon launcher is outside this change. QA must verify the actual daemon profile and restart persistence on its authorized host.
