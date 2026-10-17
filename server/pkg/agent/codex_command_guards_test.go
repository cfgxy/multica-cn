package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallCodexCommandGuardsPrependsExecutableShims(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX Codex command guard shims are not supported on Windows")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	guardBin := filepath.Join(root, "codex-home", codexGuardBinDir)
	nativeBin := filepath.Join(root, "native-bin")
	nativePath := nativeBin + string(os.PathListSeparator) + "/usr/bin:/bin"
	if err := os.MkdirAll(nativeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(nativeBin, "git"), "#!/bin/sh\nprintf 'native git\\n'\n")
	writeExecutable(t, filepath.Join(nativeBin, "gh"), "#!/bin/sh\nprintf 'native gh\\n'\n")

	env, err := installCodexCommandGuards([]string{
		"HOME=" + home,
		"PATH=" + nativePath,
	}, guardBin, home)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := envValue(env, "PATH"), guardBin+string(os.PathListSeparator)+nativePath; got != want {
		t.Fatalf("PATH = %q, want %q", got, want)
	}
	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "gh", path: filepath.Join(home, ".config", "gh-guard", "gh-pr-guard")},
		{name: "git", path: filepath.Join(home, ".config", "git-push-guard", "git-push-guard")},
	} {
		body, err := os.ReadFile(filepath.Join(guardBin, tc.name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "'"+tc.path+"'") || strings.Contains(string(body), "/untrusted/") {
			t.Errorf("%s shim does not pin the trusted guard path", tc.name)
		}
	}
	for _, name := range []string{"git", "gh"} {
		info, err := os.Stat(filepath.Join(guardBin, name))
		if err != nil {
			t.Fatalf("stat %s shim: %v", name, err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("%s shim mode = %o, want 700", name, info.Mode().Perm())
		}
	}

	cmd := exec.Command("/bin/bash", "-c", "command -v git; type -a git; command -v gh; type -a gh")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect shell command resolution: %v\n%s", err, out)
	}
	text := string(out)
	t.Logf("non-interactive command resolution:\n%s", strings.TrimSpace(text))
	if !strings.Contains(text, filepath.Join(guardBin, "git")) || !strings.Contains(text, filepath.Join(nativeBin, "git")) {
		t.Fatalf("git shim/native resolution missing from output:\n%s", text)
	}
	if !strings.Contains(text, filepath.Join(guardBin, "gh")) || !strings.Contains(text, filepath.Join(nativeBin, "gh")) {
		t.Fatalf("gh shim/native resolution missing from output:\n%s", text)
	}
}

func TestCodexGuardShimsDelegateWithoutRecursion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX Codex command guard shims are not supported on Windows")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	guardBin := filepath.Join(root, "codex-home", codexGuardBinDir)
	nativeBin := filepath.Join(root, "native-bin")
	guardAlias := filepath.Join(root, "guard-bin-alias")
	nativePath := guardAlias + string(os.PathListSeparator) + nativeBin + string(os.PathListSeparator) + "/usr/bin:/bin"
	guardDir := filepath.Join(home, ".config")
	for _, dir := range []string{nativeBin, guardDir, guardBin} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(guardBin, guardAlias); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(nativeBin, "git"), "#!/bin/sh\nprintf 'native git\\n'\n")
	writeExecutable(t, filepath.Join(nativeBin, "gh"), "#!/bin/sh\nprintf 'native gh\\n'\n")
	gitGuard := filepath.Join(guardDir, "git-push-guard", "git-push-guard")
	ghGuard := filepath.Join(guardDir, "gh-guard", "gh-pr-guard")
	for _, path := range []string{gitGuard, ghGuard} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeExecutable(t, gitGuard, "#!/usr/bin/env bash\nprintf 'git guard native=%s args=%s\\n' \"$(command -v git)\" \"$*\"\n")
	writeExecutable(t, ghGuard, "#!/usr/bin/env bash\nprintf 'gh guard native=%s args=%s\\n' \"$(command -v gh)\" \"$*\"\n")

	env, err := installCodexCommandGuards([]string{
		"HOME=" + home,
		"PATH=" + nativePath,
	}, guardBin, home)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "git", args: []string{"push", "origin", "feature"}, want: "git guard native=" + filepath.Join(nativeBin, "git") + " args=push origin feature"},
		{name: "gh", args: []string{"pr", "create", "--repo", "cfgxy/multica-cn"}, want: "gh guard native=" + filepath.Join(nativeBin, "gh") + " args=pr create --repo cfgxy/multica-cn"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(filepath.Join(guardBin, tc.name), tc.args...)
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run shim: %v\n%s", err, out)
			}
			if !strings.Contains(string(out), tc.want) {
				t.Fatalf("output = %q, want substring %q", out, tc.want)
			}
			t.Logf("%s delegation: %s", tc.name, strings.TrimSpace(string(out)))
		})
	}
}

func TestCodexGuardShimsFailClosedWhenGuardIsMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX Codex command guard shims are not supported on Windows")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	guardBin := filepath.Join(root, "codex-home", codexGuardBinDir)
	nativeBin := filepath.Join(root, "native-bin")
	nativePath := nativeBin + string(os.PathListSeparator) + "/usr/bin:/bin"
	if err := os.MkdirAll(nativeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(nativeBin, "git"), "#!/bin/sh\nprintf 'native git\\n'\n")
	writeExecutable(t, filepath.Join(nativeBin, "gh"), "#!/bin/sh\nprintf 'native gh\\n'\n")
	env, err := installCodexCommandGuards([]string{"HOME=" + home, "PATH=" + nativePath}, guardBin, home)
	if err != nil {
		t.Fatal(err)
	}

	guardPaths := []string{
		filepath.Join(home, ".config", "gh-guard", "gh-pr-guard"),
		filepath.Join(home, ".config", "git-push-guard", "git-push-guard"),
	}
	for _, state := range []string{"missing", "not executable"} {
		if state == "not executable" {
			for _, path := range guardPaths {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, name := range []string{"gh", "git"} {
			t.Run(name+" "+state+" guard", func(t *testing.T) {
				args := []string{"pr", "create"}
				if name == "git" {
					args = []string{"push", "origin", "feature"}
				}
				cmd := exec.Command(filepath.Join(guardBin, name), args...)
				cmd.Env = env
				out, err := cmd.CombinedOutput()
				if err == nil {
					t.Fatalf("%s guard unexpectedly succeeded: %s", state, out)
				}
				if !strings.Contains(string(out), "guard missing or not executable") {
					t.Fatalf("guard failure reason absent from output: %s", out)
				}
			})
		}
		if state == "not executable" {
			for _, path := range guardPaths {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	cmd := exec.Command(filepath.Join(guardBin, "git"), "status", "--short")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("non-push git command should delegate to native git: %v\n%s", err, out)
	}
	if string(out) != "native git\n" {
		t.Fatalf("non-push git output = %q, want native delegation", out)
	}
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}
