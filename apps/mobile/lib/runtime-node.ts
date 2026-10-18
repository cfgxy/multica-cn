import type { RuntimeDevice } from "@multica/core/types";

export type RuntimeNodeKind = "voice" | "cli" | "daemon";
export type RuntimeCredentialDisplayState = "configured" | "missing" | "error";

export function runtimeNodeKind(runtime: RuntimeDevice): RuntimeNodeKind {
  if (runtime.registration_source === "manual" || runtime.capabilities?.realtime_voice === true) {
    return "voice";
  }
  return runtime.daemon_id ? "daemon" : "cli";
}

export function runtimeCredentialDisplayState(
  status: RuntimeDevice["credential_status"],
): RuntimeCredentialDisplayState {
  if (status === "configured") return "configured";
  if (status === "invalid" || status === "unreachable") return "error";
  return "missing";
}
