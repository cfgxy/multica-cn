// Package clausemigration carries the legacy-migration tooling for ADR 007
// 拆单 5 (RUYI-764): turning the RUYI-733 audit inventory into clause-asset
// batches. It is deliberately a thin, dependency-light layer over the
// legislation engine and clausemark renderer — every state-changing step
// (draft → enact → rollback) runs through the governed handler paths; this
// package only owns the inventory (manifest), its drift check against live
// carrier text, the enactment proposal bodies, and the byte-level snapshot
// verification.
package clausemigration

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/multica-ai/multica/server/internal/legislation"
	"github.com/multica-ai/multica/server/pkg/clausemark"
)

// GateAnswers carries the five mandatory gate answers for a proposal.
type GateAnswers struct {
	Layer     string `json:"layer"`
	Retention string `json:"retention"`
	Cost      string `json:"cost"`
	Conflict  string `json:"conflict"`
	Dedup     string `json:"dedup"`
}

// Entry is one clause candidate from the audit inventory: a rule block that
// satisfies the ADR §8 migration criteria (independently repealable, block-
// shaped body) and is adopted as a clause asset by a migrate_clause
// proposal. Body is the byte-exact contract — at enact time the gate re-logs
// that the carrier still carries exactly this text (synthesizeMigrate),
// fail-closed against a stale inventory.
type Entry struct {
	ID        string      `json:"id"`         // tool-side stable id, e.g. "cost-no-archaeology"
	Title     string      `json:"title"`      // bold label / clause name
	Scope     string      `json:"scope"`      // carrier scope: workspace | project | squad | agent | autopilot
	Section   string      `json:"section"`    // ## section the block lives in (and restores into on rollback)
	Body      string      `json:"body"`       // byte-exact block text
	Batch     int         `json:"batch"`      // 1-based enactment batch number
	AuditItem string      `json:"audit_item"` // RUYI-733 audit provenance
	Rationale string      `json:"rationale"`  // ADR §8 可迁移判据对照
	Answers   GateAnswers `json:"answers"`
}

// Manifest is the migration inventory: the audit-derived clause candidates,
// grouped into enactment batches. LoadManifest validates the invariants the
// tool and the gate both rely on.
type Manifest struct {
	Source  string  `json:"source"` // audit provenance, e.g. "RUYI-733 审计清单"
	Entries []Entry `json:"entries"`
}

// LoadManifest decodes and validates a manifest. Validation is fail-closed:
// duplicate ids or (scope,title) pairs, an invalid scope, a body whose bold
// label does not match the title, or an empty body/answers are all errors.
func LoadManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("清单解析失败: %w", err)
	}
	if strings.TrimSpace(m.Source) == "" {
		return nil, fmt.Errorf("清单缺少来源（source）")
	}
	if len(m.Entries) == 0 {
		return nil, fmt.Errorf("清单没有任何条目")
	}
	ids := map[string]bool{}
	pairs := map[string]bool{}
	for i, e := range m.Entries {
		if ids[e.ID] {
			return nil, fmt.Errorf("条目 %d：id %q 重复", i, e.ID)
		}
		ids[e.ID] = true
		pair := e.Scope + "/" + e.Title
		if pairs[pair] {
			return nil, fmt.Errorf("条目 %d：同一载体重复条款 %q", i, e.Title)
		}
		pairs[pair] = true
		if !legislation.ValidScope(e.Scope) {
			return nil, fmt.Errorf("条目 %s：非法载体范围 %q", e.ID, e.Scope)
		}
		if e.Batch < 1 {
			return nil, fmt.Errorf("条目 %s：批次号必须从 1 开始", e.ID)
		}
		if strings.TrimSpace(e.Body) == "" {
			return nil, fmt.Errorf("条目 %s：正文为空", e.ID)
		}
		if legislation.FindClauseBlock(e.Body, e.Title) == "" {
			return nil, fmt.Errorf("条目 %s：正文首个标签与条款名 %q 不一致", e.ID, e.Title)
		}
		if e.Answers.Layer == "" || e.Answers.Retention == "" || e.Answers.Cost == "" ||
			e.Answers.Conflict == "" || e.Answers.Dedup == "" {
			return nil, fmt.Errorf("条目 %s：五答不齐全", e.ID)
		}
	}
	return &m, nil
}

// Bodies returns the batch's bodies in manifest order.
func (m *Manifest) Bodies(batch int) []string {
	var out []string
	for _, e := range m.Entries {
		if e.Batch == batch {
			out = append(out, strings.TrimRight(e.Body, "\n"))
		}
	}
	return out
}

