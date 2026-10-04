package handler

// Feature tests for the warn-only jev advisory sidecar (RUYI-347 batch A):
// the submit precheck and the gate-stage soft judgment ride alongside the
// E1–E4 engine without touching its verdicts. Every test pins the same
// contract from the dispatch card — warn-only, degrade-never-block,
// switch-off identical — through the real handler chain against the fixture
// database.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/legislation"
)

// jevStub is an in-process stand-in for the jev pilot: it records every
// judged input and answers with a canned verdict so each test scripts the
// failure probability. The judged text is held in memory only.
type jevStub struct {
	inputs []string
	srv    *httptest.Server
}

func newJevStub(t *testing.T, pFailure float64, warn bool) *jevStub {
	t.Helper()
	stub := &jevStub{}
	stub.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		stub.inputs = append(stub.inputs, body.Input)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"task": "kev_local", "p_success": 1 - pFailure, "p_failure": pFailure,
			"threshold": 0.3, "warn": warn, "decision": "fail_expected",
			"engine": "stub-engine", "threshold_source": "default",
		})
	}))
	t.Cleanup(stub.srv.Close)
	return stub
}

// withJevAdvisory injects a client for the duration of one test and restores
// whatever the process default was.
func withJevAdvisory(t *testing.T, c *legislation.AdvisoryClient) {
	t.Helper()
	prev := testHandler.JevAdvisory
	testHandler.JevAdvisory = c
	t.Cleanup(func() { testHandler.JevAdvisory = prev })
}

func jevAdvisoryRow(t *testing.T, wsID, id string) *string {
	t.Helper()
	var adv *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT jev_advisory::text FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`, wsID, id).Scan(&adv); err != nil {
		t.Fatalf("read jev_advisory: %v", err)
	}
	return adv
}

func advStr(row *string) string {
	if row == nil {
		return "<NULL>"
	}
	return *row
}

// advCompact strips the spaces PostgreSQL's jsonb text output inserts, so
// assertions can match the compact canonical form the report marshals to.
func advCompact(row *string) string {
	return strings.ReplaceAll(advStr(row), " ", "")
}

func gateErrorsRow(t *testing.T, wsID, id string) string {
	t.Helper()
	var errs string
	if err := testPool.QueryRow(context.Background(),
		`SELECT gate_errors::text FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`, wsID, id).Scan(&errs); err != nil {
		t.Fatalf("read gate_errors: %v", err)
	}
	return errs
}

func ownerApprove(t *testing.T, ownerID, wsID, id string) (int, string) {
	t.Helper()
	approve := ownerRoute(http.HandlerFunc(testHandler.ApprovePromptProposal))
	return legislationCall(t, approve.ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/approve",
			map[string]any{"confirm_diff_previewed": true}, "id", id))
}

// TestLegislationAdvisorySubmitPrecheck: submitting a draft prechecks the
// clause text, stores the true-probability verdict on the row, and echoes it
// in the response — warn flag and probability ride through verbatim.
func TestLegislationAdvisorySubmitPrecheck(t *testing.T) {
	wsID, _, memberID := legislationFixture(t)
	stub := newJevStub(t, 0.8, true)
	withJevAdvisory(t, &legislation.AdvisoryClient{BaseURL: stub.srv.URL, Timeout: 2 * time.Second})

	id := createDraft(t, wsID, memberID, "每日同步", cleanClauseText)
	code, body := legislationCall(t, testHandler.SubmitPromptProposal,
		legislationReq(memberID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/submit", nil, "id", id))
	if code != http.StatusOK {
		t.Fatalf("submit: expected 200, got %d: %s", code, body)
	}

	// The judged input is the clause text, verbatim, exactly once.
	if len(stub.inputs) != 1 || stub.inputs[0] != cleanClauseText {
		t.Fatalf("stub judged %d inputs, first %q — want the clause text once", len(stub.inputs), stub.inputs)
	}

	sum := sha256.Sum256([]byte(cleanClauseText))
	wantSHA := hex.EncodeToString(sum[:])
	var resp struct {
		JevAdvisory *struct {
			Stage       string  `json:"stage"`
			Available   bool    `json:"available"`
			Engine      string  `json:"engine"`
			PFailure    float64 `json:"p_failure"`
			Threshold   float64 `json:"threshold"`
			Warn        bool    `json:"warn"`
			InputSHA256 string  `json:"input_sha256"`
			CheckedAt   string  `json:"checked_at"`
		} `json:"jev_advisory"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode submit response: %v (%s)", err, body)
	}
	adv := resp.JevAdvisory
	if adv == nil {
		t.Fatalf("submit response carries no jev_advisory: %s", body)
	}
	if adv.Stage != "submit" || !adv.Available || adv.Engine != "stub-engine" {
		t.Fatalf("advisory stage/available/engine = %s/%v/%s", adv.Stage, adv.Available, adv.Engine)
	}
	if adv.PFailure != 0.8 || adv.Threshold != 0.3 || !adv.Warn {
		t.Fatalf("advisory verdict = p_failure %v threshold %v warn %v, want 0.8/0.3/true", adv.PFailure, adv.Threshold, adv.Warn)
	}
	if adv.InputSHA256 != wantSHA || adv.CheckedAt == "" {
		t.Fatalf("advisory provenance: sha %q checked_at %q", adv.InputSHA256, adv.CheckedAt)
	}

	row := jevAdvisoryRow(t, wsID, id)
	if row == nil || !strings.Contains(advCompact(row), `"stage":"submit"`) || !strings.Contains(advCompact(row), `"warn":true`) {
		t.Fatalf("row jev_advisory not persisted with the submit report: %s", advStr(row))
	}
}

