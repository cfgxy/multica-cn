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
