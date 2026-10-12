package computer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type recordRunner struct {
	argv  []string
	stdin string
	out   string
	err   error
}

func (r *recordRunner) Run(_ context.Context, argv []string, stdin string) (string, error) {
	r.argv = append([]string{}, argv...)
	r.stdin = stdin
	return r.out, r.err
}

func TestSSHArgsDoNotCarrySecrets(t *testing.T) {
	rec := &recordRunner{}
	remote := SSHRemote{Host: "gpu-pc", Port: 22, User: "bastion", KeyPath: "/home/tiger/.ssh/id_ed25519", RunCmd: rec, DaemonID: "test-daemon"}
	ok, err := remote.PasswordMatches("zhang", "s3cret-password")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	joined := strings.Join(rec.argv, " ")
	if strings.Contains(joined, "s3cret-password") {
		t.Fatalf("password leaked onto argv: %s", joined)
	}
	if !strings.Contains(rec.stdin, "s3cret-password") {
		t.Fatal("password was not passed on stdin")
	}
	if !strings.Contains(joined, "BatchMode=yes") || !strings.Contains(joined, "bastion@gpu-pc") {
		t.Fatalf("argv: %s", joined)
	}
}

type scriptRunner struct {
	calls []recordRunner
	err   error
}

func (s *scriptRunner) Run(_ context.Context, argv []string, stdin string) (string, error) {
	s.calls = append(s.calls, recordRunner{argv: append([]string{}, argv...), stdin: stdin})
	if s.err != nil && strings.Contains(stdin, "s3cret-password") {
		return "leaked " + stdin, s.err
	}
	return "", nil
}

func TestSSHCommandsHideSecrets(t *testing.T) {
	const password = "s3cret-password"
	const token = "glpat-real"
	rec := &scriptRunner{}
	remote := SSHRemote{Host: "gpu-pc", Port: 22, User: "bastion", KeyPath: "/home/tiger/.ssh/id_ed25519", RunCmd: rec, DaemonID: "test-daemon"}
	if err := remote.CreateUser("zhang", password); err != nil {
		t.Fatal(err)
	}
	files, err := RenderFiles("http://10.10.0.31:3000", "Zhang", "zhang@company.example", "http://10.12.0.79", token, "ANTHROPIC_API_KEY=sk-model", "mul_pat")
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.WriteFiles("zhang", files); err != nil {
		t.Fatal(err)
	}
	if err := remote.InstallDaemon("zhang"); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) < 4 {
		t.Fatalf("calls=%d", len(rec.calls))
	}
	for _, call := range rec.calls {
		joined := strings.Join(call.argv, " ")
		for _, secret := range []string{password, token, "mul_pat", "sk-model"} {
			if strings.Contains(joined, secret) {
				t.Fatalf("secret %q in argv: %s", secret, joined)
			}
		}
	}
	leaky := &scriptRunner{err: errString("exit status 1")}
	remote.RunCmd = leaky
	err = remote.CreateUser("zhang", password)
	if err == nil || strings.Contains(err.Error(), password) {
		t.Fatalf("error echoed stdin: %v", err)
	}
}

func TestSSHRejectsDotHost(t *testing.T) {
	remote := SSHRemote{Host: "..", Port: 22, User: "bastion", KeyPath: "/tmp/key"}
	if _, err := remote.UserExists("zhang"); err == nil {
		t.Fatal("accepted .. host")
	}
}

func TestAdminCommandHonorsRequestCancellation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte("#!/bin/sh\nexec sleep 5\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	remote := SSHRemote{Host: "fake", Port: 22, User: "operator", KeyPath: "/fake", Timeout: time.Minute}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := remote.RunCommandContext(ctx, "not executed"); err == nil {
		t.Fatal("cancellation ignored")
	}
	if time.Since(start) > time.Second {
		t.Fatal("SSH outlived the request budget")
	}
}

func TestRemoteFailureIncludesBoundedRedactedDiagnostics(t *testing.T) {
	cause := errors.New("exit status 127")
	runner := &recordRunner{out: strings.Repeat("x", 5000) + " bun: command not found; fake-secret", err: cause}
	remote := SSHRemote{Host: "fake", Port: 22, User: "operator", KeyPath: "/fake", RunCmd: runner}
	out, err := remote.run("unused", "", "fake-secret")
	if out != "" || !errors.Is(err, cause) || !strings.Contains(err.Error(), "bun: command not found") || strings.Contains(err.Error(), "fake-secret") || len(err.Error()) > 4200 {
		t.Fatalf("unsafe or missing diagnostics: %q %v", out, err)
	}
}

func TestDaemonPathIncludesNativeInstallDirectories(t *testing.T) {
	for _, path := range []string{"/home/%i/.local/bin", "/home/%i/.kimi-code/bin", "/home/%i/.grok/bin"} {
		if !strings.Contains(DaemonUnit, path) {
			t.Fatalf("daemon cannot resolve %s", path)
		}
	}
}

type cancellingRunner struct{ started chan struct{} }

func (r cancellingRunner) Run(ctx context.Context, _ []string, _ string) (string, error) {
	close(r.started)
	<-ctx.Done()
	return "", ctx.Err()
}

func TestInjectedSSHRunnerReceivesCancellationAndTimeout(t *testing.T) {
	for _, tc := range []string{"cancel", "timeout", "already cancelled"} {
		t.Run(tc, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan struct{})
			remote := SSHRemote{Host: "fake.invalid", Port: 22, User: "operator", KeyPath: "/fake/key", Timeout: time.Second, RunCmd: cancellingRunner{started}}
			expected := context.Canceled
			if tc == "timeout" {
				remote.Timeout = 20 * time.Millisecond
				expected = context.DeadlineExceeded
			}
			if tc == "already cancelled" {
				cancel()
			}
			done := make(chan error, 1)
			go func() { _, err := remote.RunCommandContext(ctx, "true"); done <- err }()
			if tc == "cancel" {
				select {
				case <-started:
					cancel()
				case <-time.After(time.Second):
					t.Fatal("runner was not called")
				}
			}
			select {
			case err := <-done:
				if !errors.Is(err, expected) {
					t.Fatalf("got %v, want %v", err, expected)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("injected runner ignored context")
			}
			if tc == "already cancelled" {
				select {
				case <-started:
					t.Fatal("cancelled command executed")
				default:
				}
			}
		})
	}
}
