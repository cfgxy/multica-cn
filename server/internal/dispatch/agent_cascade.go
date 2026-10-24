package dispatch

import "time"

const (
	MaxRapidAgentOnlyCommentCascadeHops = 8
	AgentOnlyCommentCascadeWindow       = 10 * time.Minute
)

// AgentOnlyCommentCascadeBlocked applies the agent-only cascade policy to the
// database-computed lineage verdict. It blocks eight rapid hops and task cycles
// at any age, but deliberately has no lifetime-depth limit.
func AgentOnlyCommentCascadeBlocked(rapidHops int32, hasCycle bool) bool {
	return rapidHops >= MaxRapidAgentOnlyCommentCascadeHops || hasCycle
}
