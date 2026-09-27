package daemon

import (
	"regexp"
	"strings"
)

var slashSkillRe = regexp.MustCompile(
	`\[/((?:[^\]\\]|\\.)+)\]\(slash://skill/([^)]+)\)`,
)

type SlashSkillRef struct {
	Label string
	ID    string
}

func ExtractSlashSkills(md string) []SlashSkillRef {
	matches := slashSkillRe.FindAllStringSubmatch(md, -1)
	seen := make(map[string]struct{}, len(matches))
	refs := make([]SlashSkillRef, 0, len(matches))

	for _, m := range matches {
		id := m[2]
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}

		label := strings.ReplaceAll(m[1], `\[`, "[")
		label = strings.ReplaceAll(label, `\]`, "]")
		refs = append(refs, SlashSkillRef{Label: label, ID: id})
	}

	return refs
}

// ExtractSlashSkillsAll extracts skill refs across several markdown sources
// (trigger comment, coalesced comments, chat message), deduplicating by ID
// across sources in first-seen order. Empty sources are skipped.
func ExtractSlashSkillsAll(sources []string) []SlashSkillRef {
	seen := make(map[string]struct{})
	refs := make([]SlashSkillRef, 0)
	for _, src := range sources {
		if src == "" {
			continue
		}
		for _, ref := range ExtractSlashSkills(src) {
			if _, ok := seen[ref.ID]; ok {
				continue
			}
			seen[ref.ID] = struct{}{}
			refs = append(refs, ref)
		}
	}
	return refs
}
