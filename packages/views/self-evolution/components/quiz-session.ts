"use client";

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
