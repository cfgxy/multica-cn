/**
 * Public Markdown component for the mobile app. Replaces ad-hoc
 * `<Text>{content}</Text>` calls in comment-card and issue-description.
 *
 * Pipeline (one-shot, memoized by content string):
 *
 *   content
 *     ↓ preprocessMobileMarkdown   (mention shortcodes + file cards)
 *     ↓ marked.lexer({ gfm, breaks })
 *     ↓ Token[]
 *     ↓ renderBlocks               (returns a React Native tree)
 *
 * Performance: typical comments are < 5 KB, descriptions < 20 KB. Lexing
 * is sub-millisecond at that size; we still memoize on content so a
 * parent re-render with the same content doesn't reparse. No persistent
 * cache (cross-comment) — each `<Markdown>` instance owns its own memo.
 */
import { useMemo } from "react";
import { View } from "react-native";
import { marked } from "marked";
import { renderBlocks } from "./render-block";
import { preprocessMobileMarkdown } from "./preprocess";
import { promoteInlineImages } from "./ast";

interface Props {
  content: string;
}

export function Markdown({ content }: Props) {
  const tokens = useMemo(() => {
    const pre = preprocessMobileMarkdown(content);
    if (!pre) return [];
    // gfm: true gives us tables, task lists, strikethrough, autolinks.
    // breaks: true makes a single newline render as <br> — matches how
    // people type in chat-style mobile inputs (and matches web's
    // remark-breaks plugin in readonly-content.tsx).
    const lexed = marked.lexer(pre, { gfm: true, breaks: true });
    // promoteInlineImages: marked always wraps `![alt](url)` in a
    // paragraph (CommonMark says image is inline). RN can't put an
    // <Image> inside a <Text>, so without this pass NO images would
    // ever render. The transform pulls images out as block siblings.
    return promoteInlineImages(lexed);
  }, [content]);

  if (tokens.length === 0) return null;
  return <View>{renderBlocks(tokens)}</View>;
}
