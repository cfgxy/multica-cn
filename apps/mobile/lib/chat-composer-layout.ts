export const CHAT_COMPOSER_MIN_HEIGHT = 52;
export const CHAT_COMPOSER_INPUT_MIN_HEIGHT = 36;
export const CHAT_COMPOSER_INPUT_MAX_HEIGHT = 120;
export const CHAT_COMPOSER_INPUT_QUEUE_MAX_HEIGHT = 64;
export const PLUS_PANEL_MAX_HEIGHT_RATIO = 0.42;
export const PLUS_PANEL_GAP = 8;
export const CHAT_COMPOSER_BOTTOM_PADDING = 8;
export const PLUS_PANEL_OFFSET = PLUS_PANEL_GAP + CHAT_COMPOSER_BOTTOM_PADDING;
export const CHAT_QUEUE_PANEL_MAX_HEIGHT = 208;
export const CHAT_QUEUE_PANEL_MIN_HEIGHT = 96;
export const CHAT_LAYOUT_RESERVED_HEIGHT = 160;

export function resolveChatComposerInputHeight(contentHeight: number): number {
  return resolveBoundedChatComposerInputHeight(
    contentHeight,
    CHAT_COMPOSER_INPUT_MAX_HEIGHT,
  );
}

export function resolveBoundedChatComposerInputHeight(
  contentHeight: number,
  maxHeight: number,
): number {
  return Math.min(
    Math.max(CHAT_COMPOSER_INPUT_MIN_HEIGHT, contentHeight),
    Math.max(CHAT_COMPOSER_INPUT_MIN_HEIGHT, maxHeight),
  );
}

export function resolveChatComposerInputMaxHeight(
  hasAttachments: boolean,
  hasQueuedMessages: boolean,
): number {
  return hasAttachments && hasQueuedMessages
    ? CHAT_COMPOSER_INPUT_QUEUE_MAX_HEIGHT
    : CHAT_COMPOSER_INPUT_MAX_HEIGHT;
}

export function resolvePlusPanelMaxHeight(
  windowHeight: number,
  composerTop: number,
): number {
  return Math.max(
    0,
    Math.min(
      Math.round(windowHeight * PLUS_PANEL_MAX_HEIGHT_RATIO),
      composerTop - PLUS_PANEL_GAP,
    ),
  );
}

export function resolveChatQueuePanelMaxHeight(contentHeight: number): number {
  const availableHeight = Math.max(0, contentHeight);
  return Math.min(
    availableHeight,
    Math.max(
      CHAT_QUEUE_PANEL_MIN_HEIGHT,
      Math.min(
        CHAT_QUEUE_PANEL_MAX_HEIGHT,
        availableHeight - CHAT_LAYOUT_RESERVED_HEIGHT,
      ),
    ),
  );
}
