package legislation

import (
	"strings"
	"testing"
)

// The carrier fixture mirrors a prompt governance document: `## ` sections,
// bold-label clauses. The tests below are the acceptance bar for E4 (RUYI-305
// 验收标准③): every gate rule has a fail-first-then-pass pair, and the gate
// is fail-closed — every anomaly lands in Errors, never silently in OK().
const carrierText = `# 平台协作规范

## 沟通规范

- **行文基调**：专业、正式、准确、简明扼要。
- **先结论后依据**：第一句给出结论，依据随后。

## 成本纪律

- **合并取数**：能一条命令取得的情报不拆多轮。
- **禁止叙述轮**：工具编排期间不带工具调用的纯文字轮禁止出现。

## 成员 UUID 索引

| 成员 | UUID |
| --- | --- |
| 示例 | 00000000-0000-0000-0000-000000000000 |
`

func baseInput() ProposalInput {
	return ProposalInput{
		ChangeKind:         "add_clause",
		ClauseName:         "证据先行",
		ClauseText:         "- **证据先行**：关键结论必须附带可复核证据。",
		TargetSection:      "沟通规范",
		GateAnswerLayer:    "workspace",
		GateAnswerRetention: "每轮输出都适用的纪律，留底座。",
		GateAnswerCost:     "常驻新增约 40 字符。",
		GateAnswerConflict: "与既有条款无冲突。",
		GateAnswerDedup:    "已 grep 现行条款，无重复。",
	}
}

func okResult(t *testing.T, res Result) {
	t.Helper()
	if !res.OK() {
		t.Fatalf("gate blocked unexpectedly: errors=%+v warnings=%+v", res.Errors, res.Warnings)
	}
}

func findLevel(t *testing.T, res Result, level, substr string) Finding {
	t.Helper()
	pool := append(append([]Finding{}, res.Errors...), res.Warnings...)
	for _, f := range pool {
		if f.Level == level && strings.Contains(f.Message, substr) {
			return f
		}
	}
	t.Fatalf("no %s finding containing %q in %+v / %+v", level, substr, res.Errors, res.Warnings)
	return Finding{}
}

// --- Synthesize: happy paths ---

func TestSynthesizeAddIntoSection(t *testing.T) {
	out, err := Synthesize(carrierText, baseInput())
	if err != nil {
		t.Fatalf("add into existing section failed: %v", err)
	}
	if !strings.Contains(out, "**证据先行**") {
		t.Fatal("clause block missing from synthesized text")
	}
	s, e := sectionSpan(splitLines(out), "沟通规范")
	if s < 0 {
		t.Fatal("target section lost")
	}
	body := strings.Join(splitLines(out)[s:e], "\n")
	if !strings.Contains(body, "证据先行") {
		t.Fatal("clause not inserted into target section")
	}
	if strings.Index(out, "**先结论后依据**") > strings.Index(out, "**证据先行**") {
		t.Fatal("clause inserted before existing clauses instead of after")
	}
}

func TestSynthesizeReviseAndRemove(t *testing.T) {
	rev := baseInput()
	rev.ChangeKind = "revise_clause"
	rev.ClauseName = "行文基调"
	rev.ClauseText = "- **行文基调**：一律专业、正式、准确。"
	out, err := Synthesize(carrierText, rev)
	if err != nil {
		t.Fatalf("revise failed: %v", err)
	}
	if strings.Contains(out, "简明扼要") {
		t.Fatal("old clause body survived revise")
	}

	rem := baseInput()
	rem.ChangeKind = "remove_clause"
	rem.ClauseName = "禁止叙述轮"
	out2, err := Synthesize(carrierText, rem)
	if err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	if strings.Contains(out2, "禁止叙述轮") {
		t.Fatal("removed clause survived")
	}
}

// --- Synthesize: structural failures (each blocks the gate downstream) ---

