package issuecoordinator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/pkg/featureflag"
)

// Enabled scopes the opt-in flag to a Codex leader task on one Issue/agent pair.
func Enabled(ctx context.Context, flags *featureflag.Service, provider, workspaceID, issueID, agentID string, isLeader bool) bool {
	if !isLeader || provider != "codex" || workspaceID == "" || issueID == "" || agentID == "" {
		return false
	}
	ctx = featureflag.WithEvalContext(ctx, featureflag.EvalContext{
		WorkspaceID: workspaceID,
		Attributes: map[string]string{
			"issue_id": issueID,
			"agent_id": agentID,
		},
	})
	return flags.IsEnabled(ctx, featureflags.IssueCoordinatorSessionPOC, false)
}

// Snapshot stores references and a digest, not task transcript or credentials.
func Snapshot(taskID string, result []byte) ([]byte, error) {
	sum := sha256.Sum256(result)
	return json.Marshal(struct {
		LastTaskID   string `json:"last_task_id"`
		ResultSHA256 string `json:"result_sha256"`
	}{LastTaskID: taskID, ResultSHA256: hex.EncodeToString(sum[:])})
}
