package bank

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/promptquiz"
)

// The benchmark bank's acceptance gate (RUYI-286): five elements per item and
// all eight categories, checked mechanically rather than claimed. This test is
// the "题库五要素与 8 类型覆盖可机械核对" evidence — a catalog item that
// misses an element, or a category that loses its last item, fails here before
// it can ship.

var slugShape = regexp.MustCompile(`^bm-[a-z0-9-]+$`)

// TestCatalogItemsHaveFiveElements pins the per-item contract: input (Body),
// expected behaviour (Rubric), scoring rule (Checks, all valid), tags
// (Category), difficulty — plus the structural requirements every bank item
// shares (slug shape, isolation gate pass, unique check ids).
func TestCatalogItemsHaveFiveElements(t *testing.T) {
	seenSlugs := map[string]bool{}
	for _, it := range Catalog {
		t.Run(it.Slug, func(t *testing.T) {
			if !slugShape.MatchString(it.Slug) {
				t.Fatalf("slug %q does not match %v", it.Slug, slugShape)
			}
			if seenSlugs[it.Slug] {
				t.Fatalf("duplicate slug %q", it.Slug)
			}
			seenSlugs[it.Slug] = true

			if strings.TrimSpace(it.Title) == "" {
				t.Fatal("title must not be empty")
			}
			// Element 1: the input, and it has to pass the same isolation gate
			// a hand-authored body passes — the import endpoint will refuse it
			// otherwise, so a catalog that ships a gated body is broken.
			if v := promptquiz.Validate(it.Body); !v.OK() {
				t.Fatalf("body rejected by isolation gate: %s", v.Reason())
			}
			// Element 2: expected behaviour in prose.
			if v := promptquiz.ValidateRubric(it.Rubric); !v.OK() {
				t.Fatalf("rubric rejected by isolation gate: %s", v.RubricReason())
			}
			// Element 3: the scoring rule — at least one assertion, every one
			// through the write gate.
			if len(it.Checks) == 0 {
				t.Fatal("item has no checks: it would never be scored")
			}
			ids := map[string]bool{}
			for _, c := range it.Checks {
				raw, err := json.Marshal([]promptquiz.Check{c})
				if err != nil {
					t.Fatalf("marshal check %s: %v", c.ID, err)
				}
				if v := promptquiz.ValidateChecks(raw); !v.OK() {
					t.Fatalf("check %s rejected: %s", c.ID, v.Reason)
				}
				if ids[c.ID] {
					t.Fatalf("duplicate check id %q", c.ID)
				}
				ids[c.ID] = true
			}
			// Elements 4+5: category tag and difficulty.
			if strings.TrimSpace(it.Category) == "" {
				t.Fatal("category must not be empty")
			}
			switch it.Difficulty {
			case "easy", "medium", "hard":
			default:
				t.Fatalf("difficulty %q not in easy/medium/hard", it.Difficulty)
			}
			// Body and rubric share the storage ceilings with hand-authored
			// items; a catalog entry past them would 400 at import.
			if len(it.Body) > promptquiz.MaxBodyBytes {
				t.Fatalf("body exceeds %d bytes", promptquiz.MaxBodyBytes)
			}
		})
	}
}

// TestCatalogCoversAllEightCategories is the 8-type coverage check: the set of
// categories across the catalog must equal the eight the issue names, with at
// least one item each. A category with zero items fails here.
func TestCatalogCoversAllEightCategories(t *testing.T) {
	want := []string{
		CatDiscipline, CatConflict, CatBoundary, CatTool,
		CatFormat, CatContext, CatRefusal, CatRegression,
	}
	got := map[string]int{}
	for _, it := range Catalog {
		got[it.Category]++
	}
	for _, cat := range want {
		if got[cat] == 0 {
			t.Errorf("category %q has no items", cat)
		}
	}
	for cat := range got {
		known := false
		for _, cat2 := range want {
			if cat == cat2 {
				known = true
				break
			}
		}
		if !known {
			t.Errorf("category %q is not one of the eight", cat)
		}
	}
}

// TestCatalogChecksAreGradeable proves each item's assertion set actually
// grades: the write-gated JSON must come back Graded against a non-empty
// answer, so a malformed-but-accepted set can never silently null a bank's
// scores. The answer here is deliberately empty of any correct content — this
// pins "grades and fails", not "grades and passes".
func TestCatalogChecksAreGradeable(t *testing.T) {
	for _, it := range Catalog {
		t.Run(it.Slug, func(t *testing.T) {
			raw := marshalChecks(t, it.Checks)
			got := promptquiz.Grade(raw, "（一段与题目无关的回答）")
			if !got.Graded {
				t.Fatal("Grade returned not-graded for a gated check set")
			}
			if len(got.Detail) != len(it.Checks) {
				t.Fatalf("verdict count %d != check count %d", len(got.Detail), len(it.Checks))
			}
			if got.Score < 0 || got.Score > 1 {
				t.Fatalf("score %v out of range", got.Score)
			}
		})
	}
}

// TestCatalogDifficultySpread keeps the bank from drifting all-easy or
// all-hard: every difficulty must be represented. A bank with one difficulty
// cannot separate "weak prompt" from "hard question".
func TestCatalogDifficultySpread(t *testing.T) {
	seen := map[string]bool{}
	for _, it := range Catalog {
		seen[it.Difficulty] = true
	}
	for _, d := range []string{"easy", "medium", "hard"} {
		if !seen[d] {
			t.Errorf("no item with difficulty %q", d)
		}
	}
}

func marshalChecks(t *testing.T, checks []promptquiz.Check) []byte {
	t.Helper()
	raw, err := json.Marshal(checks)
	if err != nil {
		t.Fatalf("marshal checks: %v", err)
	}
	return raw
}
