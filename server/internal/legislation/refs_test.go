// RUYI-869: the reference contract of structured clauses, pinned as pure
// functions. The check universe is the post-enact state of a gate run —
// missing refs, cycles, reverse static refs, and the deterministic
// ordering invariant of the marker region.
package legislation

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/clausemark"
)

func mustSlug(t *testing.T, seed string) string {
	t.Helper()
	// The check matches the cls_ shape; the tests mint nothing, they
	// hand-craft slugs of the documented shape. seed is kept for readability.
	return "cls_" + seed
}

func findingLevels(findings []Finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Level)
	}
	return out
}

func findFinding(t *testing.T, findings []Finding, want string) *Finding {
	t.Helper()
	for i := range findings {
		if strings.Contains(findings[i].Message, want) {
			return &findings[i]
		}
	}
	t.Fatalf("no finding containing %q in %+v", want, findings)
	return nil
}

// 缺失引用: a ref whose slug is not in the post-enact universe.
func TestRefContractMissingRef(t *testing.T) {
	u := &RefUniverse{
		Bodies:  map[string]string{mustSlug(t, "aaaa0001"): "见 " + mustSlug(t, "deadbeef")},
		Binding: map[string]string{mustSlug(t, "aaaa0001"): "workspace\x00ws"},
		Headings: map[string][]string{
			"workspace\x00ws": []string{"沟通规范"},
		},
		Texts: map[string]string{},
	}
	findings := CheckClauseRefs(u)
	f := findFinding(t, findings, "缺失引用")
	if f.Level != "error" {
		t.Fatalf("missing ref must be an error, got %s", f.Level)
	}
}

// 循环引用: two clauses referencing each other close a cycle.
func TestRefContractCycle(t *testing.T) {
	a, b := mustSlug(t, "aaaa0001"), mustSlug(t, "aaaa0002")
	u := &RefUniverse{
		Bodies:  map[string]string{a: "参照 " + b, b: "参照 " + a},
		Binding: map[string]string{a: "workspace\x00ws", b: "workspace\x00ws"},
		Headings: map[string][]string{
			"workspace\x00ws": []string{"沟通规范"},
		},
		Texts: map[string]string{},
	}
	findings := CheckClauseRefs(u)
	f := findFinding(t, findings, "循环引用")
	if f.Level != "error" {
		t.Fatalf("cycle must be an error, got %s", f.Level)
	}
}

// 循环引用, self-loop: a revision that makes a clause refer to itself.
func TestRefContractSelfCycle(t *testing.T) {
	a := mustSlug(t, "aaaa0001")
	u := &RefUniverse{
		Bodies:  map[string]string{},
		Binding: map[string]string{},
		Headings: map[string][]string{
			"workspace\x00ws": []string{"沟通规范"},
		},
		Texts:        map[string]string{},
		CurrentTexts: map[string]string{},
		SelfSlug:     a,
		SelfBody:     "参照 " + a + " 执行",
		SelfCarrier:  "workspace\x00ws",
	}
	findings := CheckClauseRefs(u)
	f := findFinding(t, findings, "循环引用")
	if f.Level != "error" {
		t.Fatalf("self cycle must be an error, got %s", f.Level)
	}
}

// 反向静态引用: a clause body citing its carrier's static section.
func TestRefContractReverseStaticRef(t *testing.T) {
	a := mustSlug(t, "aaaa0001")
	u := &RefUniverse{
		Bodies:  map[string]string{a: "- 汇报遵循 沟通规范 第 2 条"},
		Binding: map[string]string{a: "workspace\x00ws"},
		Headings: map[string][]string{
			"workspace\x00ws": []string{"沟通规范"},
		},
		Texts: map[string]string{},
	}
	findings := CheckClauseRefs(u)
	f := findFinding(t, findings, "反向静态引用")
	if f.Level != "error" {
		t.Fatalf("reverse static ref must be an error, got %s", f.Level)
	}
}

