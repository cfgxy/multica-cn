/**
 * Inline link renderer. Returns a `<Text>` so it composes inside a
 * paragraph (RN's text-in-text rule). Two paths:
 *
 *   - href starts with `mention://` → MentionChip (member / agent / issue)
 *   - everything else → tappable Text that opens via Linking.openURL
 *
 * Linking.openURL handles HTTP(S), mailto, tel, and arbitrary deep links.
 * The system decides what to do (Safari / Mail / dialer / etc.).
 */
import { type ReactNode } from "react";
import { Linking } from "react-native";
import { Text as ThemedText } from "@/components/ui/text";
import { LINK_CLASS } from "./tokens";
import { MentionChip } from "./mention-chip";

interface Props {
  href: string;
  /** Already-rendered inline children (text, strong, em, etc.). */
  children: ReactNode;
  /** Plain text fallback used when href is mention://; matches what the
   *  link text actually says (e.g. "@naiyuan" or "MUL-123"). */
  fallbackText: string;
}

function parseMention(href: string): { type: string; id: string } | null {
  if (!href.startsWith("mention://")) return null;
  const rest = href.slice("mention://".length);
  const slash = rest.indexOf("/");
  if (slash < 0) return null;
  const type = rest.slice(0, slash);
  const id = rest.slice(slash + 1);
  if (!type || !id) return null;
  return { type, id };
}

export function MarkdownLink({ href, children, fallbackText }: Props) {
  const mention = parseMention(href);
  if (mention) {
    return (
      <MentionChip
        type={mention.type}
        id={mention.id}
        fallback={fallbackText}
      />
    );
  }
  return (
    <ThemedText
      className={LINK_CLASS}
      onPress={() => {
        Linking.openURL(href).catch(() => {
          // Silent — RN throws if no app handles the URL. Failing loudly
          // would be worse than letting the tap no-op.
        });
      }}
    >
      {children}
    </ThemedText>
  );
}
