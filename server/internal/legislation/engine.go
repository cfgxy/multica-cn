// Package legislation implements the Prompt legislation gate (RUYI-305 E4)
// and the clause-level text surgery its validation needs (sandbox synthesis,
// structure extraction, line diff for the approval preview).
//
// The rule set is a semantic port of the ruyi prompt repository's
// tools/check_prompts.py + prompt-structure.json (规则语义移植，不搬代码):
//
//   - 结构基线: a carrier's `## ` section set and order are frozen by the
//     baseline (prompt_structure_baseline); deleting, renaming, adding or
//     reordering sections is an error. The baseline is rebuilt from the
//     synthesized full text on every enacted, so it can never drift from
//     what was actually legislated.
//   - 大象模式: revision-trace / deprecation wording ("不再", "已废弃",
//     "旧版", "deprecated"…) is an error — documents carry final content
//     only.
//   - 弱表述: discipline-clause hedging ("应该尽量", "原则上", "酌情"…) is
//     an error.
//   - 内联清单: bold-label + long顿号 enumeration is a warning; Markdown
//     tables outside whitelisted sections are an error.
//   - 内容闸五答: the proposal's five structured gate answers must all be
//     present.
//   - remove_clause dangling references: after a clause is removed, any
//     remaining mention of its name in the synthesized text is an error.
//
// Fail-closed is a property of the caller contract: every error path —
// including a panic inside the engine — must block enactment. The handler
// wraps RunGate with a recover that converts panics into gate errors.
package legislation

import (
	"fmt"
	"regexp"
	"strings"
)

// Section headings and clause labels follow the ruyi gate's extraction
// semantics. A clause is a top-level bullet or numbered item whose first
// line carries a bold label (`- **名称**：…`), or a bare bold-label
// paragraph (`**名称**：…`).
var (
	sectionRe = regexp.MustCompile(`(?m)^## (.+)$`)

	clauseLineRe = regexp.MustCompile(`(?m)^(?:[-*]|\d+\.)\s+\*\*([^*\n]+)\*\*[：:]|^\*\*([^*\n]+)\*\*[：:]`)

	headingLineRe = regexp.MustCompile(`^#{1,6} `)

	// 大象模式: revision traces / deprecation residue (ruyi ELEPHANT).
	elephantRe = regexp.MustCompile(`不再|已废弃|已下线|已迁移至|旧版|deprecated|历史上曾|此前为|原为|废弃声明`)

	// 弱表述: hedging banned in discipline clauses (ruyi WEAK).
	weakRe = regexp.MustCompile(`应该尽量|建议避免|原则上|尽量避免|尽量不|可能的话|酌情`)

	// 内联清单: bold label + colon followed by a long顿号 enumeration (the
	// rune-length gate lives in scanLines — short labels like
	// 「专业、正式、准确」must not trip it).
	inlineListRe = regexp.MustCompile(`\*\*[^*\n]{2,20}\*\*[：:][^、\n]{2,}(?:、[^、\n]{2,}){3,}`)
)

// Carrier scopes mirror the prompt_version scope CHECK constraint; the
// business column of each scope is the single read source for the
// carrier's currently effective content.
const (
	ScopeWorkspace = "workspace"
	ScopeProject   = "project"
	ScopeSquad     = "squad"
	ScopeAgent     = "agent"
)

// ValidScope reports whether s is one of the four carriers.
func ValidScope(s string) bool {
	switch s {
	case ScopeWorkspace, ScopeProject, ScopeSquad, ScopeAgent:
		return true
	}
	return false
}

// ProposalInput is the slice of a proposal the engine needs. It is fed from
// prompt_proposal rows and keeps the engine free of any DB or handler
// dependency (unit-testable in isolation).
type ProposalInput struct {
	ChangeKind     string // add_clause | revise_clause | remove_clause
	ClauseName     string
	ClauseText     string
	TargetSection  string
	GateAnswerLayer,
	GateAnswerRetention,
	GateAnswerCost,
	GateAnswerConflict,
	GateAnswerDedup string
}

