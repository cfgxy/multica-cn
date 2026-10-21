package handler

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestDecisionRequestAuthorizationSpecCanonicalAndBound(t *testing.T) {
	expires := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	first, err := buildDecisionRequestActionParams(DecisionActionWorkspaceInfoRead,
		[]byte(`{"include_counts":true,"z":1}`), "source", "target", "issue", "task", expires, "Read workspace")
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildDecisionRequestActionParams(DecisionActionWorkspaceInfoRead,
		[]byte(`{"z":1,"include_counts":true}`), "source", "target", "issue", "task", expires, "Read workspace")
	if err != nil {
		t.Fatal(err)
	}
	var firstParams, secondParams map[string]json.RawMessage
	if err := json.Unmarshal(first, &firstParams); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second, &secondParams); err != nil {
		t.Fatal(err)
	}
	var firstSpec, secondSpec decisionRequestAuthorizationSpec
	if err := json.Unmarshal(firstParams["authorization_spec"], &firstSpec); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(secondParams["authorization_spec"], &secondSpec); err != nil {
		t.Fatal(err)
	}
	if firstSpec.Digest == "" || firstSpec.Digest != secondSpec.Digest {
		t.Fatalf("canonical digest mismatch: %q != %q", firstSpec.Digest, secondSpec.Digest)
	}
	if firstSpec.Version != decisionRequestSpecVersion || firstSpec.Kind != "cli" {
		t.Fatalf("spec version/kind = %d/%q", firstSpec.Version, firstSpec.Kind)
	}

	row := db.DecisionRequest{
		ActionType:        DecisionActionWorkspaceInfoRead,
		ActionParams:      first,
		OriginWorkspaceID: parseUUID("00000000-0000-7000-8000-000000000001"),
		Role:              decisionReqRoleOrigin,
		WorkspaceID:       parseUUID("00000000-0000-7000-8000-000000000001"),
		Title:             "Read workspace",
		ExpiresAt:         pgtype.Timestamptz{Time: expires, Valid: true},
	}
	// Build again with canonical UUID metadata so row validation exercises the
	// same stable identity that persistence and approvals use.
	row.ActionParams, err = buildDecisionRequestActionParams(DecisionActionWorkspaceInfoRead,
		[]byte(`{"include_counts":true,"z":1}`), uuidToString(row.WorkspaceID), "00000000-0000-7000-8000-000000000002", "", "", expires, row.Title)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDecisionRequestRowSpec(row); err != nil {
		t.Fatalf("valid row rejected: %v", err)
	}

	row.ActionParams = []byte(`{"include_counts":false,"z":1,"authorization_spec":{}}`)
	if err := validateDecisionRequestRowSpec(row); err == nil {
		t.Fatal("mutated or missing authorization digest was accepted")
	}
}

func TestDecisionRequestAuthorizationSpecRejectsUnregisteredKinds(t *testing.T) {
	_, err := buildDecisionRequestActionParams("arbitrary_shell", []byte(`{"command":"echo unsafe"}`),
		"source", "target", "", "", time.Now().UTC(), "Run command")
	if err == nil {
		t.Fatal("unregistered action was accepted")
	}
	if _, ok := decisionRequestKind("restricted_script"); ok {
		t.Fatal("restricted script kind must stay unavailable without a proven sandbox")
	}
}

