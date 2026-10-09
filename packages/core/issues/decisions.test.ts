import { describe, expect, it } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import type { IssueDecision } from "../types";
import {
  decisionOptionDisplayLabel,
  patchDecisionInCache,
  stripDecisionOptionLetterPrefix,
  stripDecisionOptionRecommendedSuffix,
  upsertDecisionInCache,
} from "./decisions";

function card(id: string, status: IssueDecision["status"] = "open"): IssueDecision {
  return {
    id,
    issue_id: "i-1",
    source_comment_id: null,
    question: "q",
    options: [{ label: "A" }, { label: "B" }],
    multi_select: false,
    recommended_indices: [],
    status,
    selected_indices: [],
    answered_by_type: null,
    answered_by_id: null,
    answered_at: null,
    answer_comment_id: null,
    created_by_type: "agent",
    created_by_id: "a-1",
    created_at: "2026-10-02T03:00:00Z",
    updated_at: "2026-10-02T03:00:00Z",
  };
}

describe("patchDecisionInCache", () => {
  it("replaces the matching card by id and keeps list order", () => {
    const qc = new QueryClient();
    qc.setQueryData(["issues", "decisions", "i-1"], [card("d-1"), card("d-2")]);

    patchDecisionInCache(qc, "i-1", card("d-2", "answered"));

    const list = qc.getQueryData<IssueDecision[]>(["issues", "decisions", "i-1"]);
    expect(list?.map((d) => `${d.id}:${d.status}`)).toEqual(["d-1:open", "d-2:answered"]);
  });

  it("leaves the cache untouched for an unknown issue or card id", () => {
    const qc = new QueryClient();
    const prev = [card("d-1")];
    qc.setQueryData(["issues", "decisions", "i-1"], prev);

    patchDecisionInCache(qc, "i-other", card("d-1", "answered"));
    patchDecisionInCache(qc, "i-1", card("d-x", "answered"));

    expect(qc.getQueryData(["issues", "decisions", "i-1"])).toBe(prev);
  });
});

describe("upsertDecisionInCache", () => {
  it("inserts an unseen card at its created_at position", () => {
    const qc = new QueryClient();
    const early = card("d-1");
    early.created_at = "2026-10-02T03:00:00Z";
    const late = card("d-3");
    late.created_at = "2026-10-02T05:00:00Z";
    qc.setQueryData(["issues", "decisions", "i-1"], [early, late]);

    const mid = card("d-2", "answered");
    mid.created_at = "2026-10-02T04:00:00Z";
    upsertDecisionInCache(qc, "i-1", mid);

    const list = qc.getQueryData<IssueDecision[]>(["issues", "decisions", "i-1"]);
    expect(list?.map((d) => d.id)).toEqual(["d-1", "d-2", "d-3"]);
    expect(list?.[1]?.status).toBe("answered");
  });

  it("patches in place when the card already exists", () => {
    const qc = new QueryClient();
    qc.setQueryData(["issues", "decisions", "i-1"], [card("d-1"), card("d-2")]);

    upsertDecisionInCache(qc, "i-1", card("d-1", "cancelled"));

    const list = qc.getQueryData<IssueDecision[]>(["issues", "decisions", "i-1"]);
    expect(list?.map((d) => `${d.id}:${d.status}`)).toEqual(["d-1:cancelled", "d-2:open"]);
  });

  it("leaves an unfetched cache untouched", () => {
    const qc = new QueryClient();
    upsertDecisionInCache(qc, "i-1", card("d-1"));
    expect(qc.getQueryData(["issues", "decisions", "i-1"])).toBeUndefined();
  });
});

