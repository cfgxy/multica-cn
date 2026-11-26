package computer

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runWriter(t *testing.T, home string, f CredentialFiles, profile ...string) error {
	t.Helper()
	script := strings.Replace(writeFilesScript, "home=pwd.getpwuid(os.getuid()).pw_dir", `home=os.environ["COMPUTER_TEST_HOME"]`, 1)
	cmd := exec.Command("python3", append([]string{"-c", script}, profile...)...)
	cmd.Env = append(os.Environ(), "COMPUTER_TEST_HOME="+home)
	cmd.Stdin = strings.NewReader(f.Gitconfig + "\x00" + f.GitLabToken + "\x00" + f.ModelEnv + "\x00" + f.MulticaConfig + "\x00" + f.GitSSHKey + "\x00" + f.GitKnownHosts)
	return cmd.Run()
}
func TestCredentialWriterPreservesConfigAndRefusesSymlinks(t *testing.T) {
	home := t.TempDir()
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".multica"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".multica/config.json"), []byte(`{"device_name":"keep me","token":"old"}`), 0600); err != nil {
		t.Fatal(err)
	}
	old := "[credential]\n helper = keep-existing\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := RenderFiles("https://multica.example", "A \"B\" #C", "a@company.example", "https://gitlab.example", "fake-token", "OPENAI_API_KEY=fake model key", "mul_fake")
	if err != nil {
		t.Fatal(err)
	}
	if err := runWriter(t, home, f); err != nil {
		t.Fatal(err)
	}
	if err := runWriter(t, home, f); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".gitconfig"))
	if !strings.HasPrefix(string(b), old) || strings.Count(string(b), "[include]") != 1 {
		t.Fatal("config not preserved or include duplicated")
	}
	b, _ = os.ReadFile(filepath.Join(home, ".multica/config.json"))
	var m map[string]any
	if json.Unmarshal(b, &m) != nil || m["device_name"] != "keep me" || m["token"] != "mul_fake" {
		t.Fatal("config merge failed")
	}
	info, _ := os.Stat(filepath.Join(home, ".config/multica-provision/gitlab.token"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("secret permissions")
	}
	cmd := exec.Command("git", "config", "--file", filepath.Join(home, ".config/multica-provision/gitconfig"), "user.name")
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "A \"B\" #C" {
		t.Fatal("Git identity did not round trip")
	}
	if err := runWriter(t, home, f, "computer-test"); err != nil {
		t.Fatal(err)
	}
	profileConfig, _ := os.ReadFile(filepath.Join(home, ".multica/profiles/computer-test/config.json"))
	if !strings.Contains(string(profileConfig), "workspaces_root") {
		t.Fatal("managed profile missing private workspace root")
	}
	defaultConfig, _ := os.ReadFile(filepath.Join(home, ".multica/config.json"))
	if string(defaultConfig) != string(b) {
		t.Fatal("managed profile modified default config")
	}
	blocked := t.TempDir()
	if err := os.Symlink(other, filepath.Join(blocked, ".config")); err != nil {
		t.Fatal(err)
	}
	if err := runWriter(t, blocked, f); err == nil {
		t.Fatal("followed directory symlink")
	}
	entries, _ := os.ReadDir(other)
	if len(entries) != 0 {
		t.Fatal("wrote outside home")
	}
}
func TestPasswordTransportDistinguishesMismatchFromInfrastructure(t *testing.T) {
	remote := SSHRemote{Host: "example.com", Port: 22, User: "operator", KeyPath: "/fake/key"}
	for _, code := range []string{"1", "42", "43"} {
		cmd := exec.Command("sh", "-c", "exit "+code)
		err := cmd.Run()
		remote.RunCmd = &recordRunner{err: err}
		ok, got := remote.PasswordMatches("alice", "fake-password")
		if ok {
			t.Fatal("unexpected match")
		}
		if (got == nil) != (code == "42") {
			t.Fatalf("exit %s mismatch classification: %v", code, got)
		}
	}
}
func TestExecTimeout(t *testing.T) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := (execRunner{}).Run(ctx, []string{"sleep", "2"}, "")
	if err == nil || time.Since(start) > time.Second {
		t.Fatal("command was not bounded")
	}
}
func TestRedactionOverlappingSecrets(t *testing.T) {
	got := redactText("abc ab redacted", "ab", "abc", "redacted")
	if got != "[redacted] [redacted] [redacted]" {
		t.Fatalf("bad redaction: %s", got)
	}
}