// Baseline is the approved structure of a carrier: the ordered `## ` section
// list and the registered clause names. nil means "no baseline yet" and
// downgrades the structure check to a warning (first-legislation bootstrap
// happens in the handler before validation, so nil here is defensive).
type Baseline struct {
	Sections []string
	Clauses  []string
}

// Finding is one gate report line: file-relative location plus the message.
type Finding struct {
	Line    int    `json:"line,omitempty"`
	Level   string `json:"level"` // "error" | "warning"
	Message string `json:"message"`
}

// Result is the gate verdict. Errors block enactment (fail-closed); warnings
// report without blocking.
type Result struct {
	Errors   []Finding `json:"errors"`
	Warnings []Finding `json:"warnings"`
}

// OK reports whether enactment may proceed.
func (r Result) OK() bool { return len(r.Errors) == 0 }

// Sections extracts the ordered `## ` section titles of a carrier text.
func Sections(text string) []string {
	out := make([]string, 0, 16)
	for _, m := range sectionRe.FindAllStringSubmatch(text, -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

// normalizeClauseName canonicalizes a clause label for location and dedup:
// whitespace collapsed, comparison case-insensitive for latin labels.
func normalizeClauseName(name string) string {
	return strings.Join(strings.Fields(name), " ")
}

func clauseNamesEqual(a, b string) bool {
	return strings.EqualFold(normalizeClauseName(a), normalizeClauseName(b))
}

// Clauses extracts the clause names in document order using the ruyi clause
// label regex.
func Clauses(text string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 16)
	for _, m := range clauseLineRe.FindAllStringSubmatch(text, -1) {
		name := m[1]
		if name == "" {
			name = m[2]
		}
		name = normalizeClauseName(name)
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		out = append(out, name)
	}
	return out
}

// clauseSpan locates the block of the named clause: the matching line plus
// every following line until the next clause line, any heading, or EOF.
// Returns (-1, -1) when the name is not present.
func clauseSpan(lines []string, name string) (int, int) {
	start := -1
	for i, line := range lines {
		if start >= 0 {
			if isClauseStart(line) || headingLineRe.MatchString(line) {
				return start, i
			}
			continue
		}
		if m := clauseStartName(line); m != "" && clauseNamesEqual(m, name) {
			start = i
		}
	}
	if start >= 0 {
		return start, len(lines)
	}
	return -1, -1
}

func isClauseStart(line string) bool {
	return clauseStartName(line) != ""
}

// clauseStartName returns the clause label a line opens, or "".
func clauseStartName(line string) string {
	m := clauseLineRe.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	if m[1] != "" {
		return m[1]
	}
	return m[2]
}

// sectionSpan returns [start, end) line indexes of the named `## ` section
// body (excluding the heading itself). Missing section → (-1, -1).
func sectionSpan(lines []string, section string) (int, int) {
	start := -1
	for i, line := range lines {
		m := sectionRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if start >= 0 {
			return start, i
		}
		if strings.TrimSpace(m[1]) == strings.TrimSpace(section) {
			start = i + 1
		}
	}
	if start >= 0 {
		return start, len(lines)
	}
	return -1, -1
}

// Synthesize applies the proposal to a copy of the carrier's current
// effective content and returns the sandbox-synthesized full text. It never
// mutates the input. Errors here are gate errors (they block enactment).
func Synthesize(current string, p ProposalInput) (string, error) {
	name := normalizeClauseName(p.ClauseName)
	if name == "" {
		return "", fmt.Errorf("条款名为空")
	}
	switch p.ChangeKind {
	case "remove_clause":
		return synthesizeRemove(current, p, name)
	case "revise_clause":
		return synthesizeRevise(current, p, name)
	case "add_clause":
		return synthesizeAdd(current, p, name)
	default:
		return "", fmt.Errorf("未知变更类型 %q", p.ChangeKind)
	}
}

func synthesizeAdd(current string, p ProposalInput, name string) (string, error) {
	if t := firstClauseName(p.ClauseText); t != "" && !clauseNamesEqual(t, name) {
		return "", fmt.Errorf("条款文本首行标签「%s」与条款名「%s」不一致", t, name)
	}
	for _, existing := range Clauses(current) {
		if clauseNamesEqual(existing, name) {
			return "", fmt.Errorf("条款「%s」已存在，新增请改用修订", name)
		}
	}
	block := strings.TrimRight(p.ClauseText, "\n")
	if strings.TrimSpace(block) == "" {
		return "", fmt.Errorf("新增条款的条款文本为空")
	}
	lines := splitLines(current)
	if p.TargetSection != "" {
		s, e := sectionSpan(lines, p.TargetSection)
		if s < 0 {
			return "", fmt.Errorf("目标章节「%s」在当前内容中不存在（新增章节须 Owner 批复并重建基线）", p.TargetSection)
		}
		out := insertBlock(lines, e, block)
		return strings.Join(out, "\n"), nil
	}
	// No target section: append at the document tail.
	out := append(splitLines(strings.TrimRight(current, "\n")), "", block)
	return strings.Join(out, "\n"), nil
}

func synthesizeRevise(current string, p ProposalInput, name string) (string, error) {
	if t := firstClauseName(p.ClauseText); t != "" && !clauseNamesEqual(t, name) {
		return "", fmt.Errorf("条款文本首行标签「%s」与条款名「%s」不一致", t, name)
	}
	if strings.TrimSpace(p.ClauseText) == "" {
		return "", fmt.Errorf("修订条款的条款文本为空")
	}
	lines := splitLines(current)
	s, e := clauseSpan(lines, name)
	if s < 0 {
		return "", fmt.Errorf("条款「%s」在当前内容中不存在，修订请改用新增", name)
	}
	out := insertBlock(append(lines[:s:s], lines[e:]...), s, strings.TrimRight(p.ClauseText, "\n"))
	return strings.Join(out, "\n"), nil
}

func synthesizeRemove(current string, _ ProposalInput, name string) (string, error) {
	lines := splitLines(current)
	s, e := clauseSpan(lines, name)
	if s < 0 {
		return "", fmt.Errorf("条款「%s」在当前内容中不存在，无法删除", name)
	}
	out := append(lines[:s:s], lines[e:]...)
	return strings.Join(out, "\n"), nil
}

// insertBlock inserts block lines at index i, keeping one blank separator
// line before the block when the previous line is non-blank.
func insertBlock(lines []string, i int, block string) []string {
	blockLines := splitLines(block)
	out := make([]string, 0, len(lines)+len(blockLines)+2)
	out = append(out, lines[:i]...)
	if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
		out = append(out, "")
	}
	out = append(out, blockLines...)
	out = append(out, lines[i:]...)
	return out
}

