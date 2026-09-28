package handler

// The reading is only a reading if one measuring stick produced it
// (RUYI-185 review, blocker 1 + A1).
//
// pkg/promptquiz/cohort_test.go owns the rule's matrix and its clause-by-clause
// reverse verification. This file owns what only the HTTP boundary and the
// database can show:
//
//   - the baseline endpoint does not pool two wordings or two runtimes that sit
//     inside one version,
//   - the sweep's SQL graded count agrees with the sample the read path builds,
//     because a count that runs ahead of the reader would stop the sweep from
//     topping a sample up that is not actually full,
//   - the bank list never carries an item's rubric while the owner-only single
//     read does (A2), and the list carries the discrimination mark (A4).
//
// Every case is a pair: the same rows are sent once differing in the isolation
// dimension and once identical in it. The second half is what proves the first
// half separated for the reason claimed — remove the isolation and the two
// halves produce the same numbers, which fails.

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/promptquiz"
)

// reading is one row to store in prompt_quiz_result, written in the terms the
// cohort key is made of.
type reading struct {
	revision int32
	body     string // digest source; different body ⇒ different digest
	runtime  string // "" stores NULL
	model    string // "" stores NULL
	outcome  string
	tokens   int64
	unmeasur bool // true stores run_tokens NULL
}

// answered is the ordinary case: a run that answered at the current wording on
// the current runtime.
func answered(runtime, model string, tokens int64) reading {
	return reading{revision: 1, body: "anchor", runtime: runtime, model: model,
		outcome: promptquiz.OutcomeAnswered, tokens: tokens}
}

// quizScope is one agent with a prompt version and a bank item, plus the rows
// stored against it.
type quizScope struct {
	agentID string
	itemID  string
	runtime string
	version int32
}

