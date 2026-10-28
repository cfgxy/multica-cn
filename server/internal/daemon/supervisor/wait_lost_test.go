package supervisor

// RUYI-593 implementation round 1, direction 2 (RUYI-592 fix direction 2):
// a worker whose unit was killed by someone else can leave no exit record
// behind (the killer died before writing it) — pre-fix, Wait then polled the
// manifest forever and the run hung to the 2h idle watchdog (the RUYI-592
// "stall face"). Wait now converges on the Decide matrix's lost corner once
// a bounded window has elapsed: unit gone + no exit record + no lock
// synthesizes a terminal exit and persists it, so every other reader
// converges on the same evidence.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// lostTestSupervisor builds a Supervisor wired for in-process convergence
// tests: no real systemd (the unit probe is a programmable fake), a short
// convergence window, and a dead control handle shape (pump done, no
// observed exit) exactly like the RUYI-592 stall-face repro.
func lostTestSupervisor(t *testing.T, mgr *Manager, unitActive bool, probeErr error, lockHeld func(string) bool) *Supervisor {
	t.Helper()
	return &Supervisor{
		mgr:       mgr,
		binPath:   "lost-test",
		owner:     "test-self",
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		lostAfter: 300 * time.Millisecond,
		unitProbe: func(context.Context, string) (bool, error) {
			return unitActive, probeErr
		},
		LockHeld: lockHeld,
	}
}

func deadHandle(sup *Supervisor, man *Manifest) *supervisedHandle {
	closed := make(chan struct{})
	close(closed) // control connection dead: pump finished, no exit frame
	return &supervisedHandle{sup: sup, man: *man, client: &ControlClient{}, pumpDone: closed}
}

func TestWaitLostConvergence(t *testing.T) {
	mgr, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	runID := func(tag string) string { return "01a11d88-cd66-7399-99ea-7127ae79b3f" + tag + "-1" }

	t.Run("exit record resolves immediately (fast-fail face, regression)", func(t *testing.T) {
		sup := lostTestSupervisor(t, mgr, false, nil, nil)
		id := runID("0")
		man := manifestFor(id, &ExitRecord{Code: -1, Signal: "killed", At: time.Now().UTC(), Source: ExitSourceSupervisor})
		if err := mgr.WriteManifest(man); err != nil {
			t.Fatalf("WriteManifest: %v", err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := deadHandle(sup, man).Wait(context.Background())
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Wait with exit record: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Wait did not resolve although the manifest carries the exit record")
		}
	})

	t.Run("missing exit record converges as lost within the window (stall face)", func(t *testing.T) {
		sup := lostTestSupervisor(t, mgr, false, nil, nil)
		id := runID("1")
		man := manifestFor(id, nil) // killed, but the killer died before writing the record
		if err := mgr.WriteManifest(man); err != nil {
			t.Fatalf("WriteManifest: %v", err)
		}
		type waitOutcome struct {
			exit WorkerExit
			err  error
		}
		done := make(chan waitOutcome, 1)
		go func() {
			exit, err := deadHandle(sup, man).Wait(context.Background())
			done <- waitOutcome{exit, err}
		}()
		select {
		case out := <-done:
			if out.err != nil {
				t.Fatalf("Wait = %v, want synthesized lost exit", out.err)
			}
			if out.exit.Code != -1 || out.exit.Signal != "lost" {
				t.Fatalf("exit = %+v, want {code -1, signal lost}", out.exit)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Wait never converged: the stall face is back")
		}
		stored, err := mgr.ReadManifest(id)
		if err != nil {
			t.Fatalf("ReadManifest: %v", err)
		}
		if stored.Exit == nil || stored.Exit.Source != ExitSourceLost || stored.Exit.Signal != "lost" {
			t.Fatalf("persisted exit = %+v, want lost-source record", stored.Exit)
		}
		if stored.State != StateExited {
			t.Fatalf("persisted state = %s, want exited", stored.State)
		}
	})

	t.Run("held lock blocks convergence", func(t *testing.T) {
		sup := lostTestSupervisor(t, mgr, false, nil, func(string) bool { return true })
		id := runID("2")
		man := manifestFor(id, nil)
		if err := mgr.WriteManifest(man); err != nil {
			t.Fatalf("WriteManifest: %v", err)
		}
		done := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
			defer cancel()
			_, err := deadHandle(sup, man).Wait(ctx)
			done <- err
		}()
		select {
		case err := <-done:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Wait with live lock = %v, want deadline exceeded (a lock says something may still run)", err)
			}
		case <-time.After(4 * time.Second):
			t.Fatal("test harness timeout")
		}
		stored, _ := mgr.ReadManifest(id)
		if stored.Exit != nil {
			t.Fatalf("live lock must not converge, exit = %+v", stored.Exit)
		}
	})

	t.Run("unit still active blocks convergence", func(t *testing.T) {
		sup := lostTestSupervisor(t, mgr, true, nil, nil)
		id := runID("3")
		man := manifestFor(id, nil)
		if err := mgr.WriteManifest(man); err != nil {
			t.Fatalf("WriteManifest: %v", err)
		}
		done := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
			defer cancel()
			_, err := deadHandle(sup, man).Wait(ctx)
			done <- err
		}()
		select {
		case err := <-done:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Wait with live unit = %v, want deadline exceeded", err)
			}
		case <-time.After(4 * time.Second):
			t.Fatal("test harness timeout")
		}
		stored, _ := mgr.ReadManifest(id)
		if stored.Exit != nil {
			t.Fatalf("live unit must not converge, exit = %+v", stored.Exit)
		}
	})

	t.Run("unit probe failure fails open", func(t *testing.T) {
		sup := lostTestSupervisor(t, mgr, false, errors.New("systemctl exploded"), nil)
		id := runID("4")
		man := manifestFor(id, nil)
		if err := mgr.WriteManifest(man); err != nil {
			t.Fatalf("WriteManifest: %v", err)
		}
		done := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
			defer cancel()
			_, err := deadHandle(sup, man).Wait(ctx)
			done <- err
		}()
		select {
		case err := <-done:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Wait with failing probe = %v, want deadline exceeded (probe failure never fabricates)", err)
			}
		case <-time.After(4 * time.Second):
			t.Fatal("test harness timeout")
		}
		stored, _ := mgr.ReadManifest(id)
		if stored.Exit != nil {
			t.Fatalf("probe failure must not converge, exit = %+v", stored.Exit)
		}
	})
}

