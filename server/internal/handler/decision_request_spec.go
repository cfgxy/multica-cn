package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const decisionRequestSpecVersion = 1

type decisionRequestAuthorizationSpec struct {
	Version           int            `json:"version"`
	Kind              string         `json:"kind"`
	ActionType        string         `json:"action_type"`
	Scope             map[string]any `json:"scope"`
	ParameterDigest   string         `json:"parameter_digest"`
	SourceWorkspaceID string         `json:"source_workspace_id"`
	TargetWorkspaceID string         `json:"target_workspace_id"`
	SourceIssueID     string         `json:"source_issue_id,omitempty"`
	SourceTaskID      string         `json:"source_task_id,omitempty"`
	ExpiresAt         string         `json:"expires_at"`
	PublicSummary     string         `json:"public_summary"`
	Digest            string         `json:"digest"`
}

func decisionRequestKind(actionType string) (string, bool) {
	switch actionType {
	case DecisionActionWorkspaceInfoRead, DecisionActionPromptRestore, DecisionActionProjectPromptSync:
		return "cli", true
	case DecisionActionSingleIssueCreate:
		return "single_issue", true
	case DecisionActionScriptReview:
		return "script_review", true
	default:
		return "", false
	}
}

type decisionScriptReviewParams struct {
	Script      string   `json:"script"`
	Interpreter string   `json:"interpreter"`
	Args        []string `json:"args"`
	SHA256      string   `json:"sha256"`
}

func normalizeScriptReviewParams(raw json.RawMessage) (json.RawMessage, error) {
	var params decisionScriptReviewParams
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&params); err != nil {
		return nil, errors.New("script_review requires script, interpreter, args, and sha256")
	}
	if strings.TrimSpace(params.Script) == "" || len(params.Script) > 64*1024 || strings.IndexByte(params.Script, 0) >= 0 {
		return nil, errors.New("script_review script must contain 1-65536 bytes")
	}
	if params.Interpreter == "" || len(params.Interpreter) > 64 {
		return nil, errors.New("script_review interpreter must be a short identifier")
	}
	for _, r := range params.Interpreter {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._+-", r)) {
			return nil, errors.New("script_review interpreter must be an identifier, not a path")
		}
	}
	if params.Args == nil || len(params.Args) > 64 {
		return nil, errors.New("script_review args must contain at most 64 values")
	}
	for _, arg := range params.Args {
		if len(arg) > 4096 || strings.IndexByte(arg, 0) >= 0 {
			return nil, errors.New("script_review arguments must be at most 4096 bytes each")
		}
	}
	if len(params.SHA256) != 64 || params.SHA256 != strings.ToLower(params.SHA256) || params.SHA256 != sha256Hex(params.Script) {
		return nil, errors.New("script_review sha256 does not match script")
	}
	return json.Marshal(params)
}

type decisionSingleIssueCreateParams struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

func normalizeSingleIssueCreateParams(raw json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, errors.New("single_issue_create parameters must be an object")
	}
	delete(fields, "authorization_spec")
	canonical, err := json.Marshal(fields)
	if err != nil {
		return nil, errors.New("single_issue_create parameters are invalid")
	}
	var params decisionSingleIssueCreateParams
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&params); err != nil {
		return nil, errors.New("single_issue_create requires only a title and description")
	}
	if strings.TrimSpace(params.Title) == "" || len([]rune(params.Title)) > 160 {
		return nil, errors.New("single_issue_create title must be 1-160 characters")
	}
	if strings.TrimSpace(params.Description) == "" || len([]rune(params.Description)) > 20000 {
		return nil, errors.New("single_issue_create description must be 1-20000 characters")
	}
	params.Title = strings.TrimSpace(params.Title)
	return json.Marshal(params)
}

