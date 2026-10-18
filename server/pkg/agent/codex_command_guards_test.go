package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
	originalBashEnv := filepath.Join(root, "original-bash-env")
	if err := os.WriteFile(originalBashEnv, []byte("export MULTICA_TEST_ORIGINAL_BASH_ENV=loaded\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nativeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(nativeBin, "git"), "#!/bin/sh\nprintf 'native git\\n'\n")
	writeExecutable(t, filepath.Join(nativeBin, "gh"), "#!/bin/sh\nprintf 'native gh\\n'\n")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}

	env, err := installCodexCommandGuards([]string{
		"HOME=" + home,
		"PATH=" + nativePath,
		"BASH_ENV=" + originalBashEnv,
	}, guardBin, home)
	if err != nil {
		t.Fatal(err)
	}
	guardBin = filepath.Dir(envValue(env, "BASH_ENV"))
	if got, want := envValue(env, "PATH"), guardBin+string(os.PathListSeparator)+nativePath; got != want {
		t.Fatalf("PATH = %q, want %q", got, want)
	}
	if got, want := envValue(env, "BASH_ENV"), filepath.Join(guardBin, codexGuardShellEnvFile); got != want {
		t.Fatalf("BASH_ENV = %q, want %q", got, want)
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

	if err := os.WriteFile(filepath.Join(home, ".bash_profile"), []byte("export PATH=\""+nativePath+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/bash", "-lc", "printf 'original=%s\\n' \"$MULTICA_TEST_ORIGINAL_BASH_ENV\"; command -v git; type -a git; command -v gh; type -a gh")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect shell command resolution: %v\n%s", err, out)
	}
	text := string(out)
	t.Logf("non-interactive command resolution:\n%s", strings.TrimSpace(text))
	if !strings.HasPrefix(text, "original=loaded\n") {
		t.Fatalf("existing BASH_ENV was not loaded: %q", text)
	}
	if !strings.Contains(text, "original=loaded\n"+filepath.Join(guardBin, "git")+"\n") || !strings.Contains(text, filepath.Join(nativeBin, "git")) {
		t.Fatalf("git shim/native resolution missing from output:\n%s", text)
	}
	if !strings.Contains(text, "\n"+filepath.Join(guardBin, "gh")+"\n") || !strings.Contains(text, filepath.Join(nativeBin, "gh")) {
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
	originalBashEnv := filepath.Join(root, "original-bash-env")
	if err := os.WriteFile(originalBashEnv, []byte("export PATH='"+guardAlias+"':\"$PATH\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	guardDir := filepath.Join(home, ".config")
	for _, dir := range []string{nativeBin, guardDir, guardBin} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
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
		"BASH_ENV=" + originalBashEnv,
	}, guardBin, home)
	if err != nil {
		t.Fatal(err)
	}
	guardBin = filepath.Dir(envValue(env, "BASH_ENV"))
	if err := os.Symlink(guardBin, guardAlias); err != nil {
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

func TestCodexGitShimUsesTaskMaterializedGuard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX Codex command guard shims are not supported on Windows")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	guardBin := filepath.Join(root, "codex-home", codexGuardBinDir)
	nativeBin := filepath.Join(root, "native-bin")
	taskGuard := filepath.Join(root, "task-policy", "git-push-guard")
	taskGHGuard := filepath.Join(root, "task-policy", "gh-pr-guard")
	if err := os.MkdirAll(nativeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(taskGuard), 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(nativeBin, "git"), "#!/bin/sh\nprintf 'native git\\n'\n")
	writeExecutable(t, filepath.Join(nativeBin, "gh"), "#!/bin/sh\nprintf 'native gh\\n'\n")
	writeExecutable(t, taskGuard, "#!/bin/sh\nprintf 'task guard args=%s\\n' \"$*\"\n")
	writeExecutable(t, taskGHGuard, "#!/bin/sh\nprintf 'task gh guard args=%s\\n' \"$*\"\n")
	env, err := installCodexCommandGuards([]string{
		"HOME=" + home,
		"PATH=" + nativeBin + string(os.PathListSeparator) + "/usr/bin:/bin",
		"MULTICA_GIT_PUSH_GUARD=" + taskGuard,
		"MULTICA_GH_PR_GUARD=" + taskGHGuard,
	}, guardBin, home)
	if err != nil {
		t.Fatal(err)
	}
	shimPath := filepath.Join(filepath.Dir(envValue(env, "BASH_ENV")), "git")
	shim, err := os.ReadFile(shimPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(shim), "'"+taskGuard+"'") {
		t.Fatalf("git shim does not pin task guard path: %s", shim)
	}
	cmd := exec.Command(shimPath, "push", "origin", "feature/task")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "task guard args=push origin feature/task") {
		t.Fatalf("task guard delegation output=%q error=%v", out, err)
	}
	ghCmd := exec.Command(filepath.Join(filepath.Dir(envValue(env, "BASH_ENV")), "gh"), "pr", "create")
	ghOut, ghErr := ghCmd.CombinedOutput()
	if ghErr != nil || !strings.Contains(string(ghOut), "task gh guard args=pr create") {
		t.Fatalf("task gh guard delegation output=%q error=%v", ghOut, ghErr)
	}
}

func TestCodexGHShimRunsTaskMaterializedRUYI401Guard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX Codex command guard shims are not supported on Windows")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	nativeBin := filepath.Join(root, "native-bin")
	guardBin := filepath.Join(root, "codex-home", codexGuardBinDir)
	if err := os.MkdirAll(nativeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(nativeBin, "gh"), "#!/bin/sh\nprintf 'real-gh %s\\n' \"$*\"\n")
	guard, err := filepath.Abs(filepath.Join("..", "..", "internal", "daemon", "guards", "gh-pr-guard"))
	if err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(root, "canonical-repos.conf")
	if err := os.WriteFile(conf, []byte("cfgxy/fork upstream=upstream-owner/source\ncfgxy/multica-cn\ncfgxy/sub2api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env, err := installCodexCommandGuards([]string{
		"HOME=" + home,
		"PATH=" + nativeBin + string(os.PathListSeparator) + "/usr/bin:/bin",
		"MULTICA_GH_PR_GUARD=" + guard,
		"GH_GUARD_CONF=" + conf,
		"GH_GUARD_DRYRUN=1",
	}, guardBin, home)
	if err != nil {
		t.Fatal(err)
	}
	run := func(target, ghRepo string) (string, error) {
		cmd := exec.Command("/bin/bash", "-c", `gh pr create --repo "$TARGET_REPO" --title smoke`)
		for _, entry := range env {
			if strings.HasPrefix(entry, "GH_REPO=") || strings.HasPrefix(entry, "TARGET_REPO=") {
				continue
			}
			cmd.Env = append(cmd.Env, entry)
		}
		cmd.Env = append(cmd.Env, "TARGET_REPO="+target, "GH_REPO="+ghRepo)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("cfgxy/fork", "cfgxy/fork"); err != nil || !strings.Contains(out, "ALLOW") {
		t.Fatalf("canonical GH_REPO output=%q error=%v", out, err)
	}
	for _, canonical := range []string{"cfgxy/multica-cn", "cfgxy/sub2api"} {
		out, err := run(canonical, canonical)
		if err != nil || !strings.Contains(out, "ALLOW") {
			t.Fatalf("canonical regression %q output=%q error=%v", canonical, out, err)
		}
	}
	for _, bad := range []string{"upstream-owner/source", "someone/else"} {
		out, err := run("cfgxy/fork", bad)
		if err == nil || !strings.Contains(out, "gh-guard:") {
			t.Fatalf("misleading GH_REPO=%q for canonical target output=%q error=%v", bad, out, err)
		}
	}
	if out, err := run("upstream-owner/source", "upstream-owner/source"); err == nil || !strings.Contains(out, "upstream") {
		t.Fatalf("confirmed upstream output=%q error=%v", out, err)
	}
	if out, err := run("someone/else", "someone/else"); err == nil || !strings.Contains(out, "未登记 canonical") {
		t.Fatalf("non-canonical target output=%q error=%v", out, err)
	}
	missingEnv := append([]string(nil), env...)
	for i, entry := range missingEnv {
		if strings.HasPrefix(entry, "GH_GUARD_CONF=") {
			missingEnv[i] = "GH_GUARD_CONF=" + filepath.Join(root, "missing.conf")
		}
	}
	cmd := exec.Command("/bin/bash", "-c", "gh pr create --repo cfgxy/fork")
	cmd.Env = append(missingEnv, "GH_REPO=")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "登记文件不存在") {
		t.Fatalf("missing config output=%q error=%v", out, err)
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
	nativeGitLog := filepath.Join(root, "native-git.log")
	gitConfig := filepath.Join(root, "gitconfig")
	nativePath := nativeBin + string(os.PathListSeparator) + "/usr/bin:/bin"
	if err := os.MkdirAll(nativeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(nativeBin, "git"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$NATIVE_GIT_LOG\"\nprintf 'native git\\n'\n")
	writeExecutable(t, filepath.Join(nativeBin, "gh"), "#!/bin/sh\nprintf 'native gh\\n'\n")
	if err := os.WriteFile(nativeGitLog, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gitConfig, []byte("[alias]\n\tpublish-feature = !git push origin feature\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env, err := installCodexCommandGuards([]string{"HOME=" + home, "PATH=" + nativePath, "NATIVE_GIT_LOG=" + nativeGitLog, "GIT_CONFIG_GLOBAL=" + gitConfig}, guardBin, home)
	if err != nil {
		t.Fatal(err)
	}

	guardBin = filepath.Dir(envValue(env, "BASH_ENV"))
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
		for _, tc := range []struct {
			name string
			args []string
		}{
			{name: "send-pack", args: []string{"send-pack", "origin", "refs/heads/feature"}},
			{name: "alias indirect push", args: []string{"publish-feature"}},
		} {
			t.Run(tc.name+" with "+state+" guard", func(t *testing.T) {
				cmd := exec.Command(filepath.Join(guardBin, "git"), tc.args...)
				cmd.Env = env
				out, err := cmd.CombinedOutput()
				if err == nil {
					t.Fatalf("%s unexpectedly succeeded: %s", state, out)
				}
				if !strings.Contains(string(out), "git guard missing or not executable") {
					t.Fatalf("guard failure reason absent from output: %s", out)
				}
				if strings.Contains(string(out), "native git") {
					t.Fatalf("rejected command reached native git: %s", out)
				}
			})
		}
		calls, err := os.ReadFile(nativeGitLog)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(calls), "send-pack") || strings.Contains(string(calls), "publish-feature") || strings.Contains(string(calls), "push origin feature") {
			t.Fatalf("rejected remote write reached native git: %q", calls)
		}
		cmd := exec.Command(filepath.Join(guardBin, "git"), "-C", repo, "status", "--porcelain")
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("read-only git status with %s guard should delegate to native git: %v\n%s", state, err, out)
		}
		if string(out) != "native git\n" {
			t.Fatalf("git status output with %s guard = %q, want native delegation", state, out)
		}
		if state == "not executable" {
			for _, path := range guardPaths {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	calls, err := os.ReadFile(nativeGitLog)
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := "-C " + repo + " status --porcelain\n-C " + repo + " status --porcelain\n"
	if string(calls) != wantCalls {
		t.Fatalf("native git args = %q, want %q", calls, wantCalls)
	}
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}

// Starting a second profile must not mutate the startup script of a running one.
func TestCodexGuardProfilesDoNotOverwriteRunningEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shims")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	bin := filepath.Join(root, "guards")
	var environments [][]string
	var inputs [][]string
	for _, profile := range []string{"first", "second"} {
		original := filepath.Join(root, profile+"-env")
		if err := os.WriteFile(original, []byte("export PROFILE="+profile+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		input := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "BASH_ENV=" + original}
		inputs = append(inputs, input)
		env, err := installCodexCommandGuards(input, bin, home)
		if err != nil {
			t.Fatal(err)
		}
		environments = append(environments, env)
	}
	// Simulate overlapping launches/restarts of both profiles. Old processes
	// must keep their environment, and same-profile installs must remain atomic.
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := installCodexCommandGuards(inputs[i%2], bin, home)
			errors <- err
		}(i)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	for i, profile := range []string{"first", "second"} {
		cmd := exec.Command("/bin/bash", "-c", `printf '%s' "$PROFILE"`)
		cmd.Env = environments[i]
		out, err := cmd.CombinedOutput()
		if err != nil || string(out) != profile {
			t.Fatalf("profile %s: got %q, error %v", profile, out, err)
		}
	}
}

func TestCodexGuardInstallFailureDoesNotReturnUnguardedEnvironment(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "file")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ bin, home string }{
		{"", root}, {filepath.Join(root, "guards"), ""}, {filepath.Join(blocked, "guards"), root},
	} {
		env, err := installCodexCommandGuards([]string{"PATH=/usr/bin:/bin"}, tc.bin, tc.home)
		if err == nil || env != nil {
			t.Fatalf("failed install returned env %v, err %v", env, err)
		}
	}
}
