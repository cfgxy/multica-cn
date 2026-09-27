package daemon

import (
	"fmt"
	"strings"

	"server/internal/daemon/execenv"
)

// skillDialectHint renders the provider-specific invocation hint for one
// skill slug — the translation of "the user explicitly picked this skill"
// into the syntax each harness CLI actually accepts. The message body keeps
// the platform-neutral `[/名](slash://skill/<id>)` form; translation happens
// only here, at the prompt layer.
//
//   - claude / codebuddy: the Skill tool call. The permission deny rules are
//     written in the same `Skill(name)` shape (runtime_skill_policy.go), so
//     the model sees one consistent spelling.
//   - codex: the `$skill-slug` mention (Codex CLI "Skill Mentions ($)"; the
//     skill files land in CODEX_HOME/skills/<slug>/ and are discovered
//     natively by codex_user_skills.go).
//   - everything else: a generic sentence pointing at the skills directory
//     the execenv matrix wrote (context.go skillsDirPath). Files are at least
//     present under .agent_context/skills/ even for runtimes with no native
//     discovery; a stale hint degrades to "skill not found", never a hard
//     failure.
func skillDialectHint(provider, slug string) string {
	switch provider {
	case "claude", "codebuddy":
		return fmt.Sprintf("invoke it with the Skill tool: `Skill(%s)`", slug)
	case "codex":
		return fmt.Sprintf("mention it as `%s` (Codex skill mention)", slug)
	default:
		return fmt.Sprintf("load and follow the skill `%s` from your skills directory", slug)
	}
}

// writeSkillInvocationSection emits the "Explicitly selected skills" block
// for slash-command skill references found in any of the run's text sources
// (chat message, trigger comment, coalesced comments). Shared by the chat
// and comment prompt builders so both paths speak the same dialects.
//
// The invocable name is the on-disk directory slug from
// execenv.ResolveSkillSlugs over the SAME batch writeSkillFiles receives
// (task.Agent.Skills, order preserved), not the display name — listing an
// identifier the CLI cannot invoke is exactly the MUL-5529 defect. Skills
// hidden from model invocation (frontmatter `disable-model-invocation`) and
// IDs not on the agent's own list are filtered silently: an unknown or
// hidden reference must never reach the model as a callable instruction.
func writeSkillInvocationSection(b *strings.Builder, task Task, provider string, sources ...string) {
	if task.Agent == nil || len(task.Agent.Skills) == 0 {
		return
	}
	refs := ExtractSlashSkillsAll(sources)
	if len(refs) == 0 {
		return
	}

	envSkills := convertSkillsForEnv(task.Agent.Skills)
	slugs := execenv.ResolveSkillSlugs(envSkills)
	indexByID := make(map[string]int, len(task.Agent.Skills))
	for i, s := range task.Agent.Skills {
		indexByID[s.ID] = i
	}

	type skillInvocation struct{ name, slug string }
	selected := make([]skillInvocation, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		idx, ok := indexByID[ref.ID]
		if !ok {
			continue
		}
		if _, dup := seen[ref.ID]; dup {
			continue
		}
		if !execenv.SkillModelVisible(envSkills[idx]) {
			continue
		}
		seen[ref.ID] = struct{}{}
		selected = append(selected, skillInvocation{name: task.Agent.Skills[idx].Name, slug: slugs[idx]})
	}
	if len(selected) == 0 {
		return
	}

	b.WriteString("Explicitly selected skills:\n")
	for _, s := range selected {
		fmt.Fprintf(b, "- %s — %s\n", s.name, skillDialectHint(provider, s.slug))
	}
	b.WriteString("\n")
}