func TestSynthesizeErrors(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*ProposalInput)
		want string
	}{
		{"duplicate clause on add", func(p *ProposalInput) {
			p.ClauseName = "行文基调"
			p.ClauseText = "- **行文基调**：重复新增。"
		}, "已存在"},
		{"missing target section", func(p *ProposalInput) {
			p.TargetSection = "不存在的章节"
		}, "不存在"},
		{"label mismatch", func(p *ProposalInput) {
			p.ClauseText = "- **另一个名字**：首行标签与条款名不一致。"
		}, "不一致"},
		{"empty clause text", func(p *ProposalInput) {
			p.ClauseText = "   "
		}, "为空"},
		{"revise missing clause", func(p *ProposalInput) {
			p.ChangeKind = "revise_clause"
			p.ClauseName = "不存在的条款"
		}, "不存在"},
		{"remove missing clause", func(p *ProposalInput) {
			p.ChangeKind = "remove_clause"
			p.ClauseName = "不存在的条款"
		}, "无法删除"},
		{"unknown change kind", func(p *ProposalInput) {
			p.ChangeKind = "rewrite_all"
		}, "未知变更类型"},
	}
	for _, tc := range cases {
		p := baseInput()
		tc.mut(&p)
		if _, err := Synthesize(carrierText, p); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got err=%v, want substring %q", tc.name, err, tc.want)
		}
	}
}

// --- Gate rules: fail-first-then-pass pairs ---

func TestGateAddCleanProposalPasses(t *testing.T) {
	synth, err := Synthesize(carrierText, baseInput())
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	baseline := &Baseline{Sections: Sections(carrierText), Clauses: Clauses(carrierText)}
	okResult(t, RunGate(carrierText, synth, baseline, baseInput()))
}

func TestGateStructureBaseline(t *testing.T) {
	baseline := &Baseline{Sections: Sections(carrierText), Clauses: Clauses(carrierText)}

	// Fail: adding a new ## section is an unauthorized structure change.
	p := baseInput()
	p.ClauseText = "- **证据先行**：关键结论必须附带可复核证据。\n\n## 全新章节\n\n- **新章条款**：内容。"
	synth, err := Synthesize(carrierText, p)
	// The synthesize add path refuses unknown target sections, but a clause
	// block embedding a heading still must be caught at gate level.
	if err == nil {
		res := RunGate(carrierText, synth, baseline, p)
		findLevel(t, res, "error", "未经批复新增章节")
	}

	// Fail: baseline section renamed away.
	tampered := strings.Replace(carrierText, "## 沟通规范", "## 交流规范", 1)
	res := RunGate(carrierText, tampered, baseline, baseInput())
	findLevel(t, res, "error", "被删除或改名")

	// Fail: section reordered (成员索引 moved ahead of 沟通规范).
	reordered := `# 平台协作规范

## 成员 UUID 索引

| 成员 | UUID |
| --- | --- |
| 示例 | 00000000-0000-0000-0000-000000000000 |

## 沟通规范

- **行文基调**：专业、正式、准确、简明扼要。
- **先结论后依据**：第一句给出结论，依据随后。

## 成本纪律

- **合并取数**：能一条命令取得的情报不拆多轮。
- **禁止叙述轮**：工具编排期间不带工具调用的纯文字轮禁止出现。
`
	res = RunGate(carrierText, reordered, baseline, baseInput())
	findLevel(t, res, "error", "顺序被改变")

	// Pass: identical text.
	okResult(t, RunGate(carrierText, carrierText, baseline, baseInput()))
}

func TestGateNilBaselineDowngradesToWarning(t *testing.T) {
	synth, err := Synthesize(carrierText, baseInput())
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	res := RunGate(carrierText, synth, nil, baseInput())
	okResult(t, res)
	findLevel(t, res, "warning", "无结构基线")
}

func TestGateElephantPatterns(t *testing.T) {
	baseline := &Baseline{Sections: Sections(carrierText), Clauses: Clauses(carrierText)}
	patterns := []string{"不再", "已废弃", "已下线", "已迁移至", "旧版", "deprecated", "历史上曾", "此前为", "原为", "废弃声明"}
	for _, w := range patterns {
		p := baseInput()
		p.ClauseText = "- **证据先行**：该条目" + w + "由其他条款承担。"
		synth, err := Synthesize(carrierText, p)
		if err != nil {
			t.Fatalf("pattern %q: synthesize: %v", w, err)
		}
		res := RunGate(carrierText, synth, baseline, p)
		findLevel(t, res, "error", "大象")
	}

	// Pass: clean text produces no elephant findings.
	res := RunGate(carrierText, carrierText, baseline, baseInput())
	for _, f := range res.Errors {
		if strings.Contains(f.Message, "大象") {
			t.Fatalf("clean baseline text flagged as elephant: %+v", res.Errors)
		}
	}
}

