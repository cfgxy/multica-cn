export const CHAT_COMPOSER_MIN_HEIGHT = 52;
export const CHAT_COMPOSER_INPUT_MIN_HEIGHT = 36;
export const CHAT_COMPOSER_INPUT_MAX_HEIGHT = 120;

export function resolveChatComposerInputHeight(contentHeight: number): number {
  return Math.min(
    Math.max(CHAT_COMPOSER_INPUT_MIN_HEIGHT, contentHeight),
    CHAT_COMPOSER_INPUT_MAX_HEIGHT,
  );
}
