package execenv

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// runtimeMarkerBegin and runtimeMarkerEnd delimit the Multica-managed brief
// inside the runtime config file (CLAUDE.md / AGENTS.md / GEMINI.md). The
// markers exist so writeRuntimeConfigFile can:
//
//   - preserve user-authored content in the same file (the user's repo may
//     already ship a CLAUDE.md / AGENTS.md when the agent is pointed at a
//     local_directory project resource),
//   - replace the brief idempotently on subsequent runs in the same workdir
//     instead of appending duplicate copies, and
//   - leave a precise excision target for a future cleanup pass.
//
// HTML comments are used so the markers are inert in every Markdown renderer
// and harmless when fed to the agent as instructions. Changing the marker
// text is a breaking change for any file that already carries the previous
// markers — bump deliberately.
const (
	runtimeMarkerBegin = "<!-- BEGIN MULTICA-RUNTIME (auto-managed; do not edit) -->"
	runtimeMarkerEnd   = "<!-- END MULTICA-RUNTIME -->"

	// runtimeManagedSeparator is the fixed separator inserted between any
	// pre-existing user content and the marker block whenever Inject
	// appends to a file that already exists. The separator is considered
	// part of the managed region: Cleanup strips it together with the
	// block, so the file rolls back to its exact pre-injection bytes
	// regardless of whether the user file ended with no newline, one
	// newline, or multiple trailing newlines. Without a fixed-width
	// separator the cleanup path would have to renormalise the user's
	// trailing bytes and would leave a subtle but real diff every run
	// (see MUL-2753 review on PR #3438).
	//
	// Cleanup distinguishes "file we created" (no managed separator
	// precedes the block — write a missing file from scratch) from "file
	// that pre-existed" (managed separator precedes the block) so the
	// file's existence is preserved exactly across the inject→cleanup
	// cycle, including empty / whitespace-only pre-existing files.
	runtimeManagedSeparator = "\n\n"
)

// runtimeGOOS is the host-platform string used by buildMetaSkillContent and
// BuildCommentReplyInstructions to emit Windows-specific guidance. Defaults
// to runtime.GOOS; tests override it to exercise the cross-platform branches
// deterministically without having to run on every target OS.
var runtimeGOOS = runtime.GOOS

// sanitizeNameForBriefMarkdown turns a possibly-multiline display name into a
// single-line, plain-text token that is safe to embed inside markdown inline
// constructs (e.g. `**%s**`) in the agent brief. The brief is loaded as
// trusted instructions, so user-controlled name fields must not be able to
// introduce headings, lists, or close the surrounding bold span.
//
// CR/LF and other whitespace control bytes collapse to a single space; other
// C0 controls and DEL are dropped; markdown structural characters that have
// meaning in inline context (`*`, `_`, “ ` “, `\`, `[`, `]`, `<`) are
// backslash-escaped. Trailing whitespace is trimmed.
func sanitizeNameForBriefMarkdown(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	prevSpace := false
	for _, r := range name {
		switch {
		case r == '\r' || r == '\n' || r == '\t' || r == '\v' || r == '\f':
			if !prevSpace && b.Len() > 0 {
				b.WriteByte(' ')
				prevSpace = true
			}
		case r < 0x20 || r == 0x7f:
			continue
		case r == '*' || r == '_' || r == '`' || r == '\\' || r == '[' || r == ']' || r == '<':
			b.WriteByte('\\')
			b.WriteRune(r)
			prevSpace = false
		default:
			b.WriteRune(r)
			prevSpace = false
		}
	}
	return strings.TrimSpace(b.String())
}

