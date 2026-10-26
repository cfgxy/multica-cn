package clausemark

import (
	"strings"
	"testing"
)

const stableFixture = "# 平台协作规范\n\n## 沟通规范\n\n- **行文基调**：专业、正式。\n\n## 成本纪律\n\n- **合并取数**：一条命令不拆多轮。\n"

func clauseA(v int) RenderedClause {
	return RenderedClause{Slug: "cls_aaa00001", Version: v, Body: "- **每日同步**：开工前回报计划与阻塞。"}
}

func clauseB(v int) RenderedClause {
	return RenderedClause{Slug: "cls_bbb00002", Version: v, Body: "## 附则\n\n- **合并取数**：一条命令不拆多轮。"}
}

func TestParseSplitsStableAndSegments(t *testing.T) {
	rendered := Render(stableFixture, []RenderedClause{clauseA(3)})
	doc, err := Parse(rendered)
	if err != nil {
		t.Fatalf("parse rendered doc: %v", err)
	}
	if len(doc.Segments) != 1 {
		t.Fatalf("segments = %d, want 1", len(doc.Segments))
	}
	seg := doc.Segments[0]
	if seg.Slug != "cls_aaa00001" || seg.Rev != 3 {
		t.Fatalf("segment identity = %s rev %d, want cls_aaa00001 rev 3", seg.Slug, seg.Rev)
	}
	if strings.TrimSpace(seg.Body) != strings.TrimSpace(clauseA(3).Body) {
		t.Fatalf("body mismatch:\n%q\n%q", seg.Body, clauseA(3).Body)
	}
	if strings.Contains(doc.Stable, OpenTag("cls_aaa00001", 3)) {
		t.Fatalf("stable text still carries the open tag:\n%q", doc.Stable)
	}
	if !strings.Contains(doc.Stable, "## 沟通规范") {
		t.Fatalf("stable text lost its sections:\n%q", doc.Stable)
	}
}

func TestParseNegativeCases(t *testing.T) {
	open := OpenTag("cls_aaa00001", 1)
	cases := map[string]string{
		"孤立关闭标记":  stableFixture + "<!-- /clause -->\n",
		"缺少关闭标记":  stableFixture + open + "\n- **每日同步**：开工前回报计划与阻塞。\n",
		"段内嵌套打开":  stableFixture + open + "\n" + OpenTag("cls_bbb00002", 1) + "\n<!-- /clause -->\n",
		"空正文段":    stableFixture + open + "\n<!-- /clause -->\n",
		"slug格式错": strings.Replace(stableFixture+open+"\n- **x**：y。\n<!-- /clause -->\n", "cls_aaa00001", "CLAUSE-1", 1),
		"rev非数字":  strings.Replace(stableFixture+open+"\n- **x**：y。\n<!-- /clause -->\n", "rev=1", "rev=x", 1),
		"重复slug": stableFixture +
			open + "\n- **每日同步**：开工前回报。\n<!-- /clause -->\n" +
			open + "\n- **每日同步**：再次出现。\n<!-- /clause -->\n",
	}
	for name, text := range cases {
		if _, err := Parse(text); err == nil {
			t.Fatalf("%s: Parse succeeded, want error", name)
		}
	}
}

func TestReplaceSegmentPreservesInjectionSnapshot(t *testing.T) {
	current := Render(stableFixture, []RenderedClause{clauseA(2), clauseB(4)})
	before, err := Parse(current)
	if err != nil {
		t.Fatalf("parse current carrier: %v", err)
	}
	proposed := "- **每日同步**：修订后的规则。"

	got, err := ReplaceSegment(current, clauseA(2).Slug, 2, proposed)
	if err != nil {
		t.Fatalf("replace clause segment: %v", err)
	}
	doc, err := Parse(got)
	if err != nil {
		t.Fatalf("parse synthesized carrier: %v", err)
	}
	if doc.Stable != before.Stable {
		t.Fatalf("stable text changed: %q, want %q", doc.Stable, before.Stable)
	}
	if len(doc.Segments) != 2 || doc.Segments[0].Slug != clauseA(2).Slug || doc.Segments[0].Rev != 3 || strings.TrimSpace(doc.Segments[0].Body) != proposed {
		t.Fatalf("revised segment = %+v", doc.Segments)
	}
	if doc.Segments[1] != (Segment{Slug: clauseB(4).Slug, Rev: 4, Body: clauseB(4).Body + "\n"}) {
		t.Fatalf("unrelated segment changed: %+v", doc.Segments[1])
	}

	want := Render(stableFixture, []RenderedClause{
		{Slug: clauseA(2).Slug, Version: 3, Body: proposed}, clauseB(4),
	})
	if got != want {
		t.Fatalf("synthesized carrier differs from injection render:\n%s\nwant:\n%s", got, want)
	}
}

