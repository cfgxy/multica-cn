package agent

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	codexGuardBinDir       = "command-guards"
	codexGuardShellEnvFile = "bash-env"
	codexGuardPathMarker   = "__MULTICA_GUARD_PATH__"
)

const codexGuardShellEnv = `if [[ "${MULTICA_CODEX_GUARD_ACTIVE:-}" == "1" ]]; then return; fi
original_bash_env=__MULTICA_ORIGINAL_BASH_ENV__
if [[ -n "$original_bash_env" && -r "$original_bash_env" ]]; then source "$original_bash_env"; fi
guard_bin=__MULTICA_GUARD_BIN__
filtered_path="$guard_bin"
IFS=: read -r -a path_parts <<< "${PATH:-}"
for part in "${path_parts[@]}"; do
  [[ -z "$part" || "$part" == "$guard_bin" ]] && continue
  filtered_path="$filtered_path:$part"
done
export PATH="$filtered_path"
`

const codexGHShim = `#!/usr/bin/env bash
set -u
shim_dir="$(cd -- "$(dirname -- "$0")" && pwd -P)" || exit 126
guard=__MULTICA_GUARD_PATH__
if [[ -z "$guard" || ! -x "$guard" ]]; then
  printf 'codex-guard: gh guard missing or not executable; refusing gh command\n' >&2
  exit 126
fi
filtered_path=""
IFS=: read -r -a path_parts <<< "${PATH:-}"
for part in "${path_parts[@]}"; do
  [[ -z "$part" ]] && continue
  resolved_part="$part"
  if [[ -d "$part" ]]; then resolved_part="$(cd -- "$part" && pwd -P)" || resolved_part="$part"; fi
  [[ "$resolved_part" == "$shim_dir" ]] && continue
  if [[ -z "$filtered_path" ]]; then filtered_path="$part"; else filtered_path="$filtered_path:$part"; fi
done
export PATH="$filtered_path"
export MULTICA_CODEX_GUARD_ACTIVE=1
exec "$guard" "$@"
`

const codexGitShim = `#!/usr/bin/env bash
set -u
shim_dir="$(cd -- "$(dirname -- "$0")" && pwd -P)" || exit 126
guard=__MULTICA_GUARD_PATH__
filtered_path=""
IFS=: read -r -a path_parts <<< "${PATH:-}"
for part in "${path_parts[@]}"; do
  [[ -z "$part" ]] && continue
  resolved_part="$part"
  if [[ -d "$part" ]]; then resolved_part="$(cd -- "$part" && pwd -P)" || resolved_part="$part"; fi
  [[ "$resolved_part" == "$shim_dir" ]] && continue
  if [[ -z "$filtered_path" ]]; then filtered_path="$part"; else filtered_path="$filtered_path:$part"; fi
done
export PATH="$filtered_path"
if [[ -z "$guard" || ! -x "$guard" ]]; then
  command_name=""
  git_args=("$@")
  parse_args=("$@")
  while ((${#parse_args[@]})); do
    case "${parse_args[0]}" in
      -c|-C|--git-dir|--work-tree|--namespace|--super-prefix) parse_args=("${parse_args[@]:2}") ;;
      --git-dir=*|--work-tree=*|--namespace=*|--super-prefix=*|-c*|-C*) parse_args=("${parse_args[@]:1}") ;;
      -*) parse_args=("${parse_args[@]:1}") ;;
      *) command_name="${parse_args[0]}"; break ;;
    esac
  done
  if [[ "$command_name" == "status" ]]; then
    export MULTICA_CODEX_GUARD_ACTIVE=1
    exec git "${git_args[@]}"
  fi
  if [[ "$command_name" == "push" ]]; then
    printf 'codex-guard: git push guard missing or not executable; refusing push\n' >&2
  else
    printf 'codex-guard: git guard missing or not executable; refusing command: %s\n' "$command_name" >&2
  fi
  exit 126
fi
export MULTICA_CODEX_GUARD_ACTIVE=1
exec "$guard" "$@"
`

func installCodexCommandGuards(env []string, guardBinDir, home string) ([]string, error) {
	if runtime.GOOS == "windows" {
		// The current shims rely on Bash. Keep Windows Codex launches usable until
		// a native wrapper exists; this must not be mistaken for guard coverage.
		return env, nil
	}
	if guardBinDir == "" {
		return nil, fmt.Errorf("codex command guard directory is empty")
	}
	if home == "" {
		return nil, fmt.Errorf("home directory is empty")
	}
	// Each profile owns immutable configuration paths. Atomic file replacement
	// alone cannot stop a later launch from changing a running profile's BASH_ENV
	// (or the pinned guard home). Equal configurations may safely share files.
	originalShellEnv := envValue(env, "BASH_ENV")
	configID := sha256.Sum256([]byte(home + "\x00" + originalShellEnv))
	guardBinDir = filepath.Join(guardBinDir, fmt.Sprintf("%x", configID))
	ghGuard := filepath.Join(home, ".config", "gh-guard", "gh-pr-guard")
	gitGuard := filepath.Join(home, ".config", "git-push-guard", "git-push-guard")
	if configured := envValue(env, "MULTICA_GIT_PUSH_GUARD"); configured != "" {
		gitGuard = configured
	}
	if configured := envValue(env, "MULTICA_GH_PR_GUARD"); configured != "" {
		ghGuard = configured
	}
	if err := writeCodexGuardShim(filepath.Join(guardBinDir, "gh"), renderCodexGuardShim(codexGHShim, ghGuard)); err != nil {
		return nil, fmt.Errorf("install gh command guard: %w", err)
	}
	if err := writeCodexGuardShim(filepath.Join(guardBinDir, "git"), renderCodexGuardShim(codexGitShim, gitGuard)); err != nil {
		return nil, fmt.Errorf("install git command guard: %w", err)
	}
	shellEnvPath := filepath.Join(guardBinDir, codexGuardShellEnvFile)
	quotedGuardBin := "'" + strings.ReplaceAll(guardBinDir, "'", "'\\''") + "'"
	quotedOriginalShellEnv := "'" + strings.ReplaceAll(originalShellEnv, "'", "'\\''") + "'"
	shellEnv := strings.ReplaceAll(codexGuardShellEnv, "__MULTICA_GUARD_BIN__", quotedGuardBin)
	shellEnv = strings.ReplaceAll(shellEnv, "__MULTICA_ORIGINAL_BASH_ENV__", quotedOriginalShellEnv)
	if err := writeCodexGuardShim(filepath.Join(guardBinDir, codexGuardShellEnvFile), shellEnv); err != nil {
		return nil, fmt.Errorf("install Codex shell environment: %w", err)
	}
	path := envValue(env, "PATH")
	if path == "" {
		path = os.Getenv("PATH")
	}
	env = setEnvValue(env, "PATH", guardBinDir+string(os.PathListSeparator)+path)
	env = setEnvValue(env, "BASH_ENV", shellEnvPath)
	env = setEnvValue(env, "MULTICA_CODEX_GUARD_ACTIVE", "")
	return env, nil
}

func renderCodexGuardShim(template, guardPath string) string {
	quoted := "'" + strings.ReplaceAll(guardPath, "'", "'\\''") + "'"
	return strings.ReplaceAll(template, codexGuardPathMarker, quoted)
}

func writeCodexGuardShim(path, body string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".guard-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o700); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func setEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}
