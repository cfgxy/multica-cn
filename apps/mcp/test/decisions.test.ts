/**
 * Decision-card face regression (RUYI-688).
 *
 * Pins the three decision tools end to end against a stateful fake backend
 * that mirrors the Go handlers' observable contract:
 *   - list_decisions: empty / single / multiple cards, per-issue 决策编号
 *     (open cards in creation order), workspace counts, status filter;
 *   - get_decision: full field projection with options rendered as letters;
 *   - answer_decisions: the "1a 2b" grammar (1-based 决策编号 over open
 *     cards + option letters A-D), explicit answers[] targeting, and the
 *     structured failure matrix — invalid_selection, decision_not_found,
 *     invalid_option, already_answered, already_cancelled, not_found,
 *     already_closed — each rejected BEFORE any write leaves the client, so
 *     a failed call is provably read-only.
 *
 * The fake carries the server's wire shape (options as [{label}] objects)
 * so the client-side normalization is exercised too.
 */

import { describe, expect, it } from "vitest";

import type { Logger } from "../src/log.js";
import { MulticaClient } from "../src/rest.js";
import { ToolInputError } from "../src/schemas.js";
import { findTool, TOOL_DEFINITIONS } from "../src/tools.js";

const WS = "voice-notes";
const WS_ID = "11111111-1111-4111-8111-111111111111";
const ISSUE_ID = "22222222-2222-4222-8222-222222222222";

const silentLogger: Logger = { info() {}, error() {} };

interface FakeCard {
  id: string;
  question: string;
  labels: string[];
  multi_select: boolean;
  recommended_indices: number[];
  status: "open" | "answered" | "cancelled";
  selected_indices: number[];
  created_at: string;
  updated_at: string;
}

/** Wire-shape card (options as [{label}]) exactly as the Go handler emits. */
function wireCard(card: FakeCard, issueId = ISSUE_ID): Record<string, unknown> {
  return {
    id: card.id,
    issue_id: issueId,
    source_comment_id: null,
    question: card.question,
    options: card.labels.map((label) => ({ label })),
    multi_select: card.multi_select,
    recommended_indices: card.recommended_indices,
    status: card.status,
    selected_indices: card.selected_indices,
    answered_by_type: card.status === "answered" ? "member" : null,
    answered_by_id: card.status === "answered" ? "u-1" : null,
    answered_at: card.status === "answered" ? card.updated_at : null,
    answer_comment_id: null,
    created_by_type: "agent",
    created_by_id: "a-1",
    created_at: card.created_at,
    updated_at: card.updated_at,
  };
}

class DecisionBackend {
  cards: FakeCard[] = [];
  requestLog: Array<{ method: string; path: string; body: unknown }> = [];
  batchEchoSeq = 0;

  /** Seeds fresh clones — the fake mutates card state, and shared fixture
   * objects would leak answers across tests. */
  seed(...cards: FakeCard[]): void {
    this.cards = cards.map((card) => structuredClone(card));
  }

  card(id: string): FakeCard {
    const card = this.cards.find((candidate) => candidate.id === id);
    if (card === undefined) throw new Error(`fixture missing card ${id}`);
    return card;
  }

  private openSorted(): FakeCard[] {
    return this.cards
      .filter((card) => card.status === "open")
      .sort((a, b) => a.created_at.localeCompare(b.created_at));
  }

