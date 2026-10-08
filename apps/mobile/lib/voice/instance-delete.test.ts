/**
 * RUYI-566 — voice instance delete orchestration helpers. Pins the delete
 * channel routing (profile-backed instances go through the profile cascade
 * endpoint the server enforces; profile-less instances delete directly) and
 * the server-rejection copy extraction (409/403 bodies carry the human
 * reason in `error`, which ApiError's message extraction misses).
 */
import type { RuntimeDevice } from "@multica/core/types";
import {
  resolveVoiceInstanceDeleteTarget,
  runtimeDeleteErrorMessage,
} from "./instance-delete";

// Shaped like `apps/mobile/data/api.ts:ApiError` — a thrown Error carrying
// the response body. Narrowed on the `body` field rather than an
// instanceof: the lib lane stays decoupled from the data layer (same
// reason as `task-retry.ts` / `dispatch-reason.ts`).
function apiErrorLike(message: string, status: number, body: unknown): Error {
  return Object.assign(new Error(message), { status, body });
}

function runtime(overrides: Partial<RuntimeDevice>): RuntimeDevice {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    profile_id: null,
    custom_name: null,
    name: "Gemini Live",
    provider: "gemini_live",
    protocol_family: "gemini_live",
    metadata: {},
    ...overrides,
  } as unknown as RuntimeDevice;
}

describe("resolveVoiceInstanceDeleteTarget", () => {
  it("routes profile-backed instances through the profile cascade channel", () => {
    const target = resolveVoiceInstanceDeleteTarget(
      runtime({ profile_id: "prof-1" }),
    );
    expect(target).toEqual({ kind: "profile", profileId: "prof-1" });
  });

  it("routes profile-less instances through the direct runtime delete", () => {
    expect(resolveVoiceInstanceDeleteTarget(runtime({ profile_id: null }))).toEqual(
      { kind: "runtime", runtimeId: "rt-1" },
    );
    expect(resolveVoiceInstanceDeleteTarget(runtime({}))).toEqual({
      kind: "runtime",
      runtimeId: "rt-1",
    });
  });
});

describe("runtimeDeleteErrorMessage", () => {
  it("surfaces the server's `error` field (409 bound-agents refusal)", () => {
    const err = apiErrorLike("409 Conflict", 409, {
      error:
        "cannot delete runtime: agents still reference it as their voice runtime. Unbind them first.",
      code: "runtime_has_voice_bindings",
    });
    expect(runtimeDeleteErrorMessage(err, "fallback")).toBe(
      "cannot delete runtime: agents still reference it as their voice runtime. Unbind them first.",
    );
  });

  it("surfaces the plain `error` envelope (writeError shape)", () => {
    const err = apiErrorLike("403 Forbidden", 403, {
      error: "you can only delete your own runtimes",
    });
    expect(runtimeDeleteErrorMessage(err, "fallback")).toBe(
      "you can only delete your own runtimes",
    );
  });

  it("falls back to err.message when the body carries no error field", () => {
    expect(
      runtimeDeleteErrorMessage(apiErrorLike("network down", 0, undefined), "fb"),
    ).toBe("network down");
  });

  it("falls back to the caller copy for non-Error throwables", () => {
    expect(runtimeDeleteErrorMessage("boom", "fb")).toBe("fb");
    expect(runtimeDeleteErrorMessage(undefined, "fb")).toBe("fb");
  });

  it("ignores non-string error fields", () => {
    const err = apiErrorLike("400 Bad Request", 400, { error: 42 });
    expect(runtimeDeleteErrorMessage(err, "fb")).toBe("400 Bad Request");
  });
});
