/**
 * Runtime discovery pollers — mobile mirror of packages/core/runtimes/models.ts
 * and local-skills.ts. The core versions close over the web cookie-auth api
 * client, which mobile cannot reuse at runtime, so the poll loops live here
 * against the mobile Bearer client while keeping the same wire contract and
 * timing budgets (the numbers are load-bearing — see the core file comments).
 */
import { api } from "@/data/api";
import type {
  CreateRuntimeLocalSkillImportRequest,
  RuntimeLocalSkillImportRequest,
  RuntimeLocalSkillsResult,
  RuntimeModelsResult,
} from "@multica/core/types";

const POLL_INTERVAL_MS = 500;

// Sequential server windows (pending 30s + running 60s) plus slack, so the
// client never expires before the server's own, more specific timeout text.
const MODELS_POLL_TIMEOUT_MS = 30_000 + 60_000 + 10_000;
const LOCAL_SKILLS_POLL_TIMEOUT_MS = 30_000;

// Import timeout exceeds runtimeLocalSkillPendingTimeout +
// runtimeLocalSkillRunningTimeout (server/internal/handler/
// runtime_local_skills.go) — old daemons pop one import per heartbeat cycle.
const IMPORT_POLL_TIMEOUT_MS = 4 * 60_000;

export async function resolveRuntimeModels(
  runtimeId: string,
): Promise<RuntimeModelsResult> {
  const initial = await api.initiateListModels(runtimeId);
  const start = Date.now();
  let current = initial;
  while (current.status === "pending" || current.status === "running") {
    if (Date.now() - start > MODELS_POLL_TIMEOUT_MS) {
      throw new Error("model discovery timed out");
    }
    await new Promise((resolve) => setTimeout(resolve, POLL_INTERVAL_MS));
    current = await api.getListModelsResult(runtimeId, initial.id);
  }
  // Only an explicit `completed` is a catalog; anything else (including an
  // unknown status from a newer server) keeps manual model entry usable.
  if (current.status !== "completed") {
    throw new Error(
      current.error || `model discovery failed (status: ${current.status})`,
    );
  }
  return {
    models: current.models ?? [],
    unavailableModels: current.unavailable_models ?? [],
    supported: current.supported !== false,
    cached: current.cached === true,
    cachedAt: current.cached_at,
  };
}

export async function resolveRuntimeLocalSkills(
  runtimeId: string,
): Promise<RuntimeLocalSkillsResult> {
  const initial = await api.initiateListLocalSkills(runtimeId);
  const start = Date.now();
  let current = initial;
  while (current.status === "pending" || current.status === "running") {
    if (Date.now() - start > LOCAL_SKILLS_POLL_TIMEOUT_MS) {
      throw new Error("runtime local skill discovery timed out");
    }
    await new Promise((resolve) => setTimeout(resolve, POLL_INTERVAL_MS));
    current = await api.getListLocalSkillsResult(runtimeId, initial.id);
  }
  if (current.status === "failed" || current.status === "timeout") {
    throw new Error(current.error || "runtime local skill discovery failed");
  }
  return {
    skills: current.skills ?? [],
    supported: current.supported,
    mcpServers: current.mcp_servers ?? [],
    mcpSupported: current.mcp_supported === true,
  };
}

/**
 * Brings a runtime-discovered catalog entry into the workspace (RUYI-288).
 * Mirrors the shared web hook's semantics: enqueue, poll to a terminal
 * status, and let the caller refresh the catalog + skill list. Conflict
 * surfaces as a resolved rejection message, matching the web import flow's
 * failure toast path.
 */
export async function importRuntimeLocalSkill(
  runtimeId: string,
  payload: CreateRuntimeLocalSkillImportRequest,
): Promise<void> {
  let req: RuntimeLocalSkillImportRequest =
    await api.initiateImportLocalSkill(runtimeId, payload);
  const deadline = Date.now() + IMPORT_POLL_TIMEOUT_MS;
  while (req.status === "pending" || req.status === "running") {
    if (Date.now() > deadline) throw new Error("import timed out");
    await new Promise((resolve) => setTimeout(resolve, POLL_INTERVAL_MS));
    req = await api.getImportLocalSkillResult(runtimeId, req.id);
  }
  if (req.status !== "completed") {
    throw new Error(req.error ?? "import failed");
  }
}