func TestCollectedUnitRequiresTrustedExitRecord(t *testing.T) {
	mgr, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	newHandle := func(t *testing.T, source string) *supervisedHandle {
		t.Helper()
		id := "01a11d88-cd66-7399-99ea-7127ae79b3fc-1"
		man := manifestFor(id, &ExitRecord{Code: 0, At: time.Now().UTC(), Source: source})
		if err := mgr.WriteManifest(man); err != nil {
			t.Fatalf("WriteManifest: %v", err)
		}
		sup := &Supervisor{
			mgr: mgr,
			unitProbe: func(context.Context, string) (bool, error) {
				return false, ErrUnitCollected
			},
		}
		return &supervisedHandle{sup: sup, man: *man}
	}

	t.Run("launcher exit plus collected unit proves cgroup stopped", func(t *testing.T) {
		active, err := newHandle(t, ExitSourceLauncher).unitActiveOrCollected(context.Background())
		if err != nil || active {
			t.Fatalf("collected launcher exit = active:%v err:%v, want inactive without error", active, err)
		}
	})

	t.Run("synthetic lost exit is not enough", func(t *testing.T) {
		_, err := newHandle(t, ExitSourceLost).unitActiveOrCollected(context.Background())
		if err == nil {
			t.Fatal("collected unit with synthetic exit must remain unconfirmed")
		}
	})
}

func TestStopRecordsSupervisorExitAfterOwnKillCollectsUnit(t *testing.T) {
	mgr, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	man := manifestFor("01a11d88-cd66-7399-99ea-7127ae79b3fc-2", nil)
	if err := mgr.WriteManifest(man); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	killed := false
	killCalls := 0
	sup := &Supervisor{
		mgr: mgr,
		unitProbe: func(context.Context, string) (bool, error) {
			if killed {
				return false, ErrUnitCollected
			}
			return true, nil
		},
		unitKiller: func(context.Context, string) error {
			killCalls++
			killed = true
			return nil
		},
	}
	if err := (&supervisedHandle{sup: sup, man: *man}).Stop(); err != nil {
		t.Fatalf("Stop after own kill and collected unit: %v", err)
	}
	if killCalls != 1 {
		t.Fatalf("KillUnit calls = %d, want 1", killCalls)
	}
	stored, err := mgr.ReadManifest(man.RunID)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if stored.State != StateExited || stored.Exit == nil || stored.Exit.Source != ExitSourceSupervisor {
		t.Fatalf("manifest after own kill and collection = state:%s exit:%+v, want supervisor exit", stored.State, stored.Exit)
	}
}

func TestStopDoesNotRecordExitAfterOwnKillProbeFailure(t *testing.T) {
	mgr, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	man := manifestFor("01a11d88-cd66-7399-99ea-7127ae79b3fc-3", nil)
	if err := mgr.WriteManifest(man); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	killed := false
	sup := &Supervisor{
		mgr: mgr,
		unitProbe: func(context.Context, string) (bool, error) {
			if killed {
				return false, errors.New("systemctl unavailable")
			}
			return true, nil
		},
		unitKiller: func(context.Context, string) error {
			killed = true
			return nil
		},
	}
	if err := (&supervisedHandle{sup: sup, man: *man}).Stop(); err == nil {
		t.Fatal("Stop accepted a generic post-kill probe failure")
	}
	stored, err := mgr.ReadManifest(man.RunID)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if stored.Exit != nil {
		t.Fatalf("probe failure recorded an unproven exit: %+v", stored.Exit)
	}
}

func TestControlDialAfterStartIsCancellationUnconfirmed(t *testing.T) {
	err := controlDialAfterStartError("run-1", context.Canceled)
	if !errors.Is(err, agent.ErrCancellationUnconfirmed) {
		t.Fatalf("post-launch control dial error = %v, want ErrCancellationUnconfirmed", err)
	}
}
