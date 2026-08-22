import { describe, expect, it, vi, beforeEach } from "vitest";

/**
 * data/use-actor-name.ts 的 getName 兜底单测（P1 批次 8，裁定 1）。
 *
 * 三个形态必须全部落到兜底文案上：
 *   null/undefined（`??` 就能挡）、""（`??` 挡不住，需要 `||`）、
 *   "   " 纯空白（`||` 也挡不住，truthy —— 需要 `.trim() ||`）。
 * 第三种是本批次要根治的：渲染出来与空串完全一样是一片空白，拼进
 * 「{{count}} 条消息，来自 {{authors}}」还会产出破碎语句。
 *
 * mock 面：@tanstack/react-query 的 useQuery 按 queryKey 分发三份列表，
 * workspace-store 返回固定 ws id，useT 返回可辨认的兜底串。这样 hook 可以
 * 在 node 环境直接调用（它不碰 RN 原生模块，只读 TQ 缓存）。
 */

const { mockUseQuery, mockUseT } = vi.hoisted(() => ({
  mockUseQuery: vi.fn(),
  mockUseT: vi.fn(),
}));

vi.mock("@tanstack/react-query", () => ({ useQuery: mockUseQuery }));
vi.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (sel: (s: unknown) => unknown) =>
    sel({ currentWorkspaceId: "ws1" }),
}));
vi.mock("@/lib/use-t", () => ({ useT: mockUseT }));
vi.mock("@/data/queries/members", () => ({
  memberListOptions: () => ({ queryKey: ["members"] }),
}));
vi.mock("@/data/queries/agents", () => ({
  agentListOptions: () => ({ queryKey: ["agents"] }),
}));
vi.mock("@/data/queries/squads", () => ({
  squadListOptions: () => ({ queryKey: ["squads"] }),
}));

import { useActorLookup, getInitials } from "./use-actor-name";

/**
 * 用给定的三份列表装配 useQuery，然后取出 getName。
 *
 * 名字以 `use` 开头是给 eslint 的 react-hooks/rules-of-hooks 看的——它内部
 * 调 useActorLookup()，规则要求调用点本身是组件或 hook。这里没有 React
 * 运行时：useActorLookup 的三个依赖（useQuery / useWorkspaceStore / useT）
 * 全部被 mock 成普通函数，所以它退化成一次纯调用，不需要 renderHook。
 */
function useLookupWith(lists: {
  members?: { user_id: string; name?: string | null }[];
  agents?: { id: string; name?: string | null }[];
  squads?: { id: string; name?: string | null }[];
}) {
  mockUseQuery.mockImplementation((opts: { queryKey: string[] }) => {
    const which = opts.queryKey[0] as "members" | "agents" | "squads";
    return { data: lists[which] ?? [] };
  });
  return useActorLookup().getName;
}

beforeEach(() => {
  mockUseQuery.mockReset();
  mockUseT.mockReset();
  // 兜底串按 key 回显，断言时一眼能看出走的是哪条分支。
  mockUseT.mockReturnValue({ t: (key: string) => `FB:${key}` });
});

describe("getName 的空值兜底", () => {
  // 三种形态 × 三类 actor，逐个 case 展开——表驱动能少写行数，但一旦某个
  // 组合回归，逐个 it 的报错直接指出是哪一格。
  const BLANKS: [string, string | null | undefined][] = [
    ["null", null],
    ["undefined", undefined],
    ["空串", ""],
    ["纯空格", "   "],
    ["制表符与换行", "\t\n "],
    ["全角空格", "　　"],
  ];

  for (const [desc, blank] of BLANKS) {
    it(`member 的 name 为${desc}时落到 unknown_member`, () => {
      const getName = useLookupWith({
        members: [{ user_id: "m1", name: blank }],
      });
      expect(getName("member", "m1")).toBe("FB:mobile.actor.unknown_member");
    });

    it(`agent 的 name 为${desc}时落到 unknown_agent`, () => {
      const getName = useLookupWith({ agents: [{ id: "a1", name: blank }] });
      expect(getName("agent", "a1")).toBe("FB:mobile.actor.unknown_agent");
    });

    it(`squad 的 name 为${desc}时落到 unknown_squad`, () => {
      const getName = useLookupWith({ squads: [{ id: "s1", name: blank }] });
      expect(getName("squad", "s1")).toBe("FB:mobile.actor.unknown_squad");
    });
  }

  it("兜底返回值恒为非空、非纯空白——调用方可以直接拼进句子", () => {
    const getName = useLookupWith({
      members: [{ user_id: "m1", name: "   " }],
      agents: [{ id: "a1", name: "" }],
      squads: [{ id: "s1", name: null }],
    });
    for (const out of [
      getName("member", "m1"),
      getName("agent", "a1"),
      getName("squad", "s1"),
      getName(null, null),
      getName("member", "不存在的 id"),
    ]) {
      expect(out.trim().length).toBeGreaterThan(0);
    }
  });
});

describe("getName 的正常路径", () => {
  it("有真实名字时原样返回，不 trim 掉内部空格", () => {
    const getName = useLookupWith({
      members: [{ user_id: "m1", name: "Gu Xiaoyu" }],
      agents: [{ id: "a1", name: "顾 小鱼" }],
      squads: [{ id: "s1", name: "如意天团" }],
    });
    expect(getName("member", "m1")).toBe("Gu Xiaoyu");
    expect(getName("agent", "a1")).toBe("顾 小鱼");
    expect(getName("squad", "s1")).toBe("如意天团");
  });

  it("名字两端带空白时返回 trim 后的值，而不是兜底", () => {
    // 边界：有内容但带前后空白 —— 既不能误判成空、也不能把空白带进渲染。
    const getName = useLookupWith({
      members: [{ user_id: "m1", name: "  Ada  " }],
    });
    expect(getName("member", "m1")).toBe("Ada");
  });

  it("type 或 id 缺失时返回 system", () => {
    const getName = useLookupWith({});
    expect(getName(null, "m1")).toBe("FB:mobile.actor.system");
    expect(getName("member", null)).toBe("FB:mobile.actor.system");
    expect(getName(undefined, undefined)).toBe("FB:mobile.actor.system");
  });
});

describe("getInitials", () => {
  it("取前两个词的首字母并大写", () => {
    expect(getInitials("Gu Xiaoyu")).toBe("GX");
    expect(getInitials("ada")).toBe("A");
    expect(getInitials("a b c d")).toBe("AB");
  });
});