  /** Mirrors AnswerIssueDecisionsBatch: classify against the open snapshot,
   * CAS answered cards, report per-card outcomes. Never rolls back. */
  private answerBatch(issueId: string, body: unknown): Record<string, unknown> {
    const answers = (body as { answers?: Array<{ decision_id?: unknown; selected_indices?: unknown }> })
      .answers;
    if (!Array.isArray(answers) || answers.length === 0) {
      return { status: 400, payload: { error: "answers must not be empty" } };
    }
    const open = new Set(this.openSorted().map((card) => card.id));
    const results: Array<Record<string, unknown>> = [];
    const echoed: FakeCard[] = [];
    for (const answer of answers) {
      const decisionId = answer.decision_id;
      const card = this.cards.find((candidate) => candidate.id === decisionId);
      if (card === undefined || card.created_at === undefined) {
        results.push({ decision_id: decisionId, status: "not_found", error: "decision card not found on this issue" });
        continue;
      }
      if (!open.has(decisionId as string)) {
        results.push({ decision_id: decisionId, status: "conflict", error: "decision card is no longer open" });
        continue;
      }
      const indices = answer.selected_indices;
      if (
        !Array.isArray(indices) ||
        indices.length === 0 ||
        indices.some((index) => typeof index !== "number" || index < 0 || index >= card.labels.length) ||
        new Set(indices).size !== indices.length ||
        (!card.multi_select && indices.length !== 1)
      ) {
        results.push({ decision_id: decisionId, status: "invalid", error: "selected_indices failed validation" });
        continue;
      }
      card.status = "answered";
      card.selected_indices = indices as number[];
      card.updated_at = "2026-10-11T10:00:00Z";
      results.push({ decision_id: decisionId, status: "answered", decision: wireCard(card, issueId) });
      echoed.push(card);
    }
    if (echoed.length > 0) {
      this.batchEchoSeq += 1;
      return {
        status: 200,
        payload: {
          results,
          echo_comment_id: `echo-${this.batchEchoSeq}`,
          trigger_outcomes: echoed.map(() => ({
            target_type: "agent",
            target_id: "a-1",
            status: "queued",
          })),
        },
      };
    }
    return { status: 200, payload: { results } };
  }

  client(): MulticaClient {
    return new MulticaClient({
      serverUrl: "https://api.example.com",
      token: "mul_test",
      logger: silentLogger,
      fetchImpl: (async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(String(input));
        const method = init?.method ?? "GET";
        const rawBody = init?.body === undefined ? undefined : String(init.body);
        const body = rawBody === undefined || rawBody.length === 0 ? undefined : JSON.parse(rawBody);
        this.requestLog.push({ method, path: url.pathname, body });

        const respond = (status: number, payload: unknown): Response =>
          new Response(JSON.stringify(payload), {
            status,
            headers: { "content-type": "application/json" },
          });

        if (method === "GET" && url.pathname === "/api/workspaces") {
          return respond(200, [
            { id: WS_ID, slug: WS, name: "Voice", issue_prefix: "VOI" },
          ]);
        }
        if (method === "GET" && url.pathname === `/api/workspaces/${WS_ID}/decision-inbox`) {
          const status = url.searchParams.get("status");
          const items = this.cards
            .filter((card) => status === null || card.status === status)
            .sort((a, b) => b.created_at.localeCompare(a.created_at))
            .map((card) => ({
              ...wireCard(card),
              workspace_id: WS_ID,
              issue_number: 42,
              issue_identifier: "VOI-42",
              issue_title: "Ship the thing",
            }));
          return respond(200, {
            items,
            counts: {
              open: this.cards.filter((card) => card.status === "open").length,
              answered: this.cards.filter((card) => card.status === "answered").length,
              cancelled: this.cards.filter((card) => card.status === "cancelled").length,
            },
          });
        }
        const issueDecisions = url.pathname.match(/^\/api\/issues\/([^/]+)\/decisions$/);
        if (method === "GET" && issueDecisions !== null) {
          if (issueDecisions[1] === "unknown-issue") {
            return respond(404, { error: "issue not found" });
          }
          return respond(200, this.cards.map((card) => wireCard(card)));
        }
        const answerBatch = url.pathname.match(/^\/api\/issues\/([^/]+)\/decisions\/answer-batch$/);
        if (method === "POST" && answerBatch !== null) {
          const outcome = this.answerBatch(ISSUE_ID, body);
          return respond(outcome.status as number, outcome.payload);
        }
        if (method === "GET" && /^\/api\/issues\/[^/]+$/.test(url.pathname)) {
          if (url.pathname.endsWith("unknown-issue")) {
            return respond(404, { error: "issue not found" });
          }
          return respond(200, {
            id: ISSUE_ID,
            identifier: "VOI-42",
            number: 42,
            title: "Ship the thing",
            status: "in_progress",
          });
        }
        return respond(404, { error: "not found" });
      }) as typeof fetch,
    });
  }
}

async function callJson(
  client: MulticaClient,
  name: string,
  args: Record<string, unknown>,
): Promise<Record<string, unknown>> {
  const tool = findTool(name);
  if (tool === undefined) throw new Error(`missing tool ${name}`);
  return (await tool.handler(args, client)) as Record<string, unknown>;
}

