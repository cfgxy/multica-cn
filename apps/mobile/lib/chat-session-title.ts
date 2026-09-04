export const NEW_CHAT_TITLE = "New chat";

/**
 * Resolve a chat session's display title.
 *
 * `untitledLabel` lets UI call sites inject the localized untitled fallback
 * (e.g. `t("mobile.sessions.untitled")`) — "New chat" is copy, not a
 * literal. Omit it in non-UI contexts to keep the upstream English
 * default.
 */
export function chatSessionDisplayTitle(
  title: string | null | undefined,
  untitledLabel: string = NEW_CHAT_TITLE,
): string {
  return title || untitledLabel;
}