func buildDecisionRequestActionParams(actionType string, params []byte, sourceWorkspaceID, targetWorkspaceID, issueID, taskID string, expiresAt time.Time, publicSummary string) ([]byte, error) {
	kind, ok := decisionRequestKind(actionType)
	if !ok {
		return nil, errors.New("action is not registered for an authorization transaction kind")
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(params, &payload); err != nil || payload == nil {
		return nil, errors.New("authorization parameters must be a JSON object")
	}
	canonicalParams, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("authorization parameters could not be canonicalized")
	}
	parameterHash := sha256.Sum256(canonicalParams)
	scope := map[string]any{"target_workspace_id": targetWorkspaceID}
	for _, key := range []string{"project_ids", "target_id", "target_type"} {
		if value, exists := payload[key]; exists {
			var decoded any
			if err := json.Unmarshal(value, &decoded); err != nil {
				return nil, fmt.Errorf("invalid authorization scope field %q", key)
			}
			scope[key] = decoded
		}
	}
	spec := decisionRequestAuthorizationSpec{
		Version:           decisionRequestSpecVersion,
		Kind:              kind,
		ActionType:        actionType,
		Scope:             scope,
		ParameterDigest:   hex.EncodeToString(parameterHash[:]),
		SourceWorkspaceID: sourceWorkspaceID,
		TargetWorkspaceID: targetWorkspaceID,
		SourceIssueID:     issueID,
		SourceTaskID:      taskID,
		ExpiresAt:         expiresAt.UTC().Format(time.RFC3339Nano),
		PublicSummary:     publicSummary,
	}
	digestInput, err := json.Marshal(struct {
		Version           int            `json:"version"`
		Kind              string         `json:"kind"`
		ActionType        string         `json:"action_type"`
		Scope             map[string]any `json:"scope"`
		ParameterDigest   string         `json:"parameter_digest"`
		SourceWorkspaceID string         `json:"source_workspace_id"`
		TargetWorkspaceID string         `json:"target_workspace_id"`
		SourceIssueID     string         `json:"source_issue_id,omitempty"`
		SourceTaskID      string         `json:"source_task_id,omitempty"`
		ExpiresAt         string         `json:"expires_at"`
		PublicSummary     string         `json:"public_summary"`
	}{spec.Version, spec.Kind, spec.ActionType, spec.Scope, spec.ParameterDigest, spec.SourceWorkspaceID, spec.TargetWorkspaceID, spec.SourceIssueID, spec.SourceTaskID, spec.ExpiresAt, spec.PublicSummary})
	if err != nil {
		return nil, errors.New("authorization specification could not be encoded")
	}
	digest := sha256.Sum256(digestInput)
	spec.Digest = hex.EncodeToString(digest[:])
	specBytes, err := json.Marshal(spec)
	if err != nil {
		return nil, errors.New("authorization specification could not be encoded")
	}
	payload["authorization_spec"] = specBytes
	return json.Marshal(payload)
}

func readDecisionRequestSpec(row db.DecisionRequest) (decisionRequestAuthorizationSpec, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(row.ActionParams, &payload); err != nil || payload == nil {
		return decisionRequestAuthorizationSpec{}, errors.New("authorization parameters are invalid")
	}
	var spec decisionRequestAuthorizationSpec
	if err := json.Unmarshal(payload["authorization_spec"], &spec); err != nil {
		return decisionRequestAuthorizationSpec{}, errors.New("authorization specification is missing")
	}
	if spec.Version != decisionRequestSpecVersion || spec.ActionType != row.ActionType {
		return decisionRequestAuthorizationSpec{}, errors.New("authorization specification version or action mismatch")
	}
	delete(payload, "authorization_spec")
	canonicalParams, err := json.Marshal(payload)
	if err != nil {
		return decisionRequestAuthorizationSpec{}, errors.New("authorization parameters are invalid")
	}
	if row.ActionType == DecisionActionScriptReview {
		if _, err := normalizeScriptReviewParams(canonicalParams); err != nil {
			return decisionRequestAuthorizationSpec{}, errors.New("script review payload or hash is invalid")
		}
	}
	parameterHash := sha256.Sum256(canonicalParams)
	if hex.EncodeToString(parameterHash[:]) != spec.ParameterDigest {
		return decisionRequestAuthorizationSpec{}, errors.New("authorization parameters changed after approval")
	}
	rebuilt, err := buildDecisionRequestActionParams(row.ActionType, canonicalParams, spec.SourceWorkspaceID, spec.TargetWorkspaceID, spec.SourceIssueID, spec.SourceTaskID, mustParseDecisionExpiry(spec.ExpiresAt), spec.PublicSummary)
	if err != nil {
		return decisionRequestAuthorizationSpec{}, errors.New("authorization specification is invalid")
	}
	var rebuiltPayload map[string]json.RawMessage
	if json.Unmarshal(rebuilt, &rebuiltPayload) != nil {
		return decisionRequestAuthorizationSpec{}, errors.New("authorization specification is invalid")
	}
	var rebuiltSpec decisionRequestAuthorizationSpec
	if json.Unmarshal(rebuiltPayload["authorization_spec"], &rebuiltSpec) != nil || rebuiltSpec.Digest != spec.Digest {
		return decisionRequestAuthorizationSpec{}, errors.New("authorization specification digest mismatch")
	}
	if spec.SourceWorkspaceID != uuidToString(row.OriginWorkspaceID) {
		return decisionRequestAuthorizationSpec{}, errors.New("authorization source scope mismatch")
	}
	return spec, nil
}