const OPEN_A: FakeCard = {
  id: "d-1",
  question: "选哪个部署窗口？",
  labels: ["今晚", "明晚", "下周一"],
  multi_select: false,
  recommended_indices: [0],
  status: "open",
  selected_indices: [],
  created_at: "2026-10-10T01:00:00Z",
  updated_at: "2026-10-10T01:00:00Z",
};
const OPEN_B: FakeCard = {
  id: "d-2",
  question: "带哪些回归范围？",
  labels: ["冒烟", "全量", "全量+性能"],
  multi_select: true,
  recommended_indices: [],
  status: "open",
  selected_indices: [],
  created_at: "2026-10-10T02:00:00Z",
  updated_at: "2026-10-10T02:00:00Z",
};
const ANSWERED_C: FakeCard = {
  id: "d-3",
  question: "已回答的旧卡",
  labels: ["甲", "乙"],
  multi_select: false,
  recommended_indices: [],
  status: "answered",
  selected_indices: [1],
  created_at: "2026-10-09T00:00:00Z",
  updated_at: "2026-10-09T05:00:00Z",
};

// ---- registration -----------------------------------------------------------

describe("decision tool registration", () => {
  it("registers the three tools once with read-only split", () => {
    const names = TOOL_DEFINITIONS.map((tool) => tool.name);
    for (const name of ["list_decisions", "get_decision", "answer_decisions"]) {
      expect(names.filter((candidate) => candidate === name)).toHaveLength(1);
    }
    expect(findTool("list_decisions")?.description).toMatch(/read-only/i);
    expect(findTool("get_decision")?.description).toMatch(/read-only/i);
    // The one write declares its side effect: the echo comment resumes runs.
    expect(findTool("answer_decisions")?.description).toMatch(/echo comment/i);
    expect(findTool("answer_decisions")?.description).toMatch(/resumes/i);
  });
});

// ---- list_decisions ---------------------------------------------------------

describe("list_decisions", () => {
  it("answers an empty workspace inbox with zero counts", async () => {
    const backend = new DecisionBackend();
    const result = await callJson(backend.client(), "list_decisions", { workspace: WS });
    expect(result.total).toBe(0);
    expect(result.decisions).toEqual([]);
    expect(result.counts).toEqual({ open: 0, answered: 0, cancelled: 0 });
    // slug resolution adds exactly one workspace lookup before the inbox read
    expect(backend.requestLog.map((entry) => entry.path)).toEqual([
      "/api/workspaces",
      `/api/workspaces/${WS_ID}/decision-inbox`,
    ]);
  });

  it("lists cards with issue context, option letters and per-issue 决策编号", async () => {
    const backend = new DecisionBackend();
    backend.seed(OPEN_B, OPEN_A);
    const result = await callJson(backend.client(), "list_decisions", { workspace: WS });
    expect(result.total).toBe(2);
    expect(result.counts).toEqual({ open: 2, answered: 0, cancelled: 0 });
    const decisions = result.decisions as Array<Record<string, unknown>>;
    // 决策编号 follows creation order, not inbox arrival order (newest first).
    expect(decisions.map((card) => card.decision_number)).toEqual([2, 1]);
    const first = decisions[1] as Record<string, unknown>;
    expect(first.id).toBe("d-1");
    expect(first.issue_identifier).toBe("VOI-42");
    expect(first.issue_title).toBe("Ship the thing");
    expect(first.status).toBe("open");
    expect(first.options).toEqual([
      { letter: "A", label: "今晚" },
      { letter: "B", label: "明晚" },
      { letter: "C", label: "下周一" },
    ]);
    expect(first.recommended).toEqual(["A"]);
    expect(first.question).toBe("选哪个部署窗口？");
    expect(typeof first.updated_at).toBe("string");
  });

  it("numbers only open cards and honours the status filter", async () => {
    const backend = new DecisionBackend();
    backend.seed(ANSWERED_C, OPEN_A, OPEN_B);
    const filtered = await callJson(backend.client(), "list_decisions", {
      workspace: WS,
      status: "answered",
    });
    expect((filtered.decisions as unknown[]).length).toBe(1);
    const answered = (filtered.decisions as Array<Record<string, unknown>>)[0]!;
    expect(answered.decision_number).toBeNull();
    expect(answered.selected).toEqual(["B"]);

    const all = await callJson(backend.client(), "list_decisions", { workspace: WS });
    expect(all.total).toBe(3);
    expect(all.counts).toEqual({ open: 2, answered: 1, cancelled: 0 });
    const openNumbers = (all.decisions as Array<Record<string, unknown>>)
      .filter((card) => card.status === "open")
      .map((card) => card.decision_number);
    expect(openNumbers).toEqual([2, 1]); // d-1 is the oldest open card
  });
});

