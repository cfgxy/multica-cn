/**
 * Second-line preview text for the mobile chat list rows (RUYI-496).
 *
 * Mirrors the else-if chain of web's session row
 * (packages/views/chat/components/chat-thread-list.tsx: failed →
 * no_response → preview → empty) minus the typing/waiting states, which
 * web derives from its pending-task snapshot — the mobile list doesn't
 * mount that snapshot, and an unqualified "Typing…" would be a lie.
 * Archived wins over everything so the marker never hides behind a
 * preview. Markdown noise is stripped the same way as web's toPreview.
 */
import type { ChatSession } from "@multica/core/types";

export type ChatSessionPreviewKind =
  | "archived"
  | "failed"
  | "no_response"
  | "preview"
  | "empty";

export interface ChatSessionPreview {
  kind: ChatSessionPreviewKind;
  text: string;
}

type TFn = (key: string, fallback: string) => string;

/** Same strip as web's toPreview in chat-thread-list.tsx. */
function toPreview(content: string): string {
  return content
    .replace(/```[\s\S]*?```/g, " ")
    .replace(/[#*`>~]/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

export function chatSessionPreview(
  session: ChatSession,
  t: TFn,
): ChatSessionPreview {
  if (session.status === "archived") {
    return { kind: "archived", text: t("chat:mobile.sessions.archived", "archived") };
  }
  const last = session.last_message ?? null;
  if (last?.failure_reason) {
    return { kind: "failed", text: t("chat:list.failed", "Failed to send") };
  }
  if (last?.message_kind === "no_response") {
    // A no_response turn stores a non-empty English fallback as its content;
    // show the localized hint instead of leaking it (MUL-4351, mirrored).
    return {
      kind: "no_response",
      text: t("chat:list.no_response_preview", "No text reply"),
    };
  }
  if (last) {
    const prefix = last.role === "user" ? t("chat:list.you_prefix", "You: ") : "";
    return { kind: "preview", text: `${prefix}${toPreview(last.content)}` };
  }
  return { kind: "empty", text: t("chat:list.no_messages", "No messages yet") };
}