func splitLines(text string) []string {
	trimmed := strings.TrimSuffix(text, "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// firstClauseName extracts the clause label the provided block text opens
// with, or "" when it opens with none.
func firstClauseName(block string) string {
	for _, line := range splitLines(block) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		return clauseStartName(line)
	}
	return ""
}

// RunGate validates the sandbox-synthesized full text against the rule set.
// current is the carrier's effective content before the change; synthesized
// is Synthesize's output; baseline may be nil (structure check downgrades to
// a warning).
func RunGate(current, synthesized string, baseline *Baseline, p ProposalInput) Result {
	res := Result{Errors: []Finding{}, Warnings: []Finding{}}

	// 结构基线: the synthesized text must preserve the approved section
	// set and order.
	if baseline == nil {
		res.Warnings = append(res.Warnings, Finding{Level: "warning", Message: "无结构基线，跳过结构检查（首次立法将按当前内容建立基线）"})
	} else {
		checkStructure(synthesized, baseline, &res)
	}

	scanLines(synthesized, &res)

	// remove 须同步清除全部指向引用: any surviving mention of the clause
	// name in the synthesized text is an error.
	if p.ChangeKind == "remove_clause" {
		name := normalizeClauseName(p.ClauseName)
		for i, line := range splitLines(synthesized) {
			if strings.Contains(line, name) {
				res.Errors = append(res.Errors, Finding{Line: i + 1, Level: "error",
					Message: fmt.Sprintf("删除后仍存在指向引用：「%s」（删除条款须同步清除全部指向引用）", truncateRunes(line, 40))})
			}
		}
	}

	// 内容闸五答齐全性.
	answers := []struct{ label, value string }{
		{"层次归属理由", p.GateAnswerLayer},
		{"去留判据", p.GateAnswerRetention},
		{"代价声明", p.GateAnswerCost},
		{"同主题冲突裁决", p.GateAnswerConflict},
		{"重复检查结论", p.GateAnswerDedup},
	}
	for _, a := range answers {
		if strings.TrimSpace(a.value) == "" {
			res.Errors = append(res.Errors, Finding{Level: "error", Message: fmt.Sprintf("内容闸五答缺「%s」", a.label)})
		}
	}
	return res
}

func checkStructure(text string, baseline *Baseline, res *Result) {
	got := Sections(text)
	want := baseline.Sections
	gotSet := map[string]bool{}
	for _, s := range got {
		gotSet[s] = true
	}
	wantSet := map[string]bool{}
	for _, s := range want {
		wantSet[s] = true
	}
	removed, added := false, false
	for _, s := range want {
		if !gotSet[s] {
			res.Errors = append(res.Errors, Finding{Level: "error", Message: fmt.Sprintf("基线章节被删除或改名：「%s」（改标题须全仓更新引用并重建基线）", s)})
			removed = true
		}
	}
	for _, s := range got {
		if !wantSet[s] {
			res.Errors = append(res.Errors, Finding{Level: "error", Message: fmt.Sprintf("未经批复新增章节：「%s」（新条款进既有章节；结构变更须 Owner 批复并重建基线）", s)})
			added = true
		}
	}
	if !removed && !added {
		// Same set: order must match the baseline exactly.
		common := make([]string, 0, len(want))
		for _, s := range want {
			if gotSet[s] {
				common = append(common, s)
			}
		}
		gotFiltered := make([]string, 0, len(got))
		for _, s := range got {
			if wantSet[s] {
				gotFiltered = append(gotFiltered, s)
			}
		}
		for i := range common {
			if common[i] != gotFiltered[i] {
				res.Errors = append(res.Errors, Finding{Level: "error", Message: "章节顺序被改变（不破坏章节序）"})
				break
			}
		}
	}
}

// tableOKSection: sections whose title names a UUID mapping table may carry
// Markdown tables (ruyi TABLE_OK_SECTIONS semantics, generalized to any
// section whose title contains "UUID").
func tableOKSection(section string) bool {
	return strings.Contains(section, "UUID")
}

func scanLines(text string, res *Result) {
	section := ""
	for i, line := range splitLines(text) {
		if m := sectionRe.FindStringSubmatch(line); m != nil {
			section = strings.TrimSpace(m[1])
		}
		if strings.HasPrefix(strings.TrimSpace(line), "|") && !tableOKSection(section) {
			res.Errors = append(res.Errors, Finding{Line: i + 1, Level: "error",
				Message: fmt.Sprintf("Markdown 表格（改 bullet 列表）：「%s」", truncateRunes(strings.TrimSpace(line), 40))})
		}
		emit := func(re *regexp.Regexp, level string, label string) {
			for _, m := range re.FindAllString(line, -1) {
				if label == "内联清单" && len([]rune(m)) < 30 {
					continue
				}
				f := Finding{Line: i + 1, Message: fmt.Sprintf("%s：「%s」", label, truncateRunes(m, 40)), Level: level}
				if level == "error" {
					res.Errors = append(res.Errors, f)
				} else {
					res.Warnings = append(res.Warnings, f)
				}
			}
		}
		emit(elephantRe, "error", "大象/修订痕迹")
		emit(weakRe, "error", "弱表述")
		emit(inlineListRe, "warning", "内联清单")
	}
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ExtractStructure reads the baseline shape (sections + clause names +
// content hash input) off a full carrier text. The handler persists it.
func ExtractStructure(text string) (sections, clauses []string) {
	return Sections(text), Clauses(text)
}
