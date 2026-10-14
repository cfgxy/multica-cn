package clausemigration

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/clausemark"
)

// carrierFixture mirrors a governed workspace carrier (same shape as the
// legislation engine fixture).
const carrierFixture = `# 平台协作规范

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

// Entries mirror the carrierFixture lines byte for byte (the pilot manifest
// in the drill test does the same against its corpus).
var archaeology = Entry{
	ID:      "cost-merge-fetch",
	Title:   "合并取数",
	Scope:   "workspace",
	Section: "成本纪律",
	Body:    "- **合并取数**：能一条命令取得的情报不拆多轮。",
	Batch:   1,
	Answers: GateAnswers{Layer: "workspace=协作纪律", Retention: "每轮输出适用", Cost: "约 40 字符", Conflict: "无冲突", Dedup: "无重复"},
}

var metaWork = Entry{
	ID:      "cost-no-narration",
	Title:   "禁止叙述轮",
	Scope:   "workspace",
	Section: "成本纪律",
	Body:    "- **禁止叙述轮**：工具编排期间不带工具调用的纯文字轮禁止出现。",
	Batch:   2,
	Answers: GateAnswers{Layer: "workspace=协作纪律", Retention: "每轮输出适用", Cost: "约 30 字符", Conflict: "无冲突", Dedup: "无重复"},
}

func manifestFixture(entries ...Entry) *Manifest {
	return &Manifest{Source: "RUYI-733 审计清单", Entries: entries}
}

func TestLoadManifestValidation(t *testing.T) {
	good := manifestFixture(archaeology, metaWork)
	if len(good.Entries) != 2 {
		t.Fatal("fixture broken")
	}

	dup := manifestFixture(archaeology, archaeology)
	if _, err := loadForTest(dup); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("expected duplicate error, got %v", err)
	}

	badLabel := archaeology
	badLabel.Body = "- **别的标签**：正文。"
	if _, err := loadForTest(manifestFixture(badLabel)); err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("expected label mismatch error, got %v", err)
	}

	noAnswers := archaeology
	noAnswers.Answers.Cost = ""
	if _, err := loadForTest(manifestFixture(noAnswers)); err == nil || !strings.Contains(err.Error(), "五答") {
		t.Fatalf("expected gate answers error, got %v", err)
	}
}

func TestBatchesAndBodies(t *testing.T) {
	m := manifestFixture(metaWork, archaeology) // out of order on purpose
	if got := m.Batches(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("batches: %v", got)
	}
	b1 := m.Bodies(1)
	if len(b1) != 1 || b1[0] != archaeology.Body {
		t.Fatalf("batch 1 bodies: %v", b1)
	}
}

func TestCheckDriftStatuses(t *testing.T) {
	m := manifestFixture(archaeology, metaWork)

	// exact for both
	f := CheckDrift(m, map[string]string{"workspace": carrierFixture})
	if !DriftClean(f) {
		t.Fatalf("expected clean drift, got %+v", f)
	}

	// drifted: inventory stale
	driftedCarrier := strings.Replace(carrierFixture,
		"- **合并取数**：能一条命令取得的情报不拆多轮。",
		"- **合并取数**：现行正文已经被人改过了。", 1)
	f = CheckDrift(m, map[string]string{"workspace": driftedCarrier})
	if f[0].Status != DriftDrifted {
		t.Fatalf("expected drifted, got %+v", f[0])
	}

	// missing: label gone (e.g. already migrated)
	missingCarrier := strings.Replace(carrierFixture,
		"- **合并取数**：能一条命令取得的情报不拆多轮。\n", "", 1)
	f = CheckDrift(m, map[string]string{"workspace": missingCarrier})
	if f[0].Status != DriftMissing {
		t.Fatalf("expected missing, got %+v", f[0])
	}

	// ambiguous: duplicate labels
	dupCarrier := strings.Replace(carrierFixture, "## 成本纪律\n", "## 成本纪律\n\n- **合并取数**：能一条命令取得的情报不拆多轮。", 1)
	f = CheckDrift(m, map[string]string{"workspace": dupCarrier})
	if f[0].Status != DriftAmbiguous {
		t.Fatalf("expected ambiguous, got %+v", f[0])
	}
}

func TestVerifySnapshotByteExact(t *testing.T) {
	bodies := []string{archaeology.Body, metaWork.Body}
	// Build the post-enactment carrier through the real render contract:
	// stable = original minus bodies, clause segments appended by Render.
	stable := clausemark.RemoveClauses(carrierFixture, bodies)
	post := clausemark.Render(stable, []clausemark.RenderedClause{
		{Slug: "cls_aaaaaaaa", Version: 1, Body: bodies[0]},
		{Slug: "cls_bbbbbbbb", Version: 1, Body: bodies[1]},
	})
	if err := VerifySnapshot(carrierFixture, post, bodies); err != nil {
		t.Fatalf("verify failed: %v", err)
	}

	// One byte changed in the stable half must fail.
	tampered := strings.Replace(post, "平台协作规范", "平台协作规约", 1)
	if err := VerifySnapshot(carrierFixture, tampered, bodies); err == nil || !strings.Contains(err.Error(), "逐字节不一致") {
		t.Fatalf("expected stable mismatch, got %v", err)
	}

	// Tampered segment body must fail.
	segTampered := strings.Replace(post, "不拆多轮", "不许拆分轮次", 1)
	if err := VerifySnapshot(carrierFixture, segTampered, bodies); err == nil || !strings.Contains(err.Error(), "条款段") {
		t.Fatalf("expected segment mismatch, got %v", err)
	}

	// Segment order follows the render's slug ordering, not the manifest —
	// the same set in a different order must still verify.
	lines := strings.Split(post, "\n")
	var i1, i2 int
	for i, line := range lines {
		if strings.Contains(line, "cls_aaaaaaaa") {
			i1 = i
		}
		if strings.Contains(line, "cls_bbbbbbbb") {
			i2 = i
		}
	}
	lines[i1], lines[i2] = lines[i2], lines[i1]
	swapped := strings.Join(lines, "\n")
	if err := VerifySnapshot(carrierFixture, swapped, bodies); err != nil {
		t.Fatalf("order-free segment set must verify: %v", err)
	}
}

func TestProposalBodyShape(t *testing.T) {
	body := ProposalBody(archaeology, "ws-1")
	if body["change_kind"] != "migrate_clause" || body["carrier_scope_id"] != "ws-1" {
		t.Fatalf("shape: %+v", body)
	}
	if body["clause_text"] != archaeology.Body {
		t.Fatal("clause_text must be the inventory body verbatim")
	}
	anchors := body["evidence_anchors"].([]map[string]string)
	if anchors[0]["issue_id"] != "RUYI-733" {
		t.Fatalf("anchors: %+v", anchors)
	}
}

// loadForTest serializes a Manifest back through LoadManifest so validation
// is exercised on the real decode path.
func loadForTest(m *Manifest) (*Manifest, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return LoadManifest(data)
}
