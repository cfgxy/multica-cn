package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareTaskGitPushGuardBuildsIsolatedCanonicalPolicy(t *testing.T) {
	root := t.TempDir()
	guard, config, err := prepareTaskGitPushGuard(root, "task-shan-465", []RepoData{
		{URL: "https://github.com/cfgxy/sub2api.git"},
		{URL: "git@github.com:cfgxy/multica-cn.git"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := filepath.Base(guard), "git-push-guard"; got != want {
		t.Fatalf("guard path base = %q, want %q", got, want)
	}
	policy, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(policy), "cfgxy/sub2api\ncfgxy/multica-cn\n"; got != want {
		t.Fatalf("canonical policy = %q, want %q", got, want)
	}
	if info, err := os.Stat(guard); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("guard mode: info=%v err=%v", info, err)
	}
	otherGuard, otherConfig, err := prepareTaskGitPushGuard(root, "another-task", []RepoData{{URL: "https://github.com/cfgxy/multica-cn"}})
	if err != nil {
		t.Fatal(err)
	}
	if guard == otherGuard || config == otherConfig {
		t.Fatal("task guard and policy paths must be isolated")
	}
}

func TestPrepareTaskGitPushGuardRejectsUntrustedResourcesAndDrift(t *testing.T) {
	for _, raw := range []string{
		"https://github.com.evil.test/cfgxy/repo",
		"https://gitlab.com/cfgxy/repo",
		"https://github.com/cfgxy/repo/tree/main",
		"https://user:secret@github.com/cfgxy/repo",
	} {
		t.Run(raw, func(t *testing.T) {
			_, _, err := prepareTaskGitPushGuard(t.TempDir(), "task", []RepoData{{URL: raw}})
			if err == nil || !strings.Contains(err.Error(), "confirm its GitHub URL") {
				t.Fatalf("untrusted resource error = %v", err)
			}
		})
	}

	root := t.TempDir()
	_, config, err := prepareTaskGitPushGuard(root, "task", []RepoData{{URL: "https://github.com/cfgxy/multica-cn"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("cfgxy/attacker/repo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = prepareTaskGitPushGuard(root, "task", []RepoData{{URL: "https://github.com/cfgxy/multica-cn"}})
	if err == nil || !strings.Contains(err.Error(), "policy drift detected") {
		t.Fatalf("drift error = %v", err)
	}
}

func TestEmbeddedGitPushGuardAllowsCanonicalAndRejectsUpstream(t *testing.T) {
	root := t.TempDir()
	guardPath, configPath, err := prepareTaskGitPushGuard(root, "task", []RepoData{
		{URL: "https://github.com/cfgxy/multica-cn"},
		{URL: "https://github.com/cfgxy/sub2api"},
	})
	if err != nil {
		t.Fatal(err)
	}
	stubDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(stubDir, 0o700); err != nil {
		t.Fatal(err)
	}
	gitStub := filepath.Join(stubDir, "git")
	stub := "#!/bin/sh\ncase \"$*\" in\n  'remote get-url origin') echo \"${ORIGIN_URL:-https://github.com/cfgxy/multica-cn.git}\" ;;\n  'remote get-url upstream') echo \"${UPSTREAM_URL:-https://github.com/multica-ai/multica.git}\" ;;\n  'config --get push.default') echo simple ;;\n  'rev-parse --abbrev-ref HEAD') echo feature/task ;;\n  *) echo delegated; exit 0 ;;\nesac\n"
	if err := os.WriteFile(gitStub, []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	baseEnv := []string{"HOME=" + root, "PATH=" + stubDir + string(os.PathListSeparator) + os.Getenv("PATH"), "PUSH_GUARD_CONF=" + configPath}
	run := func(remote string, env []string) (string, error) {
		cmd := exec.Command(guardPath, "push", remote, "feature/task")
		cmd.Env = append(append([]string(nil), baseEnv...), env...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("origin", nil); err != nil || !strings.Contains(out, "delegated") {
		t.Fatalf("canonical push output=%q error=%v", out, err)
	}
	if out, err := run("origin", []string{"ORIGIN_URL=https://github.com/cfgxy/sub2api.git"}); err != nil || !strings.Contains(out, "delegated") {
		t.Fatalf("SHAN-465 canonical push output=%q error=%v", out, err)
	}
	if out, err := run("upstream", nil); err == nil || !strings.Contains(out, "白名单外") {
		t.Fatalf("upstream push output=%q error=%v", out, err)
	}
	if out, err := run("origin", []string{"ORIGIN_URL=https://github.com/multica-ai/multica.git"}); err == nil || !strings.Contains(out, "白名单外") {
		t.Fatalf("misdirected canonical remote output=%q error=%v", out, err)
	}
}
