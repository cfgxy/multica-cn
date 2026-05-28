package execenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Sub-issue Creation section — after MUL-2538 the platform posts the
// child-done parent notification itself, so the brief no longer carries
// any parent-notification rule (per Bohan's call on PR #3055: delete the
// guidance entirely, do not replace it with a "do not post one" sentence
// — the agent should not be thinking about parent comments at all). All
// that remains is the `--status todo` vs `--status backlog` rule for
// creating sub-issues, which is unrelated to the notification path.

func TestSubIssueCreationSectionPresentForIssueRuns(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ctx  TaskContextForEnv
	}{
		{
			name: "assignment-triggered",
			ctx:  TaskContextForEnv{IssueID: "11111111-2222-3333-4444-555555555555"},
		},
		{
			name: "comment-triggered",
			ctx: TaskContextForEnv{
				IssueID:          "22222222-3333-4444-5555-666666666666",
				TriggerCommentID: "33333333-4444-5555-6666-777777777777",
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := buildMetaSkillContent("claude", tc.ctx)

			if !strings.Contains(out, "## Sub-issue Creation") {
				t.Fatalf("expected Sub-issue Creation section in %s brief", tc.name)
			}
			for _, want := range []string{
				"**Choosing `--status` when creating sub-issues.**",
				"`--status todo` = **start now**",
				"`--status backlog` = **wait**",
				"`multica issue status <child-id> todo`",
				"all `--status todo`",
				"`--status backlog` from the start",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("[%s] section missing %q", tc.name, want)
				}
			}
		})
	}
}

// The brief must no longer carry any parent-notification guidance. PR
// #2918 added a "Tell the parent when you finish a child" rule that
// turned into noise (self-mention loops, planner ack ping-pong,
// hardcoded `MUL-` prefix). PR #3055 first downgraded it to a "do NOT
// post one" guardrail, but Bohan's product call was to remove the
// guidance entirely rather than substitute a new prohibition. These
// canaries lock that in: any wording that re-introduces the
// parent-comment concept — positive, negative, or descriptive — must
// not come back through future edits.
func TestBriefHasNoParentNotificationGuidance(t *testing.T) {
	t.Parallel()
	cases := []TaskContextForEnv{
		{IssueID: "11111111-2222-3333-4444-555555555555"},
		{
			IssueID:          "22222222-3333-4444-5555-666666666666",
			TriggerCommentID: "33333333-4444-5555-6666-777777777777",
		},
	}
	for _, ctx := range cases {
		ctx := ctx
		out := buildMetaSkillContent("claude", ctx)

		// The pre-MUL-2538 phrasing instructed the agent to compose a
		// parent comment by hand — including a hardcoded `MUL-` prefix
		// and an assignee mention. The intermediate revision (PR #3055
		// before Bohan's call) instead told the agent NOT to post one.
		// Both framings must stay out.
		for _, banned := range []string{
			// Old "do it yourself" framing (PR #2918).
			"## Parent / Sub-issue Protocol",
			"**Tell the parent when you finish a child.**",
			"multica issue comment add <parent-id>",
			"with NO `--parent`",
			"link the child as `[MUL-",
			"`@mention` the parent's assignee",
			"`mention://agent/<id>`",
			"`mention://member/<id>`",
			"`mention://squad/<id>`",
			// Intermediate "do NOT do it yourself" framing (PR #3055
			// before Bohan's call) — also out per product direction.
			"**Do NOT post your own parent-notification comment.**",
			"Do NOT post your own parent-notification comment",
			"parent-notification comment",
			"system comment on the parent fires from the status transition",
			"re-trigger the parent's assignee for nothing",
			"platform posts a top-level system comment on the parent",
			// Earlier revisions split rules by trigger type or used
			// table/subsection layouts. None of those structures should
			// come back either.
			"| Parent assignee | Parent status |",
			"The same agent as yourself",
			"| Member or squad |",
			"### A. Notify the parent",
			"### B. Choose",
			"When this issue has `parent_issue_id`:",
			"**Closing out child work** (only if this issue has `parent_issue_id`)",
			"**Notify the parent** (only if this issue has `parent_issue_id`",
			"**Creating sub-issues** (applies to any issue-bound run)",
			"For parent/child work, use these best-effort rules",
			// The protocol must no longer emit a placeholder
			// `<this-issue-id>` status flip — the workflow above owns
			// that command with the real issue id substituted.
			"`multica issue status <this-issue-id> in_review`",
			// Non-existent CLI form Elon's earlier review flagged.
			"issue list --parent",
		} {
			if strings.Contains(out, banned) {
				t.Errorf("expected %q to be removed from the brief", banned)
			}
		}
	}
}

