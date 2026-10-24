package dispatch

import (
	"testing"
)

func TestAgentOnlyCommentCascadeBlocked(t *testing.T) {
	t.Run("seven recent hops remain available", func(t *testing.T) {
		if AgentOnlyCommentCascadeBlocked(MaxRapidAgentOnlyCommentCascadeHops-1, false) {
			t.Fatal("seven recent hops must remain available")
		}
	})

	t.Run("eight recent hops are blocked", func(t *testing.T) {
		if !AgentOnlyCommentCascadeBlocked(MaxRapidAgentOnlyCommentCascadeHops, false) {
			t.Fatal("eight recent hops must be blocked")
		}
	})

	t.Run("long lived chain remains available", func(t *testing.T) {
		if AgentOnlyCommentCascadeBlocked(1, false) {
			t.Fatal("lifetime depth alone must not block a long-lived chain")
		}
	})

	t.Run("old cycle remains blocked", func(t *testing.T) {
		if !AgentOnlyCommentCascadeBlocked(0, true) {
			t.Fatal("a task cycle must remain blocked outside the rapid window")
		}
	})

	t.Run("missing task fails open", func(t *testing.T) {
		if AgentOnlyCommentCascadeBlocked(0, false) {
			t.Fatal("unreadable lineage must not suppress the handoff")
		}
	})
}
