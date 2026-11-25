package main

import (
	"bytes"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

type uploadedAttachment struct {
	localPath string
	resp      cli.AttachmentResponse
}

type byteRange struct {
	start int
	stop  int
}

type byteEdit struct {
	start       int
	stop        int
	replacement string
}

type uploadedAttachmentCandidate struct {
	id         string
	relPaths   []string
	basenames  []string
	targetURL  string
}

type uploadedAttachmentMatcher struct {
	candidates []uploadedAttachmentCandidate
}

func durableAttachmentMarkdownURL(resp cli.AttachmentResponse) string {
	if u := strings.TrimSpace(resp.MarkdownURL); u != "" {
		return u
	}
	return "/api/attachments/" + strings.TrimSpace(resp.ID) + "/download"
}

func normalizeSlashRelativePath(raw string) string {
	p := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	p = strings.TrimLeft(p, "/")
	if p == "" {
		return ""
	}
	cleaned := path.Clean(p)
	cleaned = strings.TrimPrefix(cleaned, "./")
	if cleaned == "" || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return ""
	}
	return cleaned
}

func newUploadedAttachmentMatcher(uploaded []uploadedAttachment) uploadedAttachmentMatcher {
	cwd, _ := os.Getwd()
	candidates := make([]uploadedAttachmentCandidate, 0, len(uploaded))
	for _, u := range uploaded {
		id := strings.ToLower(strings.TrimSpace(u.resp.ID))
		if id == "" {
			continue
		}
		var relPaths []string
		var basenames []string
		addRel := func(p string) {
			if norm := normalizeSlashRelativePath(p); norm != "" {
				relPaths = append(relPaths, norm)
			}
		}
		addBase := func(name string) {
			name = strings.TrimSpace(name)
			if name == "" || name == "." || name == ".." {
				return
			}
			basenames = append(basenames, name)
		}

		if u.localPath != "" {
			if !filepath.IsAbs(u.localPath) {
				addRel(u.localPath)
			} else if cwd != "" {
				if rel, err := filepath.Rel(cwd, u.localPath); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					addRel(rel)
				}
			}
			addBase(filepath.Base(u.localPath))
		}
		if u.resp.Filename != "" {
			addBase(path.Base(strings.ReplaceAll(u.resp.Filename, "\\", "/")))
		}

		candidates = append(candidates, uploadedAttachmentCandidate{
			id:        id,
			relPaths:  relPaths,
			basenames: basenames,
			targetURL: durableAttachmentMarkdownURL(u.resp),
		})
	}
	return uploadedAttachmentMatcher{candidates: candidates}
}

func stripQueryAndFragment(raw string) string {
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		return raw[:i]
	}
	return raw
}

func unescapeTargetSafe(raw string) string {
	if decoded, err := url.PathUnescape(raw); err == nil {
		return decoded
	}
	return raw
}

func (m uploadedAttachmentMatcher) resolve(rawDest string) (string, bool) {
	trimmed := strings.TrimSpace(rawDest)
	if trimmed == "" || len(m.candidates) == 0 {
		return "", false
	}

	withoutQF := stripQueryAndFragment(trimmed)
	if withoutQF == "" {
		return "", false
	}

	const attachmentScheme = "attachment://"
	if len(withoutQF) >= len(attachmentScheme) && strings.EqualFold(withoutQF[:len(attachmentScheme)], attachmentScheme) {
		rawTarget := unescapeTargetSafe(withoutQF[len(attachmentScheme):])
		norm := normalizeSlashRelativePath(rawTarget)
		if norm == "" {
			return "", false
		}
		// 1. Exact ID match
		lowerNorm := strings.ToLower(norm)
		for _, c := range m.candidates {
			if c.id == lowerNorm {
				return c.targetURL, true
			}
		}
		// 2. Exact relative path match
		for _, c := range m.candidates {
			for _, rel := range c.relPaths {
				if rel == norm {
					return c.targetURL, true
				}
			}
		}
		// 3. Basename match
		base := path.Base(norm)
		for _, c := range m.candidates {
			for _, b := range c.basenames {
				if b == norm || b == base {
					return c.targetURL, true
				}
			}
		}
		return "", false
	}

	// Relative local path/filename (no scheme, not origin-relative `/...`).
	if strings.HasPrefix(withoutQF, "/") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "?") {
		return "", false
	}
	if parsed, err := url.Parse(withoutQF); err == nil && parsed.Scheme != "" {
		return "", false
	}

	norm := normalizeSlashRelativePath(unescapeTargetSafe(withoutQF))
	if norm == "" {
		return "", false
	}
	for _, c := range m.candidates {
		for _, rel := range c.relPaths {
			if rel == norm {
				return c.targetURL, true
			}
		}
	}
	for _, c := range m.candidates {
		for _, b := range c.basenames {
			if b == norm {
				return c.targetURL, true
			}
		}
	}
	return "", false
}

