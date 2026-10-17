// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  CHAT_COMPOSER_INPUT_MAX_HEIGHT,
  CHAT_COMPOSER_INPUT_MIN_HEIGHT,
  PLUS_PANEL_GAP,
  resolveChatComposerInputHeight,
  resolvePlusPanelMaxHeight,
} from "./chat-composer-layout";

describe("resolveChatComposerInputHeight", () => {
  it("keeps the compact input at its minimum height", () => {
    expect(resolveChatComposerInputHeight(0)).toBe(CHAT_COMPOSER_INPUT_MIN_HEIGHT);
    expect(resolveChatComposerInputHeight(48)).toBe(48);
  });

  it("caps long content so it scrolls inside the input", () => {
    expect(resolveChatComposerInputHeight(480)).toBe(CHAT_COMPOSER_INPUT_MAX_HEIGHT);
  });
});

describe("resolvePlusPanelMaxHeight", () => {
  it("caps the panel to the space above the composer", () => {
    expect(resolvePlusPanelMaxHeight(800, 180)).toBe(180 - PLUS_PANEL_GAP);
  });

  it("keeps the window-based cap when enough space is available", () => {
    expect(resolvePlusPanelMaxHeight(800, 700)).toBe(336);
  });

  it("never returns a negative height when the composer is near the top", () => {
    expect(resolvePlusPanelMaxHeight(800, 0)).toBe(0);
  });
});
