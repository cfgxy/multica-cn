package issuecoordinator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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

func RequiresColdStart(runtimeMatches, sessionAvailable bool, continuityState string, hasPriorBrief, resumeUnavailable bool) bool {
	return !runtimeMatches || !sessionAvailable || continuityState == "cold_start" || hasPriorBrief || resumeUnavailable
}

const MaxSnapshotBytes = 16 * 1024
const MaxSnapshotRefs = 32

var ErrSnapshotCASConflict = errors.New("issue coordinator snapshot CAS conflict")

type Snapshot struct {
	Version           int           `json:"version"`
	IssueID           string        `json:"issue_id"`
	IssueRevision     int64         `json:"issue_revision"`
	IssueStatus       string        `json:"issue_status"`
	ResourceRefs      []string      `json:"resource_refs"`
	DecisionRefs      []DecisionRef `json:"decision_refs"`
	PlanRefs          []string      `json:"plan_refs"`
	CompletedTaskRefs []string      `json:"completed_task_refs"`
	TodoTaskRefs      []string      `json:"todo_task_refs"`
	EvidenceRefs      []string      `json:"evidence_refs"`
	RiskCommentRefs   []string      `json:"risk_comment_refs"`
}

type DecisionRef struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	AnswerCommentID string `json:"answer_comment_id,omitempty"`
}

// MarshalSnapshot stores bounded references to authoritative Issue records,
// never task output, transcripts, or credentials.
func MarshalSnapshot(snapshot Snapshot) ([]byte, error) {
	if snapshot.IssueID == "" || snapshot.IssueRevision < 0 || snapshot.IssueStatus == "" {
		return nil, fmt.Errorf("snapshot requires an Issue id, non-negative revision, and status")
	}
	if len(snapshot.ResourceRefs) > MaxSnapshotRefs || len(snapshot.DecisionRefs) > MaxSnapshotRefs || len(snapshot.PlanRefs) > MaxSnapshotRefs ||
		len(snapshot.CompletedTaskRefs) > MaxSnapshotRefs || len(snapshot.TodoTaskRefs) > MaxSnapshotRefs ||
		len(snapshot.EvidenceRefs) > MaxSnapshotRefs || len(snapshot.RiskCommentRefs) > MaxSnapshotRefs {
		return nil, fmt.Errorf("snapshot reference limit exceeded")
	}
	snapshot.Version = 1
	if snapshot.DecisionRefs == nil {
		snapshot.DecisionRefs = []DecisionRef{}
	}
	if snapshot.ResourceRefs == nil {
		snapshot.ResourceRefs = []string{}
	}
	if snapshot.PlanRefs == nil {
		snapshot.PlanRefs = []string{}
	}
	if snapshot.CompletedTaskRefs == nil {
		snapshot.CompletedTaskRefs = []string{}
	}
	if snapshot.TodoTaskRefs == nil {
		snapshot.TodoTaskRefs = []string{}
	}
	if snapshot.EvidenceRefs == nil {
		snapshot.EvidenceRefs = []string{}
	}
	if snapshot.RiskCommentRefs == nil {
		snapshot.RiskCommentRefs = []string{}
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaxSnapshotBytes {
		return nil, fmt.Errorf("snapshot exceeds %d bytes", MaxSnapshotBytes)
	}
	return encoded, nil
}