// Views that render their own index letter (decision batch bar) must not
// duplicate a letter the label already carries. The prefix form is one A-Z
// letter plus one of ： : 、 . — anything else renders untouched.
describe("stripDecisionOptionLetterPrefix", () => {
  it("strips one embedded prefix from a real RUYI-572 label (全角冒号)", () => {
    expect(
      stripDecisionOptionLetterPrefix("A：仅 /file/ 云盘文件族（drive_file_links），wiki/docx 另立后续单"),
    ).toBe("仅 /file/ 云盘文件族（drive_file_links），wiki/docx 另立后续单");
  });

  it("strips 半角冒号 with surrounding space", () => {
    expect(stripDecisionOptionLetterPrefix("B: note 扩展到 wiki/docx 链接")).toBe("note 扩展到 wiki/docx 链接");
  });

  it("strips 顿号 and 点号 separators", () => {
    expect(stripDecisionOptionLetterPrefix("A、方案一")).toBe("方案一");
    expect(stripDecisionOptionLetterPrefix("B.方案二")).toBe("方案二");
  });

  it("strips only one layer", () => {
    expect(stripDecisionOptionLetterPrefix("A：B：仅此一层")).toBe("B：仅此一层");
  });

  it("keeps the label when nothing non-empty follows the separator", () => {
    expect(stripDecisionOptionLetterPrefix("A：")).toBe("A：");
    expect(stripDecisionOptionLetterPrefix("A：  ")).toBe("A：  ");
  });

  it("keeps non-prefix forms untouched", () => {
    expect(stripDecisionOptionLetterPrefix("A-type")).toBe("A-type");
    expect(stripDecisionOptionLetterPrefix("B超 声呐")).toBe("B超 声呐");
    expect(stripDecisionOptionLetterPrefix("AB：双字母")).toBe("AB：双字母");
    expect(stripDecisionOptionLetterPrefix("a：小写")).toBe("a：小写");
    expect(stripDecisionOptionLetterPrefix("方案 A：前缀不在开头")).toBe("方案 A：前缀不在开头");
    expect(stripDecisionOptionLetterPrefix("")).toBe("");
  });

  // RUYI-620: the numbering convention also degrades to "A 选项文本"
  // (letter + space, half- or full-width) — that form double-numbers against
  // the control's own index letter just like "A：…" does.
  it("strips letter + space forms (half- and full-width)", () => {
    expect(stripDecisionOptionLetterPrefix("A 方案一")).toBe("方案一");
    expect(stripDecisionOptionLetterPrefix("B 继续观望")).toBe("继续观望");
    expect(stripDecisionOptionLetterPrefix("C　方案三")).toBe("方案三");
  });

  // RUYI-620: A-led words and double letters followed by a space are
  // content, not numbering — they must render untouched.
  it("keeps A-led words and double letters untouched", () => {
    expect(stripDecisionOptionLetterPrefix("A股龙头")).toBe("A股龙头");
    expect(stripDecisionOptionLetterPrefix("AB 测试")).toBe("AB 测试");
  });
});

// RUYI-620: the control renders the recommended badge from
// recommended_indices, so a trailing "（推荐）" in the label text doubles it.
// Only a terminating suffix is stripped — mid-label or leading occurrences
// are content.
describe("stripDecisionOptionRecommendedSuffix", () => {
  it("strips trailing （推荐）/ (推荐) with optional padding", () => {
    expect(stripDecisionOptionRecommendedSuffix("方案一（推荐）")).toBe("方案一");
    expect(stripDecisionOptionRecommendedSuffix("方案一 (推荐)")).toBe("方案一");
    expect(stripDecisionOptionRecommendedSuffix("方案一（推荐） ")).toBe("方案一");
  });

  it("keeps non-suffix occurrences untouched", () => {
    expect(stripDecisionOptionRecommendedSuffix("（推荐）方案一")).toBe("（推荐）方案一");
    expect(stripDecisionOptionRecommendedSuffix("方案一（推荐）备注")).toBe("方案一（推荐）备注");
    expect(stripDecisionOptionRecommendedSuffix("推荐方案一")).toBe("推荐方案一");
    expect(stripDecisionOptionRecommendedSuffix("方案一")).toBe("方案一");
  });
});

// RUYI-620: the single display helper every render surface (issue card,
// batch bar, mobile copies) calls — one place strips both the letter prefix
// and the recommended suffix so the control's own letter and badge render
// exactly once.
describe("decisionOptionDisplayLabel", () => {
  it("composes both strips for the render surfaces", () => {
    expect(decisionOptionDisplayLabel("A 方案一（推荐）")).toBe("方案一");
    expect(decisionOptionDisplayLabel("B：方案二")).toBe("方案二");
    expect(decisionOptionDisplayLabel("方案三")).toBe("方案三");
  });

  it("keeps non-numbering, non-suffix labels verbatim", () => {
    expect(decisionOptionDisplayLabel("A股龙头（推荐）")).toBe("A股龙头");
    expect(decisionOptionDisplayLabel("AB 测试")).toBe("AB 测试");
  });
});
