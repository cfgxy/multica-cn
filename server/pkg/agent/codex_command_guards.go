package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	codexGuardBinDir     = "command-guards"
	codexGuardPathMarker = "__MULTICA_GUARD_PATH__"
)

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
  while (($#)); do
    case "$1" in
      -c|-C|--git-dir|--work-tree|--namespace|--super-prefix) shift 2 || break ;;
      --git-dir=*|--work-tree=*|--namespace=*|--super-prefix=*|-c*|-C*) shift ;;
      -*) shift ;;
      *) command_name="$1"; break ;;
    esac
  done
  if [[ "$command_name" == "push" ]]; then
    printf 'codex-guard: git push guard missing or not executable; refusing push\n' >&2
    exit 126
  fi
  exec git "$@"
fi
exec "$guard" "$@"
`

func installCodexCommandGuards(env []string, guardBinDir, home string) ([]string, error) {
	if runtime.GOOS == "windows" {
		return nil, fmt.Errorf("POSIX command guard shims are not supported on Windows")
	}
	if guardBinDir == "" {
		return nil, fmt.Errorf("codex command guard directory is empty")
	}
	if home == "" {
		return nil, fmt.Errorf("home directory is empty")
	}
	ghGuard := filepath.Join(home, ".config", "gh-guard", "gh-pr-guard")
	gitGuard := filepath.Join(home, ".config", "git-push-guard", "git-push-guard")
	if err := writeCodexGuardShim(filepath.Join(guardBinDir, "gh"), renderCodexGuardShim(codexGHShim, ghGuard)); err != nil {
		return nil, fmt.Errorf("install gh command guard: %w", err)
	}
	if err := writeCodexGuardShim(filepath.Join(guardBinDir, "git"), renderCodexGuardShim(codexGitShim, gitGuard)); err != nil {
		return nil, fmt.Errorf("install git command guard: %w", err)
	}
	path := envValue(env, "PATH")
	if path == "" {
		path = os.Getenv("PATH")
	}
	env = setEnvValue(env, "PATH", guardBinDir+string(os.PathListSeparator)+path)
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
