package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// child builds a sibling row with the given stage (0 = unstaged/NULL) and
// status, the only two fields the stage-barrier logic reads.
func child(stage int32, status string) db.Issue {
	c := db.Issue{Status: status}
	if stage != 0 {
		c.Stage = pgtype.Int4{Int32: stage, Valid: true}
	}
	return c
}

func TestStageBarrierClosed_Unstaged(t *testing.T) {
	tests := []struct {
		name     string
		children []db.Issue
		want     bool
	}{
		{
			name:     "last child still leaves a sibling open",
			children: []db.Issue{child(0, "done"), child(0, "in_progress")},
			want:     false,
		},
		{
			name:     "every child terminal closes the single implicit stage",
			children: []db.Issue{child(0, "done"), child(0, "done")},
			want:     true,
		},
		{
			name:     "a backlog sibling holds the barrier open (no surprise cascade)",
			children: []db.Issue{child(0, "done"), child(0, "backlog")},
			want:     false,
		},
		{
			name:     "cancelled counts as terminal",
			children: []db.Issue{child(0, "done"), child(0, "cancelled")},
			want:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// completed is one of the terminal children; identity doesn't matter
			// for the unstaged path.
			if got := stageBarrierClosed(tt.children, child(0, "done")); got != tt.want {
				t.Fatalf("stageBarrierClosed = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStageBarrierClosed_Staged(t *testing.T) {
	// Three stages: 1 has two children, 2 has two, 3 has one.
	t.Run("stage 1 not fully done does not fire", func(t *testing.T) {
		children := []db.Issue{
			child(1, "done"), child(1, "in_progress"),
			child(2, "backlog"), child(2, "backlog"),
			child(3, "backlog"),
		}
		if stageBarrierClosed(children, child(1, "done")) {
			t.Fatal("expected barrier not closed while stage 1 has an open child")
		}
	})

	t.Run("closing stage 1 fires even though later stages are parked", func(t *testing.T) {
		children := []db.Issue{
			child(1, "done"), child(1, "done"),
			child(2, "backlog"), child(2, "backlog"),
			child(3, "backlog"),
		}
		if !stageBarrierClosed(children, child(1, "done")) {
			t.Fatal("expected stage 1 barrier to close")
		}
	})

	t.Run("closing stage 2 fires when stages 1 and 2 are terminal", func(t *testing.T) {
		children := []db.Issue{
			child(1, "done"), child(1, "done"),
			child(2, "done"), child(2, "done"),
			child(3, "backlog"),
		}
		if !stageBarrierClosed(children, child(2, "done")) {
			t.Fatal("expected stage 2 barrier to close")
		}
	})

	t.Run("final stage closes once its child finishes", func(t *testing.T) {
		children := []db.Issue{
			child(1, "done"), child(1, "done"),
			child(2, "done"), child(2, "done"),
			child(3, "done"),
		}
		if !stageBarrierClosed(children, child(3, "done")) {
			t.Fatal("expected final stage barrier to close")
		}
	})
}

func TestStageProgressSummary(t *testing.T) {
	children := []db.Issue{
		child(1, "done"), child(1, "done"), child(1, "done"),
		child(2, "backlog"), child(2, "backlog"), child(2, "backlog"), child(2, "backlog"),
		child(3, "backlog"), child(3, "backlog"),
	}
	summary, next := stageProgressSummary(children, 1)
	want := "Stage 1: 3/3 done; Stage 2: 0/4 done (next); Stage 3: 0/2 done"
	if summary != want {
		t.Fatalf("summary = %q, want %q", summary, want)
	}
	if next != 2 {
		t.Fatalf("nextStage = %d, want 2", next)
	}
}

func TestStageProgressSummary_FinalStageNoNext(t *testing.T) {
	children := []db.Issue{
		child(1, "done"), child(1, "done"),
		child(2, "done"),
	}
	_, next := stageProgressSummary(children, 2)
	if next != 0 {
		t.Fatalf("nextStage = %d, want 0 (no further stages)", next)
	}
}
