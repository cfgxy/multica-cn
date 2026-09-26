package promptdiscipline

import "testing"

// bash builds a tool_use message the way the daemon persists one: tool name
// "Bash", input carrying the command string.
func bash(seq int, command string) Message {
	return Message{Seq: seq, Type: "tool_use", Tool: "Bash", Input: map[string]any{"command": command}}
}

// A run that used the CLI correctly loses nothing. This is the baseline the
// whole dimension is measured against: if a clean run did not score 100, every
// other number here would be meaningless.
func TestScoreCleanRunKeepsFullScore(t *testing.T) {
	got := Score([]Message{
		bash(1, "git status --short --branch"),
		bash(2, "multica issue comment add 01a0 --content-file ./reply.md"),
	})
	if !got.Covered {
		t.Fatal("a run with Bash tool calls must be covered by the discipline rules")
	}
	if got.Score != 100 {
		t.Errorf("clean run scored %v, want 100 (deductions: %+v)", got.Score, got.Deductions)
	}
	if len(got.Deductions) != 0 {
		t.Errorf("clean run produced deductions: %+v", got.Deductions)
	}
}

// A run that made no tool call this dimension can read is NOT a perfect run —
// it is an unmeasured one. Reporting 100 here would let a text-only run inflate
// the version's median, which is the exact "no data is not zero" failure the
// rest of RUYI-184 is built to avoid.
func TestScoreRunWithoutInspectableToolCallsIsNotCovered(t *testing.T) {
	got := Score([]Message{
		{Seq: 1, Type: "text"},
		{Seq: 2, Type: "tool_use", Tool: "Read", Input: map[string]any{"file_path": "/x"}},
	})
	if got.Covered {
		t.Fatal("a run with no inspectable command must not be reported as covered")
	}
	if got.Score != 0 || len(got.Deductions) != 0 {
		t.Errorf("uncovered run must carry no score and no deductions, got %+v", got)
	}
}

func TestScoreDeductsInlineCommentContent(t *testing.T) {
	got := Score([]Message{bash(1, `multica issue comment add 01a0 --content "done"`)})
	if !hasRule(got, RuleInlineCommentContent) {
		t.Fatalf("inline --content on a comment add must be deducted, got %+v", got.Deductions)
	}
	if got.Score >= 100 {
		t.Errorf("score = %v, want below 100", got.Score)
	}
}

// --content-file is only compliant when the body lives inside the run's own
// working directory; /tmp and other shared paths are rejected by the server
// (MUL-4252), so a run that used one did not actually deliver its comment.
func TestScoreDeductsCommentBodyOutsideWorkdir(t *testing.T) {
	got := Score([]Message{bash(1, "multica issue comment add 01a0 --content-file /tmp/reply.md")})
	if !hasRule(got, RuleCommentBodyOutsideWorkdir) {
		t.Fatalf("a /tmp comment body must be deducted, got %+v", got.Deductions)
	}
}

// Bare `git stash` / `git stash pop` operate on a stack shared with every other
// worktree on the machine, so they can silently consume another session's work.
// The tagged push/apply/drop forms are the compliant ones.
func TestScoreDeductsBareGitStashButNotTaggedForms(t *testing.T) {
	bare := Score([]Message{bash(1, "git stash pop")})
	if !hasRule(bare, RuleBareGitStash) {
		t.Fatalf("bare `git stash pop` must be deducted, got %+v", bare.Deductions)
	}
	tagged := Score([]Message{
		bash(1, `git stash push -u -m "ruyi-184-wip"`),
		bash(2, "git stash list --format='%H %gs'"),
		bash(3, "git stash apply abc123"),
	})
	if hasRule(tagged, RuleBareGitStash) {
		t.Errorf("tagged stash usage must not be deducted, got %+v", tagged.Deductions)
	}
}