func TestDecisionRequestSingleIssueParamsAreNarrowAndVersioned(t *testing.T) {
	valid := json.RawMessage(`{"title":"Fix one concrete defect","description":"Make the specified callback idempotent."}`)
	normalized, err := normalizeSingleIssueCreateParams(valid)
	if err != nil {
		t.Fatalf("valid single issue params rejected: %v", err)
	}
	encoded, err := buildDecisionRequestActionParams(DecisionActionSingleIssueCreate, normalized,
		"source", "target", "", "", time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), "Create one issue")
	if err != nil {
		t.Fatalf("build single issue authorization spec: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	var spec decisionRequestAuthorizationSpec
	if err := json.Unmarshal(payload["authorization_spec"], &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Kind != "single_issue" || spec.ParameterDigest == "" || spec.Digest == "" {
		t.Fatalf("single issue spec = %+v", spec)
	}
	for _, invalid := range []json.RawMessage{
		[]byte(`{"title":"one","description":"specific","assignee_id":"forged"}`),
		[]byte(`{"title":" ","description":"specific"}`),
		[]byte(`{"title":"one","description":" "}`),
	} {
		if _, err := normalizeSingleIssueCreateParams(invalid); err == nil {
			t.Fatalf("invalid params accepted: %s", invalid)
		}
	}
}

func TestDecisionRequestScriptReviewBindsSnapshotWithoutRegisteringExecutionKind(t *testing.T) {
	script := "printf '%s\\n' reviewed"
	params := json.RawMessage(fmt.Sprintf(`{"script":%q,"interpreter":"bash","args":["-e"],"sha256":%q}`, script, sha256Hex(script)))
	normalized, err := normalizeScriptReviewParams(params)
	if err != nil {
		t.Fatalf("normalize script review params: %v", err)
	}
	source := "00000000-0000-7000-8000-000000000001"
	target := "00000000-0000-7000-8000-000000000002"
	expires := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	encoded, err := buildDecisionRequestActionParams(DecisionActionScriptReview, normalized, source, target, "", "", expires, "Review script snapshot")
	if err != nil {
		t.Fatalf("build script review spec: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	var spec decisionRequestAuthorizationSpec
	if err := json.Unmarshal(payload["authorization_spec"], &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Kind != "script_review" || spec.ActionType != DecisionActionScriptReview || spec.Digest == "" {
		t.Fatalf("script review spec = %+v", spec)
	}
	row := db.DecisionRequest{
		ActionType: DecisionActionScriptReview, ActionParams: encoded,
		OriginWorkspaceID: parseUUID(source), WorkspaceID: parseUUID(source),
		Role: decisionReqRoleOrigin, Title: "Review script snapshot",
		ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	}
	if _, err := readDecisionRequestSpec(row); err != nil {
		t.Fatalf("valid script snapshot rejected: %v", err)
	}
	var projected map[string]json.RawMessage
	if err := json.Unmarshal(decisionRequestProjectedParams(row, false), &projected); err != nil {
		t.Fatal(err)
	}
	if len(projected) != 1 || string(projected["sha256"]) != fmt.Sprintf("%q", sha256Hex(script)) {
		t.Fatalf("public params projection = %s", decisionRequestProjectedParams(row, false))
	}
	if string(decisionRequestProjectedParams(row, true)) != string(encoded) {
		t.Fatal("authorized detail projection did not retain the fixed snapshot")
	}

	var changed map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &changed); err != nil {
		t.Fatal(err)
	}
	changed["script"] = json.RawMessage(`"printf tampered"`)
	row.ActionParams, err = json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readDecisionRequestSpec(row); err == nil {
		t.Fatal("tampered script was accepted")
	}
	for _, invalid := range []json.RawMessage{
		[]byte(`{"script":"echo x","interpreter":"/bin/sh","args":[],"sha256":"` + sha256Hex("echo x") + `"}`),
		[]byte(`{"script":"echo x","interpreter":"sh","args":[],"sha256":"` + strings.ToUpper(sha256Hex("echo x")) + `"}`),
		[]byte(`{"script":"echo x","interpreter":"sh","args":[],"sha256":"` + sha256Hex("different") + `","extra":true}`),
	} {
		if _, err := normalizeScriptReviewParams(invalid); err == nil {
			t.Fatalf("invalid script review payload accepted: %s", invalid)
		}
	}
	if _, ok := decisionRequestKind("script"); ok {
		t.Fatal("generic script authorization kind must remain unavailable")
	}
	if _, err := (&Handler{}).executeDecisionRequestAction(nil, nil, db.DecisionRequest{ActionType: DecisionActionScriptReview}); err == nil {
		t.Fatal("script_review reached an action executor")
	}
}
