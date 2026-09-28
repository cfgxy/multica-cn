// @vitest-environment node
import { beforeEach, describe, expect, it } from "vitest";
import { useMentionDraftStore } from "./mention-draft-store";

const BOHAN = { type: "member" as const, id: "user-1", name: "Bohan" };
const XIAOYU = { type: "member" as const, id: "user-2", name: "Xiaoyu" };
const ISSUE = { type: "issue" as const, id: "issue-1", name: "RUYI-232" };

function reset() {
  useMentionDraftStore.setState({ mentions: [], token: null });
}

describe("useMentionDraftStore 单选语义（RUYI-232）", () => {
  beforeEach(reset);

  it("add 在空 store 上插入一条", () => {
    useMentionDraftStore.getState().add(BOHAN);
    expect(useMentionDraftStore.getState().mentions).toEqual([BOHAN]);
  });

  it("add 对同 (type,id) 幂等：重复选择同一人不产生重复 chip", () => {
    const { add } = useMentionDraftStore.getState();
    add(BOHAN);
    add({ ...BOHAN });
    expect(useMentionDraftStore.getState().mentions).toEqual([BOHAN]);
  });

  it("连续多次单选（每次关闭选择器后再次选择）累积多人", () => {
    const { add } = useMentionDraftStore.getState();
    add(BOHAN);
    add(XIAOYU);
    add(ISSUE);
    expect(useMentionDraftStore.getState().mentions).toEqual([
      BOHAN,
      XIAOYU,
      ISSUE,
    ]);
  });

  it("同名不同 id 视为不同 chip", () => {
    const { add } = useMentionDraftStore.getState();
    add(BOHAN);
    add({ type: "member", id: "user-9", name: "Bohan" });
    expect(useMentionDraftStore.getState().mentions).toHaveLength(2);
  });

  it("toggle 保持原 toggle 语义（composer 回滚路径依赖它）", () => {
    const { toggle } = useMentionDraftStore.getState();
    toggle(BOHAN);
    expect(useMentionDraftStore.getState().mentions).toEqual([BOHAN]);
    toggle(BOHAN);
    expect(useMentionDraftStore.getState().mentions).toEqual([]);
  });

  it("remove / clear 保持原语义", () => {
    const { add, remove, clear } = useMentionDraftStore.getState();
    add(BOHAN);
    add(XIAOYU);
    remove("member", "user-1");
    expect(useMentionDraftStore.getState().mentions).toEqual([XIAOYU]);
    clear();
    expect(useMentionDraftStore.getState().mentions).toEqual([]);
  });
});

describe("useMentionDraftStore 打字触发 token 通道（RUYI-232）", () => {
  beforeEach(reset);

  it("setToken 记录触发位置，覆盖旧值", () => {
    const { setToken } = useMentionDraftStore.getState();
    setToken({ start: 3, query: "" });
    expect(useMentionDraftStore.getState().token).toEqual({
      start: 3,
      query: "",
    });
    setToken({ start: 0, query: "jo" });
    expect(useMentionDraftStore.getState().token).toEqual({
      start: 0,
      query: "jo",
    });
  });

  it("setToken(null) 清除待消费 token", () => {
    const { setToken } = useMentionDraftStore.getState();
    setToken({ start: 3, query: "" });
    setToken(null);
    expect(useMentionDraftStore.getState().token).toBeNull();
  });

  it("初始状态无 token", () => {
    expect(useMentionDraftStore.getState().token).toBeNull();
  });
});