func TestReplaceSegmentFailsClosed(t *testing.T) {
	base := Render(stableFixture, []RenderedClause{clauseA(2)})
	duplicate := base + "\n" + Render("", []RenderedClause{clauseA(2)})
	malformed := base + "\n<!-- clause:id=cls_aaa00001 rev= -->\n- Stale body.\n<!-- /clause -->\n"
	cases := []struct {
		name, current string
		version       int32
	}{
		{name: "missing segment", current: stableFixture, version: 2},
		{name: "stale marker", current: base, version: 1},
		{name: "duplicate segment", current: duplicate, version: 2},
		{name: "malformed marker", current: malformed, version: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ReplaceSegment(tc.current, clauseA(2).Slug, tc.version, "- Proposed body."); err == nil {
				t.Fatal("expected fail-closed error")
			}
		})
	}
	if _, err := ReplaceSegment(base, clauseA(2).Slug, 2, " \n"); err == nil {
		t.Fatal("expected empty replacement body to fail closed")
	}
}

func TestParseEmptyDocument(t *testing.T) {
	doc, err := Parse("")
	if err != nil {
		t.Fatalf("parse empty: %v", err)
	}
	if doc.Stable != "" || len(doc.Segments) != 0 {
		t.Fatalf("empty doc parsed to %+v", doc)
	}
}

func TestRenderIsDeterministicAndIdempotent(t *testing.T) {
	clauses := []RenderedClause{clauseA(3), clauseB(1)}
	first := Render(stableFixture, clauses)

	// Determinism: same inputs, byte-identical output.
	if again := Render(stableFixture, clauses); again != first {
		t.Fatalf("render not deterministic:\n--first--\n%s\n--again--\n%s", first, again)
	}

	// Idempotence: re-rendering the rendered document (the re-render entry
	// point receives the previous business column) is byte-identical.
	second := Render(first, clauses)
	if second != first {
		t.Fatalf("render not idempotent:\n--first--\n%s\n--second--\n%s", first, second)
	}

	// Convergence after synthesized damage: an eaten opener must not leak
	// orphan marker lines into the stable text.
	damaged := strings.Replace(first, OpenTag("cls_aaa00001", 3)+"\n", "", 1)
	repaired := Render(damaged, clauses)
	if strings.Count(repaired, OpenTag("cls_aaa00001", 3)) != 1 {
		t.Fatalf("repair lost exactly-one open tag:\n%s", repaired)
	}
	if _, err := Parse(repaired); err != nil {
		t.Fatalf("repaired document does not parse: %v", err)
	}
}

func TestRenderRemovesInlineClauseBlocks(t *testing.T) {
	// The carrier still carries clause A inline (pre-asset legacy shape);
	// rendering must move it out of the stable text into the segment.
	inline := "# 规范\n\n## 纪律\n\n- **每日同步**：开工前回报计划与阻塞。\n- **其他条款**：保持不动。\n"
	out := Render(inline, []RenderedClause{clauseA(1)})
	doc, err := Parse(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Segments) != 1 {
		t.Fatalf("segments = %d, want 1", len(doc.Segments))
	}
	if strings.Count(out, "**每日同步**") != 1 {
		t.Fatalf("inline copy of the clause survived (double injection risk):\n%s", out)
	}
	if !strings.Contains(doc.Stable, "**其他条款**") {
		t.Fatalf("unrelated clause was removed from stable text:\n%s", doc.Stable)
	}
}

func TestStaticRemovesManagedClausesWithoutRenderingMarkers(t *testing.T) {
	inline := "# 规范\n\n## 纪律\n\n- **每日同步**：开工前回报计划与阻塞。\n- **其他条款**：保持不动。\n"
	legacySnapshot := Render(inline, []RenderedClause{clauseA(1)})

	static := Static(legacySnapshot, []RenderedClause{clauseA(1)})
	if strings.Contains(static, "每日同步") || strings.Contains(static, "clause:id=") {
		t.Fatalf("static carrier retained a managed clause or marker:\n%s", static)
	}
	if !strings.Contains(static, "其他条款") {
		t.Fatalf("static carrier lost unrelated content:\n%s", static)
	}
	claimed := Render(static, []RenderedClause{clauseA(1)})
	if strings.Count(claimed, "每日同步") != 1 || !strings.Contains(claimed, OpenTag(clauseA(1).Slug, 1)) {
		t.Fatalf("runtime render did not inject exactly one clause:\n%s", claimed)
	}
}

