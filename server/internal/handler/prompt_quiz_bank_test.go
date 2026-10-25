package handler

// Bank import, batch runs and graded-sample reads at the HTTP boundary
// (RUYI-286).
//
// The unit matrix lives in pkg/promptquiz (grader, isolation gate, cohort).
// This file owns the wiring the acceptance criteria name: the built-in
// catalog imports idempotently into a real workspace bank, a batch orders
// REAL agent_task_queue rows through the same fence and payload the sweep
// uses (issue_id NULL, originator_source='quiz'), the read-backs return the
// stored score and evidence untouched, and the member-visible list still
// carries neither private half — rubric (migration 935) nor rubric_checks
// (migration 950).

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/promptquiz"
	"github.com/multica-ai/multica/server/pkg/promptquiz/bank"
)

// cleanupImportedBank removes whatever an import left in the fixture
// workspace, including rows a regression would wrongly store.
func cleanupImportedBank(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM prompt_quiz_item WHERE workspace_id = $1 AND slug LIKE 'bm-%'`, testWorkspaceID)
	})
}

func importBank(t *testing.T) map[string]any {
	t.Helper()
	var resp map[string]any
	testutil.Call(t, testHandler.ImportPromptQuizBank,
		newRequest("POST", "/api/prompt-quiz/bank/import", nil)).Want(200).JSON(&resp)
	return resp
}

// TestPromptQuizBankImportLandsFiveElementRows imports the built-in catalog
// and reads the stored rows back through SQL: the graded elements (checks,
// tags, difficulty) must land in the columns migration 950 added, with the
// item's body passing the isolation gate — which import re-runs per item, so
// a catalog item that ever grew a production reference is refused here too.
func TestPromptQuizBankImportLandsFiveElementRows(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	cleanupImportedBank(t)

	resp := importBank(t)
	if got := int(resp["imported"].(float64)); got != len(bank.Catalog) {
		t.Fatalf("imported = %d, want %d", got, len(bank.Catalog))
	}

	// Every imported row: checks parse as the grader accepts, difficulty is
	// one of the three, tags carry the eight-type taxonomy, and body/rubric
	// pass the isolation gate with the same strictness a manual write gets.
	rows, err := testPool.Query(context.Background(),
		`SELECT slug, tags, difficulty, rubric_checks, body, rubric
		 FROM prompt_quiz_item WHERE workspace_id = $1 AND slug LIKE 'bm-%'`, testWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var slug, difficulty, body, rubric string
		var tags []string
		var checks []byte
		if err := rows.Scan(&slug, &tags, &difficulty, &checks, &body, &rubric); err != nil {
			t.Fatal(err)
		}
		if v := promptquiz.ValidateChecks(checks); !v.OK() {
			t.Errorf("%s: stored rubric_checks rejected: %s", slug, v.Reason)
		}
		if difficulty != "easy" && difficulty != "medium" && difficulty != "hard" {
			t.Errorf("%s: difficulty %q is not one of the three", slug, difficulty)
		}
		if len(tags) == 0 {
			t.Errorf("%s: imported without its type tag", slug)
		}
		if v := promptquiz.Validate(body); !v.OK() {
			t.Errorf("%s: body fails isolation: %s", slug, v.Reason())
		}
		if v := promptquiz.ValidateRubric(rubric); !v.OK() {
			t.Errorf("%s: rubric fails isolation: %s", slug, v.RubricReason())
		}
		n++
	}
	if n != len(bank.Catalog) {
		t.Fatalf("stored %d imported rows, want %d", n, len(bank.Catalog))
	}

	// Category coverage: the eight benchmark types must all be present, which
	// is the acceptance criterion's "8 类型可机械核对" at the storage layer.
	var types int
	dbfx.QueryRow(t,
		`SELECT count(DISTINCT t) FROM (
		     SELECT unnest(tags) AS t FROM prompt_quiz_item
		     WHERE workspace_id = $1 AND slug LIKE 'bm-%'
		 ) distinct_tags`, testWorkspaceID).Scan(&types)
	if types < 8 {
		t.Fatalf("imported rows carry %d distinct type tags, want >= 8", types)
	}
}

// TestPromptQuizBankImportIsIdempotent pins the upsert: a re-import keeps item
// ids and revisions stable — the measurements point at item ids, and an import
// that re-minted either would orphan every stored reading.
func TestPromptQuizBankImportIsIdempotent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	cleanupImportedBank(t)
	importBank(t)

	ids := map[string]string{}
	revs := map[string]int{}
	rows, err := testPool.Query(context.Background(),
		`SELECT slug, id::text, revision FROM prompt_quiz_item
		 WHERE workspace_id = $1 AND slug LIKE 'bm-%'`, testWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var slug, id string
		var rev int
		if err := rows.Scan(&slug, &id, &rev); err != nil {
			t.Fatal(err)
		}
		ids[slug], revs[slug] = id, rev
	}
	rows.Close()

	importBank(t)

	var sameID, sameRev int
	rows, err = testPool.Query(context.Background(),
		`SELECT slug, id::text, revision FROM prompt_quiz_item
		 WHERE workspace_id = $1 AND slug LIKE 'bm-%'`, testWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var slug, id string
		var rev int
		if err := rows.Scan(&slug, &id, &rev); err != nil {
			t.Fatal(err)
		}
		if ids[slug] == id {
			sameID++
		}
		if revs[slug] == rev {
			sameRev++
		}
	}
	rows.Close()
	if sameID != len(bank.Catalog) || sameRev != len(bank.Catalog) {
		t.Fatalf("re-import churned identity: %d/%d ids kept, %d/%d revisions kept",
			sameID, len(bank.Catalog), sameRev, len(bank.Catalog))
	}

	// A body rewrite DOES bump revision on the next import — the upsert's
	// only mutation, and the reason re-importing after a catalog fix is safe.
	dbfx.Exec(t, `UPDATE prompt_quiz_item SET body = body || ' （题面微调。）'
	             WHERE workspace_id = $1 AND slug = 'bm-dis-background-task'`, testWorkspaceID)
	importBank(t)
	var rev int
	dbfx.QueryRow(t, `SELECT revision FROM prompt_quiz_item
	             WHERE workspace_id = $1 AND slug = 'bm-dis-background-task'`, testWorkspaceID).Scan(&rev)
	if rev != revs["bm-dis-background-task"]+1 {
		t.Fatalf("revision = %d after body rewrite, want %d", rev, revs["bm-dis-background-task"]+1)
	}
}

// TestPromptQuizBatchCreateValidation walks the boundary: what the endpoint
// refuses, and what a refusal stores (nothing).
func TestPromptQuizBatchCreateValidation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := handlerTestRuntimeID(t)
	validAgent := dbfx.Agent(t, "quiz-batch-agent", runtimeID)
	noRuntimeAgent := dbfx.Agent(t, "quiz-batch-no-runtime", "")
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id::text = ANY($1)`,
			[]string{validAgent, noRuntimeAgent})
		testPool.Exec(context.Background(),
			`DELETE FROM agent_task_queue WHERE agent_id::text = ANY($1)`, []string{validAgent})
	})

	post := func(body map[string]any) *testutil.Response {
		return testutil.Call(t, testHandler.CreatePromptQuizBatch,
			newRequest("POST", "/api/prompt-quiz/batches", body))
	}

	t.Run("empty agents is 400", func(t *testing.T) {
		post(map[string]any{"agent_ids": []string{}}).Want(400)
	})

	t.Run("agent cap fires before any lookup", func(t *testing.T) {
		ids := make([]string, 11)
		for i := range ids {
			ids[i] = newQuizUUID(t)
		}
		post(map[string]any{"agent_ids": ids}).Want(400)
	})

	t.Run("all agents unknown is 422 and stores nothing", func(t *testing.T) {
		resp := post(map[string]any{"agent_ids": []string{newQuizUUID(t), newQuizUUID(t)}}).Want(422)
		if !strings.Contains(resp.Body.String(), "no_valid_agents") {
			t.Fatalf("body does not name the refusal: %s", resp.Body.String())
		}
		if n := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue
		                       WHERE agent_id::text = ANY($1)`, []string{validAgent}); n != 0 {
			t.Fatalf("%d rows stored for a refused batch", n)
		}
	})

	t.Run("mixed validity orders for the valid agent and names the refusals", func(t *testing.T) {
		itemID := seedQuizItem(t, "quiz-batch-validation-item")
		t.Cleanup(func() {
			testPool.Exec(context.Background(), `DELETE FROM prompt_quiz_item WHERE id = $1::uuid`, itemID)
		})
		var resp struct {
			BatchID       string `json:"batch_id"`
			Ordered       int    `json:"ordered"`
			RefusedAgents []struct {
				AgentID string `json:"agent_id"`
				Reason  string `json:"reason"`
			} `json:"refused_agents"`
		}
		testutil.Call(t, testHandler.CreatePromptQuizBatch, newRequest("POST", "/api/prompt-quiz/batches", map[string]any{
			"agent_ids": []string{validAgent, noRuntimeAgent, newQuizUUID(t)},
			"item_ids":  []string{itemID},
		})).Want(200).JSON(&resp)

		if resp.Ordered != 1 {
			t.Fatalf("ordered = %d, want 1", resp.Ordered)
		}
		reasons := map[string]string{}
		for _, r := range resp.RefusedAgents {
			reasons[r.Reason] = r.AgentID
		}
		if reasons["no_runtime"] != noRuntimeAgent {
			t.Fatalf("no_runtime refusal names %q, want the runtime-less agent", reasons["no_runtime"])
		}
		if _, ok := reasons["not_found"]; !ok {
			t.Fatal("missing not_found refusal for the unknown id")
		}
		// The one ordered row is a REAL quiz run: issue-less, quiz-originated,
		// and carrying the batch id plus the item id in its payload.
		var issueID *string
		var originator string
		dbfx.QueryRow(t, `SELECT issue_id::text, originator_source FROM agent_task_queue
		             WHERE agent_id = $1::uuid`, validAgent).Scan(&issueID, &originator)
		if issueID != nil {
			t.Fatal("quiz run carries an issue id")
		}
		if originator != promptquiz.OriginatorSource {
			t.Fatalf("originator_source = %q, want quiz", originator)
		}
	})

	t.Run("combined cap refuses a batch that would order past 50", func(t *testing.T) {
		agents := make([]string, 6)
		for i := range agents {
			agents[i] = dbfx.Agent(t, "quiz-batch-cap-"+newQuizUUID(t)[:8], runtimeID)
		}
		t.Cleanup(func() {
			testPool.Exec(context.Background(), `DELETE FROM agent WHERE id::text = ANY($1)`, agents)
		})
		items := make([]string, 9)
		for i := range items {
			items[i] = seedQuizItem(t, "quiz-batch-cap-item-"+newQuizUUID(t)[:8])
		}
		t.Cleanup(func() {
			testPool.Exec(context.Background(), `DELETE FROM prompt_quiz_item WHERE id::text = ANY($1)`, items)
		})
		// 6 × 9 = 54 > 50: refused as a whole, never truncated.
		resp := post(map[string]any{"agent_ids": agents, "item_ids": items}).Want(422)
		if !strings.Contains(resp.Body.String(), "too_many_runs") {
			t.Fatalf("body does not name the cap: %s", resp.Body.String())
		}
	})
}

// TestPromptQuizBatchOrdersRealRunsAndReadsBack is the traceability loop: a
// batch orders runs, results land, and the batch read-back returns each row's
// outcome, score, evidence and task id — graded rows counted, errored rows
// visible and ungraded (never read as 0).
func TestPromptQuizBatchOrdersRealRunsAndReadsBack(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := dbfx.Agent(t, "quiz-batch-loop", handlerTestRuntimeID(t))
	itemA := seedQuizItem(t, "quiz-batch-loop-a")
	itemB := seedQuizItem(t, "quiz-batch-loop-b")
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM agent_task_queue WHERE agent_id = $1::uuid`, agentID)
		testPool.Exec(context.Background(),
			`DELETE FROM prompt_quiz_result WHERE scope = 'agent' AND scope_id = $1::uuid`, agentID)
		testPool.Exec(context.Background(),
			`DELETE FROM agent WHERE id = $1::uuid`, agentID)
		testPool.Exec(context.Background(),
			`DELETE FROM prompt_quiz_item WHERE id::text = ANY($1)`, []string{itemA, itemB})
	})

	var created struct {
		BatchID string `json:"batch_id"`
		Ordered int    `json:"ordered"`
	}
	testutil.Call(t, testHandler.CreatePromptQuizBatch, newRequest("POST", "/api/prompt-quiz/batches", map[string]any{
		"agent_ids": []string{agentID},
		"item_ids":  []string{itemA, itemB},
	})).Want(200).JSON(&created)
	if created.Ordered != 2 {
		t.Fatalf("ordered = %d, want 2", created.Ordered)
	}

	// The batch id is IN the payload the daemon will receive, next to the
	// item id — the field set the sweep's payload test pins.
	var contexts []string
	rows, err := testPool.Query(context.Background(),
		`SELECT context FROM agent_task_queue WHERE agent_id = $1::uuid`, agentID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var c []byte
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		contexts = append(contexts, string(c))
	}
	rows.Close()
	if len(contexts) != 2 {
		t.Fatalf("%d task rows, want 2", len(contexts))
	}
	for _, c := range contexts {
		if !strings.Contains(c, created.BatchID) || !strings.Contains(c, "quiz_item_id") || !strings.Contains(c, "quiz_prompt") {
			t.Fatalf("payload is missing the batch/item/prompt fields: %s", c)
		}
	}

	// One answered-and-graded run, one errored run, both on the batch.
	gradedScore := 0.75
	for _, outcome := range []string{promptquiz.OutcomeAnswered, promptquiz.OutcomeErrored} {
		taskID := dbfx.Task(t, agentID, map[string]any{
			"status": "completed", "originator_source": promptquiz.OriginatorSource,
			"completed_at": testutil.Raw("now()"),
		})
		cols := map[string]any{
			"workspace_id": testWorkspaceID, "scope": "agent", "scope_id": agentID,
			"version": 1, "item_id": itemA, "item_revision": 1,
			"item_body_sha256": promptquiz.BodyDigest("anchor"),
			"batch_id":         created.BatchID, "task_id": taskID, "outcome": outcome,
		}
		if outcome == promptquiz.OutcomeAnswered {
			detail, _ := json.Marshal([]map[string]any{{"id": "c1", "kind": "includes_all", "passed": true, "weight": 1}})
			cols["score"] = gradedScore
			cols["score_detail"] = testutil.Raw("'" + strings.ReplaceAll(string(detail), "'", "''") + "'::jsonb")
			cols["graded_at"] = testutil.Raw("now()")
		}
		dbfx.Insert(t, "prompt_quiz_result", cols)
	}

	req := newRequest("GET", "/api/prompt-quiz/batches/"+created.BatchID, nil)
	var batch struct {
		BatchID string `json:"batch_id"`
		Rows    []struct {
			TaskID     string          `json:"task_id"`
			ItemID     string          `json:"item_id"`
			Outcome    string          `json:"outcome"`
			Score      *float64        `json:"score"`
			Detail     json.RawMessage `json:"score_detail"`
			TaskStatus string          `json:"task_status"`
		} `json:"rows"`
		Counts map[string]int `json:"counts"`
		Scores struct {
			Graded int     `json:"graded"`
			Mean   float64 `json:"mean"`
			Items  []struct {
				ItemID string `json:"item_id"`
				Graded int    `json:"graded"`
			} `json:"items"`
		} `json:"scores"`
	}
	testutil.Call(t, testHandler.GetPromptQuizBatch, withURLParam(req, "batchId", created.BatchID)).
		Want(200).JSON(&batch)

	if batch.BatchID != created.BatchID || len(batch.Rows) != 2 {
		t.Fatalf("batch read-back = %d rows, want 2 for %s", len(batch.Rows), created.BatchID)
	}
	if batch.Counts[promptquiz.OutcomeAnswered] != 1 || batch.Counts[promptquiz.OutcomeErrored] != 1 {
		t.Fatalf("counts = %v, want one answered + one errored", batch.Counts)
	}
	if batch.Scores.Graded != 1 || batch.Scores.Mean != gradedScore {
		t.Fatalf("scores = %d@%v, want 1@%v — the errored row must not read as 0",
			batch.Scores.Graded, batch.Scores.Mean, gradedScore)
	}
	for _, row := range batch.Rows {
		switch row.Outcome {
		case promptquiz.OutcomeAnswered:
			if row.Score == nil || *row.Score != gradedScore || len(row.Detail) == 0 {
				t.Errorf("graded row lost its score or evidence: %+v", row)
			}
		case promptquiz.OutcomeErrored:
			if row.Score != nil {
				t.Errorf("errored row carries a score (%v); it measured nothing", *row.Score)
			}
		}
		if row.TaskID == "" || row.TaskStatus == "" {
			t.Errorf("row lost its task join: %+v", row)
		}
	}
}