// ---- get_decision -----------------------------------------------------------

describe("get_decision", () => {
  it("projects the complete card with letter-mapped options and 决策编号", async () => {
    const backend = new DecisionBackend();
    backend.seed(OPEN_A, OPEN_B);
    const result = await callJson(backend.client(), "get_decision", {
      workspace: WS,
      issue: "VOI-42",
      decision_id: "d-2",
    });
    const decision = result.decision as Record<string, unknown>;
    expect(decision.id).toBe("d-2");
    expect(decision.decision_number).toBe(2);
    expect(decision.question).toBe("带哪些回归范围？");
    expect(decision.options).toEqual([
      { letter: "A", label: "冒烟" },
      { letter: "B", label: "全量" },
      { letter: "C", label: "全量+性能" },
    ]);
    expect(decision.multi_select).toBe(true);
    expect(decision.status).toBe("open");
    const issue = result.issue as Record<string, unknown>;
    expect(issue.identifier).toBe("VOI-42");
    expect(issue.number).toBe(42);
    expect(issue.title).toBe("Ship the thing");
  });

  it("answers not_found for an unknown issue and an unknown card without writing", async () => {
    const backend = new DecisionBackend();
    backend.seed(OPEN_A);
    const client = backend.client();
    const unknownIssue = await callJson(client, "get_decision", {
      workspace: WS,
      issue: "unknown-issue",
      decision_id: "d-1",
    });
    expect(unknownIssue.code).toBe("not_found");
    const unknownCard = await callJson(client, "get_decision", {
      workspace: WS,
      issue: "VOI-42",
      decision_id: "d-999",
    });
    expect(unknownCard.code).toBe("not_found");
    expect(backend.requestLog.every((entry) => entry.method === "GET")).toBe(true);
  });
});

// ---- answer_decisions: happy paths ------------------------------------------

describe("answer_decisions: selection grammar", () => {
  it("binds a single token and lands the 0-based indices on the wire", async () => {
    const backend = new DecisionBackend();
    backend.seed(OPEN_A);
    const result = await callJson(backend.client(), "answer_decisions", {
      workspace: WS,
      issue: "VOI-42",
      selection: "1b",
    });
    expect(result.answered).toBe(1);
    expect(backend.card("d-1").status).toBe("answered");
    expect(backend.card("d-1").selected_indices).toEqual([1]);
    const post = backend.requestLog.find((entry) => entry.method === "POST");
    expect((post?.body as { answers: unknown[] }).answers).toEqual([
      { decision_id: "d-1", selected_indices: [1] },
    ]);
    expect(result.echo_comment_id).toBe("echo-1");
    // Re-reading confirms the flipped state.
    const detail = await callJson(backend.client(), "get_decision", {
      workspace: WS,
      issue: "VOI-42",
      decision_id: "d-1",
    });
    expect((detail.decision as Record<string, unknown>).status).toBe("answered");
  });

  it("binds combined tokens across single-select and multi-select cards", async () => {
    const backend = new DecisionBackend();
    backend.seed(OPEN_A, OPEN_B);
    const result = await callJson(backend.client(), "answer_decisions", {
      workspace: WS,
      issue: "VOI-42",
      selection: "1a 2bc",
    });
    expect(result.answered).toBe(2);
    expect(backend.card("d-1").selected_indices).toEqual([0]);
    expect(backend.card("d-2").selected_indices).toEqual([1, 2]);
    const results = result.results as Array<Record<string, unknown>>;
    expect(results.map((entry) => entry.decision)).toEqual([1, 2]);
    expect(results.every((entry) => entry.status === "answered")).toBe(true);
  });

  it("accepts 、，,/ separators and uppercase letters like the product grammar", async () => {
    // Every separator variant parses; letters are contiguous within one
    // token ("2BC") and each separator must be followed by a fresh
    // "number+letters" token — "2B,C" or "2b/c" would not parse.
    for (const selection of ["1A、2BC", "1A，2BC", "1A,2BC", "1A/2BC", "1A 2BC", "1a\t2bc"]) {
      const backend = new DecisionBackend();
      backend.seed(OPEN_A, OPEN_B);
      const result = await callJson(backend.client(), "answer_decisions", {
        workspace: WS,
        issue: "VOI-42",
        selection,
      });
      expect(result.answered).toBe(2);
      expect(backend.card("d-1").selected_indices).toEqual([0]);
      expect(backend.card("d-2").selected_indices).toEqual([1, 2]);
    }
  });

  it("answers by explicit decision ids regardless of numbering", async () => {
    const backend = new DecisionBackend();
    backend.seed(OPEN_A, OPEN_B);
    const result = await callJson(backend.client(), "answer_decisions", {
      workspace: WS,
      issue: "VOI-42",
      answers: [{ decision_id: "d-2", options: "AC" }],
    });
    expect(result.answered).toBe(1);
    expect(backend.card("d-2").selected_indices).toEqual([0, 2]);
    expect(backend.card("d-1").status).toBe("open");
  });
});