// sanitizeEmailForBrief returns the email verbatim when it is safe to embed
// inline in the brief, or "" when it carries a character a real address never
// has (whitespace, control chars, or a markdown-break risk). Unlike
// sanitizeNameForBriefMarkdown it does NOT backslash-escape markdown specials:
// an agent may want to match the initiator's address exactly, and escaping
// `_`/`+` would corrupt it, while a valid email can't contain a newline to
// inject a heading anyway. Emails are validated at signup, so this is
// defense-in-depth, not the primary guard. See MUL-2645.
func sanitizeEmailForBrief(email string) string {
	email = strings.TrimSpace(email)
	if email == "" || !strings.Contains(email, "@") {
		return ""
	}
	for _, r := range email {
		if r < 0x20 || r == 0x7f || r == ' ' || r == '\\' || r == '`' || r == '*' || r == '<' || r == '>' || r == '[' || r == ']' {
			return ""
		}
	}
	return email
}

// formatProjectResource renders a single resource as a human-readable bullet.
// Unknown resource types fall back to a JSON-encoded ref so the agent can
// still read what the user attached. New resource types should add a case
// here AND in the API validator (handler/project_resource.go).
func formatProjectResource(r ProjectResourceForEnv) string {
	label := r.Label
	switch r.ResourceType {
	case "github_repo":
		var payload struct {
			URL               string `json:"url"`
			DefaultBranchHint string `json:"default_branch_hint,omitempty"`
		}
		_ = json.Unmarshal(r.ResourceRef, &payload)
		out := fmt.Sprintf("**GitHub repo**: %s", payload.URL)
		if payload.DefaultBranchHint != "" {
			out += fmt.Sprintf(" (default branch: `%s`)", payload.DefaultBranchHint)
		}
		if label != "" {
			out += " — " + label
		}
		return out
	default:
		ref := string(r.ResourceRef)
		if ref == "" {
			ref = "{}"
		}
		out := fmt.Sprintf("**%s**: `%s`", r.ResourceType, ref)
		if label != "" {
			out += " — " + label
		}
		return out
	}
}

// InjectRuntimeConfig writes the meta skill content into the runtime-specific
// config file so the agent discovers its environment through its native mechanism.
//
// For Claude:   writes {workDir}/CLAUDE.md  (skills discovered natively from .claude/skills/)
// For Codex:    writes {workDir}/AGENTS.md  (skills discovered natively via CODEX_HOME)
// For Copilot:  writes {workDir}/AGENTS.md  (skills discovered natively from .github/skills/)
// For OpenCode: writes {workDir}/AGENTS.md  (skills discovered natively from .opencode/skills/)
// For OpenClaw: writes {workDir}/AGENTS.md  (skills discovered natively from {workDir}/skills/ via per-task openclaw-config.json that pins agents.defaults.workspace)
// For Hermes:   writes {workDir}/AGENTS.md  (skills fall back to .agent_context/skills/; AGENTS.md points there)
// For Gemini:   writes {workDir}/GEMINI.md  (discovered natively by the Gemini CLI)
// For Pi:       writes {workDir}/AGENTS.md  (skills discovered natively from .pi/skills/)
// For Cursor:   writes {workDir}/AGENTS.md  (skills discovered natively from .cursor/skills/)
// For Kimi:        writes {workDir}/AGENTS.md  (Kimi Code CLI reads AGENTS.md natively; skills auto-discovered from project skills dirs)
// For Kiro:        writes {workDir}/AGENTS.md  (Kiro CLI reads AGENTS.md natively; skills auto-discovered from project skills dirs)
// For Qoder:       writes {workDir}/AGENTS.md  (skills discovered from .qoder/skills/, user-level ~/.qoder/skills is unaffected)
// For Antigravity: writes {workDir}/AGENTS.md  (agy CLI reads AGENTS.md natively; skills discovered natively from .agents/skills/ — see https://antigravity.google/docs/gcli-migration)
func InjectRuntimeConfig(workDir, provider string, ctx TaskContextForEnv) (string, error) {
	content := buildMetaSkillContent(provider, ctx)
	path := runtimeConfigPath(workDir, provider)
	if path == "" {
		// Unknown provider — skip config injection, prompt-only mode.
		return content, nil
	}
	return content, writeRuntimeConfigFile(path, content)
}

