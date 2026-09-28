package handler

// Quiz bank writes at the HTTP boundary (RUYI-185 acceptance 3).
//
// pkg/promptquiz/isolation_test.go owns the gate's matrix — which body shapes
// are references and which are not — and this file owns the wiring: that the
// gate is actually consulted on create AND on update, that a refusal is a 400
// and not a 500 from the DDL CHECK, that the refusal names the reference
// without echoing the body, and that a refused write stores nothing.
//
// The pairing is deliberate in every case: the same request is sent twice, once
// with a body that names a production entity and once with the identical
// wording minus the reference. Remove the Validate call from
// decodePromptQuizItemWrite and the first half of each pair starts returning
// 200, which fails; the second half is what proves the first half failed for
// the reference rather than for some unrelated reason.

import (
	"context"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// quizItemBody is what the bank endpoints accept.
type quizItemBody struct {
	Slug           string `json:"slug"`
	Title          string `json:"title"`
	Body           string `json:"body"`
	RuntimeProfile string `json:"runtime_profile"`
	Active         *bool  `json:"active"`
}

// cleanupQuizItemSlug removes whatever a create attempt left behind, including
// the row a regression would wrongly store.
func cleanupQuizItemSlug(t *testing.T, slug string) {
	t.Helper()
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM prompt_quiz_item WHERE workspace_id = $1 AND slug = $2`, testWorkspaceID, slug)
	})
}

// TestQuizItemCreateRefusesBodiesNamingProductionEntities is the red-then-green
// pair for the isolation gate on create.
func TestQuizItemCreateRefusesBodiesNamingProductionEntities(t *testing.T) {
	cases := []struct {
		name string
		// refused and accepted differ only by the reference.
		refused  string
		accepted string
		// kind is the bucket the refusal must name, so a test cannot pass on a
		// refusal that fired for a different reason.
		kind string
	}{
		{
			name:     "issue key",
			refused:  "Summarise the acceptance criteria of RUYI-118 in one sentence.",
			accepted: "Summarise the acceptance criteria of the issue you were given in one sentence.",
			kind:     "issue_key",
		},
		{
			name:     "bare uuid",
			refused:  "Read task 01a0d486-0a24-75b4-9058-580de6a06b9f and report its status.",
			accepted: "Read the task you were given and report its status.",
			kind:     "uuid",
		},
		{
			name:     "agent mention",
			refused:  "Ask mention://agent/1a6e45dd-ad48-4976-a030-f70e39b85abd to review this.",
			accepted: "Describe who you would ask to review this and why.",
			kind:     "mention",
		},
		{
			name:     "http link",
			refused:  "Fetch https://example.invalid/spec and summarise section 2.",
			accepted: "Describe how you would summarise section 2 of a specification.",
			kind:     "url",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			slug := "gate-" + strings.ReplaceAll(tc.name, " ", "-")
			cleanupQuizItemSlug(t, slug)

			payload := quizItemBody{Slug: slug, Title: "Gate probe", Body: tc.refused}
			refusal := testutil.Call(t, testHandler.CreatePromptQuizItem,
				newRequest("POST", "/api/prompt-quiz/items", payload)).Want(400).Text()

			if !strings.Contains(refusal, tc.kind) {
				t.Errorf("refusal does not name the reference kind %q: %s", tc.kind, refusal)
			}
			// Owner Q8: a question body does not leave the platform. The
			// validation error is the most widely echoed surface there is, so
			// the reference itself must not appear in it.
			for _, token := range strings.Fields(tc.refused) {
				if len(token) >= 8 && strings.Contains(refusal, token) {
					t.Errorf("refusal echoes body text %q: %s", token, refusal)
				}
			}
			if n := dbfx.Count(t,
				`SELECT count(*) FROM prompt_quiz_item WHERE workspace_id = $1 AND slug = $2`,
				testWorkspaceID, slug); n != 0 {
				t.Fatalf("%d rows stored for a refused body, want 0", n)
			}

			// Same request, reference removed: this is what proves the 400 above
			// was the gate and not the slug, the title or the length ceiling.
			payload.Body = tc.accepted
			var created map[string]any
			testutil.Call(t, testHandler.CreatePromptQuizItem,
				newRequest("POST", "/api/prompt-quiz/items", payload)).Want(200).JSON(&created)
			if created["body"] != tc.accepted {
				t.Errorf("stored body %v, want the accepted wording", created["body"])
			}
		})
	}
}

// TestQuizItemUpdateRefusesBodiesNamingProductionEntities covers the second
// write path. An item that passed the gate on create can be edited afterwards,
// so a gate wired only into create would leave the bank one PATCH away from
// carrying a production reference.
func TestQuizItemUpdateRefusesBodiesNamingProductionEntities(t *testing.T) {
	slug := "gate-update-probe"
	cleanupQuizItemSlug(t, slug)
	clean := "Describe how you would triage a failing build."
	var created map[string]any
	testutil.Call(t, testHandler.CreatePromptQuizItem,
		newRequest("POST", "/api/prompt-quiz/items", quizItemBody{Slug: slug, Title: "Gate probe", Body: clean})).
		Want(200).JSON(&created)
	itemID, _ := created["id"].(string)
	if itemID == "" {
		t.Fatalf("create returned no id: %v", created)
	}

	patch := func(body string) *testutil.Response {
		req := withURLParam(
			newRequest("PATCH", "/api/prompt-quiz/items/"+itemID, quizItemBody{Title: "Gate probe", Body: body}),
			"itemId", itemID)
		return testutil.Call(t, testHandler.UpdatePromptQuizItem, req)
	}

	patch("Describe how you would triage RUYI-118.").Want(400)
	var stored string
	dbfx.QueryRow(t, `SELECT body FROM prompt_quiz_item WHERE id = $1`, itemID).Scan(&stored)
	if stored != clean {
		t.Fatalf("refused PATCH changed the stored body to %q", stored)
	}

	// The revision contract, checked here because it is what makes an old
	// measurement readable: editing the wording must bump it, and the earlier
	// refused edit must not have.
	edited := "Describe how you would triage a failing build, step by step."
	patch(edited).Want(200)
	var revision int32
	dbfx.QueryRow(t, `SELECT revision FROM prompt_quiz_item WHERE id = $1`, itemID).Scan(&revision)
	if revision != 2 {
		t.Errorf("revision is %d after one accepted edit and one refused one, want 2", revision)
	}
}

// TestQuizItemCreateRefusesAnEmptyOrOversizedBody covers the two non-reference
// refusals at the boundary. The length ceiling is duplicated between the DDL
// CHECK and the gate on purpose; this is the assertion that the API returns the
// readable 400 rather than letting the constraint produce a 500.
func TestQuizItemCreateRefusesAnEmptyOrOversizedBody(t *testing.T) {
	cleanupQuizItemSlug(t, "gate-size-probe")

	blank := testutil.Call(t, testHandler.CreatePromptQuizItem,
		newRequest("POST", "/api/prompt-quiz/items",
			quizItemBody{Slug: "gate-size-probe", Title: "Gate probe", Body: "   "})).Want(400).Text()
	if !strings.Contains(blank, "empty") {
		t.Errorf("blank body refusal does not say empty: %s", blank)
	}

	oversized := testutil.Call(t, testHandler.CreatePromptQuizItem,
		newRequest("POST", "/api/prompt-quiz/items",
			quizItemBody{Slug: "gate-size-probe", Title: "Gate probe", Body: strings.Repeat("a", 4001)})).
		Want(400).Text()
	if !strings.Contains(oversized, "4000") {
		t.Errorf("oversized body refusal does not name the ceiling: %s", oversized)
	}
	if n := dbfx.Count(t,
		`SELECT count(*) FROM prompt_quiz_item WHERE workspace_id = $1 AND slug = 'gate-size-probe'`,
		testWorkspaceID); n != 0 {
		t.Errorf("%d rows stored for refused bodies, want 0", n)
	}
}