// rewriteUploadedAttachmentRefs parses body as CommonMark (with GFM) and rewrites
// link, image, autolink, and file-card destinations outside code spans/blocks
// when they reference an attachment uploaded in the same command invocation.
func rewriteUploadedAttachmentRefs(body string, uploaded []uploadedAttachment) string {
	if body == "" || len(uploaded) == 0 {
		return body
	}
	matcher := newUploadedAttachmentMatcher(uploaded)
	if len(matcher.candidates) == 0 {
		return body
	}

	source := []byte(body)
	doc := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(source))

	codeRanges := collectCodeRanges(source, doc)
	var edits []byteEdit
	refTargets := make(map[string]string)

	cursor := 0
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.FencedCodeBlock, *ast.CodeBlock, *ast.CodeSpan, *ast.HTMLBlock:
			return ast.WalkSkipChildren, nil
		case *ast.AutoLink:
			raw := string(node.URL(source))
			if replacement, ok := matcher.resolve(raw); ok {
				if start, stop, found := findAutoLinkSpan(source, raw, cursor, codeRanges); found {
					edits = append(edits, byteEdit{start: start, stop: stop, replacement: replacement})
					cursor = stop
				}
			}
		case *ast.Image:
			raw := string(node.Destination)
			if replacement, ok := matcher.resolve(raw); ok {
				minStart := max(cursor, maxDescendantStop(node))
				if start, stop, found := findInlineDestinationSpan(source, raw, minStart, len(source), codeRanges); found {
					edits = append(edits, byteEdit{start: start, stop: stop, replacement: replacement})
					cursor = stop
				} else {
					refTargets[raw] = replacement
				}
			}
		case *ast.Link:
			raw := string(node.Destination)
			if replacement, ok := matcher.resolve(raw); ok {
				minStart := max(cursor, maxDescendantStop(node))
				if start, stop, found := findInlineDestinationSpan(source, raw, minStart, len(source), codeRanges); found {
					edits = append(edits, byteEdit{start: start, stop: stop, replacement: replacement})
					cursor = stop
				} else {
					refTargets[raw] = replacement
				}
			}
		}
		return ast.WalkContinue, nil
	})

	if len(refTargets) > 0 {
		edits = append(edits, findReferenceDefinitionEdits(source, refTargets, codeRanges)...)
	}

	if len(edits) == 0 {
		return body
	}

	sort.Slice(edits, func(i, j int) bool {
		return edits[i].start < edits[j].start
	})

	var out strings.Builder
	out.Grow(len(body))
	pos := 0
	for _, edit := range edits {
		if edit.start < pos || edit.start > edit.stop || edit.stop > len(source) {
			continue
		}
		out.Write(source[pos:edit.start])
		out.WriteString(edit.replacement)
		pos = edit.stop
	}
	out.Write(source[pos:])
	return out.String()
}

func collectCodeRanges(source []byte, doc ast.Node) []byteRange {
	var ranges []byteRange
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.FencedCodeBlock:
			if lines := node.Lines(); lines != nil && lines.Len() > 0 {
				start := lines.At(0).Start
				// Include the opening fence line itself (e.g. ```md).
				if idx := bytes.LastIndexByte(source[:start], '\n'); idx >= 0 {
					start = idx + 1
				} else {
					start = 0
				}
				stop := lines.At(lines.Len() - 1).Stop
				// Include the closing fence line as well.
				if nl := bytes.IndexByte(source[stop:], '\n'); nl >= 0 {
					nextLineStart := stop + nl + 1
					if nextLineStart < len(source) {
						if closeNL := bytes.IndexByte(source[nextLineStart:], '\n'); closeNL >= 0 {
							stop = nextLineStart + closeNL + 1
						} else {
							stop = len(source)
						}
					}
				}
				ranges = append(ranges, byteRange{start: start, stop: stop})
			}
			return ast.WalkSkipChildren, nil
		case *ast.CodeBlock:
			if lines := node.Lines(); lines != nil && lines.Len() > 0 {
				ranges = append(ranges, byteRange{
					start: lines.At(0).Start,
					stop:  lines.At(lines.Len() - 1).Stop,
				})
			}
			return ast.WalkSkipChildren, nil
		case *ast.CodeSpan:
			first, last := minMaxDescendantText(node)
			if first >= 0 && last >= first {
				ranges = append(ranges, byteRange{start: first, stop: last})
			}
			return ast.WalkSkipChildren, nil
		case *ast.HTMLBlock:
			if lines := node.Lines(); lines != nil && lines.Len() > 0 {
				ranges = append(ranges, byteRange{
					start: lines.At(0).Start,
					stop:  lines.At(lines.Len() - 1).Stop,
				})
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i].start < ranges[j].start
	})
	return ranges
}

func minMaxDescendantText(n ast.Node) (int, int) {
	first := -1
	last := -1
	_ = ast.Walk(n, func(child ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if t, ok := child.(*ast.Text); ok {
			seg := t.Segment
			if first < 0 || seg.Start < first {
				first = seg.Start
			}
			if seg.Stop > last {
				last = seg.Stop
			}
		}
		return ast.WalkContinue, nil
	})
	return first, last
}

func maxDescendantStop(n ast.Node) int {
	_, last := minMaxDescendantText(n)
	if last < 0 {
		return 0
	}
	return last
}