// runtimeConfigPath returns the absolute path to the runtime config file that
// InjectRuntimeConfig writes for the given provider, or "" when the provider
// has no file-based config target. Centralising the mapping keeps Inject /
// Cleanup in lockstep — both paths consult the same table so a new provider
// added to one side cannot drift past the other.
func runtimeConfigPath(workDir, provider string) string {
	switch provider {
	case "claude", "codebuddy":
		return filepath.Join(workDir, "CLAUDE.md")
	case "codex", "copilot", "opencode", "openclaw", "hermes", "pi", "cursor", "kimi", "kiro", "antigravity", "qoder":
		return filepath.Join(workDir, "AGENTS.md")
	case "gemini":
		return filepath.Join(workDir, "GEMINI.md")
	default:
		return ""
	}
}

// writeRuntimeConfigFile writes the Multica runtime brief to path without
// clobbering any user-authored content already present. Behaviour by file
// state:
//
//   - file missing → create the file containing only the marker block, no
//     leading separator. Cleanup detects the absence of the separator and
//     restores the missing-file state by removing the file outright.
//   - file present (any content, including empty), no marker block →
//     append `<runtimeManagedSeparator>` + the marker block. The
//     separator's bytes are part of the managed region so Cleanup can
//     restore the user's pre-injection bytes exactly (no trailing-newline
//     normalisation, no surprises for files that ended without a newline
//     or with extra trailing newlines).
//   - file present, marker block already there → replace the body between
//     the markers in place so repeated runs in the same workdir don't grow
//     the file unboundedly. The pre-block content (including any managed
//     separator established by the first inject) is preserved verbatim.
//
// The previous implementation called os.WriteFile unconditionally, which
// silently truncated a repository's CLAUDE.md / AGENTS.md / GEMINI.md the
// first time the agent was pointed at the user's own directory via the
// local_directory project resource flow. See MUL-2753.
func writeRuntimeConfigFile(path, brief string) error {
	block := runtimeMarkerBegin + "\n" + strings.TrimRight(brief, "\n") + "\n" + runtimeMarkerEnd + "\n"

	existing, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return os.WriteFile(path, []byte(block), 0o644)
	}
	if err != nil {
		return fmt.Errorf("read existing runtime config %s: %w", path, err)
	}

	existingStr := string(existing)
	if start, end, ok := locateMarkerBlock(existingStr); ok {
		// Replace the existing block in place. locateMarkerBlock already
		// consumes the trailing newline that closed the previous block, so
		// successive runs don't accumulate blank lines around the block.
		// The managed separator (if any) lives in existingStr[:start] and
		// is preserved untouched.
		newContent := existingStr[:start] + block + existingStr[end:]
		return os.WriteFile(path, []byte(newContent), 0o644)
	}

	// No marker block present. Append the fixed managed separator followed
	// by the block. The separator is unconditional — including for files
	// that already end in two or more newlines — so the byte boundary
	// between user content and the managed region is deterministic, which
	// is what lets Cleanup roll back to the user's exact original bytes.
	return os.WriteFile(path, []byte(existingStr+runtimeManagedSeparator+block), 0o644)
}

// locateMarkerBlock finds the [start, end) byte range of the Multica marker
// block inside content. The returned `end` is one past the block's trailing
// newline (if any) so callers can splice the block out without leaving an
// orphan blank line behind.
//
// The end marker is searched for strictly after the begin marker. This
// matters for two malformed cases that the previous naive `strings.Index`
// pair would mishandle:
//
//   - User content carries a stray `<!-- END MULTICA-RUNTIME -->` (e.g. a
//     documentation snippet showing what the wire format looks like) before
//     any begin marker. The naive parser would find that end and reject the
//     block (`endIdx > startIdx` false), then append a fresh block — and
//     since the stray end stays in place, every subsequent run would append
//     yet another block, growing the file unboundedly.
//   - A previous run crashed between writing begin and end and left the file
//     with a half-block. The naive parser would not find an end, fall
//     through to the append branch, and stack a new block after the
//     half-block. Treating "begin found, no end after" as "the block ends
//     at EOF" makes the next write replace the half-block in place.
func locateMarkerBlock(content string) (start, end int, found bool) {
	start = strings.Index(content, runtimeMarkerBegin)
	if start < 0 {
		return 0, 0, false
	}
	afterBegin := start + len(runtimeMarkerBegin)
	endRel := strings.Index(content[afterBegin:], runtimeMarkerEnd)
	if endRel < 0 {
		// Malformed — no end marker after begin. Treat the rest of the file
		// as the block so the next write replaces it cleanly instead of
		// stacking another block beneath the half-block.
		return start, len(content), true
	}
	end = afterBegin + endRel + len(runtimeMarkerEnd)
	if end < len(content) && content[end] == '\n' {
		end++
	}
	return start, end, true
}