func newQuizScope(t *testing.T, name string) *quizScope {
	t.Helper()
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtime := handlerTestRuntimeID(t)
	agentID := dbfx.Agent(t, name, runtime)
	s := &quizScope{agentID: agentID, itemID: seedQuizItem(t, name+"-item"), runtime: runtime, version: 2}
	dbfx.Insert(t, "prompt_version", map[string]any{
		"workspace_id":   testWorkspaceID,
		"scope":          "agent",
		"scope_id":       agentID,
		"version":        s.version,
		"content":        "cohort fixture prompt",
		"content_sha256": "1111111111111111111111111111111111111111111111111111111111111111",
		"source":         "import",
	})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM prompt_version WHERE scope = 'agent' AND scope_id = $1`, agentID)
		testPool.Exec(context.Background(), `DELETE FROM prompt_quiz_result WHERE scope = 'agent' AND scope_id = $1`, agentID)
	})
	return s
}

// store writes the readings oldest-first, so the LAST element is the newest row
// and therefore the one that defines the cohort.
func (s *quizScope) store(t *testing.T, version int32, rows ...reading) {
	t.Helper()
	batchID := newQuizUUID(t)
	for i, row := range rows {
		taskID := dbfx.Task(t, s.agentID, map[string]any{
			"status":            "completed",
			"originator_source": promptquiz.OriginatorSource,
			"completed_at":      testutil.Raw("now()"),
		})
		cols := map[string]any{
			"workspace_id":     testWorkspaceID,
			"scope":            "agent",
			"scope_id":         s.agentID,
			"version":          version,
			"item_id":          s.itemID,
			"item_revision":    row.revision,
			"item_body_sha256": promptquiz.BodyDigest(row.body),
			"batch_id":         batchID,
			"task_id":          taskID,
			"outcome":          row.outcome,
			// Spread measured_at so "newest first" is well defined; i grows with
			// the caller's order, so the last argument is the newest row.
			"measured_at": testutil.Raw("now() - interval '1 second' * " + strconv.Itoa(len(rows)-i)),
		}
		if !row.unmeasur {
			cols["run_tokens"] = row.tokens
		}
		if row.runtime != "" {
			cols["runtime_id"] = row.runtime
		}
		if row.model != "" {
			cols["run_model"] = row.model
		}
		dbfx.Insert(t, "prompt_quiz_result", cols)
	}
}

func (s *quizScope) baseline(t *testing.T) PromptQuizBaselineResponse {
	t.Helper()
	code, raw := callPromptGov(t, testHandler.GetPromptQuizBaseline, http.MethodGet,
		"/api/prompt-governance/agent/"+s.agentID+"/quiz", nil,
		map[string]string{"scope": "agent", "scopeId": s.agentID})
	if code != http.StatusOK {
		t.Fatalf("baseline read returned %d: %s", code, raw)
	}
	var resp PromptQuizBaselineResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("decode baseline: %v (%s)", err, raw)
	}
	return resp
}

// graded is the sweep's own count of stored measurements for the version, the
// number its "how far from N" stop condition is computed from.
func (s *quizScope) graded(t *testing.T, version int32) int {
	t.Helper()
	have, err := testHandler.Queries.CountPromptQuizMeasurementsForVersion(context.Background(),
		db.CountPromptQuizMeasurementsForVersionParams{
			Scope: "agent", ScopeID: parseUUID(s.agentID), Version: version,
		})
	if err != nil {
		t.Fatalf("CountPromptQuizMeasurementsForVersion: %v", err)
	}
	return int(have.Graded)
}

// ── blocker 1: a bank edit must not change the measuring stick mid-curve ────

// Two wordings of one question inside ONE version. Before the cohort the reader
// pooled them, so the curve moved because the question had been reworded — and
// migration 929 had already declared that such a reading "is not comparable and
// is excluded by revision".
func TestBaselineDoesNotPoolTwoWordingsOfOneItem(t *testing.T) {
	mixed := newQuizScope(t, "quiz-cohort-mixed-wording")
	// Oldest first: two readings at the retired wording, then two at the current
	// one. The newest row defines the stick, so revision 2 is current.
	mixed.store(t, mixed.version,
		reading{revision: 1, body: "old wording", runtime: mixed.runtime, model: "m",
			outcome: promptquiz.OutcomeAnswered, tokens: 900_000},
		reading{revision: 1, body: "old wording", runtime: mixed.runtime, model: "m",
			outcome: promptquiz.OutcomeAnswered, tokens: 910_000},
		reading{revision: 2, body: "new wording", runtime: mixed.runtime, model: "m",
			outcome: promptquiz.OutcomeAnswered, tokens: 100_000},
		reading{revision: 2, body: "new wording", runtime: mixed.runtime, model: "m",
			outcome: promptquiz.OutcomeAnswered, tokens: 110_000},
	)

	resp := mixed.baseline(t)
	if resp.Current.N != 2 {
		t.Errorf("current group N = %d, want 2 — the two readings taken before the item was reworded were pooled back in", resp.Current.N)
	}
	if resp.Incomparable != 2 {
		t.Errorf("incomparable = %d, want 2; a group that shrank from 4 to 2 must say why, or the drop looks like a collection failure", resp.Incomparable)
	}
	// The retired wording is an order of magnitude more expensive, so pooling is
	// visible in the level as well as in N.
	if resp.Current.Median > 200_000 {
		t.Errorf("current median = %g, want the ~100k level of the current wording", resp.Current.Median)
	}

	// The pair: identical rows except that all four share one wording. If the
	// separation above were caused by anything other than the wording, this half
	// would also come back as 2.
	same := newQuizScope(t, "quiz-cohort-one-wording")
	same.store(t, same.version,
		answered(same.runtime, "m", 900_000),
		answered(same.runtime, "m", 910_000),
		answered(same.runtime, "m", 100_000),
		answered(same.runtime, "m", 110_000),
	)
	control := same.baseline(t)
	if control.Current.N != 4 || control.Incomparable != 0 {
		t.Errorf("control group N = %d, incomparable = %d; want 4 and 0 — with the wording held equal nothing may be excluded, otherwise the assertion above does not isolate the wording",
			control.Current.N, control.Incomparable)
	}
}

// The A1 ruling at the boundary: the same prompt measured on another runtime or
// another model is a reading from another instrument.
func TestBaselineDoesNotPoolTwoRuntimes(t *testing.T) {
	mixed := newQuizScope(t, "quiz-cohort-mixed-runtime")
	other := dbfx.Runtime(t, "quiz-cohort-other-runtime")
	mixed.store(t, mixed.version,
		answered(other, "m", 900_000),
		answered(mixed.runtime, "other-model", 920_000),
		answered(mixed.runtime, "m", 100_000),
		answered(mixed.runtime, "m", 110_000),
	)

	resp := mixed.baseline(t)
	if resp.Current.N != 2 {
		t.Errorf("current group N = %d, want 2 — readings from another runtime or model were pooled in", resp.Current.N)
	}
	if resp.Incomparable != 2 {
		t.Errorf("incomparable = %d, want 2 (one other runtime, one other model)", resp.Incomparable)
	}

	same := newQuizScope(t, "quiz-cohort-one-runtime")
	same.store(t, same.version,
		answered(same.runtime, "m", 900_000),
		answered(same.runtime, "m", 920_000),
		answered(same.runtime, "m", 100_000),
		answered(same.runtime, "m", 110_000),
	)
	control := same.baseline(t)
	if control.Current.N != 4 || control.Incomparable != 0 {
		t.Errorf("control group N = %d, incomparable = %d; want 4 and 0 — with the instrument held equal nothing may be excluded",
			control.Current.N, control.Incomparable)
	}
}

// A reading stored without a runtime cannot be placed on any instrument, so it
// is not comparable with the attributed ones and not with another unattributed
// row either.
func TestBaselineExcludesUnattributedReadings(t *testing.T) {
	s := newQuizScope(t, "quiz-cohort-unattributed")
	s.store(t, s.version,
		reading{revision: 1, body: "anchor", outcome: promptquiz.OutcomeAnswered, tokens: 900_000},
		answered(s.runtime, "m", 100_000),
	)
	resp := s.baseline(t)
	if resp.Current.N != 1 || resp.Incomparable != 1 {
		t.Errorf("N = %d, incomparable = %d; want 1 and 1 — an unattributed reading has no instrument to be compared on",
			resp.Current.N, resp.Incomparable)
	}
}

// The baseline version is re-read through the CURRENT version's stick. A version
// measured before the bank was edited contributes only the readings still
// comparable with what the deployment measures now, and says how many it lost.
func TestBaselineGroupIsReadThroughTheCurrentStick(t *testing.T) {
	s := newQuizScope(t, "quiz-cohort-cross-version")
	// v1: mostly the retired wording, one reading at what is now current.
	s.store(t, 1,
		reading{revision: 1, body: "old wording", runtime: s.runtime, model: "m",
			outcome: promptquiz.OutcomeAnswered, tokens: 900_000},
		reading{revision: 1, body: "old wording", runtime: s.runtime, model: "m",
			outcome: promptquiz.OutcomeAnswered, tokens: 910_000},
		reading{revision: 2, body: "new wording", runtime: s.runtime, model: "m",
			outcome: promptquiz.OutcomeAnswered, tokens: 200_000},
	)
	// v2 (current): the new wording only.
	s.store(t, s.version,
		reading{revision: 2, body: "new wording", runtime: s.runtime, model: "m",
			outcome: promptquiz.OutcomeAnswered, tokens: 100_000},
	)

	resp := s.baseline(t)
	if resp.Comparison == nil {
		t.Fatal("no comparison returned although two versions were measured")
	}
	if resp.Comparison.Baseline.N != 1 {
		t.Errorf("baseline group N = %d, want 1 — only the v1 reading taken at the current wording is comparable", resp.Comparison.Baseline.N)
	}
	if resp.BaselineIncomparable != 2 {
		t.Errorf("baseline_incomparable = %d, want 2 — the v1 readings at the retired wording must be counted, not silently dropped", resp.BaselineIncomparable)
	}
}

// ── blocker 1, second half: the sweep's count must not run ahead of the reader ──

// The sweep stops topping a sample up when its own count reaches N. If that
// count includes rows the reader excludes, the sweep believes a sample is full
// while the endpoint reports far fewer readings — and the shortfall never gets
// filled.
func TestSweepGradedCountMatchesTheReadPath(t *testing.T) {
	s := newQuizScope(t, "quiz-cohort-graded-parity")
	other := dbfx.Runtime(t, "quiz-cohort-graded-other-runtime")
	s.store(t, s.version,
		// Retired wording: a reading, but not on the current stick.
		reading{revision: 1, body: "old wording", runtime: s.runtime, model: "m",
			outcome: promptquiz.OutcomeAnswered, tokens: 900_000},
		// Another instrument: same.
		answered(other, "m", 910_000),
		// Answered but no usage rows ever arrived: not a reading at all.
		reading{revision: 1, body: "anchor", runtime: s.runtime, model: "m",
			outcome: promptquiz.OutcomeAnswered, unmeasur: true},
		// A run that produced no answer.
		reading{revision: 1, body: "anchor", runtime: s.runtime, model: "m",
			outcome: promptquiz.OutcomeErrored, tokens: 5_000},
		// On the stick.
		answered(s.runtime, "m", 100_000),
		answered(s.runtime, "m", 110_000),
	)

	resp := s.baseline(t)
	if resp.Current.N != 2 {
		t.Fatalf("read path N = %d, want 2", resp.Current.N)
	}
	if got := s.graded(t, s.version); got != resp.Current.N {
		t.Errorf("the sweep counts %d graded measurements while the read path builds a sample of %d; the sweep would stop topping up a sample that is not full",
			got, resp.Current.N)
	}
	if outcomes := resp.Outcomes; outcomes[promptquiz.OutcomeErrored] != 1 || outcomes[promptquiz.OutcomeAnswered] != 5 {
		t.Errorf("outcomes = %v, want 5 answered and 1 errored — outcome counts describe the version, not the current wording", outcomes)
	}

	// The pair: the same six rows, all on the stick and all measured. Both
	// numbers must move together, which is what makes the agreement above a
	// statement about one shared rule rather than a coincidence of two zeros.
	control := newQuizScope(t, "quiz-cohort-graded-control")
	control.store(t, control.version,
		answered(control.runtime, "m", 900_000),
		answered(control.runtime, "m", 910_000),
		answered(control.runtime, "m", 920_000),
		answered(control.runtime, "m", 930_000),
		answered(control.runtime, "m", 100_000),
		answered(control.runtime, "m", 110_000),
	)
	cresp := control.baseline(t)
	if cresp.Current.N != 6 {
		t.Fatalf("control read path N = %d, want 6", cresp.Current.N)
	}
	if got := control.graded(t, control.version); got != 6 {
		t.Errorf("control graded count = %d, want 6; the count is not tracking the reader, it is just small", got)
	}
}

// ── A2: the private half stays on the grading side ──────────────────────────

func TestBankListOmitsRubricAndCarriesDiscrimination(t *testing.T) {
	s := newQuizScope(t, "quiz-rubric-visibility")
	const secret = "EXPECTED ANSWER: name all three constraints"
	dbfx.Exec(t, `UPDATE prompt_quiz_item SET rubric = $1 WHERE id = $2`, secret, s.itemID)
	// Enough identical readings for the mark to be judgeable at all.
	s.store(t, s.version,
		answered(s.runtime, "m", 100_000),
		answered(s.runtime, "m", 100_000),
		answered(s.runtime, "m", 100_000),
		answered(s.runtime, "m", 100_000),
		answered(s.runtime, "m", 100_000),
		answered(s.runtime, "m", 100_000),
	)

	var list struct {
		Items []map[string]any `json:"items"`
	}
	raw := testutil.Call(t, testHandler.ListPromptQuizItems,
		newRequest(http.MethodGet, "/api/prompt-quiz/items", nil)).Want(200).Text()
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatalf("decode list: %v (%s)", err, raw)
	}
	var found map[string]any
	for _, item := range list.Items {
		if _, ok := item["rubric"]; ok {
			t.Error("the member-visible bank list carries a rubric field")
		}
		if item["id"] == uuidToStringOrEmpty(s.itemID) {
			found = item
		}
	}
	if found == nil {
		t.Fatalf("the seeded item is missing from the bank list: %s", raw)
	}
	// A4: the mark travels with the list, because deciding whether to edit or
	// retire the question is done in this list.
	if found["discrimination"] != string(promptquiz.DiscriminationFlat) {
		t.Errorf("discrimination = %v, want %q — six identical readings are a question that separates nothing",
			found["discrimination"], promptquiz.DiscriminationFlat)
	}

	// The owner-only single read is the one place the private half is returned.
	// Without it there would be no way to maintain a rubric at all, which is why
	// the list's omission is a routing decision and not a missing feature.
	var detail map[string]any
	testutil.Call(t, testHandler.GetPromptQuizItem,
		withURLParam(newRequest(http.MethodGet, "/api/prompt-quiz/items/"+s.itemID, nil), "itemId", s.itemID)).
		Want(200).JSON(&detail)
	if detail["rubric"] != secret {
		t.Errorf("single-item read returned rubric %v, want the stored one — the list's omission would then be hiding the field from its only legitimate reader too",
			detail["rubric"])
	}
}

// uuidToStringOrEmpty normalises a fixture id for comparison against a JSON
// field, which the handler renders through uuidToString.
func uuidToStringOrEmpty(id string) string {
	return uuidToString(parseUUID(id))
}