func TestGateWeakWording(t *testing.T) {
	baseline := &Baseline{Sections: Sections(carrierText), Clauses: Clauses(carrierText)}
	patterns := []string{"应该尽量", "建议避免", "原则上", "尽量避免", "尽量不", "可能的话", "酌情"}
	for _, w := range patterns {
		p := baseInput()
		p.ClauseText = "- **证据先行**：" + w + "补充证据。"
		synth, err := Synthesize(carrierText, p)
		if err != nil {
			t.Fatalf("pattern %q: synthesize: %v", w, err)
		}
		res := RunGate(carrierText, synth, baseline, p)
		findLevel(t, res, "error", "弱表述")
	}

	res := RunGate(carrierText, carrierText, baseline, baseInput())
	for _, f := range res.Errors {
		if strings.Contains(f.Message, "弱表述") {
			t.Fatalf("clean baseline text flagged as weak: %+v", res.Errors)
		}
	}
}

func TestGateInlineListWarning(t *testing.T) {
	baseline := &Baseline{Sections: Sections(carrierText), Clauses: Clauses(carrierText)}
	p := baseInput()
	p.ClauseText = "- **证据先行**：覆盖范围、证据强度、复核流程、归档要求四项须同时满足方可通过验证并进入下一环节。"
	synth, err := Synthesize(carrierText, p)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	res := RunGate(carrierText, synth, baseline, p)
	f := findLevel(t, res, "warning", "内联清单")
	if f.Line == 0 {
		t.Fatal("inline-list warning carries no line number")
	}
	// Warnings never block.
	okResult(t, res)
}

func TestGateTableBan(t *testing.T) {
	baseline := &Baseline{Sections: Sections(carrierText), Clauses: Clauses(carrierText)}

	// Fail: a table row smuggled into a non-whitelisted section via revise.
	p := baseInput()
	p.ChangeKind = "revise_clause"
	p.ClauseName = "行文基调"
	p.ClauseText = "| 违规 | 表格 |\n| --- | --- |\n| a | b |"
	synth, err := Synthesize(carrierText, p)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	res := RunGate(carrierText, synth, baseline, p)
	findLevel(t, res, "error", "Markdown 表格")

	// Pass: the whitelisted UUID section keeps its table.
	res = RunGate(carrierText, carrierText, baseline, baseInput())
	for _, f := range res.Errors {
		if strings.Contains(f.Message, "Markdown 表格") {
			t.Fatalf("UUID section table flagged: %+v", res.Errors)
		}
	}
}

func TestGateRemoveDanglingReference(t *testing.T) {
	baseline := &Baseline{Sections: Sections(carrierText), Clauses: Clauses(carrierText)}

	// The carrier mentions 叙述轮 inside 合并取数's body? It does not — build
	// one that does, so removal leaves a dangling reference behind.
	withRef := strings.Replace(carrierText,
		"- **合并取数**：能一条命令取得的情报不拆多轮。",
		"- **合并取数**：能一条命令取得的情报不拆多轮，参照禁止叙述轮条款执行。", 1)

	p := baseInput()
	p.ChangeKind = "remove_clause"
	p.ClauseName = "禁止叙述轮"
	synth, err := Synthesize(withRef, p)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	res := RunGate(withRef, synth, baseline, p)
	findLevel(t, res, "error", "指向引用")

	// Pass: once the clause is actually removed, the original carrier has no
	// other mention left.
	clean, err := Synthesize(carrierText, p)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	res = RunGate(carrierText, clean, baseline, p)
	for _, f := range res.Errors {
		if strings.Contains(f.Message, "指向引用") {
			t.Fatalf("clean removal flagged as dangling: %+v", res.Errors)
		}
	}
}