func validateDecisionRequestGroupSpec(rows []db.DecisionRequest) error {
	if len(rows) == 0 {
		return errors.New("authorization request group is empty")
	}
	var origin, target *db.DecisionRequest
	var expected decisionRequestAuthorizationSpec
	for i := range rows {
		row := &rows[i]
		spec, err := readDecisionRequestSpec(*row)
		if err != nil {
			return err
		}
		if i == 0 {
			expected = spec
		} else if spec.Digest != expected.Digest {
			return errors.New("authorization request rows do not share one specification digest")
		}
		switch row.Role {
		case decisionReqRoleOrigin:
			if origin != nil {
				return errors.New("authorization request has duplicate origin rows")
			}
			origin = row
		case decisionReqRoleTarget:
			if target != nil {
				return errors.New("authorization request has duplicate target rows")
			}
			target = row
		default:
			return errors.New("authorization request has an invalid row role")
		}
		if row.ActionType != expected.ActionType || row.Title != expected.PublicSummary ||
			row.ExpiresAt.Time.UTC().Format(time.RFC3339Nano) != expected.ExpiresAt {
			return errors.New("authorization request metadata does not match its specification")
		}
		issueID, taskID := "", ""
		if row.OriginIssueID.Valid {
			issueID = uuidToString(row.OriginIssueID)
		}
		if row.OriginTaskID.Valid {
			taskID = uuidToString(row.OriginTaskID)
		}
		if issueID != expected.SourceIssueID || taskID != expected.SourceTaskID ||
			uuidToString(row.OriginWorkspaceID) != expected.SourceWorkspaceID {
			return errors.New("authorization source metadata does not match its specification")
		}
	}
	if origin == nil {
		return errors.New("authorization request has no origin row")
	}
	if uuidToString(origin.WorkspaceID) != expected.SourceWorkspaceID {
		return errors.New("authorization origin workspace does not match its specification")
	}
	actualTarget := origin.WorkspaceID
	if target != nil {
		actualTarget = target.WorkspaceID
	}
	if uuidToString(actualTarget) != expected.TargetWorkspaceID {
		return errors.New("authorization target workspace does not match its specification")
	}
	return nil
}

func validateDecisionRequestRowSpec(row db.DecisionRequest) error {
	spec, err := readDecisionRequestSpec(row)
	if err != nil {
		return err
	}
	if row.Role == decisionReqRoleOrigin && uuidToString(row.WorkspaceID) != spec.SourceWorkspaceID {
		return errors.New("authorization origin workspace does not match its specification")
	}
	if row.Role == decisionReqRoleTarget && uuidToString(row.WorkspaceID) != spec.TargetWorkspaceID {
		return errors.New("authorization target workspace does not match its specification")
	}
	issueID, taskID := "", ""
	if row.OriginIssueID.Valid {
		issueID = uuidToString(row.OriginIssueID)
	}
	if row.OriginTaskID.Valid {
		taskID = uuidToString(row.OriginTaskID)
	}
	if issueID != spec.SourceIssueID || taskID != spec.SourceTaskID || row.Title != spec.PublicSummary ||
		row.ExpiresAt.Time.UTC().Format(time.RFC3339Nano) != spec.ExpiresAt {
		return errors.New("authorization request metadata does not match its specification")
	}
	return nil
}

func mustParseDecisionExpiry(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
