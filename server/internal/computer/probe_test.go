package computer

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func probeRemote(runner CommandRunner) SSHRemote {
	return SSHRemote{Host: "gpu-pc", Port: 22, User: "bastion", KeyPath: "/home/tiger/.ssh/id_ed25519", RunCmd: runner}
}

func TestProbeReadsRemoteReport(t *testing.T) {
	rec := &recordRunner{out: `{"ok":true,"facts":{"hostname":"tensor","cpus":32,"memory_mb":128000},"checks":[{"name":"sudo","ok":true}]}`}
	res, err := probeRemote(rec).Probe()
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Facts.Hostname != "tensor" || res.Facts.CPUs != 32 || len(res.Checks) != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
	joined := strings.Join(rec.argv, " ")
	if !strings.Contains(joined, "BatchMode=yes") || !strings.Contains(joined, "bastion@gpu-pc") {
		t.Fatalf("argv: %s", joined)
	}
	if rec.stdin != "" {
		t.Fatalf("probe must not write stdin: %q", rec.stdin)
	}
	// A read-only probe must not provision or mutate the computer. Tool names
	// appear as PATH lookups, so assert on what it actually writes and runs
	// with privilege instead.
	for _, forbidden := range []string{"systemctl enable", "systemctl restart", "systemctl disable", "os.makedirs", "os.replace", "os.unlink", "os.write"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("probe mutates remote via %q", forbidden)
		}
	}
	// Elevated commands are limited to the two read-only checks.
	for _, call := range strings.Split(joined, `"sudo","-n",`)[1:] {
		if !strings.HasPrefix(call, `"true"`) && !strings.HasPrefix(call, `"python3","-c",PAM`) {
			t.Fatalf("unexpected privileged call: %.60s", call)
		}
	}
}

func TestProbeReportsTransportFailureAsFailedCheck(t *testing.T) {
	rec := &recordRunner{out: "Host key verification failed.", err: errors.New("exit status 255")}
	res, err := probeRemote(rec).Probe()
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || len(res.Checks) != 1 || res.Checks[0].Name != "ssh" || res.Checks[0].OK {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !strings.Contains(res.Checks[0].Detail, "Host key verification failed") {
		t.Fatalf("detail lost: %q", res.Checks[0].Detail)
	}
}

func TestProbeRejectsInvalidHost(t *testing.T) {
	remote := probeRemote(&recordRunner{out: "{}"})
	remote.Host = "-oProxyCommand=touch /tmp/pwn"
	if _, err := remote.Probe(); err == nil {
		t.Fatal("expected invalid host to be rejected")
	}
}

func TestProbeRejectsNonJSON(t *testing.T) {
	if _, err := probeRemote(&recordRunner{out: "Welcome to Ubuntu\n"}).Probe(); err == nil {
		t.Fatal("expected invalid probe response")
	}
}

// The probe script must be valid python3 and emit a parsable report.
func TestProbeScriptRunsLocally(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	out, err := exec.Command("python3", "-c", probeScript).CombinedOutput()
	if err != nil {
		t.Fatalf("probe script failed: %v\n%s", err, out)
	}
	res, err := probeRemote(&recordRunner{out: string(out)}).Probe()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	names := map[string]bool{}
	for _, c := range res.Checks {
		names[c.Name] = true
	}
	for _, want := range []string{"ssh", "python3", "sudo", "pam", "systemctl", "useradd", "runuser", "apt-get"} {
		if !names[want] {
			t.Fatalf("check %q missing from %+v", want, res.Checks)
		}
	}
	if res.Facts.Hostname == "" || res.Facts.CPUs == 0 {
		t.Fatalf("facts not collected: %+v", res.Facts)
	}
}