// Comment-triggered briefs must NOT carry any unconditional status-flip
// command targeting the current issue. Previous revisions had a
// dedicated protocol step that wrote `multica issue status <this-issue-id> in_review`;
// the comment-triggered workflow rule "Do NOT change the issue status
// unless the comment explicitly asks for it" must remain the source of
// truth (Elon's blocking review on PR #2918).
func TestCommentTriggeredProtocolDoesNotForceInReview(t *testing.T) {
	t.Parallel()
	ctx := TaskContextForEnv{
		IssueID:          "55555555-6666-7777-8888-999999999999",
		TriggerCommentID: "66666666-7777-8888-9999-aaaaaaaaaaaa",
	}
	out := buildMetaSkillContent("claude", ctx)

	if strings.Contains(out, "`multica issue status <this-issue-id> in_review`") {
		t.Errorf("comment-triggered brief must not contain a placeholder `<this-issue-id> in_review` flip — that conflicts with the comment-triggered \"do not change status unless asked\" rule")
	}

	const guardrail = "Do NOT change the issue status unless the comment explicitly asks for it"
	if !strings.Contains(out, guardrail) {
		t.Errorf("expected the comment-triggered workflow guardrail %q to be present", guardrail)
	}
}

// Assignment-triggered briefs are the inverse boundary: when the agent
// owns the issue lifecycle, the brief AS A WHOLE must still tell it to
// flip to in_review on completion. The flip lives in the
// assignment-triggered workflow above (with the real id substituted).
func TestAssignmentTriggeredProtocolStillFlipsInReview(t *testing.T) {
	t.Parallel()
	const issueID = "77777777-8888-9999-aaaa-bbbbbbbbbbbb"
	ctx := TaskContextForEnv{IssueID: issueID}
	out := buildMetaSkillContent("claude", ctx)

	want := "`multica issue status " + issueID + " in_review`"
	if !strings.Contains(out, want) {
		t.Errorf("assignment-triggered brief must still flip to in_review on completion (expected %q in the workflow above)", want)
	}
}

// The sub-issue creation rule must reach top-level parents that have no
// `parent_issue_id` of their own — that is where the `todo` vs `backlog`
// decision matters most. The section must not gate on this issue being
// a child, and must not even mention `parent_issue_id`.
func TestSubIssueCreationSectionIsUnconditional(t *testing.T) {
	t.Parallel()
	ctx := TaskContextForEnv{
		IssueID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
	}
	out := buildMetaSkillContent("claude", ctx)

	const header = "## Sub-issue Creation"
	start := strings.Index(out, header)
	if start == -1 {
		t.Fatalf("sub-issue creation section missing")
	}
	rest := out[start:]
	end := strings.Index(rest[len(header):], "\n## ")
	var section string
	if end == -1 {
		section = rest
	} else {
		section = rest[:len(header)+end]
	}

	if strings.Contains(section, "parent_issue_id") {
		t.Errorf("Sub-issue Creation section must not reference `parent_issue_id` — it applies to any issue-bound run, including top-level parents:\n%s", section)
	}
}

// Workspace Context block: workspace.context (the per-workspace system prompt
// owners set in Settings → General) must reach the brief as `## Workspace
// Context` for every task kind so agents see a consistent shared system prompt
// regardless of how they were triggered. Empty content must skip the heading
// entirely — bare headings would just add noise.
func TestWorkspaceContextRenderedAcrossTaskKinds(t *testing.T) {
	t.Parallel()
	const wsContext = "All comments must be in English. Prefer concise PR descriptions."
	cases := []struct {
		name string
		ctx  TaskContextForEnv
	}{
		{
			name: "assignment-triggered",
			ctx: TaskContextForEnv{
				IssueID:          "11111111-2222-3333-4444-555555555555",
				WorkspaceContext: wsContext,
			},
		},
		{
			name: "comment-triggered",
			ctx: TaskContextForEnv{
				IssueID:          "22222222-3333-4444-5555-666666666666",
				TriggerCommentID: "33333333-4444-5555-6666-777777777777",
				WorkspaceContext: wsContext,
			},
		},
		{
			name: "chat",
			ctx: TaskContextForEnv{
				ChatSessionID:    "chat-1",
				WorkspaceContext: wsContext,
			},
		},
		{
			name: "quick-create",
			ctx: TaskContextForEnv{
				QuickCreatePrompt: "create me an issue",
				WorkspaceContext:  wsContext,
			},
		},
		{
			name: "autopilot run-only",
			ctx: TaskContextForEnv{
				AutopilotRunID:   "run-1",
				WorkspaceContext: wsContext,
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := buildMetaSkillContent("claude", tc.ctx)

			if !strings.Contains(out, "## Workspace Context") {
				t.Fatalf("[%s] expected `## Workspace Context` heading", tc.name)
			}
			if !strings.Contains(out, wsContext) {
				t.Errorf("[%s] brief missing workspace context body %q", tc.name, wsContext)
			}
			// The block must precede Available Commands so it acts as
			// background framing, not a footer hidden below CLI usage.
			ctxIdx := strings.Index(out, "## Workspace Context")
			cmdsIdx := strings.Index(out, "## Available Commands")
			if ctxIdx == -1 || cmdsIdx == -1 || ctxIdx > cmdsIdx {
				t.Errorf("[%s] `## Workspace Context` must appear above `## Available Commands` (ctx=%d, cmds=%d)", tc.name, ctxIdx, cmdsIdx)
			}
		})
	}
}

