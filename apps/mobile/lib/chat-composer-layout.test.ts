// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  CHAT_COMPOSER_INPUT_MAX_HEIGHT,
  CHAT_COMPOSER_INPUT_MIN_HEIGHT,
  resolveChatComposerInputHeight,
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
