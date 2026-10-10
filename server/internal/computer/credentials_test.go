package computer

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func readTestSettings(t *testing.T, home string) (Settings, error) {
	t.Helper()
	script := strings.Replace(readSettingsScript, "home=pwd.getpwuid(os.getuid()).pw_dir", `home=os.environ["COMPUTER_TEST_HOME"]`, 1)
	cmd := exec.Command("python3", "-c", script, "computer-test")
	cmd.Env = append(os.Environ(), "COMPUTER_TEST_HOME="+home)
	out, err := cmd.Output()
	if err != nil {
		return Settings{}, err
	}
	var settings Settings
	err = json.Unmarshal(out, &settings)
	return settings, err
}

func TestCredentialReadWriteRoundTrip(t *testing.T) {
	home := t.TempDir()
	f, err := RenderFiles("https://multica.example", `A "B"`, "a@example.com", "https://git.example:8443", "test-git-token", "OPENAI_API_KEY=fake key with spaces", "mul_fake")
	if err != nil {
		t.Fatal(err)
	}
	f.GitSSHKey, f.GitKnownHosts = "fake private key", "fake known hosts"
	if err := runWriter(t, home, f, "computer-test"); err != nil {
		t.Fatal(err)
	}
	s, err := readTestSettings(t, home)
	if err != nil {
		t.Fatal(err)
	}
	if s.GitName != `A "B"` || s.GitEmail != "a@example.com" || s.GitLabURL != "https://git.example:8443" || s.GitLabToken != "test-git-token" || s.MulticaPAT != "mul_fake" || s.GitSSHKey != f.GitSSHKey || s.GitKnownHosts != f.GitKnownHosts || s.ModelEnv != "OPENAI_API_KEY=fake key with spaces" {
		t.Fatal("credential round trip mismatch")
	}
	merged := MergeSettings(s, Settings{GitName: "Changed"})
	if merged.GitName != "Changed" || merged.MulticaPAT != s.MulticaPAT || merged.ModelEnv != s.ModelEnv {
		t.Fatal("partial write erased unrelated settings")
	}
}