func TestWorkspaceContextHeadingSkippedWhenEmpty(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ctx  TaskContextForEnv
	}{
		{
			name: "empty string",
			ctx: TaskContextForEnv{
				IssueID:          "11111111-2222-3333-4444-555555555555",
				WorkspaceContext: "",
			},
		},
		{
			name: "whitespace only",
			ctx: TaskContextForEnv{
				IssueID:          "11111111-2222-3333-4444-555555555555",
				WorkspaceContext: "   \n\t  \r\n",
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := buildMetaSkillContent("claude", tc.ctx)
			if strings.Contains(out, "## Workspace Context") {
				t.Errorf("[%s] empty workspace context must NOT emit the heading", tc.name)
			}
		})
	}
}

func TestSubIssueCreationSectionSkippedForNonIssueModes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ctx  TaskContextForEnv
	}{
		{
			name: "chat",
			ctx:  TaskContextForEnv{ChatSessionID: "chat-1"},
		},
		{
			name: "quick-create",
			ctx:  TaskContextForEnv{QuickCreatePrompt: "create me an issue"},
		},
		{
			name: "autopilot run-only",
			ctx:  TaskContextForEnv{AutopilotRunID: "run-1"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := buildMetaSkillContent("claude", tc.ctx)
			if strings.Contains(out, "## Sub-issue Creation") {
				t.Errorf("%s mode must NOT emit the Sub-issue Creation section", tc.name)
			}
		})
	}
}

// writeRuntimeConfigFile is the safe replacement for the previous
// unconditional os.WriteFile of CLAUDE.md / AGENTS.md / GEMINI.md. The three
// states it must handle correctly are: file missing, file present without
// markers (user-authored content already there — the regression case from
// MUL-2753), and file present with markers (idempotent second-run replace).

func TestWriteRuntimeConfigFileCreatesMissingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	const brief = "# Multica Agent Runtime\n\nbrief body line"

	if err := writeRuntimeConfigFile(path, brief); err != nil {
		t.Fatalf("writeRuntimeConfigFile returned error: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back file: %v", err)
	}
	s := string(got)
	if !strings.HasPrefix(s, runtimeMarkerBegin+"\n") {
		t.Errorf("output should start with begin marker, got:\n%s", s)
	}
	if !strings.Contains(s, brief) {
		t.Errorf("output should contain brief body, got:\n%s", s)
	}
	if !strings.Contains(s, "\n"+runtimeMarkerEnd+"\n") {
		t.Errorf("output should contain end marker followed by newline, got:\n%s", s)
	}
}

func TestWriteRuntimeConfigFilePreservesUserContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	const userContent = "# User repo CLAUDE.md\n\n- rule one\n- rule two\n"
	if err := os.WriteFile(path, []byte(userContent), 0o644); err != nil {
		t.Fatalf("seed user file: %v", err)
	}

	const brief = "## Multica brief\n\ninjected body"
	if err := writeRuntimeConfigFile(path, brief); err != nil {
		t.Fatalf("writeRuntimeConfigFile returned error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back file: %v", err)
	}
	s := string(got)
	// The user's original content must be untouched and appear before the
	// injected marker block; this is the core regression case from MUL-2753.
	if !strings.HasPrefix(s, userContent) {
		t.Errorf("user content must be preserved verbatim at the top of the file, got:\n%s", s)
	}
	beginIdx := strings.Index(s, runtimeMarkerBegin)
	endIdx := strings.Index(s, runtimeMarkerEnd)
	if beginIdx < 0 || endIdx <= beginIdx {
		t.Fatalf("expected a well-formed marker block in:\n%s", s)
	}
	if beginIdx < len(userContent) {
		t.Errorf("begin marker must appear after user content, beginIdx=%d userLen=%d", beginIdx, len(userContent))
	}
	if !strings.Contains(s, brief) {
		t.Errorf("brief body missing from output:\n%s", s)
	}
}