// ---- answer_decisions: structured failures with zero writes -----------------

describe("answer_decisions: structured failures", () => {
  it("rejects a malformed selection as invalid_selection with zero wire writes", async () => {
    const backend = new DecisionBackend();
    backend.seed(OPEN_A);
    for (const bad of ["1", "a", "0a", "1x", "1 a prose", "1a-", "1e", "、1a", "1a 2b,c"]) {
      const result = await callJson(backend.client(), "answer_decisions", {
        workspace: WS,
        issue: "VOI-42",
        selection: bad,
      });
      expect(result.code, bad).toBe("invalid_selection");
      expect(result.answered).toBe(0);
    }
    expect(backend.requestLog.every((entry) => entry.method === "GET")).toBe(true);
  });

  it("answers a token pointing past the open set as decision_not_found", async () => {
    const backend = new DecisionBackend();
    backend.seed(OPEN_A);
    const result = await callJson(backend.client(), "answer_decisions", {
      workspace: WS,
      issue: "VOI-42",
      selection: "2b 1a",
    });
    expect(result.answered).toBe(0);
    expect(result.code).toBe("decision_not_found");
    expect((result.errors as Array<Record<string, unknown>>)[0]?.decision).toBe(2);
    expect(backend.card("d-1").status).toBe("open");
    expect(backend.requestLog.every((entry) => entry.method === "GET")).toBe(true);
  });

  it("treats an empty selection string as a tool input error", async () => {
    const backend = new DecisionBackend();
    backend.seed(OPEN_A);
    await expect(
      findTool("answer_decisions")?.handler(
        { workspace: WS, issue: "VOI-42", selection: "" },
        backend.client(),
      ),
    ).rejects.toBeInstanceOf(ToolInputError);
    expect(backend.requestLog.every((entry) => entry.method === "GET")).toBe(true);
  });

  it("requires exactly one of selection or answers", async () => {
    const backend = new DecisionBackend();
    const client = backend.client();
    await expect(
      findTool("answer_decisions")?.handler(
        { workspace: WS, issue: "VOI-42" },
        client,
      ),
    ).rejects.toBeInstanceOf(ToolInputError);
    await expect(
      findTool("answer_decisions")?.handler(
        { workspace: WS, issue: "VOI-42", selection: "1a", answers: [{ decision_id: "d-1", options: "A" }] },
        client,
      ),
    ).rejects.toBeInstanceOf(ToolInputError);
    expect(backend.requestLog).toHaveLength(0);
  });

  it("maps an out-of-range option letter to invalid_option without writing", async () => {
    const backend = new DecisionBackend();
    backend.seed(OPEN_A, OPEN_B);
    const result = await callJson(backend.client(), "answer_decisions", {
      workspace: WS,
      issue: "VOI-42",
      selection: "1ad",
    });
    expect(result.answered).toBe(0);
    expect(result.code).toBe("invalid_option");
    const errors = result.errors as Array<Record<string, unknown>>;
    expect(errors[0]?.decision_id).toBe("d-1");
    expect(backend.card("d-1").status).toBe("open");
    expect(backend.requestLog.every((entry) => entry.method === "GET")).toBe(true);
  });

  it("enforces single-select exactly-one and multi-select no-repeat locally", async () => {
    const backend = new DecisionBackend();
    backend.seed(OPEN_A, OPEN_B);
    const single = await callJson(backend.client(), "answer_decisions", {
      workspace: WS,
      issue: "VOI-42",
      selection: "1ab",
    });
    expect(single.code).toBe("invalid_option");
    expect((single.errors as Array<Record<string, unknown>>)[0]?.message).toMatch(/single-select/);
    const repeated = await callJson(backend.client(), "answer_decisions", {
      workspace: WS,
      issue: "VOI-42",
      selection: "2aa",
    });
    expect(repeated.code).toBe("invalid_option");
    expect((repeated.errors as Array<Record<string, unknown>>)[0]?.message).toMatch(/twice/);
    expect(backend.requestLog.every((entry) => entry.method === "GET")).toBe(true);
  });

  it("maps a duplicate submission to already_answered with precise state", async () => {
    const backend = new DecisionBackend();
    backend.seed({ ...OPEN_A });
    const first = await callJson(backend.client(), "answer_decisions", {
      workspace: WS,
      issue: "VOI-42",
      selection: "1a",
    });
    expect(first.answered).toBe(1);
    const requestCountAfterFirst = backend.requestLog.length;
    const second = await callJson(backend.client(), "answer_decisions", {
      workspace: WS,
      issue: "VOI-42",
      selection: "1a",
    });
    expect(second.answered).toBe(0);
    expect(second.code).toBe("already_closed");
    expect(backend.card("d-1").selected_indices).toEqual([0]); // unchanged
    // No POST on the rejected re-submission.
    expect(
      backend.requestLog.slice(requestCountAfterFirst).every((entry) => entry.method === "GET"),
    ).toBe(true);
  });

  it("answers explicit ids on settled cards with already_answered / already_cancelled", async () => {
    const backend = new DecisionBackend();
    backend.seed(ANSWERED_C, { ...OPEN_B, status: "cancelled" });
    const result = await callJson(backend.client(), "answer_decisions", {
      workspace: WS,
      issue: "VOI-42",
      answers: [
        { decision_id: "d-3", options: "A" },
        { decision_id: "d-2", options: "A" },
        { decision_id: "d-nope", options: "A" },
      ],
    });
    expect(result.answered).toBe(0);
    expect(result.code).toBe("already_answered");
    const codes = (result.errors as Array<Record<string, unknown>>).map(
      (entry) => entry.code,
    );
    expect(codes).toEqual(["already_answered", "already_cancelled", "not_found"]);
    expect(backend.requestLog.every((entry) => entry.method === "GET")).toBe(true);
  });

  it("rejects an unknown issue with not_found before any write", async () => {
    const backend = new DecisionBackend();
    const result = await callJson(backend.client(), "answer_decisions", {
      workspace: WS,
      issue: "unknown-issue",
      selection: "1a",
    });
    expect(result.code).toBe("not_found");
    expect(backend.requestLog.every((entry) => entry.method === "GET")).toBe(true);
  });

  it("surfaces a mid-flight race as already_closed per card", async () => {
    const backend = new DecisionBackend();
    backend.seed(OPEN_A);
    const client = backend.client();
    // Sabotage: mark the card answered after the tool's fresh read but before
    // the backend applies the batch — the server-side CAS reports conflict.
    const tool = findTool("answer_decisions");
    const originalFetch = (
      client as unknown as { fetchImpl: typeof fetch }
    ).fetchImpl;
    (
      client as unknown as { fetchImpl: typeof fetch }
    ).fetchImpl = (async (input: RequestInfo | URL, init?: RequestInit) => {
      const response = await originalFetch(input, init);
      if (
        (init?.method ?? "GET") === "GET" &&
        String(input).includes("/decisions")
      ) {
        backend.card("d-1").status = "answered";
      }
      return response;
    }) as typeof fetch;
    const result = (await tool?.handler(
      { workspace: WS, issue: "VOI-42", selection: "1a" },
      client,
    )) as Record<string, unknown>;
    const race = (result.results as Array<Record<string, unknown>>)[0];
    expect(result.answered).toBe(0);
    expect(race?.code).toBe("already_closed");
  });
});