func TestRenderRemovesSupersededInlineClauseBlocks(t *testing.T) {
	oldBody := "- **每日同步**：开工前回报计划与阻塞。"
	newBody := "- **每日同步**：开工前回报计划、阻塞和预计完成时间。"
	inline := "# 规范\n\n## 纪律\n\n" + oldBody + "\n- **其他条款**：保持不动。\n"

	out := Render(inline, []RenderedClause{{
		Slug:             "cls_aaa00001",
		Version:          2,
		Body:             newBody,
		SupersededBodies: []string{oldBody},
	}})

	if strings.Contains(out, oldBody) || strings.Count(out, "**每日同步**") != 1 {
		t.Fatalf("superseded inline clause remained outside the managed region:\n%s", out)
	}
	if !strings.Contains(out, newBody) || !strings.Contains(out, "**其他条款**") {
		t.Fatalf("render lost the new or unrelated clause:\n%s", out)
	}
}

func TestRenderRemovesLegacySectionBlocks(t *testing.T) {
	legacy := "# 规范\n\n## 交接\n\n- 旧规则。\n\n## 保留章节\n\n- 保留规则。\n"
	section := RenderedClause{Slug: "cls_ccc00003", Version: 2, Body: "## 交接\n\n- 新规则。"}
	out := Render(legacy, []RenderedClause{section})
	if strings.Contains(out, "旧规则") || strings.Count(out, "## 交接") != 1 {
		t.Fatalf("legacy section remained outside the managed region:\n%s", out)
	}
	if !strings.Contains(out, "新规则") || !strings.Contains(out, "## 保留章节") {
		t.Fatalf("render damaged the replacement or adjacent section:\n%s", out)
	}
	if _, err := Parse(out); err != nil {
		t.Fatalf("rendered legacy section does not parse: %v", err)
	}
}

func TestRenderEmptyClauseSetKeepsStable(t *testing.T) {
	out := Render(stableFixture+"\n"+OpenTag("cls_aaa00001", 1)+"\n- **x**：y。\n<!-- /clause -->\n", nil)
	if strings.Contains(out, "clause:") {
		t.Fatalf("empty clause set must remove the segment entirely:\n%s", out)
	}
	if !strings.Contains(out, "## 成本纪律") {
		t.Fatalf("stable text lost:\n%s", out)
	}
	if _, err := Parse(out); err != nil {
		t.Fatalf("stable-only document must parse: %v", err)
	}
}

func TestStripMarkersToleratesOrphans(t *testing.T) {
	text := stableFixture + "<!-- /clause -->\n\n" + OpenTag("cls_aaa00001", 1) + "\n孤儿正文\n"
	out := StripMarkers(text)
	if strings.Contains(out, "clause") && strings.Contains(out, "<!--") {
		t.Fatalf("orphan markers survived:\n%s", out)
	}
	if !strings.Contains(out, "## 沟通规范") {
		t.Fatalf("stable text lost:\n%s", out)
	}
}

func TestNewSlugShapeAndUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		s, err := NewSlug()
		if err != nil {
			t.Fatalf("NewSlug: %v", err)
		}
		if !ValidSlug(s) {
			t.Fatalf("slug %q fails its own contract", s)
		}
		if seen[s] {
			t.Fatalf("slug %q repeated within 100 draws", s)
		}
		seen[s] = true
	}
}

func TestValidSlugRejectsBadShapes(t *testing.T) {
	for _, bad := range []string{"", "cls", "cls_ABCDEFGH", "cls_abc", "cls_abcdefg1!", "cls_abcdef123", "xxxxxxxxx"} {
		if ValidSlug(bad) {
			t.Fatalf("ValidSlug(%q) = true, want false", bad)
		}
	}
}

// ScanMarkers is the reconciliation observer: it reports every open tag in
// free text — including shapes Parse would reject — so drift checks see
// hand-edited carriers as they are, not as the renderer would have them.
func TestScanMarkersObservesWithoutValidating(t *testing.T) {
	text := "开头\n" +
		OpenTag("cls_aaa00001", 3) + "\n正文\n<!-- /clause -->\n" +
		"行内引用 " + OpenTag("cls_bbb00002", 1) + " 不在行首\n" +
		"未闭合 " + OpenTag("cls_ccc00003", 12) + "\n" +
		"坏slug <!-- clause:id=BAD_0001 rev=2 --> 与坏rev " +
		strings.Replace(OpenTag("cls_ddd00004", 1), "rev=1", "rev=x", 1) + "\n"

	got := ScanMarkers(text)
	want := []MarkerRef{
		{Slug: "cls_aaa00001", Rev: 3},
		{Slug: "cls_bbb00002", Rev: 1},
		{Slug: "cls_ccc00003", Rev: 12},
	}
	if len(got) != len(want) {
		t.Fatalf("ScanMarkers = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("marker[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	if out := ScanMarkers(stableFixture); len(out) != 0 {
		t.Fatalf("stable text scanned %+v, want none", out)
	}
}