func TestReadCredentialsEmptyAccountAndManualGitIdentity(t *testing.T) {
	home := t.TempDir()
	s, err := readTestSettings(t, home)
	if err != nil || s != (Settings{}) {
		t.Fatalf("empty account: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\nname = Manual\nemail = manual@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err = readTestSettings(t, home)
	if err != nil || s.GitName != "Manual" {
		t.Fatalf("manual identity: %v", err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(home, ".config")); err != nil {
		t.Fatal(err)
	}
	if _, err = readTestSettings(t, home); err == nil {
		t.Fatal("followed credential directory symlink")
	}
}

func TestAccountCreationNeedsNoCredentialsOrDaemon(t *testing.T) {
	for _, existing := range []bool{false, true} {
		r := &fakeRemote{exists: existing, password: "secret"}
		result, err := Apply(r, &memAttempts{}, Request{ComputerID: "host", Username: "alice", Password: "secret", AccountOnly: true})
		if err != nil || (result.Action != ActionCreate && result.Action != ActionReuse) {
			t.Fatalf("account-only setup: %v", err)
		}
		if r.written || r.started || r.deleted {
			t.Fatal("account-only setup changed credentials or daemon")
		}
	}
}

func TestAccountUpgradePreservesRemoteCredentials(t *testing.T) {
	r := &fakeRemote{exists: true, password: "secret"}
	_, err := Apply(r, &memAttempts{}, Request{ComputerID: "host", Username: "alice", Password: "secret", PreserveFiles: true})
	if err != nil || r.written || !r.started {
		t.Fatalf("upgrade changed credentials or failed: %v", err)
	}
}

func TestCredentialsMayBeSavedIncrementally(t *testing.T) {
	for _, s := range []Settings{{}, {GitName: "Alice"}, {GitEmail: "alice@example.com"}, {ModelEnv: "OPENAI_API_KEY=fake"}} {
		if err := s.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if (Settings{GitLabToken: "fake"}).Validate() == nil {
		t.Fatal("GitLab token without origin accepted")
	}
	if (Settings{MulticaPAT: "invalid"}).Validate() == nil {
		t.Fatal("invalid PAT accepted")
	}
}

func TestUpgradeAuthenticatesBeforeReadingCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, password string
		attempts       int
		called         bool
	}{
		{"correct", "secret", 0, true}, {"wrong", "wrong", 0, false}, {"locked", "secret", 5, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeRemote{exists: true, password: "secret"}
			called := false
			_, err := Apply(r, &memAttempts{n: tc.attempts}, Request{ComputerID: "host", Username: "alice", Password: tc.password, PreserveFiles: true,
				AfterAuthenticate: func() error { called = true; return ClassifiedError("credentials_required") },
			})
			if called != tc.called || r.started || r.written {
				t.Fatal("credentials or daemon accessed before successful authentication")
			}
			if called && ErrorCode(err, "unknown") != "credentials_required" {
				t.Fatalf("lost safe failure code: %v", err)
			}
		})
	}
}

func TestDaemonConfigOnlyPreservesCredentialsAndTransport(t *testing.T) {
	home := t.TempDir()
	files, err := RenderFiles("https://old.invalid", "Alice", "alice@example.com", "", "", "OPENAI_API_KEY=keep-model", "old-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := runWriter(t, home, files, "computer-test"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".multica/profiles/computer-test/config.json")
	if err := os.WriteFile(path, []byte(`{"token":"old-token","device_name":"keep"}`), 0600); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{}
	for _, relative := range []string{".gitconfig", ".config/multica-provision/gitconfig", ".config/multica-provision/model.env"} {
		data, err := os.ReadFile(filepath.Join(home, relative))
		if err != nil {
			t.Fatal(err)
		}
		before[relative] = string(data)
	}
	rec := &recordRunner{}
	remote := SSHRemote{Host: "fake", Port: 22, User: "operator", KeyPath: "fake", DaemonID: "test", WorkspaceID: "workspace", HealthPort: 12345, RunCmd: rec}
	if err := remote.WriteDaemonConfig("alice", "https://new.invalid", "new-secret-token"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(rec.argv, " "), "new-secret-token") {
		t.Fatal("token leaked into argv")
	}
	script := strings.Replace(writeDaemonConfigScript, "home=pwd.getpwuid(os.getuid()).pw_dir", `home=os.environ["COMPUTER_TEST_HOME"]`, 1)
	run := func() error {
		cmd := exec.Command("python3", "-c", script, "computer-test")
		cmd.Env = append(os.Environ(), "COMPUTER_TEST_HOME="+home)
		cmd.Stdin = strings.NewReader(rec.stdin)
		return cmd.Run()
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if json.Unmarshal(data, &config) != nil || config["token"] != "new-secret-token" || config["device_name"] != "keep" || config["workspace_id"] != "workspace" || config["health_port"] != float64(12345) || config["server_url"] != "https://new.invalid" {
		t.Fatal("managed configuration mismatch")
	}
	for relative, original := range before {
		data, err := os.ReadFile(filepath.Join(home, relative))
		if err != nil || string(data) != original {
			t.Fatalf("rewrote %s", relative)
		}
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("token permissions")
	}
	// Reading the token must not parse or depend on unrelated Git credentials.
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("invalid Git config"), 0600); err != nil {
		t.Fatal(err)
	}
	readScript := strings.Replace(readDaemonTokenScript, "home=pwd.getpwuid(os.getuid()).pw_dir", `home=os.environ["COMPUTER_TEST_HOME"]`, 1)
	cmd := exec.Command("python3", "-c", readScript, "computer-test")
	cmd.Env = append(os.Environ(), "COMPUTER_TEST_HOME="+home)
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != `"new-secret-token"` {
		t.Fatal("token read failed")
	}
	// Symlinked profiles cannot redirect writes into another account.
	if err := os.RemoveAll(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if run() == nil {
		t.Fatal("followed symlinked profile")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("wrote outside profile")
	}
	rec.err = fmt.Errorf("remote output containing new-secret-token")
	rec.out = "new-secret-token"
	if err := remote.WriteDaemonConfig("alice", "https://new.invalid", "new-secret-token"); err == nil || strings.Contains(err.Error(), "new-secret-token") {
		t.Fatal("remote error leaked secret")
	}
}
