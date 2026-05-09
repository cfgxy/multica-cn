/**
 * Public Markdown component for the mobile app. Internal implementation:
 * `EnrichedMarkdownText` from `react-native-enriched-markdown` (Software
 * Mansion, native md4c parser, no WebView).
 *
 * Why this engine vs the previous hand-rolled walker (see Decision log in
 * `apps/mobile/docs/markdown-renderer-research.md`):
 *
 *   - md4c renders into iOS NSAttributedString end-to-end. No nested-Text
 *     runs, so the chronic RN inline-Text border/padding bug (#10775 /
 *     #45925) is structurally impossible. Inline code chips, links, and
 *     CJK paragraphs all flow correctly without per-element workarounds.
 *   - GFM tables / task lists / strikethrough are first-class.
 *   - LaTeX math is native (block `$$...$$` with `flavor="github"`).
 *   - Streaming-aware via the companion `react-native-streamdown` package.
 *   - Native context menu, native image actions (Save / Copy / Share).
 *
 * Trade-offs accepted:
 *   - Mention links degrade from avatar chips → colored links. The
 *     `mention://` URL still routes via `onLinkPress` to the right place
 *     (issue navigation), but no inline avatar.
 *   - File cards degrade to `[📎 name](url)` plain links (already the
 *     preprocess output — no further regression).
 *   - Code blocks no longer use the Shiki highlighter. md4c's built-in
 *     code rendering is plain monospace; if syntax highlighting comes
 *     back, it will need to be a separate path.
 *
 * Pipeline:
 *   content
 *     ↓ preprocessMobileMarkdown   (legacy mention shortcodes + file cards
 *                                   + HTML strip with `<br>` → "  \n")
 *     ↓ EnrichedMarkdownText (md4c, GFM)
 *
 * Style customisation lives in `markdown-style.ts` so the component file
 * stays focused on routing / event wiring.
 */
import { useCallback, useMemo } from "react";
import { Linking } from "react-native";
import { router } from "expo-router";
import { EnrichedMarkdownText } from "react-native-enriched-markdown";
import { useWorkspaceStore } from "@/data/workspace-store";
import { preprocessMobileMarkdown } from "./preprocess";
import { MARKDOWN_STYLE } from "./markdown-style";

interface Props {
  content: string;
}

export function Markdown({ content }: Props) {
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);

  const processed = useMemo(
    () => preprocessMobileMarkdown(content),
    [content],
  );

  const onLinkPress = useCallback(
    ({ url }: { url: string }) => {
      // mention://issue/<uuid> → navigate to issue detail. Other mention
      // types (member / agent) currently no-op; future iteration could
      // open a profile sheet.
      if (url.startsWith("mention://")) {
        const rest = url.slice("mention://".length);
        const slash = rest.indexOf("/");
        if (slash < 0) return;
        const type = rest.slice(0, slash);
        const id = rest.slice(slash + 1);
        if (type === "issue" && id && wsSlug) {
          router.push(`/${wsSlug}/issue/${id}`);
        }
        return;
      }
      // Everything else — http(s), mailto, tel, app-scheme deep links —
      // hand off to the system. Linking.openURL throws if no app handles
      // the URL; the catch keeps a stray tap from crashing the screen.
      Linking.openURL(url).catch(() => {
        // Silent: failing loudly is worse than a no-op tap.
      });
    },
    [wsSlug],
  );

  if (!processed) return null;

  return (
    <EnrichedMarkdownText
      flavor="github"
      markdown={processed}
      markdownStyle={MARKDOWN_STYLE}
      onLinkPress={onLinkPress}
    />
  );
}
