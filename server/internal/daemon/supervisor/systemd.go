package supervisor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"log/slog"
)

// systemdCtl wraps the user systemd manager the supervisor drives. Every
// invocation carries an environment that works even when the daemon was
// started from a context without the usual session variables: the user
// runtime directory and bus address are derived from the UID when missing.
type systemdCtl struct {
	env []string
	log *slog.Logger
}

// NewSystemdCtl builds a controller for the current user's systemd manager.
func NewSystemdCtl(log *slog.Logger) *systemdCtl {
	if log == nil {
		log = slog.Default()
	}
	env := os.Environ()
	uid := os.Getuid()
	runtimeDir := fmt.Sprintf("/run/user/%d", uid)
	ensure := func(key, val string) {
		for _, kv := range env {
			if strings.HasPrefix(kv, key+"=") {
				return
			}
		}
		env = append(env, key+"="+val)
	}
	ensure("XDG_RUNTIME_DIR", runtimeDir)
	ensure("DBUS_SESSION_BUS_ADDRESS", "unix:path="+runtimeDir+"/bus")
	return &systemdCtl{env: env, log: log}
}

// Available reports whether the user systemd manager is usable here: both
// binaries exist and the user manager answers. "degraded" still runs units.
func (s *systemdCtl) Available() error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("supervisor: systemctl not found: %w", err)
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return fmt.Errorf("supervisor: systemd-run not found: %w", err)
	}
	out, err := s.run(context.Background(), 5*time.Second, "is-system-running")
	state := strings.TrimSpace(string(out))
	switch {
	case err == nil && (state == "running" || state == "initializing" || state == "starting"):
		return nil
	case isExitCode(err, 1) && state == "degraded":
		return nil
	default:
		return fmt.Errorf("supervisor: user systemd not usable (state %q): %v", state, err)
	}
}

// startUnit runs systemd-run for the launcher. Type=exec makes the run call
// fail when the launcher cannot even start, instead of reporting a unit that
// then immediately dies unobserved.
func (s *systemdCtl) startUnit(ctx context.Context, unit, specPath, binPath string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// systemd-run, not `systemctl run`: the latter is not a portable verb —
	// e.g. this repo's E2E host rejects --unit= there (RUYI-349).
	out, err := s.runBin(ctx, "systemd-run", 20*time.Second,
		"--user",
		"--unit="+unit,
		"--collect",
		"--quiet",
		"--property=KillMode=control-group",
		"--property=Type=exec",
		"--setenv="+launcherSpecEnvVar+"="+specPath,
		"--", binPath, LauncherSubcommand,
	)
	if err != nil {
		return fmt.Errorf("supervisor: systemd-run %s: %w (%s)", unit, err, firstLine(string(out)))
	}
	return nil
}

// UnitActive reports whether the unit currently exists in an active-ish state.
func (s *systemdCtl) UnitActive(ctx context.Context, unit string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := s.run(ctx, 5*time.Second, "is-active", unit)
	state := strings.TrimSpace(string(out))
	switch state {
	case "active", "activating", "reloading":
		return true, nil
	case "inactive", "failed", "deactivating":
		return false, nil
	default:
		return false, fmt.Errorf("supervisor: is-active %s: %q (%v)", unit, state, err)
	}
}

// ListUnits returns the names of all multica-run units systemd currently knows.
func (s *systemdCtl) ListUnits(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := s.run(ctx, 10*time.Second,
		"list-units", unitPrefix+"*", "--all", "--no-legend", "--plain")
	if err != nil && !isExitCode(err, 1) {
		return nil, fmt.Errorf("supervisor: list-units: %w", err)
	}
	var units []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name := strings.Fields(line)[0]
		if strings.HasPrefix(name, unitPrefix) && strings.HasSuffix(name, ".service") {
			units = append(units, name)
		}
	}
	return units, nil
}

// StopUnit gracefully stops the unit. systemd's own StopTimeoutSIGTERM (90s
// default) applies; callers that need immediacy use KillUnit.
func (s *systemdCtl) StopUnit(ctx context.Context, unit string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := s.run(ctx, 30*time.Second, "stop", unit); err != nil {
		return fmt.Errorf("supervisor: stop %s: %w", unit, err)
	}
	return nil
}

// KillUnit SIGKILLs every process in the unit's cgroup — the hard, precise
// cancel primitive. Mirrors what exec's WaitDelay expiry does to a legacy
// child, minus every risk of collateral damage beyond this one unit.
func (s *systemdCtl) KillUnit(ctx context.Context, unit string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := s.run(ctx, 30*time.Second, "kill", "--kill-who=all", "--signal=SIGKILL", unit); err != nil {
		return fmt.Errorf("supervisor: kill %s: %w", unit, err)
	}
	return nil
}

func (s *systemdCtl) run(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	// Every systemctl call is user-manager scoped: the run units live under
	// the user instance, and an unscoped call silently answers from the
	// system manager, where they do not exist (RUYI-349 E2E finding).
	return s.runBin(ctx, "systemctl", timeout, append([]string{"--user"}, args...)...)
}

func (s *systemdCtl) runBin(ctx context.Context, binary string, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = s.env
	return cmd.CombinedOutput()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func isExitCode(err error, code int) bool {
	ee, ok := err.(*exec.ExitError)
	return ok && ee.ExitCode() == code
}
