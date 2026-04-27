"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { agentListOptions, memberListOptions as _members } from "../workspace/queries";
import { runtimeListOptions } from "../runtimes/queries";
import { activeTasksOptions } from "./queries";
import { deriveAgentPresence, deriveAgentPresenceDetail } from "./derive-presence";
import type { AgentPresence, AgentPresenceDetail } from "./types";

// Re-render every 30s so the FAILED state auto-clears once its 2-minute
// window has elapsed, even if no underlying query data has changed.
// Without this tick, a user who tabs away and comes back 5 minutes later
// would still see the red indicator.
const PRESENCE_TICK_MS = 30_000;

function usePresenceTick(): number {
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setTick((t) => t + 1), PRESENCE_TICK_MS);
    return () => clearInterval(id);
  }, []);
  return tick;
}

/**
 * Derived agent presence ("available" / "working" / "pending" / "failed" /
 * "offline"), or "loading" while the underlying queries are still resolving.
 *
 * Accepts wsId as a parameter so the hook works outside WorkspaceIdProvider
 * (e.g. inside hover cards rendered before the workspace is mounted).
 */
export function useAgentPresence(
  wsId: string | undefined,
  agentId: string | undefined,
): AgentPresence | "loading" {
  const { data: agents } = useQuery({
    ...agentListOptions(wsId ?? ""),
    enabled: !!wsId,
  });
  const { data: runtimes } = useQuery({
    ...runtimeListOptions(wsId ?? ""),
    enabled: !!wsId,
  });
  const { data: activeTasks } = useQuery({
    ...activeTasksOptions(wsId ?? ""),
    enabled: !!wsId,
  });
  const tick = usePresenceTick();

  return useMemo<AgentPresence | "loading">(() => {
    if (!wsId || !agentId) return "loading";
    if (!agents || !runtimes || !activeTasks) return "loading";

    const agent = agents.find((a) => a.id === agentId);
    if (!agent) return "loading";
    const runtime = runtimes.find((r) => r.id === agent.runtime_id);
    if (!runtime) return "loading";

    const tasks = activeTasks.filter((t) => t.agent_id === agentId);
    return deriveAgentPresence({ agent, runtime, recentTasks: tasks, now: Date.now() });
    // tick is intentionally read so the memo recomputes every PRESENCE_TICK_MS
    // ms; eslint will complain if it's not in the deps array.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [wsId, agentId, agents, runtimes, activeTasks, tick]);
}

/**
 * Same as useAgentPresence but returns a detail object including running /
 * queued counts and (when failed) the failure reason. Use this for hover
 * cards and other places that need to render the +N badge or failure copy.
 */
export function useAgentPresenceDetail(
  wsId: string | undefined,
  agentId: string | undefined,
): AgentPresenceDetail | "loading" {
  const { data: agents } = useQuery({
    ...agentListOptions(wsId ?? ""),
    enabled: !!wsId,
  });
  const { data: runtimes } = useQuery({
    ...runtimeListOptions(wsId ?? ""),
    enabled: !!wsId,
  });
  const { data: activeTasks } = useQuery({
    ...activeTasksOptions(wsId ?? ""),
    enabled: !!wsId,
  });
  const tick = usePresenceTick();

  return useMemo<AgentPresenceDetail | "loading">(() => {
    if (!wsId || !agentId) return "loading";
    if (!agents || !runtimes || !activeTasks) return "loading";

    const agent = agents.find((a) => a.id === agentId);
    if (!agent) return "loading";
    const runtime = runtimes.find((r) => r.id === agent.runtime_id);
    if (!runtime) return "loading";

    const tasks = activeTasks.filter((t) => t.agent_id === agentId);
    return deriveAgentPresenceDetail({
      agent,
      runtime,
      recentTasks: tasks,
      now: Date.now(),
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [wsId, agentId, agents, runtimes, activeTasks, tick]);
}