// The Multica runtime injects MULTICA_* variables into every child process, so
// an unscoped `go test` is testing a polluted configuration. The rule fires on
// the go test invocation itself, not on unrelated commands that mention it.
func TestScoreDeductsUnscopedGoTest(t *testing.T) {
	dirty := Score([]Message{bash(1, "cd server && go test ./pkg/taskfailure -count=1")})
	if !hasRule(dirty, RuleUnscopedGoTest) {
		t.Fatalf("unscoped `go test` must be deducted, got %+v", dirty.Deductions)
	}
	clean := Score([]Message{bash(1, "env -u MULTICA_TOKEN -u MULTICA_TASK_ID go test ./pkg/taskfailure -count=1")})
	if hasRule(clean, RuleUnscopedGoTest) {
		t.Errorf("an env -u scoped `go test` must not be deducted, got %+v", clean.Deductions)
	}
}

// One top-level comment per run. Threaded replies (--parent) are not top-level
// and may legitimately appear more than once, so they must not trip the rule.
func TestScoreDeductsMultipleTopLevelComments(t *testing.T) {
	multi := Score([]Message{
		bash(1, "multica issue comment add 01a0 --content-file ./a.md"),
		bash(2, "multica issue comment add 01a0 --content-file ./b.md"),
	})
	if !hasRule(multi, RuleMultipleTopLevelComments) {
		t.Fatalf("two top-level comments in one run must be deducted, got %+v", multi.Deductions)
	}
	single := Score([]Message{
		bash(1, "multica issue comment add 01a0 --content-file ./a.md"),
		bash(2, "multica issue comment add 01a0 --parent 01b1 --content-file ./b.md"),
	})
	if hasRule(single, RuleMultipleTopLevelComments) {
		t.Errorf("a top-level comment plus a threaded reply must not be deducted, got %+v", single.Deductions)
	}
}

// Deductions are capped so that one catastrophic run cannot drag a version's
// median arbitrarily far negative; the score floor is 0, not a negative number.
func TestScoreFloorsAtZero(t *testing.T) {
	msgs := []Message{}
	for i := 1; i <= 40; i++ {
		msgs = append(msgs, bash(i, `multica issue comment add 01a0 --content "spam"`))
	}
	got := Score(msgs)
	if got.Score < 0 {
		t.Errorf("score = %v, want >= 0", got.Score)
	}
}

// Every deduction has to name a rule that exists in the rule table, carry a
// positive weight and point at the message it came from. Without the seq a
// deduction is unauditable: the drill-down cannot show the operator what
// happened.
func TestDeductionsAreAuditable(t *testing.T) {
	got := Score([]Message{
		bash(1, `multica issue comment add 01a0 --content "done"`),
		bash(2, "git stash pop"),
	})
	known := map[RuleID]bool{}
	for _, r := range Rules() {
		if r.Points <= 0 {
			t.Errorf("rule %s has non-positive weight %d", r.ID, r.Points)
		}
		known[r.ID] = true
	}
	for _, d := range got.Deductions {
		if !known[d.Rule] {
			t.Errorf("deduction references unknown rule %q", d.Rule)
		}
		if d.Seq <= 0 {
			t.Errorf("deduction %+v has no message seq to point at", d)
		}
		if d.Points <= 0 {
			t.Errorf("deduction %+v has non-positive points", d)
		}
	}
}

// The deduction record must never carry the command text itself. Commands
// routinely contain paths, issue ids and occasionally argument values that have
// no business being copied into a table the dashboard scans wholesale
// (ADR-002 §3 keeps bodies out of the rollup).
func TestDeductionsCarryNoCommandText(t *testing.T) {
	const secretish = "supersecret-argument-value"
	got := Score([]Message{bash(1, `multica issue comment add 01a0 --content "`+secretish+`"`)})
	if len(got.Deductions) == 0 {
		t.Fatal("expected a deduction to inspect")
	}
	for _, d := range got.Deductions {
		if containsFold(d.Detail, secretish) {
			t.Errorf("deduction detail leaked command text: %q", d.Detail)
		}
	}
}

func hasRule(r Result, id RuleID) bool {
	for _, d := range r.Deductions {
		if d.Rule == id {
			return true
		}
	}
	return false
}