// TestPromptQuizSamplesReturnsGradedRows pins the version drill-down: rows
// newest first, score and evidence passed through untouched, item identity
// joined in, errored rows present with a null score.
func TestPromptQuizSamplesReturnsGradedRows(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := dbfx.Agent(t, "quiz-samples-scope", handlerTestRuntimeID(t))
	itemID := seedQuizItem(t, "quiz-samples-item")
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM prompt_quiz_result WHERE scope = 'agent' AND scope_id = $1::uuid`, agentID)
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1::uuid`, agentID)
		testPool.Exec(context.Background(), `DELETE FROM prompt_quiz_item WHERE id = $1::uuid`, itemID)
	})

	detail, _ := json.Marshal([]map[string]any{{"id": "c1", "passed": false, "weight": 1}})
	for i, outcome := range []string{promptquiz.OutcomeAnswered, promptquiz.OutcomeErrored} {
		taskID := dbfx.Task(t, agentID, map[string]any{
			"status": "completed", "originator_source": promptquiz.OriginatorSource,
			"completed_at": testutil.Raw("now()"),
		})
		cols := map[string]any{
			"workspace_id": testWorkspaceID, "scope": "agent", "scope_id": agentID,
			"version": 3, "item_id": itemID, "item_revision": 1,
			"item_body_sha256": promptquiz.BodyDigest("anchor"),
			"batch_id":         newQuizUUID(t), "task_id": taskID, "outcome": outcome,
			// i grows with the loop, so the SECOND row is the newest.
			"measured_at": testutil.Raw("now() - interval '1 hour' * " + string(rune('0'+i))),
		}
		if outcome == promptquiz.OutcomeAnswered {
			cols["score"] = 0.5
			cols["score_detail"] = testutil.Raw("'" + strings.ReplaceAll(string(detail), "'", "''") + "'::jsonb")
			cols["graded_at"] = testutil.Raw("now()")
		}
		dbfx.Insert(t, "prompt_quiz_result", cols)
	}

	w := httptest.NewRecorder()
	r := newRequest("GET",
		"/api/prompt-quiz/samples?scope=agent&scope_id="+agentID+"&version=3", nil)
	testHandler.GetPromptQuizSamples(w, r)
	if w.Code != 200 {
		t.Fatalf("samples = %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Rows []struct {
			Version  int32           `json:"version"`
			ItemID   string          `json:"item_id"`
			ItemSlug string          `json:"item_slug"`
			Outcome  string          `json:"outcome"`
			Score    *float64        `json:"score"`
			Detail   json.RawMessage `json:"score_detail"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Rows) != 2 {
		t.Fatalf("%d sample rows, want 2", len(resp.Rows))
	}
	if resp.Rows[0].Outcome != promptquiz.OutcomeAnswered {
		t.Fatalf("newest row is %q, want the answered one (newest first)", resp.Rows[0].Outcome)
	}
	if resp.Rows[0].Score == nil || *resp.Rows[0].Score != 0.5 || len(resp.Rows[0].Detail) == 0 {
		t.Errorf("newest row lost score or evidence")
	}
	if resp.Rows[0].ItemSlug != "quiz-samples-item" || resp.Rows[0].ItemID != itemID {
		t.Errorf("item identity not joined: %+v", resp.Rows[0])
	}
	if resp.Rows[1].Score != nil {
		t.Errorf("errored row carries a score; it must read as not graded")
	}
}

// TestPromptQuizItemWriteGradingFields covers the owner write path's new
// fields: checks pass the grader's validator before storage, a bad check is a
// 400 naming the reason, tags normalize, and the MEMBER list still carries
// neither private half while carrying the new presentation fields.
func TestPromptQuizItemWriteGradingFields(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	slug := "quiz-grading-fields-item"
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM prompt_quiz_item WHERE workspace_id = $1 AND slug = $2`, testWorkspaceID, slug)
	})

	body := map[string]any{
		"slug": slug, "title": "grading fields", "body": "Describe your background-waiting rule.",
		"rubric_checks": []map[string]any{
			{"id": "c1", "kind": "includes_all", "weight": 2, "phrases": []string{"同回合", "收齐"}},
			{"id": "c2", "kind": "excludes", "phrases": []string{"可以等下一回合"}},
		},
		"tags":       []string{" 纪律性 ", "纪律性", "", "格式"},
		"difficulty": "hard",
	}
	var created map[string]any
	testutil.Call(t, testHandler.CreatePromptQuizItem,
		newRequest("POST", "/api/prompt-quiz/items", body)).Want(200).JSON(&created)

	if created["difficulty"] != "hard" {
		t.Errorf("difficulty = %v, want hard", created["difficulty"])
	}
	tags, _ := created["tags"].([]any)
	if len(tags) != 2 {
		t.Errorf("tags = %v, want 2 after trim/dedup/drop-empty", created["tags"])
	}
	detail, _ := created["rubric_checks"].(string)
	if detail == "" {
		// The detail read returns the checks it stored; the exact wire shape
		// (string vs raw JSON) is a decoder concern, presence is the contract.
		if created["rubric_checks"] == nil {
			t.Errorf("created response lost rubric_checks")
		}
	}

	// A check outside the vocabulary is a 400 naming the kind, and stores
	// nothing on the update path either.
	bad, _ := json.Marshal([]map[string]any{{"id": "x", "kind": "sentiment"}})
	badBody := map[string]any{"title": "grading fields", "body": "same body", "rubric_checks": json.RawMessage(bad)}
	w := httptest.NewRecorder()
	testHandler.UpdatePromptQuizItem(w,
		withURLParam(newRequest("PATCH", "/api/prompt-quiz/items/x", badBody), "itemId", created["id"].(string)))
	if w.Code != 400 {
		t.Fatalf("bad check kind = %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "kind") {
		t.Fatalf("refusal does not name the failing field: %s", w.Body.String())
	}

	// The member-visible list: tags and difficulty travel, neither private
	// half does — including rubric_checks, which a SELECT * would leak.
	w = httptest.NewRecorder()
	testHandler.ListPromptQuizItems(w, newRequest("GET", "/api/prompt-quiz/items?active=true", nil))
	if w.Code != 200 {
		t.Fatalf("member list = %d: %s", w.Code, w.Body.String())
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, item := range list.Items {
		if item["slug"] != slug {
			continue
		}
		found = true
		for _, forbidden := range []string{"rubric", "rubric_checks"} {
			if _, ok := item[forbidden]; ok {
				t.Errorf("member list carries the private half %q", forbidden)
			}
		}
		if _, ok := item["tags"]; !ok {
			t.Errorf("member list lost tags")
		}
	}
	if !found {
		t.Fatal("the created item is missing from the member list")
	}
}
