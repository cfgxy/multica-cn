/**
 * Inline mention chip. Renders a `<Text>` (NOT a `<View>` / `<Pressable>`)
 * so it composes inside a paragraph's `<Text>` parent — RN's text-in-text
 * rule. Tappability is via `onPress` on the Text itself, which works.
 *
 * Three URL shapes flow through here:
 *   - mention://member/<uuid>
 *   - mention://agent/<uuid>
 *   - mention://issue/<uuid>
 *
 * Resolution:
 *   - member / agent: useActorLookup (already implemented in
 *     apps/mobile/data/use-actor-name.ts). Cache hit rate is near 100%
 *     because the workspace layout pre-fetches both lists on mount.
 *   - issue: V2.1 fallback to the markdown link's text (which is the
 *     `MUL-XXX` identifier or whatever the editor wrote). Tap navigates
 *     to the issue detail screen using router.push.
 *
 * Cache miss: render the original markdown link text verbatim. The user
 * always sees something readable; we never show a raw uuid.
 */
import { Text } from "react-native";
import { router } from "expo-router";
import { Text as ThemedText } from "@/components/ui/text";
import { useActorLookup } from "@/data/use-actor-name";
import { useWorkspaceStore } from "@/data/workspace-store";
import {
  MENTION_AGENT_CLASS,
  MENTION_ISSUE_CLASS,
  MENTION_MEMBER_CLASS,
} from "./tokens";

interface Props {
  /** "member" | "agent" | "issue" — anything else falls back to plain text. */
  type: string;
  /** UUID from the URL path. */
  id: string;
  /** Original markdown link text, used as fallback when resolution misses. */
  fallback: string;
}

export function MentionChip({ type, id, fallback }: Props) {
  const { getName } = useActorLookup();
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);

  if (type === "member" || type === "agent") {
    const name = getName(type, id);
    const display = name === "Unknown" || name === "Unknown Agent"
      ? fallback
      : `@${name}`;
    return (
      <ThemedText
        className={
          type === "member" ? MENTION_MEMBER_CLASS : MENTION_AGENT_CLASS
        }
      >
        {display}
      </ThemedText>
    );
  }

  if (type === "issue") {
    const onPress = () => {
      if (wsSlug) router.push(`/${wsSlug}/issue/${id}`);
    };
    return (
      <ThemedText className={MENTION_ISSUE_CLASS} onPress={onPress}>
        {fallback}
      </ThemedText>
    );
  }

  // Unknown mention type — render the original text so nothing is dropped.
  return <Text>{fallback}</Text>;
}
