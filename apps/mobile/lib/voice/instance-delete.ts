/**
 * Voice instance delete orchestration (RUYI-566). The server refuses a
 * direct `DELETE /api/runtimes/:id` with 409
 * `runtime_profile_instance_delete_unsupported` while a live runtime
 * profile backs the instance — the supported channel is deleting the
 * profile, whose single-transaction cascade tears down the instance, its
 * stored credentials and the profile row (RUYI-540 QA-verified). The
 * settings screen therefore routes by `profile_id`.
 *
 * Pure data, no RN runtime — colocated vitest lane covers the channel
 * routing and the rejection-copy extraction.
 */
import type { RuntimeDevice } from "@multica/core/types";

export type VoiceInstanceDeleteTarget =
  | { kind: "profile"; profileId: string }
  | { kind: "runtime"; runtimeId: string };

export function resolveVoiceInstanceDeleteTarget(
  runtime: Pick<RuntimeDevice, "id" | "profile_id">,
): VoiceInstanceDeleteTarget {
  if (runtime.profile_id) {
    return { kind: "profile", profileId: runtime.profile_id };
  }
  return { kind: "runtime", runtimeId: runtime.id };
}

/**
 * User-facing copy for a failed instance delete. Server rejections (409
 * bound-agents refusal, 403 not-owner) carry the human reason in the
 * body's `error` field — mobile `ApiError`'s message extraction only reads
 * `message`, so pull `error` out here. Narrowed on the `body` field rather
 * than an ApiError instanceof — the lib lane stays decoupled from the data
 * layer (same reason as `task-retry.ts` / `dispatch-reason.ts`).
 */
export function runtimeDeleteErrorMessage(
  err: unknown,
  fallback: string,
): string {
  const body = (err as { body?: unknown } | null)?.body;
  if (body && typeof body === "object") {
    const error = (body as { error?: unknown }).error;
    if (typeof error === "string" && error.length > 0) return error;
  }
  if (err instanceof Error && err.message) return err.message;
  return fallback;
}
