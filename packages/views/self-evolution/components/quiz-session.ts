"use client";

interface StoredQuizBatch {
  batchId: string;
  orderedAt: number;
  expectedRows: number;
}

function storage(): Storage | null {
  if (typeof window === "undefined") return null;
  try {
    return window.sessionStorage;
  } catch {
    return null;
  }
}

function selectedAgentKey(wsId: string): string {
  return `multica:quiz:${wsId}:agent`;
}

function batchKey(wsId: string, agentId: string): string {
  return `multica:quiz:${wsId}:agent:${agentId}:batch`;
}

export function readQuizAgent(wsId: string): string {
  try {
    return storage()?.getItem(selectedAgentKey(wsId)) ?? "";
  } catch {
    return "";
  }
}

export function writeQuizAgent(wsId: string, agentId: string): void {
  try {
    storage()?.setItem(selectedAgentKey(wsId), agentId);
  } catch {
    // sessionStorage 为尽力而为，当前视图仍可继续使用。
  }
}

export function readQuizBatch(wsId: string, agentId: string): StoredQuizBatch | null {
  try {
    const raw = storage()?.getItem(batchKey(wsId, agentId));
    if (!raw) return null;
    const value: unknown = JSON.parse(raw);
    if (value === null || typeof value !== "object") return null;
    const record = value as Record<string, unknown>;
    if (typeof record.batchId !== "string" || record.batchId === "") return null;
    return {
      batchId: record.batchId,
      orderedAt: typeof record.orderedAt === "number" ? record.orderedAt : 0,
      expectedRows:
        typeof record.expectedRows === "number" && record.expectedRows > 0
          ? record.expectedRows
          : 0,
    };
  } catch {
    return null;
  }
}

export function writeQuizBatch(
  wsId: string,
  agentId: string,
  batchId: string,
  orderedAt: number,
  expectedRows: number,
): void {
  try {
    storage()?.setItem(
      batchKey(wsId, agentId),
      JSON.stringify({ batchId, orderedAt, expectedRows }),
    );
  } catch {
    // sessionStorage 为尽力而为，当前视图仍可继续使用。
  }
}