// CleanupRuntimeConfig excises the Multica marker block from the runtime
// config file for the given provider and restores the file to its exact
// pre-injection state, byte for byte. The cleanup is the second half of
// the contract `writeRuntimeConfigFile` establishes: together they must
// round-trip a user's local repository config across an arbitrary number
// of Multica runs without ever touching a single non-managed byte.
//
// Behaviour, mirroring the three Inject states:
//
//   - file has no marker block → no-op (nothing was ever injected here);
//   - block is at the start of the file with no preceding managed
//     separator → the file was created by Inject from a missing-file
//     state. Remove the file outright so the post-cleanup directory
//     listing is byte-identical to the pre-Inject one.
//   - block is preceded by the fixed managed separator → strip the
//     separator together with the block; whatever remains (which may be
//     an empty pre-existing file, a whitespace-only file, or arbitrary
//     user content) is the user's original file, written back verbatim
//     with NO trailing-newline normalisation and NO TrimSpace-based file
//     removal heuristic. Both of those were sources of subtle diff in
//     PR #3438 review feedback.
//
// Required for the local_directory flow (WorkDir is the user's own repo):
// without this pass, a manual `claude` / `codex` / `gemini` run started by
// the user inside the same directory after a Multica task would pick up
// the stale brief and act on the previous task's issue id, trigger
// comment id, and reply rules. Cloud workspace runs never trigger this
// pollution because their workdir is daemon scratch that the GC loop
// deletes wholesale; the daemon skips this Cleanup on those workdirs.
//
// Missing files, unknown providers, and files without a marker block are
// no-ops — Cleanup is safe to call defensively.
func CleanupRuntimeConfig(workDir, provider string) error {
	path := runtimeConfigPath(workDir, provider)
	if path == "" {
		return nil
	}
	existing, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read runtime config %s: %w", path, err)
	}
	existingStr := string(existing)
	start, end, ok := locateMarkerBlock(existingStr)
	if !ok {
		return nil
	}
	pre := existingStr[:start]
	post := existingStr[end:]

	// Detect — and strip — the fixed managed separator that Inject puts
	// immediately before the block whenever it appended to a file that
	// pre-existed. The absence of the separator is the marker that says
	// "Inject created this file from scratch", which is the only case
	// where Cleanup is allowed to delete the file.
	hadManagedSeparator := strings.HasSuffix(pre, runtimeManagedSeparator)
	if hadManagedSeparator {
		pre = pre[:len(pre)-len(runtimeManagedSeparator)]
	}
	remainder := pre + post

	if !hadManagedSeparator && remainder == "" {
		// Inject created the file (no managed separator → block was the
		// only content). Restore the missing-file state.
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove runtime config %s: %w", path, err)
		}
		return nil
	}
	// File pre-existed (possibly empty, possibly whitespace-only,
	// possibly with user content) — write the remainder back exactly,
	// without any normalisation. An empty `remainder` here means the
	// user's original file was empty; we still write it (zero-byte file)
	// so the file's existence is preserved.
	return os.WriteFile(path, []byte(remainder), 0o644)
}

