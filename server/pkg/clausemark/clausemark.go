// Package clausemark implements the carrier marker contract of ADR 007
// (RUYI-751): the `<!-- clause:id=cls_xxxxxxxx rev=N -->` segments that bound
// the rendered clause region inside a carrier's business column.
//
// The markers have exactly four jobs (ADR 007 §标记格式): delimit the
// read-only rendered region for the re-renderer, carry the snapshot fallback
// for degraded claim-time injection, give the repo-side gate a recognizable
// region, and let humans locate the region. The normal dynamic injection
// path never parses markers — it renders straight from clause_version.
//
// Two tolerance levels live here on purpose:
//
//   - Parse is STRICT: a malformed segment is an error. It feeds snapshot
//     injection accounting, where a silent misparse would under-count.
//   - StripMarkers is TOLERANT: it removes well-formed regions and any
//     orphaned marker lines. It feeds the re-renderer, whose job is to
//     converge a damaged document back to a clean render.
package clausemark

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// OpenLine matches a clause region opener and captures the slug and revision:
// <!-- clause:id=cls_a1b2c3d4 rev=3 -->
var openLine = regexp.MustCompile(`^<!--\s*clause:id=(cls_[0-9a-z]{8})\s+rev=(\d+)\s*-->\s*$`)

// CloseLine matches a clause region closer: <!-- /clause -->
var closeLine = regexp.MustCompile(`^<!--\s*/clause\s*-->\s*$`)

// Segment is one parsed clause region.
type Segment struct {
	Slug string // cls_… identity, never mutated
	Rev  int    // must equal the rendered clause_version at render time
	Body string // the rendered clause text between the marker lines
}

// Document is the result of parsing a carrier business column.
type Document struct {
	// Stable is the text outside every clause region — the part the Owner
	// may hand-edit. Marker lines are removed from it.
	Stable string
	// Segments are the parsed regions in document order.
	Segments []Segment
}

// Parse splits a carrier business column into its stable context and its
// clause segments. It is strict: an unclosed region, a close without an
// open, a nested open, a malformed slug/rev, a duplicate slug, or an empty
// body is an error naming the offending line.
func Parse(text string) (Document, error) {
	var doc Document
	lines := splitLines(text)
	stable := make([]string, 0, len(lines))

	openIdx := -1
	var cur Segment
	for i, line := range lines {
		switch {
		case openIdx < 0 && openLine.MatchString(line):
			m := openLine.FindStringSubmatch(line)
			rev := 0
			fmt.Sscanf(m[2], "%d", &rev)
			cur = Segment{Slug: m[1], Rev: rev}
			openIdx = i
		case openIdx < 0 && closeLine.MatchString(line):
			return Document{}, fmt.Errorf("clausemark: 第 %d 行出现孤立的关闭标记 <!-- /clause -->", i+1)
		case openIdx >= 0 && openLine.MatchString(line):
			return Document{}, fmt.Errorf("clausemark: 第 %d 行在未关闭的条款段内出现新的打开标记", i+1)
		case openIdx >= 0 && closeLine.MatchString(line):
			if strings.TrimSpace(cur.Body) == "" {
				return Document{}, fmt.Errorf("clausemark: 条款段 %s（第 %d 行起）正文为空", cur.Slug, openIdx+1)
			}
			for _, seen := range doc.Segments {
				if seen.Slug == cur.Slug {
					return Document{}, fmt.Errorf("clausemark: 条款 %s 在第 %d 行重复出现", cur.Slug, i+1)
				}
			}
			doc.Segments = append(doc.Segments, cur)
			openIdx = -1
		case openIdx >= 0:
			cur.Body += line + "\n"
		default:
			stable = append(stable, line)
		}
	}
	if openIdx >= 0 {
		return Document{}, fmt.Errorf("clausemark: 条款段 %s（第 %d 行起）缺少关闭标记 <!-- /clause -->", cur.Slug, openIdx+1)
	}
	doc.Stable = strings.TrimRight(strings.Join(stable, "\n"), "\n")
	return doc, nil
}

