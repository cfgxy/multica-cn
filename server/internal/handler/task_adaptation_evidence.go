package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/taskadaptation"
)

const adaptationCandidateRef = "capability_adaptation_candidate:"

func (h *Handler) validateAdaptationCandidateEvidence(ctx context.Context, workspaceID pgtype.UUID, anchors []map[string]string) error {
	containsCandidate := false
	for _, anchor := range anchors {
		if strings.HasPrefix(anchor["ref"], adaptationCandidateRef) {
			containsCandidate = true
			break
		}
	}
	if !containsCandidate {
		return nil
	}

	candidateID := ""
	receipts := make([]taskadaptation.ExperienceReceipt, 0, len(anchors))
	for _, anchor := range anchors {
		ref := anchor["ref"]
		if !strings.HasPrefix(ref, adaptationCandidateRef) {
			return fmt.Errorf("experience candidate evidence must use one candidate reference")
		}
		id := strings.TrimPrefix(ref, adaptationCandidateRef)
		if len(id) != 64 {
			return fmt.Errorf("experience candidate reference is invalid")
		}
		if candidateID != "" && candidateID != id {
			return fmt.Errorf("experience candidate evidence references multiple candidates")
		}
		candidateID = id

		taskID := anchor["task_id"]
		taskUUID, err := util.ParseUUID(taskID)
		if err != nil {
			return fmt.Errorf("experience candidate evidence requires a valid task_id")
		}
		var storedTaskID, issueID string
		var rawContext, rawResult []byte
		err = h.DB.QueryRow(ctx, `
SELECT t.id::text, COALESCE(t.issue_id::text, ''), t.context, t.result
FROM agent_task_queue t
JOIN agent a ON a.id = t.agent_id
WHERE a.workspace_id = $1 AND t.id = $2 AND t.status = 'completed'`, workspaceID, taskUUID).Scan(
			&storedTaskID, &issueID, &rawContext, &rawResult,
		)
		if err != nil {
			return fmt.Errorf("experience candidate source task is not a completed task in this workspace")
		}
		if anchorIssueID := anchor["issue_id"]; anchorIssueID != "" && anchorIssueID != issueID {
			return fmt.Errorf("experience candidate issue evidence does not match its source task")
		}
		snapshot := taskadaptation.SnapshotFromContext(rawContext)
		if snapshot != nil {
			snapshot.TaskID = storedTaskID
		}
		var result struct {
			CapabilityAdaptationResult *taskadaptation.Outcome `json:"capability_adaptation_result"`
		}
		if json.Unmarshal(rawResult, &result) != nil {
			return fmt.Errorf("experience candidate source task has an invalid result")
		}
		if result.CapabilityAdaptationResult == nil || anchor["note"] != snapshotPolicyNote(snapshot) {
			return fmt.Errorf("experience candidate policy evidence does not match its source task")
		}
		receipts = append(receipts, taskadaptation.ExperienceReceipt{
			TaskID: storedTaskID, Snapshot: snapshot, Outcome: result.CapabilityAdaptationResult,
		})
	}
	return taskadaptation.ValidateExperienceCandidate(candidateID, receipts)
}

func snapshotPolicyNote(snapshot *taskadaptation.Snapshot) string {
	if snapshot == nil {
		return ""
	}
	return snapshot.PolicyID + "@" + snapshot.PolicyVersion +
		"; applied; scope=current_task; evidence=repeated_verified_task_receipts; limitation=repeated_pattern_not_approved_workspace_policy"
}