// buildMetaSkillContent generates the meta skill markdown that teaches the
// agent about the Multica runtime environment and available CLI tools.
//
// As of MUL-3560 PR 0.5 the builder is a thin kind-aware dispatcher: each
// section of the brief lives in its own `writeXxx` helper (see
// runtime_config_sections.go), and the assembly order is driven by the
// taskKind classification (see runtime_config_kind.go).
//
// PR 0.6 layers per-kind section gating on top of that dispatcher: every
// section that a given kind has no use for is now elided at the call site
// instead of rendered and ignored. The current matrix is:
//
//	Section               | comment | assign | autopilot | quick_create | chat
//	----------------------+---------+--------+-----------+--------------+------
//	Available Commands    |   full  |  full  |   full    |   minimal    | full
//	Comment Formatting    |    ✓    |   ✓    |     —     |      —       |  —
//	Repositories          |    △    |   △    |     △     |      —       |  △
//	Project Context       |    △    |   △    |     —     |      —       |  —
//	Issue Metadata        |    ✓    |   ✓    |     —     |      —       |  —
//	Instruction Precedence|    —    |   ✓    |     —     |      —       |  —
//	Sub-issue Creation    |    ✓    |   ✓    |     —     |      —       |  —
//	Skills                |    ✓    |   ✓    |     ✓     |      —       |  ✓
//	Mentions              |    ✓    |   ✓    |     —     |      —       |  —
//	Attachments           |    ✓    |   ✓    |     —     |      —       |  —
//
// (✓ always; — never; △ data-driven inside the helper.) Always-on rows —
// Header, Background Task Safety, Agent Identity, Requesting User, Task
// Initiator, Workspace Context, Workflow, Always Use CLI, Output — are
// shared by every kind and emitted unconditionally (or gated by their own
// data preconditions).
//
// The matrix above is the source of truth for any later content-gating
// change; updating either side without the other is the regression
// TestBuildMetaSkillContentKindMatrix catches.
func buildMetaSkillContent(provider string, ctx TaskContextForEnv) string {
	var b strings.Builder
	kind := classifyTask(ctx)

	// === Always-on prelude ===
	//
	// Every kind starts with the same identity-and-actor-framing block:
	// header, background-task-safety, agent identity, requesting user,
	// task initiator, workspace context. Each helper internally suppresses
	// itself when its preconditions are not met (e.g. RequestingUser does
	// nothing on an empty profile description), so the prelude is uniform
	// across kinds even though the rendered output varies.
	writeHeader(&b)
	writeBackgroundTaskSafetyInstructions(&b)
	writeAgentIdentity(&b, ctx)

	writeRequestingUser(&b, ctx)
	writeTaskInitiator(&b, ctx)
	writeWorkspaceContext(&b, ctx)

	// === Available Commands ===
	//
	// Most kinds get the full Core CLI list. Quick-create collapses to a
	// minimal "just `issue create`" form because its hard guardrails
	// forbid get / status / comment add anyway — there is no value in
	// rendering 4k chars of commands the agent must not call. Autopilot
	// keeps the full list because run-only autopilot tasks are open-
	// ended and may need any command via their instructions.
	switch kind {
	case kindQuickCreate:
		writeAvailableCommandsQuickCreate(&b)
	default:
		writeAvailableCommands(&b)
	}

	// === Comment Formatting ===
	//
	// Only kinds that actually post issue comments need the
	// `--content-file` shell-safety drill. Chat replies go through the
	// chat pipeline, not `comment add`; quick-create runs exactly one
	// `issue create` then exits; autopilot run-only does not comment by
	// default (per its workflow guardrails).
	if kind == kindCommentTriggered || kind == kindAssignmentTriggered {
		writeCommentFormatting(&b)
	}

	// === Conditional context sections ===
	//
	// Repositories: cut from quick-create only — that kind's hard
	// guardrails forbid checkout. All other kinds keep the existing
	// data-driven `if len(ctx.Repos) > 0` guard inside the helper.
	if kind != kindQuickCreate {
		writeRepositories(&b, ctx)
	}

	// Project Context: scoped to issue kinds. Chat / quick-create /
	// autopilot do not operate on an issue belonging to a project, and
	// even when they did, the project resources pointer would be noise
	// for their workflows.
	if kind.hasIssueContext() {
		writeProjectContext(&b, ctx)
	}

	// Issue Metadata: only kinds that operate on a real Multica issue
	// (comment-triggered / assignment-triggered) can read or pin metadata,
	// so we gate by hasIssueContext. Chat / quick-create / autopilot
	// would otherwise just produce a guaranteed-failed `metadata list`
	// call on every entry.
	if kind.hasIssueContext() {
		writeIssueMetadata(&b)
	}

	// Instruction Precedence: only assignment-triggered runs see the
	// "agent identity wins over the assignment workflow" guardrail, since
	// they are the only kind whose workflow auto-flips status / drives
	// the full issue lifecycle. Other kinds' workflows are read/reply
	// only.
	if kind == kindAssignmentTriggered {
		writeInstructionPrecedence(&b)
	}

	// === Workflow ===
	//
	// The Workflow heading is uniform across kinds; the body switches
	// on the classified taskKind. Each case calls a single helper, so
	// the matrix of "which workflow does each kind get" is auditable as
	// one switch.
	writeWorkflowHeader(&b)
	switch kind {
	case kindChat:
		writeWorkflowChat(&b)
	case kindQuickCreate:
		writeWorkflowQuickCreate(&b)
	case kindAutopilotRunOnly:
		writeWorkflowAutopilot(&b, ctx)
	case kindCommentTriggered:
		writeWorkflowComment(&b, provider, ctx)
	case kindAssignmentTriggered:
		writeWorkflowAssignment(&b, ctx)
	}

	// === Trailing sections ===
	//
	// Sub-issue Creation is meaningful only for kinds with a parent /
	// child relationship — i.e. those that operate on a real issue.
	if kind.hasIssueContext() && ctx.IssueID != "" {
		writeSubIssueCreation(&b)
	}

	// Skills: kept for every kind that may chain into a discovered
	// skill at runtime (comment / assignment / autopilot / chat).
	// Quick-create is a one-shot `issue create` and never loads skills,
	// so we drop the list. The helper still no-ops when ctx.AgentSkills
	// is empty.
	if kind != kindQuickCreate {
		writeSkills(&b, provider, ctx)
	}

	// Mentions: only the kinds that produce a comment need the
	// `@mention` side-effect discipline. Chat / quick-create / autopilot
	// never emit a comment under their workflow, so the section is pure
	// noise for them.
	if kind == kindCommentTriggered || kind == kindAssignmentTriggered {
		writeMentions(&b)
	}

	// Attachments: same shape as Comment Formatting / Mentions — only
	// kinds that work on a real issue (and therefore could surface an
	// attached file in the issue / comment timeline) need the CLI
	// pointer.
	if kind == kindCommentTriggered || kind == kindAssignmentTriggered {
		writeAttachments(&b)
	}

	writeAlwaysUseCLI(&b)
	writeOutput(&b, kind, ctx)

	return b.String()
}

func writeBackgroundTaskSafetyInstructions(b *strings.Builder) {
	b.WriteString("## Background Task Safety\n\n")
	b.WriteString("Multica marks this task terminal when your top-level agent process/turn exits. Any background work you started but did not collect before exiting can be orphaned: its result may be lost, and the user may see a completed/failed task even though the delegated work was never synthesized.\n\n")
	b.WriteString("- Do NOT end your turn while background tasks, async subagents, background shell commands, or detached tool calls are still running.\n")
	b.WriteString("- If a tool or runtime offers a background mode, use it only when you can explicitly wait for completion and collect the result before your final response.\n")
	b.WriteString("- If a tool response says to wait for a future notification/reminder instead of collecting now, do not rely on that in Multica-managed runs. Block on the appropriate wait/output/collect operation before exiting.\n")
	b.WriteString("- If you cannot observe or collect a background task's result, do not spawn it in the background; run the work synchronously instead.\n")
	b.WriteString("- Before posting your final result or exiting silently, account for every background task you started and incorporate its output or failure into your response.\n\n")
}
