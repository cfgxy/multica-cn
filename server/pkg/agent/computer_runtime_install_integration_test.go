//go:build agentintegration

package agent_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/pkg/agent"
)

// This test installs real CLIs, but never authenticates a model account or runs
// a model task. The target must be a designated disposable Linux user.
func TestComputerRuntimeInstallSmoke(t *testing.T) {
	if os.Getenv("MULTICA_RUN_REAL_AGENT_SMOKE") != "1" {
		t.Skip("requires explicit real-agent smoke authorization")
	}
	if os.Getenv("MULTICA_COMPUTER_SMOKE_ENVIRONMENT") != "nonproduction" {
		t.Fatal("set MULTICA_COMPUTER_SMOKE_ENVIRONMENT=nonproduction for a designated disposable host")
	}
	required := func(name string) string {
		v := os.Getenv(name)
		if v == "" {
			t.Fatalf("%s is required", name)
		}
		return v
	}
	host := required("MULTICA_COMPUTER_SMOKE_HOST")
	user := required("MULTICA_COMPUTER_SMOKE_LINUX_USER")
	if !strings.HasPrefix(user, "multica_smoke_") || computer.ValidateLinuxUsername(user) != nil {
		t.Fatal("use a disposable multica_smoke_* Linux account")
	}
	port, err := strconv.Atoi(required("MULTICA_COMPUTER_SMOKE_PORT"))
	if err != nil {
		t.Fatal("invalid SSH port")
	}
	remote := computer.SSHRemote{Host: host, Port: port, User: required("MULTICA_COMPUTER_SMOKE_OPERATOR"), KeyPath: required("MULTICA_COMPUTER_SMOKE_SSH_KEY"), Timeout: 6 * time.Minute}
	exists, err := remote.UserExists(user)
	if err != nil || !exists {
		t.Fatalf("provision the designated disposable account before running this test: %v", err)
	}
	type target struct{ id, command, install string }
	targets := []target{}
	for _, r := range agent.BuiltinRuntimes {
		targets = append(targets, target{r.ID, r.DefaultCommand, r.InstallCommand})
	}
	for _, r := range agent.ProtocolFamilyInstalls {
		targets = append(targets, target{r.ID, r.DefaultCommand, r.InstallCommand})
	}
	for _, r := range targets {
		t.Run(r.id, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
			defer cancel()
			cmd := strings.ReplaceAll(strings.ReplaceAll(r.install, "{{user}}", user), "{{version}}", "latest")
			if _, err := remote.RunCommandContext(ctx, "sudo -n timeout -k 15s 300s flock -n /run/lock/multica-runtime-"+user+" "+cmd); err != nil {
				t.Fatalf("installation failed (%s); inspect the disposable host", computer.ErrorCode(err, "installer_failed"))
			}
			p, err := remote.ProbeRuntime(ctx, user, r.command)
			if err != nil || p.State != "installed" {
				t.Fatalf("installed CLI was not usable: state=%s code=%s err=%v", p.State, p.Code, err)
			}
			t.Logf("runtime=%s executable=%s version=%s managed_path=%s", r.id, p.Path, p.Version, computer.RuntimePath)
		})
	}
}