// TestLegislationAdvisoryGateWarnOnly: at approve time the synthesized
// full-carrier text is judged and the gate-stage report overwrites the
// submit-stage one — while a maximal failure probability still cannot flip
// the E1–E4 verdict (warn-only, never blocks).
func TestLegislationAdvisoryGateWarnOnly(t *testing.T) {
	wsID, ownerID, memberID := legislationFixture(t)
	stub := newJevStub(t, 0.99, true)
	withJevAdvisory(t, &legislation.AdvisoryClient{BaseURL: stub.srv.URL, Timeout: 2 * time.Second})

	id := createDraft(t, wsID, memberID, "每日同步", cleanClauseText)
	submitDraft(t, wsID, memberID, id)
	code, body := ownerApprove(t, ownerID, wsID, id)
	if code != http.StatusOK {
		t.Fatalf("approve: expected 200, got %d: %s", code, body)
	}
	if got := proposalStatus(t, wsID, id); got != "enacted" {
		t.Fatalf("p_failure 0.99 blocked the enact: status = %s, body %s", got, body)
	}

	if len(stub.inputs) != 2 {
		t.Fatalf("stub judged %d inputs, want 2 (submit clause + gate synthesis)", len(stub.inputs))
	}
	synth := stub.inputs[1]
	if !strings.Contains(synth, "每日同步") || !strings.Contains(synth, "平台协作规范") {
		t.Fatalf("gate judged %q — want the synthesized full carrier", truncForAssert(synth))
	}

	row := jevAdvisoryRow(t, wsID, id)
	if row == nil || !strings.Contains(advCompact(row), `"stage":"gate"`) || !strings.Contains(advCompact(row), `"p_failure":0.99`) {
		t.Fatalf("row jev_advisory not overwritten by the gate report: %s", advStr(row))
	}
	if errs := gateErrorsRow(t, wsID, id); errs != "[]" {
		t.Fatalf("warn-only violated: gate_errors = %s", errs)
	}
}

