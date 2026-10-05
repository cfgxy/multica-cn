import { beforeEach, describe, expect, it } from "vitest";
import { useSharedIntentStore, type ShareDestination } from "./shared-intent-store";
import type { SharedFile } from "@/lib/share-payload";

function file(name: string): SharedFile {
  return {
    uri: `file:///cache/share-intent/${name}`,
    name,
    mimeType: "application/pdf",
    size: 10,
  };
}

const chatDest: ShareDestination = { kind: "chat", agentId: "agent-1" };
const issueDest: ShareDestination = { kind: "issue" };

describe("shared-intent-store", () => {
  beforeEach(() => {
    useSharedIntentStore.getState().cancel();
  });

  it("setPayload 存文件；setDestination 定目的地；互不覆盖", () => {
    const s = useSharedIntentStore.getState();
    s.setPayload([file("a.pdf")]);
    s.setDestination(chatDest);

    const state = useSharedIntentStore.getState();
    expect(state.files).toHaveLength(1);
    expect(state.destination).toEqual(chatDest);
  });

  it("重复分享覆盖上一份负载（不合并）", () => {
    const s = useSharedIntentStore.getState();
    s.setPayload([file("a.pdf"), file("b.pdf")]);
    s.setPayload([file("c.pdf")]);
    expect(useSharedIntentStore.getState().files.map((f) => f.name)).toEqual(["c.pdf"]);
  });

  it("takeFor 只在目的地 kind 匹配时一次性取走并清空", () => {
    const s = useSharedIntentStore.getState();
    s.setPayload([file("a.pdf")]);
    s.setDestination(issueDest);

    const wrong = useSharedIntentStore.getState().takeFor("chat");
    expect(wrong).toBeNull();
    // 取错方向不消耗负载
    expect(useSharedIntentStore.getState().files).toHaveLength(1);

    const taken = useSharedIntentStore.getState().takeFor("issue");
    expect(taken?.files.map((f) => f.name)).toEqual(["a.pdf"]);
    expect(taken?.destination).toEqual(issueDest);
    // 一次性：取走即清空
    expect(useSharedIntentStore.getState().files).toHaveLength(0);
    expect(useSharedIntentStore.getState().destination).toBeNull();
    expect(useSharedIntentStore.getState().takeFor("issue")).toBeNull();
  });

  it("无目的地时 takeFor 返回 null 且不消耗", () => {
    const s = useSharedIntentStore.getState();
    s.setPayload([file("a.pdf")]);
    expect(useSharedIntentStore.getState().takeFor("issue")).toBeNull();
    expect(useSharedIntentStore.getState().files).toHaveLength(1);
  });

  it("cancel 清空负载与目的地", () => {
    const s = useSharedIntentStore.getState();
    s.setPayload([file("a.pdf")]);
    s.setDestination(chatDest);
    useSharedIntentStore.getState().cancel();
    const state = useSharedIntentStore.getState();
    expect(state.files).toHaveLength(0);
    expect(state.destination).toBeNull();
  });

  it("空文件负载取消目的地后 takeFor 返回 null", () => {
    const s = useSharedIntentStore.getState();
    s.setDestination(chatDest);
    expect(useSharedIntentStore.getState().takeFor("chat")).toBeNull();
  });
});
