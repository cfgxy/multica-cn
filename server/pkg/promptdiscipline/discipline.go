// Package promptdiscipline scores a single run against the mechanically
// decidable discipline clauses of the workspace prompt (RUYI-184, dimension
// D2).
//
// SCOPE, AND WHY IT IS DELIBERATELY SMALL:
//
// Most of a prompt is judgement — "state the conclusion first", "escalate when
// the spec is missing" — and no rule here will ever read those. What this
// package covers is the subset a machine can decide from what the run actually
// executed: the CLI invocation shape, the paths passed to it, the shell
// commands run. ADR-002 §2.2 makes the coverage itself a first-class number on
// the dashboard for exactly this reason: a run scoring 100 means "broke none of
// the rules we can check", never "followed the prompt".
//
// WHAT NEVER LEAVES THIS PACKAGE:
//
// A Deduction carries the rule id, its weight and the message seq it came from.
// It never carries the command text. Commands contain paths, issue ids and
// argument values, and the deductions land in prompt_quality_daily, a table the
// dashboard scans wholesale — copying transcript text into it would widen the
// disclosure surface for no read benefit. The drill-down re-reads task_message
// by (task_id, seq) when an operator asks for the evidence.
package promptdiscipline

import (
	"regexp"
	"strings"
)

// RuleID names one mechanically decidable clause. Stable strings: they are
// persisted in prompt_quality_daily.discipline_deductions and read back by the
// dashboard, so renaming one silently reclassifies history.
type RuleID string

const (
	// RuleInlineCommentContent: agent-authored comment bodies must go through
	// --content-file. Inline --content mangles multi-line bodies (MUL-2904).
	RuleInlineCommentContent RuleID = "inline_comment_content"

	// RuleCommentBodyOutsideWorkdir: --content-file / --description-file /
	// --attachment paths must live inside the run's working directory. The
	// server rejects /tmp and other shared paths (MUL-4252), so a run that
	// passed one did not deliver anything.
	RuleCommentBodyOutsideWorkdir RuleID = "comment_body_outside_workdir"

	// RuleBareGitStash: the stash stack is shared with the main checkout and
	// every other worktree, so a bare stash/pop can consume another session's
	// work. The tagged push/apply/drop forms are compliant.
	RuleBareGitStash RuleID = "bare_git_stash"

	// RuleUnscopedGoTest: the runtime injects MULTICA_* into every child
	// process, so a `go test` that does not clear them is testing a polluted
	// configuration.
	RuleUnscopedGoTest RuleID = "unscoped_go_test"

	// RuleMultipleTopLevelComments: exactly one top-level comment per run.
	// Threaded replies (--parent) are not top-level and are unlimited.
	RuleMultipleTopLevelComments RuleID = "multiple_top_level_comments"
)

// Rule is one entry of the rule table. Points is the deduction weight out of
// the 100-point starting score; the weights express how much each breach costs
// the reader, not how annoying it is.
type Rule struct {
	ID     RuleID
	Points int
	// Detail is the fixed, text-free explanation persisted with a deduction.
	Detail string
}

// rules is the whole D2 rule table. Adding a rule raises coverage and lowers
// every historical score's comparability, so the table changes with a version
// note on the dashboard, not silently.
var rules = []Rule{
	{ID: RuleInlineCommentContent, Points: 25, Detail: "comment body passed inline instead of --content-file"},
	{ID: RuleCommentBodyOutsideWorkdir, Points: 25, Detail: "comment/description/attachment path outside the run working directory"},
	{ID: RuleBareGitStash, Points: 20, Detail: "bare git stash/pop on the shared stash stack"},
	{ID: RuleUnscopedGoTest, Points: 15, Detail: "go test run without clearing MULTICA_* from the environment"},
	{ID: RuleMultipleTopLevelComments, Points: 20, Detail: "more than one top-level issue comment in a single run"},
}

// Rules returns the rule table. Copied on each call so a caller cannot mutate
// the scoring weights of a running server.
func Rules() []Rule {
	out := make([]Rule, len(rules))
	copy(out, rules)
	return out
}

func ruleByID(id RuleID) Rule {
	for _, r := range rules {
		if r.ID == id {
			return r
		}
	}
	// Unreachable: every deduction site names a constant from the table above.
	// Returning a zero-weight rule keeps a future editing mistake from silently
	// inventing a deduction with an arbitrary cost.
	return Rule{ID: id}
}

// Message is the subset of a persisted task_message row this package reads.
// Deliberately not the sqlc row type: the scorer must stay callable from a
// test with a literal, and from the rollup job with rows it assembled itself.
type Message struct {
	Seq   int
	Type  string
	Tool  string
	Input map[string]any
}

// Deduction is one recorded breach. Detail is the rule's fixed text — never
// anything derived from the command.
type Deduction struct {
	Rule   RuleID `json:"rule"`
	Points int    `json:"points"`
	Seq    int    `json:"seq"`
	Detail string `json:"detail"`
}