// TestLegislationAdvisoryDisabledKeepsGateIdentical: with the layer switched
// off (empty BaseURL) the advisory column stays NULL and the E1–E4 verdicts
// are byte-identical to the enabled run over the same weak clause.
func TestLegislationAdvisoryDisabledKeepsGateIdentical(t *testing.T) {
	wsOn, ownerOn, memberOn := legislationFixture(t)
	wsOff, ownerOff, memberOff := legislationFixture(t)

	stub := newJevStub(t, 0.01, false)
	withJevAdvisory(t, &legislation.AdvisoryClient{BaseURL: stub.srv.URL, Timeout: 2 * time.Second})
	idOn := createDraft(t, wsOn, memberOn, "弱表述条款", weakClauseText)
	submitDraft(t, wsOn, memberOn, idOn)
	// A weak-wording clause is blocked by E1: the approve answers 409 with
	// the gate_failed verdict — identical for both runs by the end of the test.
	if code, body := ownerApprove(t, ownerOn, wsOn, idOn); code != http.StatusConflict {
		t.Fatalf("approve (enabled): expected 409 gate_failed, got %d: %s", code, body)
	}

	withJevAdvisory(t, &legislation.AdvisoryClient{}) // disabled: empty BaseURL
	idOff := createDraft(t, wsOff, memberOff, "弱表述条款", weakClauseText)
	code, body := legislationCall(t, testHandler.SubmitPromptProposal,
		legislationReq(memberOff, wsOff, http.MethodPost, "/api/prompt-legislation/proposals/"+idOff+"/submit", nil, "id", idOff))
	if code != http.StatusOK {
		t.Fatalf("submit (disabled): expected 200, got %d: %s", code, body)
	}
	if row := jevAdvisoryRow(t, wsOff, idOff); row != nil {
		t.Fatalf("disabled layer wrote jev_advisory: %s", advStr(row))
	}
	if code, body := ownerApprove(t, ownerOff, wsOff, idOff); code != http.StatusConflict {
		t.Fatalf("approve (disabled): expected 409 gate_failed, got %d: %s", code, body)
	}

	if got := proposalStatus(t, wsOff, idOff); got != "gate_failed" {
		t.Fatalf("disabled run status = %s, want gate_failed: %s", got, body)
	}
	errsOn, errsOff := gateErrorsRow(t, wsOn, idOn), gateErrorsRow(t, wsOff, idOff)
	if errsOn == "" || errsOn != errsOff {
		t.Fatalf("E1–E4 verdict differs between enabled and disabled runs:\n enabled:  %s\n disabled: %s", errsOn, errsOff)
	}
	// The enabled row's last verdict is the gate-stage report; the disabled
	// row keeps NULL — the switch semantics from the dispatch card.
	if row := jevAdvisoryRow(t, wsOn, idOn); row == nil || !strings.Contains(advCompact(row), `"stage":"gate"`) {
		t.Fatalf("enabled run lost its gate-stage advisory: %s", advStr(row))
	}
}

// TestLegislationAdvisoryUnreachableDegrades: a dead service must not fail
// submit or approve — the report records the skip reason and both flows
// complete exactly as they would with the layer off.
func TestLegislationAdvisoryUnreachableDegrades(t *testing.T) {
	wsID, ownerID, memberID := legislationFixture(t)
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	withJevAdvisory(t, &legislation.AdvisoryClient{BaseURL: deadURL, Timeout: 300 * time.Millisecond})

	id := createDraft(t, wsID, memberID, "每日同步", cleanClauseText)
	code, body := legislationCall(t, testHandler.SubmitPromptProposal,
		legislationReq(memberID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/submit", nil, "id", id))
	if code != http.StatusOK {
		t.Fatalf("submit must survive an unreachable advisory: got %d: %s", code, body)
	}
	row := jevAdvisoryRow(t, wsID, id)
	if row == nil || !strings.Contains(advCompact(row), `"available":false`) || !strings.Contains(*row, "unreachable") {
		t.Fatalf("submit row lacks the degrade report: %s", advStr(row))
	}

	if code, body := ownerApprove(t, ownerID, wsID, id); code != http.StatusOK {
		t.Fatalf("approve must survive an unreachable advisory: got %d: %s", code, body)
	}
	if got := proposalStatus(t, wsID, id); got != "enacted" {
		t.Fatalf("advisory outage blocked the enact: status = %s", got)
	}
}

func truncForAssert(s string) string {
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}
