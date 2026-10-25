"use client";

interface StoredQuizBatch {
  batchId: string;
  expectedRows: number | null;
  orderedAt?: number;
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
    // Session storage is best-effort; the current view remains usable.
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
    const expectedRows = record.expectedRows;
    return {
      batchId: record.batchId,
      expectedRows:
        typeof expectedRows === "number" && Number.isInteger(expectedRows) && expectedRows >= 0
          ? expectedRows
          : null,
      ...(typeof record.orderedAt === "number" ? { orderedAt: record.orderedAt } : {}),
    };
  } catch {
    return null;
  }
}

export function writeQuizBatch(
  wsId: string,
  agentId: string,
  batchId: string,
  expectedRows: number | null = null,
  orderedAt = Date.now(),
): void {
  try {
    storage()?.setItem(batchKey(wsId, agentId), JSON.stringify({ batchId, expectedRows, orderedAt }));
  } catch {
    // Session storage is best-effort; the current view remains usable.
  }
}