// Result is one run's discipline outcome.
//
// Covered is the three-state carrier: false means the run made no command this
// rule table can read, and then Score is meaningless and must not enter the
// version's median. A text-only run is unmeasured, not perfect.
type Result struct {
	Covered    bool
	Score      int
	Deductions []Deduction
}

// MaxScore is the score a run starts at, before any deduction. Exported
// because it is the top of the scale the dashboard prints D2 against ("90 /
// 100"), and that scale must not be re-stated anywhere: a second copy of 100
// cannot be checked against this one, and nothing fails when they drift.
const MaxScore = 100

// Score evaluates one run's messages.
func Score(messages []Message) Result {
	var (
		deductions []Deduction
		inspected  int
		topLevel   []int
	)

	for _, m := range messages {
		cmd, ok := shellCommand(m)
		if !ok {
			continue
		}
		inspected++

		if isCommentAdd(cmd) {
			if inlineContentRe.MatchString(cmd) {
				deductions = append(deductions, deduct(RuleInlineCommentContent, m.Seq))
			}
			if !parentFlagRe.MatchString(cmd) {
				topLevel = append(topLevel, m.Seq)
			}
		}
		if p, found := firstOutsideWorkdirPath(cmd); found && p {
			deductions = append(deductions, deduct(RuleCommentBodyOutsideWorkdir, m.Seq))
		}
		if bareStashRe.MatchString(cmd) {
			deductions = append(deductions, deduct(RuleBareGitStash, m.Seq))
		}
		if goTestRe.MatchString(cmd) && !envClearRe.MatchString(cmd) {
			deductions = append(deductions, deduct(RuleUnscopedGoTest, m.Seq))
		}
	}

	// One deduction per extra top-level comment, attributed to the extra one:
	// the first comment is compliant, the second is the breach.
	if len(topLevel) > 1 {
		for _, seq := range topLevel[1:] {
			deductions = append(deductions, deduct(RuleMultipleTopLevelComments, seq))
		}
	}

	if inspected == 0 {
		// Nothing this table can read ran. Not a perfect run — an unmeasured
		// one; the rollup must leave it out of the median entirely.
		return Result{Covered: false}
	}

	score := MaxScore
	for _, d := range deductions {
		score -= d.Points
	}
	if score < 0 {
		score = 0
	}
	return Result{Covered: true, Score: score, Deductions: deductions}
}

func deduct(id RuleID, seq int) Deduction {
	r := ruleByID(id)
	return Deduction{Rule: r.ID, Points: r.Points, Seq: seq, Detail: r.Detail}
}

// shellCommand extracts the command string from a Bash tool_use message. Other
// tools (Read, Edit, Write) carry no discipline signal this table can decide,
// so they are not counted as inspected either — a run that only read files is
// uncovered, which is the honest answer.
func shellCommand(m Message) (string, bool) {
	if m.Type != "tool_use" || !strings.EqualFold(m.Tool, "Bash") {
		return "", false
	}
	raw, ok := m.Input["command"]
	if !ok {
		return "", false
	}
	cmd, ok := raw.(string)
	if !ok || strings.TrimSpace(cmd) == "" {
		return "", false
	}
	return cmd, true
}

var (
	// `multica issue comment add`, tolerating the flag noise around it.
	commentAddRe = regexp.MustCompile(`multica\s+issue\s+comment\s+add\b`)
	// Inline body. --content-file / --content-stdin are different flags, so the
	// boundary assertion matters: `--content-file` must not match.
	inlineContentRe = regexp.MustCompile(`--content(?:=|\s)`)
	parentFlagRe    = regexp.MustCompile(`--parent(?:=|\s)`)
	// Bare stash: `git stash` with no subcommand, or an outright `pop`. The
	// push/apply/drop/list forms carry an explicit tag or SHA and are the
	// compliant path.
	bareStashRe = regexp.MustCompile(`git\s+stash\s*(?:$|;|&&|\|\||\n|pop\b|save\b)`)
	goTestRe    = regexp.MustCompile(`\bgo\s+test\b`)
	envClearRe  = regexp.MustCompile(`\benv\s+(?:-u\s+\S+\s+)+`)
	// Body/attachment paths the server resolves against the run workdir.
	pathFlagRe = regexp.MustCompile(`--(?:content-file|description-file|attachment)(?:=|\s+)(\S+)`)
)

func isCommentAdd(cmd string) bool { return commentAddRe.MatchString(cmd) }

// firstOutsideWorkdirPath reports whether any workdir-scoped flag in the
// command points outside the run's working directory. An absolute path is
// outside it unless the run happens to be rooted there, which this package
// cannot know — but the only absolute roots that appear in practice are the
// shared ones the server rejects, and treating them as breaches is the
// direction that fails safe for a rule whose whole point is that the shared
// path silently loses the delivery.
func firstOutsideWorkdirPath(cmd string) (outside bool, found bool) {
	for _, m := range pathFlagRe.FindAllStringSubmatch(cmd, -1) {
		found = true
		p := strings.Trim(m[1], `"'`)
		if strings.HasPrefix(p, "/") {
			return true, true
		}
	}
	return false, found
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