func TestCreateUserCompensatesPasswordFailure(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "created")
	for name, script := range map[string]string{
		"apt-get":  "#!/bin/sh\nexit 0\n",
		"useradd":  "#!/bin/sh\n: > \"$COMPUTER_TEST_MARKER\"\n",
		"chpasswd": "#!/bin/sh\ncat >/dev/null\nexit 1\n",
		"userdel":  "#!/bin/sh\nrm -- \"$COMPUTER_TEST_MARKER\"\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	rec := &recordRunner{}
	r := SSHRemote{Host: "test", Port: 22, User: "operator", KeyPath: "/fake/key", RunCmd: rec}
	if err := r.CreateUser("alice", "fake-password"); err != nil {
		t.Fatal(err)
	}
	command := strings.TrimPrefix(rec.argv[len(rec.argv)-1], "sudo -n ")
	cmd := exec.Command("sh", "-c", command)
	cmd.Env = append(os.Environ(), "PATH="+dir+":/usr/bin:/bin", "COMPUTER_TEST_MARKER="+marker)
	cmd.Stdin = strings.NewReader(rec.stdin)
	if err := cmd.Run(); err == nil {
		t.Fatal("failed password setup reported success")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("new account was not compensated")
	}
}

func TestDeniedAttemptsDoNotExtendLockout(t *testing.T) {
	dir := privateTempDir(t)
	store := &FileStore{Dir: dir}
	if err := store.WithKey("machine", "alice", func(a Attempt) error { _, _, err := a.Reserve(1); return err }); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "machine/alice.attempts")
	old := time.Now().Add(-14 * time.Minute)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	if err := store.WithKey("machine", "alice", func(a Attempt) error {
		_, allowed, err := a.Reserve(1)
		if allowed {
			t.Fatal("limit ignored")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(file)
	if time.Since(info.ModTime()) < 13*time.Minute {
		t.Fatal("denial extended lockout")
	}
	old = time.Now().Add(-16 * time.Minute)
	_ = os.Chtimes(file, old, old)
	if err := store.WithKey("machine", "alice", func(a Attempt) error {
		n, allowed, err := a.Reserve(1)
		if !allowed || n != 1 {
			t.Fatal("lockout did not expire")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRemotePythonScriptsParse(t *testing.T) {
	rec := &scriptRunner{}
	binary := filepath.Join(t.TempDir(), "fake-cli")
	if err := os.WriteFile(binary, []byte("not executed"), 0600); err != nil {
		t.Fatal(err)
	}
	remote := SSHRemote{Host: "fake", Port: 22, User: "operator", KeyPath: "/fake/key", RunCmd: rec, DaemonID: "test-daemon", BinaryPath: binary}
	_, _ = remote.PasswordMatches("alice", "fake-password")
	for _, err := range []error{remote.CreateUser("alice", "fake-password"), remote.Prepare(), remote.InstallDaemon("alice"), remote.RemoveDaemon("alice")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, call := range rec.calls {
		command := call.argv[len(call.argv)-1]
		if !strings.Contains(command, "python3 -c") {
			continue
		}
		cmd := exec.Command("python3", "-c", `import ast,shlex,sys; a=shlex.split(sys.stdin.read()); ast.parse(a[a.index("-c")+1])`)
		cmd.Stdin = strings.NewReader(command)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("embedded Python does not parse: %s", out)
		}
	}
}

func TestPAMSudoPrivilegeCheckFailsClosed(t *testing.T) {
	start := strings.Index(pamScript, "env=dict(os.environ,LC_ALL=")
	end := strings.Index(pamScript[start:], "pw=sys.stdin") + start
	for _, tc := range []struct {
		name, out, stderr string
		code              int
		allow             bool
	}{
		{"ubuntu-denial-zero", "User alice is not allowed to run sudo on test-host.", "", 0, true},
		{"denial-one", "User alice is not allowed to run sudo on test-host.", "", 1, true},
		{"privileged", "User alice may run the following commands on test-host:\n (ALL) ALL", "", 0, false},
		{"silent-error", "", "", 1, false},
		{"config-error", "User alice is not allowed to run sudo on test-host.", "sudoers error", 1, false},
		{"unknown-status", "User alice is not allowed to run sudo on test-host.", "", 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, _ := json.Marshal(map[string]any{"out": tc.out, "err": tc.stderr, "code": tc.code})
			prefix := `import os,sys,subprocess,json,types
c=json.loads(sys.stdin.read()); u="alice"
os.uname=lambda: types.SimpleNamespace(nodename="test-host")
def run(*args,**kwargs):
    assert kwargs["env"]["LC_ALL"]=="C"
    return types.SimpleNamespace(stdout=c["out"],stderr=c["err"],returncode=c["code"])
subprocess.run=run
`
			cmd := exec.Command("python3", "-c", prefix+pamScript[start:end])
			cmd.Stdin = strings.NewReader(string(payload))
			err := cmd.Run()
			if (err == nil) != tc.allow {
				t.Fatalf("unexpected policy decision: %v", err)
			}
		})
	}
}

func TestCreateUserInstallerFailures(t *testing.T) {
	for _, stage := range []string{"apt", "apt-timeout"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			for name, script := range map[string]string{
				"apt-get": `if [ "$FAIL_STAGE" = apt-timeout ]; then
(sh -c 'sleep 0.4; touch "$TEST_DIR/child-survived"') &
wait
else test "$FAIL_STAGE" != apt; fi`,
				"useradd":  `touch "$TEST_DIR/account"; touch "$TEST_DIR/created"`,
				"userdel":  `rm "$TEST_DIR/account"`,
				"chpasswd": `cat >/dev/null`,
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			rec := &recordRunner{}
			remote := SSHRemote{Host: "fake", Port: 22, User: "operator", KeyPath: "/fake", RunCmd: rec}
			if err := remote.CreateUser("alice", "test-password"); err != nil {
				t.Fatal(err)
			}
			command := strings.TrimPrefix(rec.argv[len(rec.argv)-1], "sudo -n ")
			command = strings.ReplaceAll(command, "p.wait(timeout=120)", "p.wait(timeout=0.1)")
			cmd := exec.Command("sh", "-c", command)
			cmd.Env = append(os.Environ(), "PATH="+dir+":/usr/bin:/bin", "FAIL_STAGE="+stage, "TEST_DIR="+dir)
			cmd.Stdin = strings.NewReader(rec.stdin)
			if out, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("failure accepted: %s", out)
			}
			if _, err := os.Stat(filepath.Join(dir, "account")); !os.IsNotExist(err) {
				t.Fatal("left a partially initialized account")
			}
			if stage == "apt-timeout" {
				time.Sleep(500 * time.Millisecond)
				if _, err := os.Stat(filepath.Join(dir, "child-survived")); !os.IsNotExist(err) {
					t.Fatal("apt child survived installer timeout")
				}
			}
			_, created := os.Stat(filepath.Join(dir, "created"))
			if strings.HasPrefix(stage, "apt") && !os.IsNotExist(created) {
				t.Fatal("created an account before system prerequisites succeeded")
			}
		})
	}
}

// Shell initialization must not depend on customization downloads.
func TestCreateUserDoesNotRequireShellCustomizationDownload(t *testing.T) {
	dir := t.TempDir()
	for name, script := range map[string]string{
		"apt-get": "exit 0", "useradd": `touch "$TEST_DIR/account"`, "chpasswd": "cat >/dev/null",
		"userdel": `touch "$TEST_DIR/deleted"`, "runuser": `touch "$TEST_DIR/network"; exit 99`,
		"curl": `touch "$TEST_DIR/network"; exit 28`, "git": `touch "$TEST_DIR/network"; exit 99`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	rec := &recordRunner{}
	remote := SSHRemote{Host: "fake", Port: 22, User: "operator", KeyPath: "/fake", RunCmd: rec}
	if err := remote.CreateUser("alice", "test-password"); err != nil {
		t.Fatal(err)
	}
	command := strings.TrimPrefix(rec.argv[len(rec.argv)-1], "sudo -n ")
	command = strings.Replace(command, "account=pwd.getpwnam(u)", `account=type("Account",(),dict(pw_dir=os.environ["TEST_DIR"],pw_uid=os.getuid(),pw_gid=os.getgid()))()`, 1)
	cmd := exec.Command("sh", "-c", command)
	cmd.Env = append(os.Environ(), "PATH="+dir+":/usr/bin:/bin", "TEST_DIR="+dir)
	cmd.Stdin = strings.NewReader(rec.stdin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("account depends on shell customization: %v %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "account")); err != nil {
		t.Fatal("account not created")
	}
	if body, err := os.ReadFile(filepath.Join(dir, ".zshrc")); err != nil || len(body) == 0 {
		t.Fatalf("new account lacks zsh startup configuration: %v", err)
	}
	for _, name := range []string{"network", "deleted"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("unexpected step: %s", name)
		}
	}
}

func runZshInitializer(t *testing.T, home string) error {
	t.Helper()
	script := strings.Replace(initializeZshScript, "account=pwd.getpwnam(u)", `account=type("Account",(),dict(pw_dir=os.environ["COMPUTER_TEST_HOME"],pw_uid=os.getuid(),pw_gid=os.getgid()))()`, 1)
	cmd := exec.Command("python3", "-c", script+"\ninitialize_zsh(\"test-user\")\n")
	cmd.Env = append(os.Environ(), "COMPUTER_TEST_HOME="+home)
	return cmd.Run()
}

func TestInitializeZshPreservesStartupFiles(t *testing.T) {
	for _, kind := range []string{"regular", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			startup := filepath.Join(home, ".zshrc")
			target := startup
			if kind == "symlink" {
				target = filepath.Join(t.TempDir(), "shared-zshrc")
				if err := os.Symlink(target, startup); err != nil {
					t.Fatal(err)
				}
			}
			original := "# Existing user or system skeleton configuration\n"
			if err := os.WriteFile(target, []byte(original), 0640); err != nil {
				t.Fatal(err)
			}
			if err := runZshInitializer(t, home); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(startup)
			if err != nil || string(got) != original {
				t.Fatalf("startup configuration replaced: %v", err)
			}
			info, err := os.Stat(target)
			if err != nil || info.Mode().Perm() != 0640 {
				t.Fatal("existing permissions changed")
			}
			if kind == "symlink" {
				info, err := os.Lstat(startup)
				if err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatal("existing symlink replaced")
				}
			}
		})
	}
}

func TestInitializeZshRejectsHomeSymlink(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := runZshInitializer(t, link); err == nil {
		t.Fatal("followed home symlink")
	}
	if _, err := os.Stat(filepath.Join(target, ".zshrc")); !os.IsNotExist(err) {
		t.Fatal("wrote outside home")
	}
}

func TestInitializeZshFirstLogin(t *testing.T) {
	home := t.TempDir()
	if err := runZshInitializer(t, home); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(home, ".zshrc"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("startup file not private: %v", err)
	}
	// Use only a disposable home, never the developer's shell configuration.
	t.Run("interactive-shell", func(t *testing.T) {
		zsh, err := exec.LookPath("zsh")
		if err != nil {
			t.Skip("zsh is not installed")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, zsh, "-d", "-i", "-c", `print -r -- "$HISTFILE"; print -r -- "$path[1]"`)
		cmd.Env = []string{"HOME=" + home, "ZDOTDIR=" + home, "PATH=/usr/bin:/bin", "TERM=dumb"}
		output, err := cmd.CombinedOutput()
		want := home + "/.zsh_history\n" + home + "/.local/bin\n"
		if err != nil || string(output) != want {
			t.Fatalf("first login prompted or warned: %v\n%s", err, output)
		}
	})
}
