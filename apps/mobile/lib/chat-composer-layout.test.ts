// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  CHAT_COMPOSER_INPUT_MAX_HEIGHT,
  CHAT_COMPOSER_INPUT_MIN_HEIGHT,
  CHAT_QUEUE_PANEL_MAX_HEIGHT,
  CHAT_QUEUE_PANEL_MIN_HEIGHT,
  PLUS_PANEL_GAP,
  resolveChatQueuePanelMaxHeight,
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

describe("resolveChatQueuePanelMaxHeight", () => {
  it("keeps the full queue window when the keyboard leaves enough room", () => {
    expect(resolveChatQueuePanelMaxHeight(500)).toBe(CHAT_QUEUE_PANEL_MAX_HEIGHT);
  });

  it("shrinks the queue window as the composer area gets shorter", () => {
    expect(resolveChatQueuePanelMaxHeight(260)).toBe(100);
  });

  it("retains a scrollable queue header at the minimum available height", () => {
    expect(resolveChatQueuePanelMaxHeight(100)).toBe(CHAT_QUEUE_PANEL_MIN_HEIGHT);
  });

  it("never allocates more height than the measured content area", () => {
    expect(resolveChatQueuePanelMaxHeight(72)).toBe(72);
    expect(resolveChatQueuePanelMaxHeight(0)).toBe(0);
  });
});