// Batch numbers, ascending, deduplicated.
func (m *Manifest) Batches() []int {
	seen := map[int]bool{}
	var out []int
	for _, e := range m.Entries {
		if !seen[e.Batch] {
			seen[e.Batch] = true
			out = append(out, e.Batch)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Drift statuses.
const (
	DriftExact     = "exact"     // carrier block byte-equals the inventory body
	DriftDrifted   = "drifted"   // label present, text differs — inventory is stale
	DriftMissing   = "missing"   // label absent — already migrated or removed
	DriftAmbiguous = "ambiguous" // duplicate labels — first-match semantics unsafe
)

// DriftFinding reports one entry's drift status against live carrier text.
type DriftFinding struct {
	EntryID string `json:"entry_id"`
	Status  string `json:"status"`
	Detail  string `json:"detail,omitempty"`
}

// CheckDrift compares every entry against the current business-column text
// of its carrier (carriers maps scope → text; a scope with several carriers
// is resolved by the caller — the pilot migrates the workspace carrier).
func CheckDrift(m *Manifest, carriers map[string]string) []DriftFinding {
	out := make([]DriftFinding, 0, len(m.Entries))
	for _, e := range m.Entries {
		f := DriftFinding{EntryID: e.ID}
		text, ok := carriers[e.Scope]
		if !ok {
			f.Status = DriftMissing
			f.Detail = "载体文本缺失"
			out = append(out, f)
			continue
		}
		block := legislation.FindClauseBlock(text, e.Title)
		if block == "" {
			f.Status = DriftMissing
			f.Detail = "载体中不存在该标签的块"
			out = append(out, f)
			continue
		}
		if block != strings.TrimRight(e.Body, "\n") {
			f.Status = DriftDrifted
			f.Detail = "现行正文与清单正文逐字节不一致（清单过期，先更新清单）"
			out = append(out, f)
			continue
		}
		// First match matched, but a second block with the same label makes
		// both migration and rollback ambiguous — refuse.
		if legislation.CountClauseBlocks(text, e.Title) > 1 {
			f.Status = DriftAmbiguous
			f.Detail = "存在重复标签块，先消除重复再迁移"
			out = append(out, f)
			continue
		}
		f.Status = DriftExact
		out = append(out, f)
	}
	return out
}

// DriftClean reports whether every finding is exact.
func DriftClean(findings []DriftFinding) bool {
	for _, f := range findings {
		if f.Status != DriftExact {
			return false
		}
	}
	return len(findings) > 0
}

// VerifySnapshot is the byte-level acceptance check for one enacted batch:
// the post-enactment carrier must parse cleanly, its stable context must
// byte-equal the pre-enactment text with every migrated body removed, and
// its clause segments must byte-equal the migrated bodies as a set. The
// render orders segments by clause slug (stable within one asset set, but
// unrelated to manifest batch order), so the segment check is order-free by
// design — bodies are compared sorted, byte for byte. (Parse keeps the
// render's trailing newline inside a segment body; both sides are
// newline-normalized for the comparison.)
func VerifySnapshot(pre, post string, bodies []string) error {
	doc, err := clausemark.Parse(post)
	if err != nil {
		return fmt.Errorf("迁移后载体解析失败: %w", err)
	}
	expected := clausemark.RemoveClauses(pre, bodies)
	if doc.Stable != expected {
		return fmt.Errorf("稳定上下文逐字节不一致（除条款段外必须与迁移前一致）：%s", firstDiff(doc.Stable, expected))
	}
	if len(doc.Segments) != len(bodies) {
		return fmt.Errorf("条款段数量不一致：载体 %d 段，清单 %d 条", len(doc.Segments), len(bodies))
	}
	got := make([]string, len(doc.Segments))
	for i, seg := range doc.Segments {
		got[i] = strings.TrimRight(seg.Body, "\n")
	}
	want := make([]string, len(bodies))
	for i, b := range bodies {
		want[i] = strings.TrimRight(b, "\n")
	}
	sort.Strings(got)
	sort.Strings(want)
	for i := range got {
		if got[i] != want[i] {
			return fmt.Errorf("条款段正文与清单逐字节不一致：%s", firstDiff(got[i], want[i]))
		}
	}
	return nil
}

// firstDiff locates the first differing byte for error messages.
func firstDiff(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return fmt.Sprintf("首个差异在字节 %d：%q vs %q", i, snippet(a[i:]), snippet(b[i:]))
		}
	}
	if len(a) != len(b) {
		return fmt.Sprintf("长度不同（%d vs %d 字节），公共前缀后 %q vs %q", len(a), len(b), snippet(a[min(n, len(a)-1):]), snippet(b[min(n, len(b)-1):]))
	}
	return "内容一致"
}

func snippet(s string) string {
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// ProposalBody builds the CreatePromptProposal request body for one entry:
// a migrate_clause draft whose clause_text is the inventory body. The gate
// re-verifies body == carrier text at enact time.
func ProposalBody(e Entry, scopeID string) map[string]any {
	return map[string]any{
		"carrier_scope":         e.Scope,
		"carrier_scope_id":      scopeID,
		"change_kind":           "migrate_clause",
		"clause_name":           e.Title,
		"clause_text":           e.Body,
		"gate_answer_layer":     e.Answers.Layer,
		"gate_answer_retention": e.Answers.Retention,
		"gate_answer_cost":      e.Answers.Cost,
		"gate_answer_conflict":  e.Answers.Conflict,
		"gate_answer_dedup":     e.Answers.Dedup,
		"evidence_anchors": []map[string]string{
			{"issue_id": "RUYI-733", "ref": e.AuditItem},
		},
	}
}