func TestWriteRuntimeConfigFileReplacesExistingBlock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	const userBefore = "# User AGENTS.md\n\nuser line above\n"
	const userAfter = "\nuser line below the block\n"
	original := userBefore +
		runtimeMarkerBegin + "\n" +
		"OLD BRIEF CONTENT THAT MUST GO AWAY\n" +
		runtimeMarkerEnd + "\n" +
		userAfter
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const newBrief = "## New Multica brief\n\nfresh body"
	if err := writeRuntimeConfigFile(path, newBrief); err != nil {
		t.Fatalf("writeRuntimeConfigFile returned error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back file: %v", err)
	}
	s := string(got)
	if !strings.HasPrefix(s, userBefore) {
		t.Errorf("content above the marker block must be preserved, got:\n%s", s)
	}
	if !strings.HasSuffix(s, userAfter) {
		t.Errorf("content below the marker block must be preserved, got:\n%s", s)
	}
	if strings.Contains(s, "OLD BRIEF CONTENT THAT MUST GO AWAY") {
		t.Errorf("previous block body must be replaced, got:\n%s", s)
	}
	if !strings.Contains(s, newBrief) {
		t.Errorf("new brief body missing from output:\n%s", s)
	}
	if strings.Count(s, runtimeMarkerBegin) != 1 || strings.Count(s, runtimeMarkerEnd) != 1 {
		t.Errorf("there must be exactly one begin/end marker pair, got:\n%s", s)
	}
}

func TestWriteRuntimeConfigFileIsIdempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	const userContent = "# User CLAUDE.md\n\nimportant rules\n"
	if err := os.WriteFile(path, []byte(userContent), 0o644); err != nil {
		t.Fatalf("seed user file: %v", err)
	}

	const brief = "## Multica brief\n\nbody"
	for i := 0; i < 5; i++ {
		if err := writeRuntimeConfigFile(path, brief); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back file: %v", err)
	}
	s := string(got)
	if strings.Count(s, runtimeMarkerBegin) != 1 {
		t.Errorf("repeated runs must not duplicate the begin marker, count=%d, file:\n%s", strings.Count(s, runtimeMarkerBegin), s)
	}
	if strings.Count(s, runtimeMarkerEnd) != 1 {
		t.Errorf("repeated runs must not duplicate the end marker, count=%d, file:\n%s", strings.Count(s, runtimeMarkerEnd), s)
	}
	if strings.Count(s, brief) != 1 {
		t.Errorf("repeated runs must not duplicate the brief body, count=%d, file:\n%s", strings.Count(s, brief), s)
	}
	if !strings.HasPrefix(s, userContent) {
		t.Errorf("user content must remain intact at the top of the file, got:\n%s", s)
	}
}

// InjectRuntimeConfig is the production entry point — verify the marker
// semantics propagate through it for each provider's target filename.
func TestInjectRuntimeConfigPreservesUserContent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		provider string
		filename string
	}{
		{"claude", "CLAUDE.md"},
		{"codex", "AGENTS.md"},
		{"copilot", "AGENTS.md"},
		{"opencode", "AGENTS.md"},
		{"openclaw", "AGENTS.md"},
		{"hermes", "AGENTS.md"},
		{"pi", "AGENTS.md"},
		{"cursor", "AGENTS.md"},
		{"kimi", "AGENTS.md"},
		{"kiro", "AGENTS.md"},
		{"gemini", "GEMINI.md"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.provider, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, tc.filename)
			const userContent = "# User-authored file\n\ndon't touch this\n"
			if err := os.WriteFile(path, []byte(userContent), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}

			content, err := InjectRuntimeConfig(dir, tc.provider, TaskContextForEnv{
				IssueID: "11111111-2222-3333-4444-555555555555",
			})
			if err != nil {
				t.Fatalf("InjectRuntimeConfig: %v", err)
			}
			if content == "" {
				t.Fatalf("returned brief content must be non-empty")
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			s := string(got)
			if !strings.HasPrefix(s, userContent) {
				t.Errorf("[%s] user content must be preserved verbatim at the top of %s, got:\n%s", tc.provider, tc.filename, s)
			}
			if !strings.Contains(s, runtimeMarkerBegin) || !strings.Contains(s, runtimeMarkerEnd) {
				t.Errorf("[%s] %s must contain the runtime marker block, got:\n%s", tc.provider, tc.filename, s)
			}
		})
	}
}

func TestInjectRuntimeConfigUnknownProviderSkipsWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Seed all three candidate filenames so we can verify none of them get
	// written when the provider is unknown.
	for _, name := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("untouched\n"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	if _, err := InjectRuntimeConfig(dir, "totally-unknown-provider", TaskContextForEnv{
		IssueID: "11111111-2222-3333-4444-555555555555",
	}); err != nil {
		t.Fatalf("InjectRuntimeConfig: %v", err)
	}
	for _, name := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != "untouched\n" {
			t.Errorf("unknown provider must not write %s; got:\n%s", name, string(got))
		}
	}
}