// The owner permits the forward direction: a static context naming clause
// slugs is not a violation — the check universe only constrains clause
// bodies, so a clean body with no refs passes.
func TestRefContractCleanBodyPasses(t *testing.T) {
	a, b := mustSlug(t, "aaaa0001"), mustSlug(t, "aaaa0002")
	u := &RefUniverse{
		Bodies:  map[string]string{a: "- 每日同步：开工前回报当日计划", b: "参照 " + a},
		Binding: map[string]string{a: "workspace\x00ws", b: "workspace\x00ws"},
		Headings: map[string][]string{
			"workspace\x00ws": []string{"沟通规范", "成本纪律"},
		},
		Texts:        map[string]string{},
		CurrentTexts: map[string]string{},
	}
	if findings := CheckClauseRefs(u); len(findings) != 0 {
		t.Fatalf("clean DAG must pass, got %+v", findings)
	}
}

// A removed clause leaving a surviving ref is a 缺失引用: the universe
// models the post-enact state (removed slug absent).
func TestRefContractRemovedRefLeavesDangling(t *testing.T) {
	a, gone := mustSlug(t, "aaaa0001"), mustSlug(t, "aaaa0009")
	u := &RefUniverse{
		Bodies:  map[string]string{a: "见 " + gone},
		Binding: map[string]string{a: "workspace\x00ws"},
		Headings: map[string][]string{
			"workspace\x00ws": []string{"沟通规范"},
		},
		Texts: map[string]string{},
	}
	findings := CheckClauseRefs(u)
	findFinding(t, findings, "缺失引用")
}

// 静态缺失引用: the forward direction (static context naming an active
// clause slug) is the owner's permitted shape, but a static text naming a
// slug that is not in the post-enact universe fails closed.
func TestRefContractStaticMissingRef(t *testing.T) {
	live, gone := mustSlug(t, "aaaa0001"), mustSlug(t, "aaaa00ff")
	u := &RefUniverse{
		Bodies:  map[string]string{},
		Binding: map[string]string{},
		Headings: map[string][]string{
			"workspace\x00ws": {},
		},
		CurrentTexts: map[string]string{},
		Texts: map[string]string{
			"workspace\x00ws": "## 沟通规范\n\n参见 " + live + " 与 " + gone + " 的约定。",
		},
	}
	// Live slug is not in the universe either here — the static text may
	// only reference the post-enact active set.
	findings := CheckClauseRefs(u)
	findFinding(t, findings, "缺失引用")
}

// 静态正向引用: a static context naming a clause that IS in the post-enact
// universe is the owner's permitted forward direction — no finding.
func TestRefContractStaticForwardRefPasses(t *testing.T) {
	live := mustSlug(t, "aaaa0001")
	u := &RefUniverse{
		Bodies:  map[string]string{live: "- 每日同步：开工前回报当日计划"},
		Binding: map[string]string{live: "workspace\x00ws"},
		Headings: map[string][]string{
			"workspace\x00ws": {}, /* no static heading named in the body */
		},
		CurrentTexts: map[string]string{},
		Texts:        map[string]string{"workspace\x00ws": "## 沟通规范\n\n参见 " + live + " 的约定。"},
	}
	if findings := CheckClauseRefs(u); len(findings) != 0 {
		t.Fatalf("static forward ref must pass, got %+v", findings)
	}
}

// 确定性顺序, newly introduced: a synthesized carrier text whose region
// sits out of ascending-slug order is a gate error.
func TestRefContractOrderNewlyIntroduced(t *testing.T) {
	low, high := mustSlug(t, "aaaa0001"), mustSlug(t, "aaaa00ff")
	// high first, low second: out of order.
	outOfOrder := clausemark.OpenTag(high, 1) + "\nhigh body\n" + clausemark.CloseTag() + "\n" +
		clausemark.OpenTag(low, 1) + "\nlow body\n" + clausemark.CloseTag()
	u := &RefUniverse{
		Bodies:       map[string]string{},
		Binding:      map[string]string{},
		Headings:     map[string][]string{},
		CurrentTexts: map[string]string{"workspace\x00ws": "## 沟通规范\n\n- **行文基调**：正式。"},
		Texts:        map[string]string{"workspace\x00ws": "## 沟通规范\n\n" + outOfOrder},
	}
	findings := CheckClauseRefs(u)
	f := findFinding(t, findings, "顺序非升序")
	if f.Level != "error" {
		t.Fatalf("newly introduced disorder must be an error, got %s", f.Level)
	}
}