func TestGateFiveAnswersRequired(t *testing.T) {
	baseline := &Baseline{Sections: Sections(carrierText), Clauses: Clauses(carrierText)}
	fields := []string{"GateAnswerLayer", "GateAnswerRetention", "GateAnswerCost", "GateAnswerConflict", "GateAnswerDedup"}
	for _, field := range fields {
		p := baseInput()
		switch field {
		case "GateAnswerLayer":
			p.GateAnswerLayer = ""
		case "GateAnswerRetention":
			p.GateAnswerRetention = ""
		case "GateAnswerCost":
			p.GateAnswerCost = ""
		case "GateAnswerConflict":
			p.GateAnswerConflict = ""
		case "GateAnswerDedup":
			p.GateAnswerDedup = ""
		}
		synth, err := Synthesize(carrierText, p)
		if err != nil {
			t.Fatalf("%s: synthesize: %v", field, err)
		}
		res := RunGate(carrierText, synth, baseline, p)
		if res.OK() {
			t.Fatalf("%s empty: gate passed although a gate answer is missing", field)
		}
		findLevel(t, res, "error", "内容闸五答")
	}
}

// --- Fail-closed ---

// TestGateFailClosedOnEveryAnomaly pins the direction of every rule: each
// anomaly class must land in Errors (blocking), and a synthesized text
// derived from a proposal whose gate fails can never be OK. If any of these
// regresses to a warning or to silence, an invalid prompt text can be
// enacted.
func TestGateFailClosedOnEveryAnomaly(t *testing.T) {
	baseline := &Baseline{Sections: Sections(carrierText), Clauses: Clauses(carrierText)}
	anomalies := []struct {
		name string
		mut  func(*ProposalInput)
	}{
		{"elephant", func(p *ProposalInput) {
			p.ClauseText = "- **证据先行**：旧版做法不再采用。"
		}},
		{"weak", func(p *ProposalInput) {
			p.ClauseText = "- **证据先行**：原则上应附证据。"
		}},
		{"table", func(p *ProposalInput) {
			p.ClauseText = "| x | y |\n| --- | --- |\n| 1 | 2 |"
		}},
	}
	for _, a := range anomalies {
		p := baseInput()
		a.mut(&p)
		synth, err := Synthesize(carrierText, p)
		if err != nil {
			// Synthesis refusal is itself fail-closed: nothing reaches the
			// carrier.
			continue
		}
		res := RunGate(carrierText, synth, baseline, p)
		if res.OK() {
			t.Errorf("%s: gate OK on anomalous proposal — fail-closed broken", a.name)
		}
	}
}

// --- Extraction & diff helpers ---

func TestExtractStructure(t *testing.T) {
	sections, clauses := ExtractStructure(carrierText)
	wantSections := []string{"沟通规范", "成本纪律", "成员 UUID 索引"}
	if len(sections) != len(wantSections) {
		t.Fatalf("sections = %v, want %v", sections, wantSections)
	}
	for i := range wantSections {
		if sections[i] != wantSections[i] {
			t.Fatalf("sections = %v, want %v", sections, wantSections)
		}
	}
	found := map[string]bool{}
	for _, c := range clauses {
		found[c] = true
	}
	for _, want := range []string{"行文基调", "先结论后依据", "合并取数", "禁止叙述轮"} {
		if !found[want] {
			t.Errorf("clause %q missing from extraction: %v", want, clauses)
		}
	}
}

func TestDiffLines(t *testing.T) {
	d := DiffLines("a\nb\nc\n", "a\nX\nc\n")
	kinds := ""
	for _, l := range d {
		kinds += l.Kind[0:1]
	}
	// a, X, b, c → context, add, del, context (or context, del, add, context —
	// the tie-break prefers deletes first).
	if kinds != "cadc" && kinds != "cdac" {
		t.Fatalf("unexpected diff kinds %q: %+v", kinds, d)
	}
	hasAdd, hasDel := false, false
	for _, l := range d {
		if l.Kind == "add" && l.Text == "X" {
			hasAdd = true
		}
		if l.Kind == "del" && l.Text == "b" {
			hasDel = true
		}
	}
	if !hasAdd || !hasDel {
		t.Fatalf("diff lost the change: %+v", d)
	}

	big := strings.Repeat("x\n", maxDiffLines+5)
	d2 := DiffLines(big, big+"y\n")
	if len(d2) < maxDiffLines {
		t.Fatal("oversized input degraded path lost lines")
	}
}
