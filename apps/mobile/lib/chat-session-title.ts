export const NEW_CHAT_TITLE = "New chat";

/**
 * 兜底文案默认取英文原文（上游行为），调用方在组件里拿到 t() 后应传入
 * 本地化的兜底值（如 chat:machine.sessions.untitled），否则无标题会话会
 * 在非英文界面里露出英文。
 */
export function chatSessionDisplayTitle(
  title: string | null | undefined,
  fallback: string = NEW_CHAT_TITLE,
): string {
  return title || fallback;
}
