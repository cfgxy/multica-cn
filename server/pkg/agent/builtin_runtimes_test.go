package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAdminInstallDescriptorsUseTargetUserEnvironment(t *testing.T) {
	for _, rt := range ProtocolFamilyInstalls {
		if !strings.Contains(rt.InstallCommand, "runuser -u {{user}}") {
			t.Errorf("%s does not switch to the requested user", rt.ID)
		}
		if !rt.LatestOnly && !strings.Contains(rt.InstallCommand, "{{version}}") {
			t.Errorf("%s advertises version pinning but discards the version", rt.ID)
		}
	}
}

func TestRuntimeInstallerTimeoutStopsNewSessionChildren(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required to simulate runuser's session boundary")
	}
	dir := t.TempDir()
	// Simulate runuser's session without root and use only a test-created CLI.
	runuser := "#!" + python + "\nimport os,sys\nos.setsid()\nos.execvp(sys.argv[4],sys.argv[4:])\n"
	if err := os.WriteFile(filepath.Join(dir, "runuser"), []byte(runuser), 0700); err != nil {
		t.Fatal(err)
	}
	command := runtimeUserCommand(`(sleep 1; touch "$HOME/escaped") & wait`)
	command = strings.Replace(command, "270s", "0.1s", 1)
	command = strings.Replace(command, "10s", "0.1s", 1)
	cmd := exec.Command("sh", "-c", strings.ReplaceAll(command, "{{user}}", "tester"))
	cmd.Env = []string{"HOME=" + dir, "PATH=" + dir + ":/usr/bin:/bin"}
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 124 {
		t.Fatalf("expected installer timeout, got %v: %s", err, out)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(dir, "escaped")); !os.IsNotExist(err) {
		t.Fatalf("installer child survived timeout: %v", err)
	}
}

func TestRuntimeInstallCommands(t *testing.T) {
	for _, tc := range []struct {
		name, id, version, args      string
		downloadFails, missingBinary bool
	}{
		{name: "private npm", id: "codex", version: "1.2.3"},
		{name: "npm latest checks registry", id: "codex", version: "latest"},
		{name: "grok native directory", id: "grok", version: "1.2.3", args: "1.2.3\n"},
		{name: "grok latest", id: "grok", version: "latest", args: "\n"},
		{name: "kimi native directory", id: "kimi", version: "1.2.3", args: "--version\n1.2.3\n"},
		{name: "kimi latest without version argument", id: "kimi", version: "latest", args: "\n"},
		{name: "omp standalone without bun", id: "omp", version: "1.2.3", args: "--binary\n--ref\nv1.2.3\n"},
		{name: "omp release tag", id: "omp", version: "v1.2.3", args: "--binary\n--ref\nv1.2.3\n"},
		{name: "omp latest", id: "omp", version: "latest", args: "--binary\n"},
		{name: "download fails without executing partial script", id: "grok", version: "1.2.3", downloadFails: true},
		{name: "installer succeeds without binary", id: "grok", version: "1.2.3", missingBinary: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, script := range map[string]string{
				"runuser": `shift 3; exec "$@"`,
				"node":    `exit 0`,
				"npm": `test "$NPM_CONFIG_PREFIX" = "$HOME/.local" || exit 42
printf '%s\n' "$@" > "$HOME/npm-args"
mkdir -p "$HOME/.local/bin"
printf '#!/bin/sh\nexit 0\n' > "$HOME/.local/bin/codex"
chmod +x "$HOME/.local/bin/codex"`,
				"curl": `while [ "$#" -gt 0 ]; do
if [ "$1" = "-o" ]; then shift; break; fi
shift
done
cat > "$1" <<'INSTALLER'
touch "$HOME/executed"
printf '%s\n' "$@" > "$HOME/installer-args"
if [ "$MISSING_BINARY" = 1 ]; then exit 0; fi
case "$INSTALL_TARGET" in
 grok) test "${1:-}" != latest || exit 45; bindir="$HOME/.grok/bin";;
 kimi) test "$KIMI_INSTALL_DIR" = "$HOME/.kimi-code" || exit 43; test -z "$KIMI_VERSION" || exit 44; bindir="$KIMI_INSTALL_DIR/bin";;
 omp) test "$PI_INSTALL_DIR" = "$HOME/.local/bin" || exit 43; bindir="$PI_INSTALL_DIR";;
esac
mkdir -p "$bindir"
printf '#!/bin/sh\nexit 0\n' > "$bindir/$INSTALL_TARGET"
chmod +x "$bindir/$INSTALL_TARGET"
INSTALLER
exit "$DOWNLOAD_EXIT"`,
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			rt, _ := ProtocolFamilyInstallByID(tc.id)
			command := rt.InstallCommand
			if tc.id == "omp" {
				builtin, _ := BuiltinRuntimeByID("omp")
				command = builtin.InstallCommand
			}
			command = strings.ReplaceAll(command, "{{user}}", "alice")
			command = strings.ReplaceAll(command, "{{version}}", tc.version)
			command = strings.ReplaceAll(command, ":/usr/local/bin:/usr/bin:/bin", ":"+dir+":/usr/bin:/bin")
			if tc.missingBinary {
				// Never fall through to a developer-installed vendor executable.
				command = strings.ReplaceAll(command, "grok --version", filepath.Join(dir, "absent-grok")+" --version")
			}
			cmd := exec.Command("sh", "-c", command)
			cmd.Env = append(os.Environ(), "HOME="+dir, "PATH="+dir+":/usr/bin:/bin", "INSTALL_TARGET="+tc.id, "DOWNLOAD_EXIT=0", "MISSING_BINARY=0")
			if tc.downloadFails {
				cmd.Env = append(cmd.Env, "DOWNLOAD_EXIT=22")
			}
			if tc.missingBinary {
				cmd.Env = append(cmd.Env, "MISSING_BINARY=1")
			}
			out, err := cmd.CombinedOutput()
			if (err != nil) != (tc.downloadFails || tc.missingBinary) {
				t.Fatalf("unexpected result: %v: %s", err, out)
			}
			if tc.downloadFails {
				if _, err := os.Stat(filepath.Join(dir, "executed")); !os.IsNotExist(err) {
					t.Fatal("executed a partial download")
				}
			} else if !tc.missingBinary && tc.id != "codex" {
				args, err := os.ReadFile(filepath.Join(dir, "installer-args"))
				if err != nil || string(args) != tc.args {
					t.Fatalf("installer arguments: %q, %v", args, err)
				}
			}
			if tc.id == "codex" {
				args, err := os.ReadFile(filepath.Join(dir, "npm-args"))
				if err != nil || !strings.Contains(string(args), dir+"/.local") || !strings.Contains(string(args), "@openai/codex@"+tc.version) {
					t.Fatalf("wrong npm destination/package: %s, %v", args, err)
				}
				wantCache := "--prefer-offline"
				if tc.version == "latest" {
					wantCache = "--prefer-online"
				}
				if !strings.Contains(string(args), wantCache+"\n") {
					t.Fatalf("wrong npm cache policy for %s: %s", tc.version, args)
				}
			}
		})
	}
}
