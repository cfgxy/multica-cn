package util

import (
	"regexp"
	"strings"
)

// Mention represents a parsed @mention from markdown content.
type Mention struct {
	Type string // "member", "agent", "issue", or "all"
	ID   string // user_id, agent_id, issue_id, or "all"
}

// MentionRe matches [@Label](mention://type/id) or [Label](mention://issue/id) in markdown.
// The @ prefix is optional to support issue mentions which use [MUL-123](mention://issue/...).
// Uses .+? (non-greedy) instead of [^\]]* so labels containing square brackets
// (e.g. "David[TF]") are matched correctly — the ](mention:// anchor is specific
// enough to prevent over-matching.
var MentionRe = regexp.MustCompile(`\[@?(.+?)\]\(mention://(member|agent|squad|issue|all)/([0-9a-fA-F-]+|all)\)`)

// IsMentionAll returns true if the mention is an @all mention.
func (m Mention) IsMentionAll() bool {
	return m.Type == "all"
}

// ParseMentions extracts deduplicated mentions from markdown content.
func ParseMentions(content string) []Mention {
	matches := MentionRe.FindAllStringSubmatch(content, -1)
	seen := make(map[string]bool)
	var result []Mention
	for _, m := range matches {
		key := m[2] + ":" + m[3]
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, Mention{Type: m[2], ID: m[3]})
	}
	return result
}

// StripAgentMentionLinks keeps the display label while removing every agent
// mention link. It is for system-synthesized comments, which cannot dispatch a
// task and therefore must not claim to delegate work.
func StripAgentMentionLinks(content string) string {
	const agentMentionPrefix = "](mention://agent/"

	var stripped strings.Builder
	remaining := content
	for {
		linkEnd := strings.Index(remaining, agentMentionPrefix)
		if linkEnd < 0 {
			stripped.WriteString(remaining)
			return stripped.String()
		}
		labelStart, ok := markdownLinkLabelStart(remaining, linkEnd)
		if !ok {
			// This is not a well-formed Markdown link. Preserve it and keep
			// scanning after the agent target prefix for the next candidate.
			prefixEnd := linkEnd + len(agentMentionPrefix)
			stripped.WriteString(remaining[:prefixEnd])
			remaining = remaining[prefixEnd:]
			continue
		}

		stripped.WriteString(remaining[:labelStart])
		stripped.WriteString(remaining[labelStart+1 : linkEnd])
		targetEnd := strings.IndexByte(remaining[linkEnd+len(agentMentionPrefix):], ')')
		if targetEnd < 0 {
			// The truncated target cannot activate as a Markdown link, but leaving
			// its protocol in a synthetic comment would still misrepresent a
			// dispatch. Preserve the trailing text without the agent link prefix.
			stripped.WriteString(remaining[linkEnd+len(agentMentionPrefix):])
			return stripped.String()
		}
		remaining = remaining[linkEnd+len(agentMentionPrefix)+targetEnd+1:]
	}
}

// markdownLinkLabelStart returns the opening bracket for a link whose closing
// bracket starts at linkEnd. Bracket balancing keeps an agent link from
// consuming a preceding member or issue link, while retaining nested labels.
func markdownLinkLabelStart(content string, linkEnd int) (int, bool) {
	depth := 1
	for i := linkEnd - 1; i >= 0; i-- {
		if markdownBracketEscaped(content, i) {
			continue
		}
		switch content[i] {
		case ']':
			depth++
		case '[':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

func markdownBracketEscaped(content string, index int) bool {
	backslashes := 0
	for index--; index >= 0 && content[index] == '\\'; index-- {
		backslashes++
	}
	return backslashes%2 != 0
}

// IsAgentSelfMention reports whether content explicitly mentions agentID.
func IsAgentSelfMention(content, agentID string) bool {
	agentUUID, err := ParseUUID(agentID)
	if err != nil {
		return false
	}
	for _, mention := range ParseMentions(content) {
		if mention.Type != "agent" {
			continue
		}
		mentionedUUID, err := ParseUUID(mention.ID)
		if err == nil && mentionedUUID == agentUUID {
			return true
		}
	}
	return false
}

// HasMentionAll returns true if any mention in the slice is an @all mention.
func HasMentionAll(mentions []Mention) bool {
	for _, m := range mentions {
		if m.IsMentionAll() {
			return true
		}
	}
	return false
}