// StripMarkers removes every well-formed clause region and any orphaned
// marker line, returning the remaining stable text. Unlike Parse it never
// fails: the re-renderer runs it on texts that may carry gate-synthesized
// damage (an eaten opener, a stray closer) and must converge regardless.
func StripMarkers(text string) string {
	lines := splitLines(text)
	out := make([]string, 0, len(lines))
	inRegion := false
	for _, line := range lines {
		switch {
		case !inRegion && openLine.MatchString(line):
			inRegion = true
		case inRegion && closeLine.MatchString(line):
			inRegion = false
		case inRegion:
			// body of a region — dropped
		default:
			if closeLine.MatchString(line) || openLine.MatchString(line) {
				continue // orphaned marker line
			}
			out = append(out, line)
		}
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// RenderedClause is one clause the renderer emits into the snapshot region.
type RenderedClause struct {
	Slug             string
	Version          int
	Body             string
	SupersededBodies []string
}

// Static removes every clause region and every asset-managed clause block from
// a carrier business column, leaving only the owner-editable static context.
// It is the only representation persisted back to a carrier business column.
// Dynamic clauses are composed for a claimed task with Render instead.
func Static(text string, clauses []RenderedClause) string {
	stable := StripMarkers(text)
	for _, c := range clauses {
		remove := func(body string) {
			stable = removeClauseBlock(stable, body)
			stable = removeSectionBlock(stable, body)
			stable = removeExactBlock(stable, body)
		}
		remove(c.Body)
		for _, body := range c.SupersededBodies {
			remove(body)
		}
	}
	return strings.TrimRight(stable, "\n")
}

// Render builds a claim-time context: the owner-editable static context with
// the deterministic snapshot region built from the effective dynamic clauses.
// Its output is runtime-only and must not be persisted as a carrier column.
//
// removeInline lists the clause titles whose bold-label blocks must leave the
// stable text — the asset-managed set. Without this, a clause would be
// injected twice: once inline (legacy position) and once from the segment.
//
// Determinism: same inputs produce byte-identical output. Idempotence:
// Render(Parse(Render(x, cs), removeInline), cs) == Render(x, cs) — the
// second pass finds no inline blocks left to remove and re-emits the same
// segment. Both properties are pinned by tests.
func Render(text string, clauses []RenderedClause) string {
	stable := Static(text, clauses)

	var sb strings.Builder
	sb.WriteString(stable)
	if len(clauses) > 0 {
		if strings.TrimSpace(stable) != "" {
			sb.WriteString("\n\n")
		}
		for i, c := range clauses {
			if i > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(OpenTag(c.Slug, c.Version))
			sb.WriteString("\n")
			sb.WriteString(strings.TrimRight(c.Body, "\n"))
			sb.WriteString("\n")
			sb.WriteString(CloseTag())
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// ReplaceSegment revises one rendered asset segment only when its marker
// carries the expected active version. Parse rejects malformed or duplicate
// markers; a missing or stale target also fails closed.
func ReplaceSegment(text, slug string, expectedVersion int32, body string) (string, error) {
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("clausemark: replacement body is empty")
	}
	for i, line := range splitLines(text) {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "<!-- clause:id=") && !openLine.MatchString(trimmed) {
			return "", fmt.Errorf("clausemark: malformed clause opener on line %d", i+1)
		}
		if strings.Contains(trimmed, "<!-- /clause") && !closeLine.MatchString(trimmed) {
			return "", fmt.Errorf("clausemark: malformed clause closer on line %d", i+1)
		}
	}
	doc, err := Parse(text)
	if err != nil {
		return "", err
	}
	found := false
	for i := range doc.Segments {
		if doc.Segments[i].Slug != slug {
			continue
		}
		if doc.Segments[i].Rev != int(expectedVersion) {
			return "", fmt.Errorf("clausemark: clause %s marker version %d does not match active version %d", slug, doc.Segments[i].Rev, expectedVersion)
		}
		doc.Segments[i].Rev++
		doc.Segments[i].Body = strings.TrimRight(body, "\n")
		found = true
	}
	if !found {
		return "", fmt.Errorf("clausemark: clause %s segment is missing", slug)
	}

	rendered := make([]RenderedClause, 0, len(doc.Segments))
	for _, segment := range doc.Segments {
		rendered = append(rendered, RenderedClause{
			Slug: segment.Slug, Version: segment.Rev, Body: segment.Body,
		})
	}
	return Render(doc.Stable, rendered), nil
}

// OpenTag renders the canonical opener for a clause version.
func OpenTag(slug string, version int) string {
	return fmt.Sprintf("<!-- clause:id=%s rev=%d -->", slug, version)
}

// CloseTag renders the canonical closer.
func CloseTag() string { return "<!-- /clause -->" }

// NewSlug mints a fresh cls_ identity: 8 lowercase alphanumeric characters
// from crypto/rand. The 36^8 space makes collision negligible; the UNIQUE
// (workspace_id, slug) index is the backstop.
func NewSlug() (string, error) {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("clausemark: mint slug: %w", err)
	}
	for i, b := range raw {
		raw[i] = alphabet[int(b)%len(alphabet)]
	}
	return "cls_" + string(raw), nil
}

// ValidSlug reports whether s matches the marker contract's slug shape.
func ValidSlug(s string) bool {
	return openLine.MatchString("<!-- clause:id=" + s + " rev=1 -->")
}

// MarkerRef is one open tag found in free text, in order of appearance.
type MarkerRef struct {
	Slug string
	Rev  int
}

var scanOpenRe = regexp.MustCompile(`<!--\s*clause:id=(cls_[0-9a-z]{8})\s+rev=(\d+)\s*-->`)

// ScanMarkers finds every clause open tag anywhere in text — not only on its
// own line, the way Parse requires. The reconciliation drift check compares
// these against the carrier's expected effective set; it observes, so
// unclosed or embedded markers are reported rather than rejected.
func ScanMarkers(text string) []MarkerRef {
	var out []MarkerRef
	for _, m := range scanOpenRe.FindAllStringSubmatch(text, -1) {
		rev, err := strconv.Atoi(m[2])
		if err != nil {
			continue // the character class makes this unreachable
		}
		out = append(out, MarkerRef{Slug: m[1], Rev: rev})
	}
	return out
}

// removeClauseBlock removes the bold-label clause block whose first line
// matches the label of block (the asset body), using the same span rules as
// the legislation engine: from the matching clause line to the next clause
// line, any heading, or EOF. Text with no such block is returned unchanged.
func removeClauseBlock(text, block string) string {
	label := firstClauseLabel(block)
	if label == "" {
		return text
	}
	lines := splitLines(text)
	out := make([]string, 0, len(lines))
	skipping := false
	for _, line := range lines {
		if skipping {
			if isClauseStart(line) || isHeading(line) {
				skipping = false
			} else {
				continue
			}
		}
		if !skipping && isClauseStart(line) && strings.EqualFold(clauseLabel(line), label) {
			skipping = true
			continue
		}
		out = append(out, line)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// removeSectionBlock moves a legacy section-sized clause asset out of stable
// text before it is rendered in the managed region. New clause assets use
// bold labels, but the initial inventory also contains whole ## sections.
func removeSectionBlock(text, block string) string {
	section := firstSectionHeading(block)
	if section == "" {
		return text
	}
	lines := splitLines(text)
	start := -1
	end := len(lines)
	for i, line := range lines {
		heading := sectionHeading(line)
		if heading == "" {
			continue
		}
		if start >= 0 {
			end = i
			break
		}
		if heading == section {
			start = i
		}
	}
	if start < 0 {
		return text
	}
	out := append(lines[:start:start], lines[end:]...)
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// removeExactBlock removes full-line occurrences of an asset body. Some valid
// clause assets are plain prose rather than a bold-label rule or a ## section;
// those shapes must still leave the persisted static carrier when the asset is
// promoted to runtime-only injection.
func removeExactBlock(text, block string) string {
	block = strings.TrimRight(block, "\n")
	if strings.TrimSpace(block) == "" {
		return text
	}
	needle := splitLines(block)
	lines := splitLines(text)
	if len(needle) == 0 || len(needle) > len(lines) {
		return text
	}

	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		if i+len(needle) <= len(lines) {
			matched := true
			for j := range needle {
				if lines[i+j] != needle[j] {
					matched = false
					break
				}
			}
			if matched {
				i += len(needle)
				continue
			}
		}
		out = append(out, lines[i])
		i++
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

func firstSectionHeading(block string) string {
	for _, line := range splitLines(block) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		return sectionHeading(line)
	}
	return ""
}

func sectionHeading(line string) string {
	if !strings.HasPrefix(line, "## ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "## "))
}

// RemoveClauses removes each clause block — matched by its leading bold
// label, the same block semantics re-render uses — from text. It backs
// legacy-migration snapshot verification (ADR 007 §8): the stable half of a
// migrated document must equal the pre-migration text with every migrated
// body removed, byte for byte.
func RemoveClauses(text string, blocks []string) string {
	for _, block := range blocks {
		text = removeClauseBlock(text, block)
	}
	return text
}

var clauseStartRe = regexp.MustCompile(`^(?:[-*]|\d+\.)\s+\*\*([^*\n]+)\*\*[：:]|^\*\*([^*\n]+)\*\*[：:]`)
var headingRe = regexp.MustCompile(`^#{1,6} `)

func isClauseStart(line string) bool { return clauseLabel(line) != "" }

func clauseLabel(line string) string {
	m := clauseStartRe.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	if m[1] != "" {
		return normalize(m[1])
	}
	return normalize(m[2])
}

func firstClauseLabel(block string) string {
	for _, line := range splitLines(block) {
		if l := clauseLabel(line); l != "" {
			return l
		}
	}
	return ""
}

func isHeading(line string) bool { return headingRe.MatchString(line) }

func normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
