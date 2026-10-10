package computer

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type runtimeProbeRunner struct{ home string }

func (r runtimeProbeRunner) Run(ctx context.Context, argv []string, _ string) (string, error) {
	command := strings.TrimPrefix(argv[len(argv)-1], "sudo -n runuser -u 'tester' -- ")
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Env = []string{"HOME=" + r.home, "PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	return string(out), err
}
func TestRuntimeProbeDistinguishesMissingAndBrokenExecutable(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	remote := SSHRemote{Host: "fake.invalid", Port: 22, User: "operator", KeyPath: "fake", RunCmd: runtimeProbeRunner{home}}
	for _, tc := range []struct{ name, body, state, version, code string }{
		{"missing", "", "missing", "", "cli_missing"},
		{"broken", "#!/bin/sh\necho dependency-missing >&2\nexit 1\n", "version_failed", "", "version_check_failed"},
		{"empty", "#!/bin/sh\nexit 0\n", "version_failed", "", "version_check_failed"},
		{"installed", "#!/bin/sh\nprintf 'test-cli 1.2.3\\n'\n", "installed", "test-cli 1.2.3", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(bin, "multica-test-cli")
			if tc.body != "" {
				if err := os.WriteFile(file, []byte(tc.body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			p, err := remote.ProbeRuntime(context.Background(), "tester", "multica-test-cli")
			if err != nil {
				t.Fatal(err)
			}
			if p.State != tc.state || p.Version != tc.version || p.Code != tc.code {
				t.Fatalf("unexpected probe: %+v", p)
			}
			if tc.body != "" && p.Path != file {
				t.Fatalf("wrong user's PATH: %q", p.Path)
			}
		})
	}
}
func TestComputerErrorCodesDoNotExposeRawSecrets(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, "ssh_timeout"}, {context.Canceled, "cancelled"},
		{errors.New("sudo: a password is required"), "sudo_failed"},
		{errors.New("Install Node.js and npm in /usr/bin"), "node_npm_missing"},
		{errors.New("secret-access-token failure"), "installer_failed"},
		{errors.New("npm error code E502\nnpm error 502 Bad Gateway - GET http://server/api/runtime-downloads/secret-access-token"), "runtime_download_failed"},
		{errors.New("npm ERR! code E502"), "runtime_download_failed"},
		{errors.New("curl: (22) The requested URL returned error: 502"), "runtime_download_failed"},
	} {
		code := ErrorCode(tc.err, "installer_failed")
		if code != tc.want {
			t.Fatalf("code=%q want=%q", code, tc.want)
		}
		if strings.Contains(ErrorSummary(code), "secret-access-token") {
			t.Fatal("secret leaked")
		}
	}
}

func TestInstallerTimeoutDoesNotBecomeSSHTimeout(t *testing.T) {
	for _, code := range []string{"124", "137"} {
		err := exec.Command("sh", "-c", "exit "+code).Run()
		if got := ErrorCode(err, "installer_failed"); got != "installer_timeout" {
			t.Fatalf("exit %s mapped to %s", code, got)
		}
		if got := ErrorCode(err, "version_check_failed"); got != "version_check_failed" {
			t.Fatalf("probe exit %s incorrectly mapped to %s", code, got)
		}
	}
}

func TestRuntimeProbeRejectsShellSyntaxBeforeExecution(t *testing.T) {
	for _, command := range []string{"cli;id", "cli name", "$(id)", "cli\nid", "/tmp/cli", "-cli"} {
		runner := &recordRunner{}
		remote := SSHRemote{Host: "fake.invalid", Port: 22, User: "operator", KeyPath: "/fake/key", RunCmd: runner}
		if _, err := remote.ProbeRuntime(context.Background(), "tester", command); err == nil {
			t.Fatalf("accepted command %q", command)
		}
		if len(runner.argv) != 0 {
			t.Fatalf("executed invalid command %q", command)
		}
	}
}