// 确定性顺序, inherited: the pre-proposal text already disordered downgrades
// to a warning — the re-renderer heals it on the next successful enactment,
// so blocking would deadlock the carrier behind itself.
func TestRefContractOrderInheritedDowngradesToWarning(t *testing.T) {
	low, high := mustSlug(t, "aaaa0001"), mustSlug(t, "aaaa00ff")
	outOfOrder := clausemark.OpenTag(high, 1) + "\nhigh body\n" + clausemark.CloseTag() + "\n" +
		clausemark.OpenTag(low, 1) + "\nlow body\n" + clausemark.CloseTag()
	both := "## 沟通规范\n\n" + outOfOrder
	u := &RefUniverse{
		Bodies:       map[string]string{},
		Binding:      map[string]string{},
		Headings:     map[string][]string{},
		CurrentTexts: map[string]string{"workspace\x00ws": both},
		Texts:        map[string]string{"workspace\x00ws": both},
	}
	findings := CheckClauseRefs(u)
	for _, f := range findings {
		if strings.Contains(f.Message, "顺序非升序") && f.Level != "warning" {
			t.Fatalf("inherited disorder must be a warning, got %s: %s", f.Level, f.Message)
		}
	}
	if len(findings) == 0 {
		t.Fatal("inherited disorder must still be reported (as a warning)")
	}
}

// An ascending region passes the ordering check.
func TestRefContractOrderAscendingPasses(t *testing.T) {
	low, high := mustSlug(t, "aaaa0001"), mustSlug(t, "aaaa00ff")
	asc := clausemark.OpenTag(low, 1) + "\nlow body\n" + clausemark.CloseTag() + "\n" +
		clausemark.OpenTag(high, 1) + "\nhigh body\n" + clausemark.CloseTag()
	u := &RefUniverse{
		Bodies:       map[string]string{},
		Binding:      map[string]string{},
		Headings:     map[string][]string{},
		CurrentTexts: map[string]string{},
		Texts:        map[string]string{"workspace\x00ws": "## 沟通规范\n\n" + asc},
	}
	if findings := CheckClauseRefs(u); len(findings) != 0 {
		t.Fatalf("ascending order must pass, got %+v", findings)
	}
}

// A damaged region (unclosed marker) is an error: the re-renderer cannot
// converge unparseable text into a byte-stable snapshot.
func TestRefContractDamagedRegionFails(t *testing.T) {
	u := &RefUniverse{
		Bodies:       map[string]string{},
		Binding:      map[string]string{},
		Headings:     map[string][]string{},
		CurrentTexts: map[string]string{},
		Texts: map[string]string{
			"workspace\x00ws": "## 沟通规范\n\n" + clausemark.OpenTag(mustSlug(t, "aaaa0001"), 1) + "\nbody",
		},
	}
	findings := CheckClauseRefs(u)
	findFinding(t, findings, "条款区段损坏")
}

// findCycle is deterministic: the same graph yields the same cycle.
func TestFindCycleDeterministic(t *testing.T) {
	adj := map[string][]string{
		"b": {"c"},
		"c": {"a"},
		"a": {"b"},
	}
	one := findCycle([]string{"a", "b", "c"}, adj)
	two := findCycle([]string{"a", "b", "c"}, adj)
	if len(one) == 0 {
		t.Fatal("expected a cycle")
	}
	if strings.Join(one, " ") != strings.Join(two, " ") {
		t.Fatalf("cycle not deterministic: %v vs %v", one, two)
	}
}
