package daemon

import (
	"log/slog"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/supervisor"
	"github.com/multica-ai/multica/server/pkg/agent"
)

const testTaskID = "01a0fad5-cc0c-7165-a08c-0467d42e6f27"

func TestSanitizeRunID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"01a0fad5-cc0c-7165-a08c-0467d42e6f27", "01a0fad5-cc0c-7165-a08c-0467d42e6f27"},
		{"MUL-1234", "mul-1234"},
		{"weird id!!", "weird-id"},
		{"---", "task"},
		{"", "task"},
	}
	for _, c := range cases {
		if got := sanitizeRunID(c.in); got != c.want {
			t.Errorf("sanitizeRunID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := make([]byte, 0, 64)
	for i := 0; i < 64; i++ {
		long = append(long, 'a')
	}
	if got := sanitizeRunID(string(long)); len(got) != 48 {
		t.Errorf("sanitizeRunID(long) length = %d, want 48", len(got))
	}
}

func newTestSupervisor(t *testing.T) *supervisor.Supervisor {
	t.Helper()
	mgr, err := supervisor.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	sup, err := supervisor.New(mgr, nil, "/tmp/fake-daemon", nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return sup
}

func TestPlanSupervisedRun(t *testing.T) {
	newDaemon := func(sup *supervisor.Supervisor) *Daemon {
		return &Daemon{supervisor: sup, logger: slog.Default()}
	}

	t.Run("nil supervisor keeps legacy", func(t *testing.T) {
		if got := newDaemon(nil).planSupervisedRun("claude", testTaskID, 1); got != nil {
			t.Fatalf("want nil, got %+v", got)
		}
	})
	t.Run("provider whitelist", func(t *testing.T) {
		if got := newDaemon(newTestSupervisor(t)).planSupervisedRun("kimi", testTaskID, 1); got != nil {
			t.Fatalf("kimi is Phase 2; want nil, got %+v", got)
		}
	})
	t.Run("non-uuid task id falls back to legacy", func(t *testing.T) {
		// "mul-1234-1" cannot head a systemd unit name per the run-id
		// grammar (hex first char required); legacy is the only safe answer.
		if got := newDaemon(newTestSupervisor(t)).planSupervisedRun("claude", "MUL-1234", 1); got != nil {
			t.Fatalf("want nil for non-uuid task id, got %+v", got)
		}
	})
	t.Run("fresh launch", func(t *testing.T) {
		got := newDaemon(newTestSupervisor(t)).planSupervisedRun("claude", testTaskID, 1)
		if got == nil || got.Reattach || got.RunID != testTaskID+"-1" {
			t.Fatalf("want fresh run %s-1, got %+v", testTaskID, got)
		}
	})
	t.Run("reattach on live manifest", func(t *testing.T) {
		sup := newTestSupervisor(t)
		d := newDaemon(sup)
		if err := sup.Manager().WriteManifest(&supervisor.Manifest{
			Version: 1, RunID: testTaskID + "-1", TaskID: testTaskID, State: supervisor.StateRunning,
		}); err != nil {
			t.Fatalf("seed manifest: %v", err)
		}
		got := d.planSupervisedRun("claude", testTaskID, 1)
		if got == nil || !got.Reattach || got.RunID != testTaskID+"-1" {
			t.Fatalf("want reattach -1, got %+v", got)
		}
	})
	t.Run("exited manifest bumps generation", func(t *testing.T) {
		sup := newTestSupervisor(t)
		d := newDaemon(sup)
		if err := sup.Manager().WriteManifest(&supervisor.Manifest{
			Version: 1, RunID: testTaskID + "-1", TaskID: testTaskID, State: supervisor.StateExited,
			Exit: &supervisor.ExitRecord{Code: 0, Source: supervisor.ExitSourceLauncher},
		}); err != nil {
			t.Fatalf("seed manifest: %v", err)
		}
		got := d.planSupervisedRun("claude", testTaskID, 1)
		if got == nil || got.Reattach || got.RunID != testTaskID+"-1-2" {
			t.Fatalf("want fresh generation -1-2, got %+v", got)
		}
	})
	t.Run("live g2 manifest reattaches", func(t *testing.T) {
		sup := newTestSupervisor(t)
		d := newDaemon(sup)
		for _, m := range []*supervisor.Manifest{
			{Version: 1, RunID: testTaskID + "-1", TaskID: testTaskID, State: supervisor.StateExited,
				Exit: &supervisor.ExitRecord{Code: 0, Source: supervisor.ExitSourceLauncher}},
			{Version: 1, RunID: testTaskID + "-1-2", TaskID: testTaskID, State: supervisor.StateRunning},
		} {
			if err := sup.Manager().WriteManifest(m); err != nil {
				t.Fatalf("seed manifest: %v", err)
			}
		}
		got := d.planSupervisedRun("claude", testTaskID, 1)
		if got == nil || !got.Reattach || got.RunID != testTaskID+"-1-2" {
			t.Fatalf("want reattach generation -1-2, got %+v", got)
		}
	})
}

var _ = agent.Supervision{}
