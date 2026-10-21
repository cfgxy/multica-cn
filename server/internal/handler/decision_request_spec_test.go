package handler

import (
	"encoding/json"
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
}