func offsetInRanges(pos int, ranges []byteRange) (int, bool) {
	for _, r := range ranges {
		if pos >= r.start && pos < r.stop {
			return r.stop, true
		}
		if r.start > pos {
			break
		}
	}
	return pos, false
}

func isBackslashEscaped(source []byte, idx int) bool {
	count := 0
	for i := idx - 1; i >= 0 && source[i] == '\\'; i-- {
		count++
	}
	return count%2 == 1
}

func findAutoLinkSpan(source []byte, raw string, minStart int, codeRanges []byteRange) (int, int, bool) {
	target := []byte("<" + raw + ">")
	pos := minStart
	for pos < len(source) {
		if next, inside := offsetInRanges(pos, codeRanges); inside {
			pos = next
			continue
		}
		idx := bytes.Index(source[pos:], target)
		if idx < 0 {
			return 0, 0, false
		}
		abs := pos + idx
		if next, inside := offsetInRanges(abs, codeRanges); inside {
			pos = next
			continue
		}
		return abs + 1, abs + 1 + len(raw), true
	}
	return 0, 0, false
}

func findInlineDestinationSpan(source []byte, expectedDest string, minStart, maxStop int, codeRanges []byteRange) (int, int, bool) {
	if maxStop > len(source) {
		maxStop = len(source)
	}
	for i := minStart; i < maxStop-1; i++ {
		if next, inside := offsetInRanges(i, codeRanges); inside {
			i = next - 1
			continue
		}
		if source[i] != ']' || source[i+1] != '(' || isBackslashEscaped(source, i) {
			continue
		}
		destStart, destEnd, parsedDest, ok := parseCommonMarkInlineDest(source, i+2, maxStop)
		if !ok {
			continue
		}
		if parsedDest == expectedDest || unescapeCommonMark(parsedDest) == expectedDest {
			return destStart, destEnd, true
		}
	}
	return 0, 0, false
}

func parseCommonMarkInlineDest(source []byte, pos, limit int) (int, int, string, bool) {
	for pos < limit && (source[pos] == ' ' || source[pos] == '\t' || source[pos] == '\n' || source[pos] == '\r') {
		pos++
	}
	if pos >= limit {
		return 0, 0, "", false
	}
	if source[pos] == '<' {
		start := pos
		for i := pos + 1; i < limit; i++ {
			if source[i] == '\n' || source[i] == '\r' {
				return 0, 0, "", false
			}
			if source[i] == '>' && !isBackslashEscaped(source, i) {
				return start, i + 1, string(source[start+1 : i]), true
			}
		}
		return 0, 0, "", false
	}

	start := pos
	depth := 0
	for pos < limit {
		ch := source[pos]
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			break
		}
		if ch == '(' && !isBackslashEscaped(source, pos) {
			depth++
		} else if ch == ')' && !isBackslashEscaped(source, pos) {
			if depth == 0 {
				break
			}
			depth--
		}
		pos++
	}
	if pos == start || depth != 0 {
		return 0, 0, "", false
	}
	return start, pos, string(source[start:pos]), true
}

func unescapeCommonMark(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && isASCIIPunctuation(s[i+1]) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isASCIIPunctuation(b byte) bool {
	return (b >= '!' && b <= '/') || (b >= ':' && b <= '@') || (b >= '[' && b <= '`') || (b >= '{' && b <= '~')
}

func findReferenceDefinitionEdits(source []byte, targets map[string]string, codeRanges []byteRange) []byteEdit {
	var edits []byteEdit
	offset := 0
	for offset < len(source) {
		lineEnd := bytes.IndexByte(source[offset:], '\n')
		var nextOffset int
		if lineEnd < 0 {
			lineEnd = len(source)
			nextOffset = len(source)
		} else {
			lineEnd = offset + lineEnd
			nextOffset = lineEnd + 1
		}
		if _, inside := offsetInRanges(offset, codeRanges); inside {
			offset = nextOffset
			continue
		}
		line := source[offset:lineEnd]
		// Match leading up to 3 spaces followed by `[label]: <dest>` or `[label]: dest`.
		i := 0
		for i < len(line) && i < 3 && line[i] == ' ' {
			i++
		}
		if i < len(line) && line[i] == '[' {
			closeIdx := -1
			for j := i + 1; j < len(line); j++ {
				if line[j] == ']' && !isBackslashEscaped(line, j) {
					closeIdx = j
					break
				}
			}
			if closeIdx > i+1 && closeIdx+1 < len(line) && line[closeIdx+1] == ':' {
				destStartRel, destEndRel, parsedDest, ok := parseCommonMarkInlineDest(line, closeIdx+2, len(line))
				if ok {
					if repl, matched := targets[parsedDest]; matched {
						edits = append(edits, byteEdit{
							start:       offset + destStartRel,
							stop:        offset + destEndRel,
							replacement: repl,
						})
					} else if repl, matched := targets[unescapeCommonMark(parsedDest)]; matched {
						edits = append(edits, byteEdit{
							start:       offset + destStartRel,
							stop:        offset + destEndRel,
							replacement: repl,
						})
					}
				}
			}
		}
		offset = nextOffset
	}
	return edits
}
